// Package enrich derives structured schedule-change/special-hours records
// from the freeform HTML fields of an ottrec dataset version. It is
// deliberately conservative: effect flags are only set when backed by a
// trigger word, and anything short of certain carries an ambiguity marker
// instead of a guess (see notes/approaches.md). Every fragment of source
// text is accounted for by exactly one output object.
package enrich

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ottrec/website/pkg/ottrecidx"
)

// Ambiguity markers beyond the date/clock ones.
const (
	ambActivityUnmatched    = "activity-unmatched"
	ambActivityMultiple     = "activity-multiple-candidates"
	ambClassUnmatched       = "class-unmatched"
	ambNoSlotOverlap        = "no-slot-overlap"
	ambAddedScheduled       = "added-time-already-scheduled"
	ambTimeDisambiguated    = "activity-time-disambiguated"
	ambHeadUnparsed         = "head-unparsed"
	ambDateOnlyItem         = "date-only-item"
	ambNoSubject            = "no-subject"
	ambHoursContext         = "hours-context-unknown"
	ambPossibleActivityTime = "possible-activity-time"
	ambFreeformItem         = "freeform-item"
	ambDateOutsideSched     = "date-outside-schedule"
	ambActivityTypo         = "activity-typo-match"
	ambOtherGroup           = "matched-other-group"
	ambClassTitlePartial    = "class-title-partial"
	ambTimeChangeUnparsed   = "time-change-unparsed"
)

const producedByParser = "parser"

// sessKey identifies one concrete session: a date plus clock range.
type sessKey struct {
	date       string // ISO
	start, end int    // minutes from midnight
}

// rec is one extracted fragment before placement.
type rec struct {
	kind        string // notice | unparsed | ignored
	reason      string
	n           notice
	blockHash   string
	seq         int
	off         [2]int
	id          string
	sessions    []sessKey
	novel       bool
	sources     []string
	duplicateOf []string
}

// EnrichVersion builds the enrichment output for one dataset version.
func EnrichVersion(version string, data ottrecidx.DataRef) *Output {
	out := &Output{
		Version:   version,
		Generated: time.Now(),
		Stats:     map[string]int{},
	}
	for fac := range data.Facilities() {
		fc := &facCtx{out: out, fac: fac}
		fc.anchor = fac.GetSourceDate()
		if fc.anchor.IsZero() {
			fc.anchor = data.Index().Updated()
		}
		for grp := range fac.ScheduleGroups() {
			fc.matchers = append(fc.matchers, newGroupMatcher(grp))
		}
		for _, m := range fc.matchers {
			fc.processBlock(m.grp.GetScheduleChangesHTML(), "schedule_changes", m)
		}
		fc.processBlock(fac.GetSpecialHoursHTML(), "special_hours", nil)
		fc.processBlock(fac.GetNotificationsHTML(), "notifications", nil)
		fc.collapse()
		fc.place()
	}
	return out
}

type facCtx struct {
	out      *Output
	fac      ottrecidx.FacilityRef
	anchor   time.Time
	matchers []*groupMatcher
	recs     []rec
}

type blockCtx struct {
	*facCtx
	grp       *groupMatcher // nil for facility-level sources
	source    string
	blockHash string
	seq       int
}

