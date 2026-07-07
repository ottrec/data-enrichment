package enrich

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ottrec/scraper/schema"
	"github.com/ottrec/website/pkg/ottrecidx"
)

// The pattern tables below encode the city's phrasing vocabulary, collected
// from the full dataset history (see notes/data-shape.md). All of them run
// against foldText'd or normText'd text; see processSentence for the order
// they apply in, which is load-bearing.
var (
	// boilerplateRe matches standing notices with no schedule meaning
	// (contact-for-hours blurbs, "pickleball moved to the gym schedule");
	// they become ignored/boilerplate objects.
	boilerplateRe = regexp.MustCompile(`^(please contact .*(opening hours|washroom)|public skating is not available at this location|the park is open year ?round|.*can now be found in the .*schedule.*|gymnasium sports will resume in the fall)`)
	// "See Winter Break schedule." cross-references (capture: the schedule name)
	seeRe = regexp.MustCompile(`(?i)\bsee\s+(?:the\s+)?(.{0,80}?schedule)s?\b`)
	// whole-facility closure sentences ("The facility is closed and all
	// programs cancelled."); also sets the closure context for following items
	facilityRe = regexp.MustCompile(`^(?:the )?facility\b.*\b(closed|close|not available|unavailable)\b`)
	// "All drop-ins cancelled" (whole posted scope)
	allDropinsRe = regexp.MustCompile(`^(?:all|both) (?:drop ?ins?|programs)(?: are)?(?: cancelled)?$`)
	// "All <classes> drop-ins" (capture: the class phrase, e.g. "skating and
	// ice sports"), resolved by resolveClass
	allClassRe = regexp.MustCompile(`^(?:all|both) (?:drop ?in )?(.+?)(?: drop ?ins?| programs| sessions| activities)?$`)
	// "Regular season, <date range>" seasonal operating statements
	seasonRe     = regexp.MustCompile(`(?i)^(regular season|pre-? ?season)\b[ ,]*`)
	closedSeason = regexp.MustCompile(`^closed for the season`)
	// a comma clause that is only an effect keyword ("..., cancelled")
	keywordRe = regexp.MustCompile(`^(?:and |are |is |will be |all )*(cancelled|canceled|added|closed)[. ]*$`)
	// "moved to <destination>" / "changed to <replacement>" clauses
	movedRe   = regexp.MustCompile(`(?i)^moved (?:to |inside to )?(.+)$`)
	changedRe = regexp.MustCompile(`(?i)^changed to (.+)$`)
	// an effect keyword glued to the phrase without a comma ("All drop-in
	// skating and ice sports cancelled")
	trailingKwRe  = regexp.MustCompile(`(?i)[ ,]+(?:and |are |is |will be )*(cancelled|canceled|added|closed)[. ]*$`)
	untilNoticeRe = regexp.MustCompile(`(?i)\buntil further notice\b`)
	// "X is closed ...", "X closed until further notice", "X will be closed"
	subjectClosedRe = regexp.MustCompile(`^(.+?)(?: is| are| was| were| will be)?(?: temporarily| now| also)? (?:closed|not available|unavailable)\b`)
	// "... and all programs cancelled" riding on a subject closure, which
	// upgrades it to a broad (group/facility) cancellation
	allProgramsRe = regexp.MustCompile(`\ball .{0,40}?(?:drop ?ins?|programs)(?: are)? cancelled\b`)
	// "All changerooms closed for maintenance": an amenity closure phrased
	// through the "all X" form (capture: the amenity phrase)
	allAmenityClosedRe = regexp.MustCompile(`^(.+?),? (?:are |is )?closed(?: for .+)?$`)
	genericFacility    = map[string]bool{"facility": true, "centre": true, "center": true, "complex": true, "building": true, "hall": true, "community": true, "recreation": true, "park": true, "pool": true, "arena": true, "rink": true, "dome": true}
)

// amenityCore are nouns that identify a facility amenity subject; a phrase is
// an amenity if its leading tokens are qualifiers up to a core noun.
var amenityCore = map[string]bool{
	"pool": true, "pools": true, "tub": true, "tubs": true, "sauna": true,
	"saunas": true, "steam": true, "whirlpool": true, "whirlpools": true,
	"board": true, "boards": true, "slide": true, "slides": true, "wall": true,
	"elevator": true, "elevators": true,
	"changeroom": true, "changerooms": true,
	"court": true, "courts": true, "gym": true, "gyms": true, "gymnasium": true,
	"arena": true, "arenas": true, "rink": true, "rinks": true,
	"washroom": true, "washrooms": true,
	"lawn": true, "hill": true, "room": true, "rooms": true, "ice": true,
	"heater": true, "centre": true, "center": true,
	"track": true, "tracks": true, "field": true, "fields": true,
}

var amenityQualifier = map[string]bool{
	"main": true, "baby": true, "training": true, "therapeutic": true,
	"lap": true, "whale": true, "wave": true, "leisure": true, "outdoor": true,
	"indoor": true, "hot": true, "diving": true, "rock": true, "weight": true,
	"cardio": true, "squash": true, "change": true, "sledding": true,
	"great": true, "men's": true, "women's": true, "mens": true, "womens": true,
	"25m": true, "50m": true, "1m": true, "3m": true, "m": true, "metre": true,
	"meter": true, "1": true, "3": true, "25": true, "50": true, "pool": true,
	"customer": true, "service": true, "athletics": true, "cross": true,
	"country": true, "ski": true,
}