// add appends one extracted fragment, assigning its sequence and id.
func (b *blockCtx) add(kind, reason string, n notice, off [2]int, sessions []sessKey, novel bool) {
	n.Source = b.source
	if b.grp != nil {
		n.Group = b.grp.label
	}
	r := rec{
		kind: kind, reason: reason, n: n,
		blockHash: b.blockHash, seq: b.seq, off: off,
		sessions: sessions, novel: novel,
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%s\x00%d", b.fac.GetName(), n.Group, b.blockHash, b.seq))
	r.id = hex.EncodeToString(sum[:6])
	b.seq++
	b.out.Stats["object/"+kind]++
	if reason != "" {
		b.out.Stats["object/"+kind+"/"+reason]++
	}
	b.recs = append(b.recs, r)
}

func (b *blockCtx) addIgnored(reason, text, html string, off [2]int, st *walkState, ambig []string) {
	b.add("ignored", reason, notice{
		Section: st.section, RawText: text, RawHTML: html, Ambiguities: ambig,
	}, off, nil, false)
}

// walkState is the running context while walking a block's parts.
type walkState struct {
	section string
	head    *dateSpec
	headRaw string
	// closureContext is set by an intro line like "The facility is not
	// available on the following dates ...:" so bare date items that follow
	// inherit the closure instead of looking like hours.
	closureContext bool
}

func (fc *facCtx) processBlock(blockHTML, source string, grp *groupMatcher) {
	if strings.TrimSpace(blockHTML) == "" {
		return
	}
	fc.out.Stats["block/"+source]++
	sum := sha256.Sum256([]byte(blockHTML))
	b := &blockCtx{facCtx: fc, grp: grp, source: source, blockHash: hex.EncodeToString(sum[:8])}
	parts, ok := splitBlock(blockHTML)
	if !ok {
		b.add("unparsed", "parse-error", notice{RawHTML: blockHTML, RawText: normText(blockHTML)}, [2]int{0, len(blockHTML)}, nil, false)
		return
	}
	st := &walkState{}
	for _, part := range parts {
		switch part.Kind {
		case "heading":
			st.section = part.Text
			st.head, st.headRaw = nil, ""
			st.closureContext = false
			// a heading can itself be the date context ("Wednesday, July 1",
			// "Notice: December 5")
			ht := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(part.Text, "Notice:"), "Notice"))
			if spec, rest, ok := parseLeadingDate(ht, fc.anchor); ok && restIsTrivial(rest) {
				st.head, st.headRaw = &spec, part.Text
			}
			b.addIgnored("heading", part.Text, part.HTML, part.Off, st, nil)
		case "para":
			for line := range strings.SplitSeq(part.Text, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				if spec, rest, ok := parseLeadingDate(line, fc.anchor); ok && restIsTrivial(rest) {
					st.head, st.headRaw = &spec, line
					b.addIgnored("date-context", line, part.HTML, part.Off, st, nil)
					continue
				}
				b.processItem(st, line, part.HTML, part.Off, part.Links, nil)
			}
		case "list":
			for _, li := range part.Items {
				b.processLi(st, li)
			}
			st.closureContext = false
		}
	}
}

func (b *blockCtx) processLi(st *walkState, li liNode) {
	lines := splitLines(li.Head)
	if len(li.Items) == 0 {
		// leaf: possibly "date<br>item<br>item"
		local := *st
		for i, line := range lines {
			if i == 0 && len(lines) > 1 {
				if spec, rest, ok := parseLeadingDate(line, b.anchor); ok && restIsTrivial(rest) {
					local.head, local.headRaw = &spec, line
					b.addIgnored("date-context", line, li.HeadHTML, li.Off, &local, nil)
					continue
				}
			}
			b.processItem(&local, line, li.HeadHTML, li.Off, li.Links, nil)
		}
		return
	}

	head := ""
	if len(lines) > 0 {
		head = lines[0]
	}
	if spec, rest, ok := parseLeadingDate(head, b.anchor); ok && restIsTrivial(rest) {
		// date-headed: children are the items
		local := *st
		local.head, local.headRaw = &spec, head
		b.addIgnored("date-context", head, "", li.Off, &local, nil)
		for _, line := range lines[1:] {
			b.processItem(&local, line, li.HeadHTML, li.Off, li.Links, nil)
		}
		for _, sub := range li.Items {
			b.processLi(&local, sub)
		}
		return
	} else if len(spec.Ambig) > 0 {
		// garbled date head: children still get the head text, marked
		local := *st
		local.head, local.headRaw = &spec, head
		b.addIgnored("date-context", head, "", li.Off, &local, spec.Ambig)
		for _, sub := range li.Items {
			b.processLi(&local, sub)
		}
		return
	}

	// inverted form: a statement whose children are all dates. Single dates
	// merge into one notice; each range child gets its own.
	var singles dateSpec
	var ranges []dateSpec
	var childIgnored []liNode
	allDates := len(li.Items) > 0
	for _, sub := range li.Items {
		if len(sub.Items) > 0 {
			allDates = false
			break
		}
		spec, rest, ok := parseLeadingDate(sub.Head, b.anchor)
		if !ok || !restIsTrivial(rest) {
			allDates = false
			break
		}
		childIgnored = append(childIgnored, sub)
		if !spec.From.IsZero() || spec.OpenEnded {
			ranges = append(ranges, spec)
			continue
		}
		singles.Dates = append(singles.Dates, spec.Dates...)
		singles.Ambig = append(singles.Ambig, spec.Ambig...)
		singles.Raw = strings.TrimSpace(singles.Raw + " " + spec.Raw)
	}
	if allDates && (!singles.empty() || len(ranges) > 0) {
		for _, sub := range childIgnored {
			b.addIgnored("date-context", sub.Head, sub.HeadHTML, sub.Off, st, nil)
		}
		specs := ranges
		if !singles.empty() {
			specs = append(specs, singles)
		}
		for i := range specs {
			local := *st
			local.head, local.headRaw = &specs[i], specs[i].Raw
			b.processItem(&local, head, li.HeadHTML, li.Off, li.Links, nil)
		}
		return
	}

	// unrecognized head: emit it as an item and process children normally
	if head != "" {
		b.processItem(st, head, li.HeadHTML, li.Off, li.Links, []string{ambHeadUnparsed})
	}
	for _, sub := range li.Items {
		b.processLi(st, sub)
	}
}

func splitLines(s string) []string {
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// collapse folds special_hours notices that duplicate a group's
// schedule_changes notice into ignored/duplicate stubs pointing at the
// surviving objects, which gain "special_hours" in Sources.
func (fc *facCtx) collapse() {
	groupKeys := map[string][]*rec{}
	for i := range fc.recs {
		r := &fc.recs[i]
		if r.kind == "notice" && r.n.Source == "schedule_changes" {
			for _, k := range dedupeKeys(&r.n) {
				groupKeys[k] = append(groupKeys[k], r)
			}
		}
	}
	if len(groupKeys) == 0 {
		return
	}
	for i := range fc.recs {
		r := &fc.recs[i]
		if r.kind != "notice" || r.n.Source != "special_hours" {
			continue
		}
		for _, k := range dedupeKeys(&r.n) {
			survivors, ok := groupKeys[k]
			if !ok {
				continue
			}
			r.kind = "ignored"
			r.reason = "duplicate"
			r.sessions = nil
			for _, s := range survivors {
				r.duplicateOf = append(r.duplicateOf, s.id)
				if !slices.Contains(s.sources, "special_hours") {
					s.sources = []string{"schedule_changes", "special_hours"}
				}
			}
			fc.out.Stats["dedupe/special-duplicates-changes"]++
			fc.out.Stats["object/notice"]--
			fc.out.Stats["object/ignored"]++
			fc.out.Stats["object/ignored/duplicate"]++
			break
		}
	}
}

// dedupeKeys builds comparison keys for a notice: dates + effects + scope.
// Broad scopes (group/class/facility) share a key so the city's merged
// facility-level phrasing ("skating and ice sports") matches the per-group
// copies ("skating").
func dedupeKeys(n *notice) []string {
	var d string
	if n.Dates != nil {
		d = strings.Join(n.Dates.Dates, ",") + "|" + n.Dates.From + "|" + n.Dates.To + "|" + fmt.Sprint(n.Dates.OpenEnded) + "|" + strings.Join(n.Dates.Weekdays, ",")
	}
	e := n.Effects
	e.SeeURL = ""
	eff := fmt.Sprintf("%+v", e)
	var sc string
	switch n.Scope.Level {
	case "group", "class", "facility":
		sc = "broad"
	case "activity":
		var t string
		if n.Time != nil {
			t = fmt.Sprintf("%d-%d", n.Time.StartMin, n.Time.EndMin)
		}
		sc = "act:" + strings.Join(n.Scope.Activities, ",") + "@" + t
	case "amenity":
		sc = "amenity:" + foldText(n.Scope.Phrase)
	default:
		sc = "none:" + foldText(n.RawText)
	}
	return []string{d + "||" + eff + "||" + sc}
}

// place converts this facility's recs into flat Objects plus the reference
// hierarchy, descending each notice to the most specific guaranteed level.
func (fc *facCtx) place() {
	if len(fc.recs) == 0 {
		return
	}
	facName := fc.fac.GetName()
	f := Facility{Name: facName}

	// group/activity builders, in stable first-reference order
	actsByGroup := map[string]map[string]bool{}
	for _, m := range fc.matchers {
		set := map[string]bool{}
		for _, e := range m.acts {
			set[e.name] = true
		}
		actsByGroup[m.label] = set
	}
	type actB struct {
		novel    bool
		objects  []string
		sessions map[sessKey]*Session
		sessOrd  []sessKey
	}
	type grpB struct {
		objects []string
		acts    map[string]*actB
		actOrd  []string
	}
	groups := map[string]*grpB{}
	var grpOrd []string
	grp := func(label string) *grpB {
		g := groups[label]
		if g == nil {
			g = &grpB{acts: map[string]*actB{}}
			groups[label] = g
			grpOrd = append(grpOrd, label)
		}
		return g
	}
	act := func(label, name string, novel bool) *actB {
		g := grp(label)
		a := g.acts[name]
		if a == nil {
			a = &actB{sessions: map[sessKey]*Session{}}
			g.acts[name] = a
			g.actOrd = append(g.actOrd, name)
		}
		if novel {
			a.novel = true
		}
		return a
	}

	for i := range fc.recs {
		r := &fc.recs[i]
		obj := fc.buildObject(r)
		fc.out.Objects = append(fc.out.Objects, obj)

		if r.kind != "notice" {
			// structural/ignored/unparsed fragments live where posted
			if r.n.Group != "" {
				g := grp(r.n.Group)
				g.objects = append(g.objects, r.id)
			} else {
				f.Objects = append(f.Objects, r.id)
			}
			continue
		}

		sc := r.n.Scope
		switch sc.Level {
		case "activity", "class":
			if sc.MatchQuality == matchMultiple {
				// candidates only; stays at group level
				for _, gl := range targetGroups(sc, r.n.Group) {
					g := grp(gl)
					g.objects = append(g.objects, r.id)
				}
				if len(targetGroups(sc, r.n.Group)) == 0 {
					f.Objects = append(f.Objects, r.id)
				}
				continue
			}
			gls := targetGroups(sc, r.n.Group)
			placedAny := false
			for _, name := range sc.Activities {
				for _, gl := range gls {
					if !actsByGroup[gl][name] && !r.novel {
						continue
					}
					a := act(gl, name, r.novel)
					placedAny = true
					if len(r.sessions) > 0 {
						for _, sk := range r.sessions {
							s := a.sessions[sk]
							if s == nil {
								s = &Session{Date: sk.date, StartMin: sk.start, EndMin: sk.end}
								a.sessions[sk] = s
								a.sessOrd = append(a.sessOrd, sk)
							}
							if r.n.Effects.Added {
								s.Added = append(s.Added, r.id)
							} else {
								s.Objects = append(s.Objects, r.id)
							}
						}
					} else {
						a.objects = append(a.objects, r.id)
					}
				}
			}
			if !placedAny {
				// no group/activity node to hang it on; keep at group/facility
				if len(gls) > 0 {
					for _, gl := range gls {
						g := grp(gl)
						g.objects = append(g.objects, r.id)
					}
				} else {
					f.Objects = append(f.Objects, r.id)
				}
			}
		case "group":
			gls := targetGroups(sc, r.n.Group)
			if len(gls) == 0 {
				f.Objects = append(f.Objects, r.id)
			}
			for _, gl := range gls {
				g := grp(gl)
				g.objects = append(g.objects, r.id)
			}
		default: // facility, amenity, none
			f.Objects = append(f.Objects, r.id)
		}
	}

	// finalize the tree, matcher order first for groups
	var order []string
	for _, m := range fc.matchers {
		if _, ok := groups[m.label]; ok {
			order = append(order, m.label)
		}
	}
	for _, gl := range grpOrd {
		if !slices.Contains(order, gl) {
			order = append(order, gl)
		}
	}
	for _, gl := range order {
		gb := groups[gl]
		g := Group{Label: gl, Objects: gb.objects}
		for _, name := range gb.actOrd {
			ab := gb.acts[name]
			a := Activity{Name: name, Novel: ab.novel, Objects: ab.objects}
			slices.SortFunc(ab.sessOrd, func(x, y sessKey) int {
				if x.date != y.date {
					return strings.Compare(x.date, y.date)
				}
				if x.start != y.start {
					return x.start - y.start
				}
				return x.end - y.end
			})
			for _, sk := range ab.sessOrd {
				a.Sessions = append(a.Sessions, *ab.sessions[sk])
			}
			g.Activities = append(g.Activities, a)
		}
		f.Groups = append(f.Groups, g)
	}
	fc.out.Facilities = append(fc.out.Facilities, f)
}

// targetGroups picks the group labels a scoped notice applies to.
func targetGroups(sc scope, posted string) []string {
	if len(sc.Groups) > 0 {
		return sc.Groups
	}
	if posted != "" {
		return []string{posted}
	}
	return nil
}

func (fc *facCtx) buildObject(r *rec) Object {
	obj := Object{
		ID: r.id, Kind: r.kind, Reason: r.reason,
		Facility: fc.fac.GetName(), Source: r.n.Source, SourceGroup: r.n.Group,
		Sources: r.sources, DuplicateOf: r.duplicateOf,
		BlockHash: r.blockHash, Seq: r.seq,
		Section: r.n.Section, DateText: r.n.DateText,
		RawHTML: r.n.RawHTML, RawText: r.n.RawText,
		Dates: r.n.Dates, Time: r.n.Time,
		MatchQuality: r.n.Scope.MatchQuality,
		Phrase:       r.n.Scope.Phrase,
		Amenity:      r.n.Scope.Amenity,
		Ambiguities:  r.n.Ambiguities,
	}
	if r.off != [2]int{} {
		obj.HTMLOffset = []int{r.off[0], r.off[1]}
	}
	if r.kind == "notice" {
		obj.ProducedBy = producedByParser
		if r.n.Effects.any() {
			e := r.n.Effects
			obj.Effects = &e
		}
		if r.n.Scope.MatchQuality == matchMultiple {
			obj.Candidates = r.n.Scope.Activities
		}
	}
	return obj
}