// processItem parses one extracted line/item and emits objects for it.
func (b *blockCtx) processItem(st *walkState, text, itemHTML string, off [2]int, links []anchor, extraAmbig []string) {
	t := strings.TrimSpace(text)
	if t == "" {
		return
	}
	b.out.Stats["item"]++
	folded := foldText(t)
	if boilerplateRe.MatchString(folded) {
		b.out.Stats["boilerplate"]++
		b.add("ignored", "boilerplate", notice{
			Section: st.section, DateText: st.headRaw, RawHTML: itemHTML, RawText: t,
		}, off, nil, false)
		return
	}

	n := notice{
		Section:     st.section,
		DateText:    st.headRaw,
		RawHTML:     itemHTML,
		RawText:     t,
		Ambiguities: slices.Clone(extraAmbig),
	}

	// dates: the item's own leading date wins over the head context
	spec := st.head
	working := t
	if own, rest, ok := parseLeadingDate(t, b.anchor); ok {
		spec = &own
		n.DateText = own.Raw
		working = rest
	}
	if spec != nil {
		n.Ambiguities = append(n.Ambiguities, spec.Ambig...)
	}

	// multi-sentence items get one parse per sentence ("The 25 m pool is
	// closed between 7:30 and 10:30 am. Lane swim, 7:30 to 8:30 am,
	// cancelled"), sharing the raw text and date context
	if sents := splitSentences(working); len(sents) > 1 {
		mark := len(b.recs)
		for _, s := range sents {
			nn := n
			nn.Ambiguities = slices.Clone(n.Ambiguities)
			b.processSentence(nn, st, spec, s, off, links)
		}
		// per-sentence unparsed records are redundant: the notices carry the
		// full raw text, and an all-unparsed item needs only one record
		notices, unparsed := 0, 0
		for _, r := range b.recs[mark:] {
			switch r.kind {
			case "notice":
				notices++
			case "unparsed":
				unparsed++
			}
		}
		if unparsed > 0 && (notices > 0 || unparsed > 1) {
			kept := b.recs[:mark]
			first := true
			for _, r := range b.recs[mark:] {
				if r.kind != "unparsed" {
					kept = append(kept, r)
					continue
				}
				b.out.Stats["unparsed/"+r.reason]--
				b.out.Stats["object/unparsed"]--
				b.out.Stats["object/unparsed/"+r.reason]--
				if notices == 0 && first {
					// collapse to a single whole-item record
					first = false
					r.reason = "freeform"
					r.n.RawText = n.RawText
					b.out.Stats["unparsed/freeform"]++
					b.out.Stats["object/unparsed"]++
					b.out.Stats["object/unparsed/freeform"]++
					kept = append(kept, r)
				}
			}
			b.recs = kept
		}
		return
	}
	b.processSentence(n, st, spec, working, off, links)
}

// processSentence parses one sentence of an item and emits objects for it.
//
// The order of checks is load-bearing: see-schedule cross-references, then
// time extraction (so closure sentences keep their times), then the
// sentence-level patterns (facility closures, season statements, "<subject>
// is closed" with facility-name -> activity -> amenity subject resolution),
// then comma-clause decomposition into effect keywords / moved-changed /
// trailing "only" restriction / subject phrase, then the "all X" scope
// phrases, the empty-phrase branch (bare effects, date+clock hours items,
// date-only items), and finally the activity/amenity/freeform subject
// resolution with slot validation and session explosion.
func (b *blockCtx) processSentence(n notice, st *walkState, spec *dateSpec, working string, off [2]int, links []anchor) {
	openEnded := untilNoticeRe.MatchString(working)

	defaultLevel := "facility"
	if b.grp != nil {
		defaultLevel = "group"
	}

	var sessions []sessKey
	emit := func() {
		n.Dates = toDateSpan(spec, openEnded)
		n.Ambiguities = dedupeStrings(n.Ambiguities)
		b.out.Stats["notice"]++
		b.out.Stats["scope/"+n.Scope.Level]++
		countEffects(b.out.Stats, n.Effects)
		for _, a := range n.Ambiguities {
			b.out.Stats["amb/"+a]++
		}
		b.add("notice", "", n, off, sessions, n.Scope.MatchQuality == matchNovel)
	}
	unparsed := func(reason string) {
		b.out.Stats["unparsed/"+reason]++
		b.add("unparsed", reason, n, off, nil, false)
	}

	// "See X schedule" cross-references
	if m := seeRe.FindStringSubmatch(working); m != nil {
		n.Effects.SeeSchedule = normText(m[1])
		for _, l := range links {
			if l.Href != "" {
				n.Effects.SeeURL = l.Href
				break
			}
		}
		n.Scope = scope{Level: defaultLevel, MatchQuality: matchScopePhrase}
		emit()
		return
	}

	// time extraction first, so closure sentences keep their times
	clocks, remainder := findClockRanges(working)
	singles, remainder := findSingleEnded(remainder)
	clocks = append(clocks, singles...)
	for _, cm := range clocks {
		if cm.EndEarly {
			n.Effects.TimeChange = true
		}
	}

	fworking := foldText(working)

	// whole-facility closure sentences
	if facilityRe.MatchString(fworking) {
		n.Effects.Closure = true
		n.Effects.Cancelled = strings.Contains(fworking, "cancelled") || strings.Contains(fworking, "canceled")
		n.Scope = scope{Level: "facility", MatchQuality: matchScopePhrase}
		if b.grp != nil {
			n.Scope.Level = "group" // scoped by where the city posted it
			n.Scope.Groups = []string{b.grp.label}
		}
		st.closureContext = true
		b.emitTimes(&n, spec, clocks, &sessions, emit)
		return
	}

	// season statements
	if closedSeason.MatchString(fworking) {
		n.Effects.Closure = true
		n.Effects.SeasonalHours = true
		n.Scope = scope{Level: defaultLevel, MatchQuality: matchScopePhrase}
		openEnded = true
		emit()
		return
	}
	if m := seasonRe.FindStringSubmatch(working); m != nil {
		n.Effects.SeasonalHours = true
		n.Scope = scope{Level: "facility", MatchQuality: matchScopePhrase, Phrase: normText(m[1])}
		if own, rest, ok := parseLeadingDate(working[len(m[0]):], b.anchor); ok && restIsTrivial(rest) {
			spec = &own
			n.DateText = own.Raw
			n.Ambiguities = append(n.Ambiguities, own.Ambig...)
		}
		emit()
		return
	}

	// "<subject> is closed ..." sentences: resolve the subject, which may be
	// the facility itself, an activity, or an amenity (an amenity closure
	// makes no claims about activities: e.g. one closed arena of two does
	// not cancel the skating in the other)
	fremainder := foldText(remainder)
	if m := subjectClosedRe.FindStringSubmatch(fremainder); m != nil && !strings.HasPrefix(fremainder, "all ") {
		n.Effects.Closure = true
		n.Effects.Cancelled = allProgramsRe.MatchString(fremainder)
		subject := strings.TrimPrefix(strings.TrimSpace(m[1]), "the ")
		if serviceDeskPhrase(subject) && closureOnly(n.Effects) {
			b.out.Stats["ignored/service-desk"]++
			b.add("ignored", "service-desk", n, off, nil, false)
			return
		}
		n.Scope.Phrase = subject
		q, acts, groups, typo := b.matchActivity(subject)
		if typo {
			n.Ambiguities = append(n.Ambiguities, ambActivityTypo)
		}
		switch {
		case subjectIsFacility(subject, b.fac.GetName()):
			n.Scope.Level = "facility"
			n.Scope.MatchQuality = matchScopePhrase
		case n.Effects.Cancelled:
			// "the pool is closed and all programs cancelled": group-wide
			n.Scope.Level = defaultLevel
			n.Scope.Amenity = subject
			n.Scope.MatchQuality = matchScopePhrase
			if b.grp != nil {
				n.Scope.Groups = []string{b.grp.label}
			}
		case q == matchExact || q == matchNormalized || q == matchFuzzy:
			n.Scope.Level = "activity"
			n.Scope.Activities = actNames(acts)
			n.Scope.Groups = groups
			n.Scope.MatchQuality = q
			b.emitTimesWithSlots(&n, spec, clocks, acts, &sessions, emit)
			return
		case isAmenity(subject):
			n.Scope.Level = "amenity"
			n.Scope.Amenity = amenityName(subject)
			n.Scope.MatchQuality = matchNone
		default:
			n.Scope.Level = "none"
			n.Scope.MatchQuality = matchNone
			n.Ambiguities = append(n.Ambiguities, ambActivityUnmatched)
		}
		b.emitTimes(&n, spec, clocks, &sessions, emit)
		return
	}

	var phraseParts []string
	for _, clause := range strings.Split(remainder, ",") {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		fc := foldText(clause)
		if m := keywordRe.FindStringSubmatch(fc); m != nil {
			setKeyword(&n.Effects, m[1])
			continue
		}
		if m := movedRe.FindStringSubmatch(clause); m != nil {
			n.Effects.MovedTo = strings.Trim(normText(m[1]), " .")
			continue
		}
		if m := changedRe.FindStringSubmatch(clause); m != nil {
			n.Effects.ChangedTo = strings.Trim(normText(m[1]), " .")
			continue
		}
		if fc == "schedule change" || fc == "schedule changes" {
			n.Effects.TimeChange = true
			continue
		}
		if strings.HasSuffix(fc, " only") && len(phraseParts) > 0 {
			n.Effects.Restriction = strings.Trim(normText(clause), " .")
			continue
		}
		phraseParts = append(phraseParts, clause)
	}
	phrase := strings.Trim(strings.Join(phraseParts, ", "), " .")
	// keyword glued to the phrase without a comma
	if m := trailingKwRe.FindStringSubmatch(phrase); m != nil {
		setKeyword(&n.Effects, strings.ToLower(m[1]))
		phrase = strings.Trim(phrase[:len(phrase)-len(m[0])], " .,")
	}
	if n.Effects.ChangedTo != "" && len(clocks) >= 2 {
		n.Effects.TimeChange = true
	}
	if openEnded && strings.Contains(fworking, "closed") {
		n.Effects.Closure = true
	}
	n.Scope.Phrase = phrase
	fphrase := foldText(phrase)

	// "all drop-ins cancelled" / "all X cancelled" scope phrases
	if allDropinsRe.MatchString(fphrase) || (fphrase == "" && n.Effects.Cancelled && strings.HasPrefix(fworking, "all")) {
		n.Effects.Cancelled = true
		n.Scope.Level = defaultLevel
		n.Scope.MatchQuality = matchScopePhrase
		if b.grp != nil {
			n.Scope.Groups = []string{b.grp.label}
		}
		emit()
		return
	}
	if m := allClassRe.FindStringSubmatch(fphrase); m != nil && (n.Effects.any() || spec != nil) {
		// "All changerooms closed for maintenance": an amenity closure, not
		// an activity class
		if cm := allAmenityClosedRe.FindStringSubmatch(m[1]); cm != nil && isAmenity(cm[1]) {
			n.Effects.Closure = true
			n.Scope.Level = "amenity"
			n.Scope.Amenity = amenityName(cm[1])
			n.Scope.MatchQuality = matchScopePhrase
			b.emitTimes(&n, spec, clocks, &sessions, emit)
			return
		}
		acts := b.resolveClass(&n, m[1])
		b.emitTimesWithSlots(&n, spec, clocks, acts, &sessions, emit)
		return
	}

	// hours/closure items: a date context but no subject phrase
	if fphrase == "" {
		switch {
		case n.Effects.any():
			n.Scope.Level = defaultLevel
			if !n.Effects.Closure && !n.Effects.Cancelled {
				n.Ambiguities = append(n.Ambiguities, ambNoSubject)
			}
		case spec != nil && len(clocks) > 0:
			n.Scope.Level = defaultLevel
			switch {
			case st.closureContext:
				n.Effects.Closure = true
			case b.grp != nil:
				// inside a schedule-changes block a bare time is likely an
				// orphaned activity time change, never ignorable facility
				// hours
				n.Scope.Groups = []string{b.grp.label}
				n.Ambiguities = append(n.Ambiguities, ambPossibleActivityTime)
			case strings.Contains(foldText(st.section), "customer service"):
				b.out.Stats["ignored/service-desk"]++
				b.add("ignored", "service-desk", n, off, nil, false)
				return
			case strings.Contains(foldText(st.section), "hours"):
				n.Effects.ModifiedHours = true
			default:
				// bare "date, clock range" items are facility hours (which we
				// don't need to associate further) unless they could be an
				// orphaned activity time change: a range exactly equal to an
				// activity slot on those dates, or one too short to plausibly
				// be facility hours
				var acts []*actEntry
				if b.grp != nil {
					acts = b.grp.acts
				} else {
					for _, m := range b.matchers {
						acts = append(acts, m.acts...)
					}
				}
				// only session-length slots are evidence: all-day slots
				// (weight room, hot tub) mirror the facility hours, so an
				// exact match against them carries no signal
				var slots []slotInfo
				for _, s := range gatherSlots(acts, spec) {
					if s.r.End-s.r.Start < 6*60 {
						slots = append(slots, s)
					}
				}
				var exact, short bool
				for _, cm := range clocks {
					for _, c := range cm.Cands {
						if c.End-c.Start < 4*60 {
							short = true
						}
						if rel, _ := clockRelation(c, slots); rel == relExact {
							exact = true
						}
					}
				}
				switch {
				case exact:
					n.Ambiguities = append(n.Ambiguities, ambPossibleActivityTime)
				case short:
					n.Ambiguities = append(n.Ambiguities, ambHoursContext)
				default:
					n.Effects.ModifiedHours = true
				}
			}
		case spec != nil:
			n.Scope.Level = "facility"
			if st.closureContext || strings.Contains(foldText(st.section), "closure") {
				n.Effects.Closure = true
			} else {
				n.Ambiguities = append(n.Ambiguities, ambDateOnlyItem)
			}
		default:
			unparsed("empty")
			return
		}
		b.emitTimes(&n, spec, clocks, &sessions, emit)
		return
	}

	if serviceDeskPhrase(phrase) && closureOnly(n.Effects) && n.Effects.Closure {
		b.out.Stats["ignored/service-desk"]++
		b.add("ignored", "service-desk", n, off, nil, false)
		return
	}

	// activity subject
	q, acts, groups, typo := b.matchActivity(phrase)
	if q == matchNone && b.grp != nil {
		// wrong-group postings ("Public skating, cancelled" under ice
		// sports): match the facility's other groups, marked
		if q2, acts2, groups2, typo2 := b.matchOtherGroups(phrase); q2 != matchNone {
			q, acts, groups, typo = q2, acts2, groups2, typo2
			n.Ambiguities = append(n.Ambiguities, ambOtherGroup)
		}
	}
	if typo && q != matchNone {
		n.Ambiguities = append(n.Ambiguities, ambActivityTypo)
	}
	switch q {
	case matchExact, matchNormalized, matchFuzzy:
		n.Scope.Level = "activity"
		n.Scope.Activities = actNames(acts)
		n.Scope.Groups = groups
		n.Scope.MatchQuality = q
	case matchMultiple:
		n.Scope.Level = "activity"
		n.Scope.Activities = actNames(acts)
		n.Scope.Groups = groups
		n.Scope.MatchQuality = q
		n.Ambiguities = append(n.Ambiguities, ambActivityMultiple)
	default:
		if isAmenity(phrase) {
			n.Scope.Level = "amenity"
			n.Scope.Amenity = amenityName(fphrase)
			n.Scope.MatchQuality = matchNone
		} else if n.Effects.Added && spec != nil {
			// an added session for an activity not in the published schedule
			// ("Women's only swim, 8:15 to 9:15 pm, added"): expected for
			// additions, and worth surfacing as the new activity itself,
			// labeled by the raw subject phrase
			n.Scope.Level = "activity"
			n.Scope.Activities = []string{phrase}
			n.Scope.MatchQuality = matchNovel
		} else if n.Effects.any() {
			n.Scope.Level = "none"
			n.Scope.MatchQuality = matchNone
			n.Ambiguities = append(n.Ambiguities, ambActivityUnmatched)
		} else if spec == nil {
			unparsed("freeform")
			return
		} else {
			n.Scope.Level = "none"
			n.Scope.MatchQuality = matchNone
			n.Ambiguities = append(n.Ambiguities, ambFreeformItem)
		}
	}
	if n.Scope.Level == "activity" {
		b.checkDateInSchedules(&n, spec, acts)
		acts = b.maybeDisambiguate(&n, spec, clocks, acts)
	}
	b.emitTimesWithSlots(&n, spec, clocks, acts, &sessions, emit)
}

// resolveClass applies an "all <classes>" phrase: whole group when a segment
// covers the group (or the group titles it matches for facility-level
// notices), otherwise the activities matching a segment.
func (b *blockCtx) resolveClass(n *notice, classPhrase string) []*actEntry {
	segs := classSegments(classPhrase)
	n.Scope.MatchQuality = matchScopePhrase
	if len(segs) == 0 {
		// only stopwords ("all drop-in activities"): everything
		if b.grp != nil {
			n.Scope.Level = "group"
			n.Scope.Groups = []string{b.grp.label}
			return b.grp.acts
		}
		n.Scope.Level = "facility"
		return nil
	}
	if b.grp != nil {
		for _, seg := range segs {
			if b.grp.coversGroup(seg) {
				n.Scope.Level = "group"
				n.Scope.Groups = []string{b.grp.label}
				return b.grp.acts
			}
		}
		var acts []*actEntry
		for _, seg := range segs {
			acts = append(acts, b.grp.matchClass(seg)...)
		}
		if len(acts) > 0 {
			n.Scope.Level = "class"
			n.Scope.Activities = actNames(acts)
			n.Scope.Groups = []string{b.grp.label}
			return acts
		}
		// the city sometimes posts a change under the wrong group ("All
		// drop-in skating" in the swim group): match sibling groups, marked
		var groups []string
		for _, m := range b.matchers {
			if m == b.grp {
				continue
			}
			for _, seg := range segs {
				if m.coversGroup(seg) || subset(seg, m.titleToks) {
					groups = append(groups, m.label)
					for _, e := range m.acts {
						acts = append(acts, e)
					}
					break
				}
				if es := m.matchClass(seg); len(es) > 0 {
					groups = append(groups, m.label)
					acts = append(acts, es...)
				}
			}
		}
		if len(groups) > 0 {
			n.Scope.Level = "class"
			n.Scope.Groups = dedupeStrings(groups)
			n.Scope.Activities = actNames(acts)
			n.Ambiguities = append(n.Ambiguities, ambOtherGroup)
			return acts
		}
		n.Scope.Level = "class"
		n.Ambiguities = append(n.Ambiguities, ambClassUnmatched)
		return nil
	}
	var groups []string
	var acts []*actEntry
	for _, seg := range segs {
		matched := false
		for _, m := range b.matchers {
			if m.coversGroup(seg) {
				groups = append(groups, m.label)
				matched = true
			}
		}
		if !matched {
			for _, m := range b.matchers {
				acts = append(acts, m.matchClass(seg)...)
			}
		}
	}
	if len(groups) == 0 && len(acts) == 0 {
		// partial title match ("all gymnasium programming" vs the
		// "Gymnasium Sports" group), only once nothing better matched
		for _, seg := range segs {
			for _, m := range b.matchers {
				if subset(seg, m.titleToks) {
					groups = append(groups, m.label)
					acts = append(acts, m.acts...)
				}
			}
		}
		if len(groups) > 0 {
			n.Ambiguities = append(n.Ambiguities, ambClassTitlePartial)
		}
	}
	switch {
	case len(groups) > 0 && len(acts) == 0:
		n.Scope.Level = "group"
		n.Scope.Groups = dedupeStrings(groups)
		for _, m := range b.matchers {
			if slices.Contains(n.Scope.Groups, m.label) {
				acts = append(acts, m.acts...)
			}
		}
	case len(groups) > 0 || len(acts) > 0:
		n.Scope.Level = "class"
		n.Scope.Groups = dedupeStrings(groups)
		n.Scope.Activities = actNames(acts)
	default:
		n.Scope.Level = "class"
		n.Ambiguities = append(n.Ambiguities, ambClassUnmatched)
	}
	return acts
}

// matchActivity matches a subject phrase against the block's group (or all
// groups for facility-level sources).
func (b *blockCtx) matchActivity(phrase string) (string, []*actEntry, []string, bool) {
	if b.grp != nil {
		r := b.grp.match(phrase)
		return r.Quality, r.Acts, nil, r.Typo
	}
	order := []string{matchExact, matchNormalized, matchFuzzy, matchMultiple}
	byQ := map[string][]*actEntry{}
	byQGroups := map[string][]string{}
	byQTypo := map[string]bool{}
	for _, m := range b.matchers {
		r := m.match(phrase)
		if r.Quality == matchNone {
			continue
		}
		byQ[r.Quality] = append(byQ[r.Quality], r.Acts...)
		byQGroups[r.Quality] = append(byQGroups[r.Quality], m.label)
		byQTypo[r.Quality] = byQTypo[r.Quality] || r.Typo
	}
	for _, q := range order {
		if len(byQ[q]) == 0 {
			continue
		}
		acts, groups := byQ[q], dedupeStrings(byQGroups[q])
		typo := byQTypo[q]
		if q == matchFuzzy && len(acts) > 1 {
			q = matchMultiple
		}
		return q, acts, groups, typo
	}
	return matchNone, nil, nil, false
}

// matchOtherGroups matches a phrase against the facility's groups other
// than the block's own, for wrong-group postings.
func (b *blockCtx) matchOtherGroups(phrase string) (string, []*actEntry, []string, bool) {
	saved := b.grp
	b.grp = nil
	defer func() { b.grp = saved }()
	q, acts, groups, typo := b.matchActivity(phrase)
	groups = slices.DeleteFunc(groups, func(g string) bool { return g == saved.label })
	if len(groups) == 0 {
		return matchNone, nil, nil, false
	}
	return q, acts, groups, typo
}

// serviceDeskPhrase reports whether a phrase unambiguously refers to the
// customer service desk, closures of which don't affect any schedule and
// are ignored ("Customer Service is closed.", "Client Services Desk").
func serviceDeskPhrase(s string) bool {
	toks := tokens(s)
	if len(toks) == 0 {
		return false
	}
	core := false
	for _, t := range toks {
		switch t {
		case "service", "services", "desk", "desks":
			core = true
		case "customer", "customers", "client", "clients":
		default:
			return false
		}
	}
	return core
}

// closureOnly reports whether the effects carry nothing beyond a closure
// (and possibly modified hours).
func closureOnly(e Effects) bool {
	e.Closure = false
	e.ModifiedHours = false
	e.SeasonalHours = false
	return !e.any()
}

// isAmenity reports whether the phrase names a facility amenity: qualifier
// tokens leading to a core amenity noun.
func isAmenity(phrase string) bool {
	toks := tokens(phrase)
	if len(toks) > 0 && amenityCore[toks[len(toks)-1]] {
		return true
	}
	for i, t := range toks {
		if amenityCore[t] {
			return true
		}
		if !amenityQualifier[t] || i >= 3 {
			return false
		}
	}
	return false
}

// amenityName trims an amenity phrase to its leading noun phrase (through
// the first core noun), unless the whole phrase ends with a core noun
// ("roger senecal arena").
func amenityName(phrase string) string {
	toks := tokens(phrase)
	if len(toks) > 0 && amenityCore[toks[len(toks)-1]] {
		return strings.Join(toks, " ")
	}
	for i, t := range toks {
		if amenityCore[t] {
			return strings.Join(toks[:i+1], " ")
		}
	}
	return strings.Join(toks, " ")
}

// subjectIsFacility reports whether a closure subject names the facility
// itself: either generic facility words only, or sharing a distinctive
// (non-generic) token with the facility name.
func subjectIsFacility(subject, facName string) bool {
	st := tokens(subject)
	if len(st) == 0 {
		return false
	}
	generic := true
	for _, t := range st {
		if !genericFacility[t] {
			generic = false
			break
		}
	}
	if generic {
		return true
	}
	ft := tokenSet(facName)
	for _, t := range st {
		if ft[t] && !genericFacility[t] {
			return true
		}
	}
	return false
}

func setKeyword(e *Effects, kw string) {
	switch kw {
	case "cancelled", "canceled":
		e.Cancelled = true
	case "added":
		e.Added = true
	case "closed":
		e.Closure = true
	}
}

func countEffects(stats map[string]int, e Effects) {
	for k, v := range map[string]bool{
		"cancelled": e.Cancelled, "added": e.Added, "timeChange": e.TimeChange,
		"closure": e.Closure, "seasonalHours": e.SeasonalHours,
		"modifiedHours": e.ModifiedHours, "movedTo": e.MovedTo != "",
		"changedTo": e.ChangedTo != "", "restriction": e.Restriction != "",
		"seeSchedule": e.SeeSchedule != "",
	} {
		if v {
			stats["effect/"+k]++
		}
	}
}

func toDateSpan(spec *dateSpec, openEnded bool) *DateSpan {
	if spec == nil || spec.empty() {
		if openEnded {
			return &DateSpan{OpenEnded: true}
		}
		return nil
	}
	ds := &DateSpan{OpenEnded: spec.OpenEnded || openEnded}
	for _, d := range spec.Dates {
		ds.Dates = append(ds.Dates, schema.MakeDateFromGo(d))
	}
	if !spec.From.IsZero() {
		ds.From = schema.MakeDateFromGo(spec.From)
	}
	if !spec.To.IsZero() {
		ds.To = schema.MakeDateFromGo(spec.To)
	}
	ds.Weekdays = append(ds.Weekdays, spec.Weekdays...)
	return ds
}

func iso(t time.Time) string {
	return t.Format("2006-01-02")
}

func dedupeStrings(s []string) []string {
	slices.Sort(s)
	return slices.Compact(s)
}

// slotInfo is one concrete schedule slot an item might refer to.
type slotInfo struct {
	label string
	r     schema.ClockRange
	wd    time.Weekday
	hasWd bool
	fixed schema.Date // non-zero for fixed-date (holiday) slots
}

// gatherSlots collects the matched activities' slots that could fall on the
// spec's dates (or weekdays; all slots when no dates resolved).
func gatherSlots(acts []*actEntry, spec *dateSpec) []slotInfo {
	dates := []time.Time(nil)
	wds := map[time.Weekday]bool{}
	all := true
	if spec != nil {
		dates = spec.allDates(45)
		for _, d := range dates {
			wds[d.Weekday()] = true
		}
		for _, wd := range spec.Weekdays {
			wds[wd] = true
		}
		all = len(wds) == 0
	}
	var slots []slotInfo
	for _, e := range acts {
		for _, ref := range e.refs {
			sched := ref.Schedule()
			er, erOK := sched.ComputeEffectiveDateRange()
			for tm := range ref.Times() {
				r, ok := tm.GetRange()
				if !ok {
					continue
				}
				if sd, ok := tm.SingleDate(); ok {
					if all || slices.ContainsFunc(dates, func(d time.Time) bool {
						return schema.MakeDateFromGo(d)/10 == sd/10
					}) {
						slots = append(slots, slotInfo{label: fmt.Sprintf("%s %s", sd, r), r: r, fixed: sd})
					}
					continue
				}
				wd, ok := tm.GetWeekday()
				if !ok {
					continue
				}
				if !all {
					if !wds[wd] {
						continue
					}
					if len(dates) > 0 && erOK && !slices.ContainsFunc(dates, func(d time.Time) bool {
						return d.Weekday() == wd && !dateOutsideRange(er, d)
					}) {
						continue
					}
				}
				slots = append(slots, slotInfo{label: fmt.Sprintf("%s %s", wd, r), r: r, wd: wd, hasWd: true})
			}
		}
	}
	return slots
}

// dateOutsideRange reports whether d is excluded by the schedule's effective
// range (negative-only semantics: a range can only rule a date out).
func dateOutsideRange(er schema.DateRange, d time.Time) bool {
	x := schema.MakeDateFromGo(d) / 10
	if !er.From.IsZero() && x < er.From/10 {
		return true
	}
	if !er.To.IsZero() && x > er.To/10 {
		return true
	}
	return false
}

const (
	relExact     = "exact"
	relWithin    = "within"
	relCovers    = "covers"
	relOverlaps  = "overlaps"
	relNovel     = "novel"
	relNone      = "none"
	relUnchecked = "unchecked"
)

func relRank(rel string) int {
	switch rel {
	case relExact:
		return 4
	case relWithin:
		return 3
	case relCovers:
		return 2
	case relOverlaps:
		return 1
	}
	return 0
}

// clockRelation computes how a clock range relates to the slots, and which
// slots it touches.
func clockRelation(c schema.ClockRange, slots []slotInfo) (string, []slotInfo) {
	rel := relNone
	var touched []slotInfo
	for _, s := range slots {
		var r string
		switch {
		case s.r == c:
			r = relExact
		case s.r.Start <= c.Start && c.End <= s.r.End:
			r = relWithin
		case c.Start <= s.r.Start && s.r.End <= c.End:
			r = relCovers
		case c.Start < s.r.End && s.r.Start < c.End:
			r = relOverlaps
		default:
			continue
		}
		touched = append(touched, s)
		if relRank(r) > relRank(rel) {
			rel = r
		}
	}
	return rel, touched
}

func slotLabels(slots []slotInfo) []string {
	var out []string
	for _, s := range slots {
		out = append(out, s.label)
	}
	return dedupeStrings(out)
}

// explode enumerates the concrete sessions the spec's dates select from the
// slots (nil when the dates can't be enumerated).
func explode(spec *dateSpec, slots []slotInfo) []sessKey {
	if spec == nil {
		return nil
	}
	var out []sessKey
	for _, d := range spec.allDates(45) {
		dd := schema.MakeDateFromGo(d) / 10
		for _, s := range slots {
			switch {
			case s.fixed != 0 && s.fixed/10 == dd, s.hasWd && s.wd == d.Weekday():
				out = append(out, sessKey{date: schema.MakeDateFromGo(d), start: int(s.r.Start), end: int(s.r.End)})
			}
		}
	}
	return dedupeSess(out)
}

// explodeClock enumerates sessions for an explicit clock range on the spec's
// dates (added times).
func explodeClock(spec *dateSpec, r schema.ClockRange) []sessKey {
	if spec == nil {
		return nil
	}
	var out []sessKey
	for _, d := range spec.allDates(45) {
		out = append(out, sessKey{date: schema.MakeDateFromGo(d), start: int(r.Start), end: int(r.End)})
	}
	return dedupeSess(out)
}

func dedupeSess(s []sessKey) []sessKey {
	slices.SortFunc(s, func(a, b sessKey) int {
		if a.date != b.date {
			return int(a.date - b.date)
		}
		if a.start != b.start {
			return a.start - b.start
		}
		return a.end - b.end
	})
	return slices.Compact(s)
}

// checkDateInSchedules marks activity-scoped notices whose resolved dates
// fall outside every schedule range the matched activities appear in.
func (b *blockCtx) checkDateInSchedules(n *notice, spec *dateSpec, acts []*actEntry) {
	if spec == nil {
		return
	}
	dates := spec.allDates(45)
	if len(dates) == 0 {
		return
	}
	sawRange, inside := false, false
	for _, e := range acts {
		for _, ref := range e.refs {
			er, ok := ref.Schedule().ComputeEffectiveDateRange()
			if !ok {
				return // an unbounded schedule; can't exclude anything
			}
			sawRange = true
			for _, d := range dates {
				if !dateOutsideRange(er, d) {
					inside = true
				}
			}
		}
	}
	if sawRange && !inside {
		n.Ambiguities = append(n.Ambiguities, ambDateOutsideSched)
	}
}

// maybeDisambiguate narrows a multiple-candidate activity match when exactly
// one candidate has an exact slot for the item's time on the item's dates.
func (b *blockCtx) maybeDisambiguate(n *notice, spec *dateSpec, clocks []clockMention, acts []*actEntry) []*actEntry {
	if n.Scope.MatchQuality != matchMultiple || len(clocks) != 1 || n.Effects.Added {
		return acts
	}
	var hit []*actEntry
	for _, e := range acts {
		slots := gatherSlots([]*actEntry{e}, spec)
		for _, c := range clocks[0].Cands {
			if rel, _ := clockRelation(c, slots); rel == relExact {
				hit = append(hit, e)
				break
			}
		}
	}
	if len(hit) == 1 {
		n.Scope.Activities = actNames(hit)
		n.Scope.MatchQuality = matchFuzzy
		n.Ambiguities = slices.DeleteFunc(n.Ambiguities, func(s string) bool { return s == ambActivityMultiple })
		n.Ambiguities = append(n.Ambiguities, ambTimeDisambiguated)
		return hit
	}
	return acts
}

// emitTimes emits the notice once per clock mention (or once with none),
// without slot validation (no activity scope).
func (b *blockCtx) emitTimes(n *notice, spec *dateSpec, clocks []clockMention, sessOut *[]sessKey, emit func()) {
	b.emitTimesWithSlots(n, spec, clocks, nil, sessOut, emit)
}

// emitTimesWithSlots emits the notice once per clock mention, attaching the
// best-relating candidate interpretation, its slot relation, and the
// concrete sessions the notice descends to.
func (b *blockCtx) emitTimesWithSlots(n *notice, spec *dateSpec, clocks []clockMention, acts []*actEntry, sessOut *[]sessKey, emit func()) {
	if len(clocks) == 0 {
		*sessOut = nil
		if len(acts) > 0 && (n.Effects.Cancelled || n.Effects.Closure) {
			// whole-activity cancellation: all its slots on those dates
			if slots := gatherSlots(acts, spec); len(slots) > 0 {
				n.Time = &TimeAssoc{Relation: relCovers, Slots: slotLabels(slots)}
				*sessOut = explode(spec, slots)
			}
		}
		emit()
		return
	}
	var slots []slotInfo
	if len(acts) > 0 {
		slots = gatherSlots(acts, spec)
	}
	base := *n
	for _, cm := range clocks {
		nn := base
		nn.Ambiguities = slices.Clone(base.Ambiguities)
		best := cm.Cands[0]
		rel, touched := relUnchecked, []slotInfo(nil)
		if len(acts) > 0 {
			rel, touched = clockRelation(best, slots)
			for _, c := range cm.Cands[1:] {
				if r, t := clockRelation(c, slots); relRank(r) > relRank(rel) {
					best, rel, touched = c, r, t
				}
			}
		}
		if cm.Inferred {
			if len(cm.Cands) == 1 || (rel != relNone && len(acts) > 0) {
				nn.Ambiguities = append(nn.Ambiguities, ambMeridiemInferred)
			} else {
				nn.Ambiguities = append(nn.Ambiguities, ambMeridiemAmbiguous)
			}
		}
		if len(acts) > 0 {
			switch {
			case nn.Effects.Added && rel == relExact:
				nn.Ambiguities = append(nn.Ambiguities, ambAddedScheduled)
			case nn.Effects.Added && rel == relNone:
				rel = relNovel
			case rel == relNone && (nn.Effects.Cancelled || nn.Effects.Closure):
				nn.Ambiguities = append(nn.Ambiguities, ambNoSlotOverlap)
			}
		}
		nn.Time = &TimeAssoc{
			Text:      cm.Text,
			StartMin:  int(best.Start),
			EndMin:    int(best.End),
			OpenStart: cm.OpenStart,
			OpenEnd:   cm.OpenEnd,
			Relation:  rel,
			Slots:     slotLabels(touched),
		}
		switch {
		case nn.Effects.Added:
			// the added time itself is the session
			*sessOut = explodeClock(spec, best)
		case len(touched) > 0:
			*sessOut = explode(spec, touched)
		default:
			*sessOut = nil
		}
		if len(acts) > 0 {
			b.out.Stats["time/relation/"+rel]++
		}
		nn.Ambiguities = dedupeStrings(nn.Ambiguities)
		save := *n
		*n = nn
		emit()
		*n = save
	}
}

var _ = ottrecidx.TZ // keep the import for date helpers
