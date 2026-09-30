// Package enrichidx provides indexed, trust-rule-aware access to an
// enrichment [epb.Output] for consumers joining it back to the dataset it was
// derived from (e.g. the website /today page). Lookups are keyed by the raw
// dataset identifiers the output carries: facility name, group label, raw
// activity label, and concrete session date + clock range.
//
// Zero values are safe everywhere and mean "no enrichment": all queries on
// them return the conservative answer (no warning suppression, no cancels, no
// additions), so consumers can treat enrichment as a progressive enhancement.
//
// The trust rules from the enrichment notes are encoded here rather than at
// call sites: tree position is the guarantee, only sufficiently validated
// session refs report cancellation, unknown kinds/effects (from a newer
// schema) can never rule anything out, and amenity-scoped notices never claim
// schedule effects. What an ambiguity marker costs is one row of markerPolicy:
// stated, likely (one tier down) or warn; an unknown marker is warn.
package enrichidx

import (
	"slices"
	"strings"
	"time"

	epb "github.com/ottrec/data-enrichment/schema"
	"github.com/ottrec/scraper/schema"
)

// Warning classifies what a facility's or group's enrichment objects imply
// for sessions in a date window, ordered by severity.
type Warning int

const (
	// WarnNone means every object either is ignorable or definitely does not
	// apply within the window.
	WarnNone Warning = iota
	// WarnNotice means informational content may apply within the window,
	// but nothing that directly affects the schedule or facility hours
	// (amenity closures, seasonal-range notes, effectless notices).
	WarnNotice
	// WarnChanges means content that may affect the schedule or facility
	// hours may apply within the window (or content the parser or this
	// consumer could not classify, which cannot be ruled out).
	WarnChanges
)

// AddedSession is a session added by a notice (not part of the published
// schedule).
type AddedSession struct {
	ActivityLabel string // raw activity label, or the notice's subject phrase when Novel
	Novel         bool   // the activity itself is not in the published schedule
	Date          schema.Date
	Start, End    int // minutes from midnight; End may exceed 1440
	// Uncertain means the notice carries a marker the policy table rates
	// likely rather than stated (a typo match, a wrong-group posting): the
	// session is probably real, and a consumer should say so.
	Uncertain bool
}

// SessionNotices is what validated session-level notices say about one
// published session. The zero value means no notices.
type SessionNotices struct {
	// Cancelled means a notice cancels or closes the whole published slot,
	// and the notice carries no marker the policy table rates below stated.
	Cancelled bool
	// LikelyCancelled is Cancelled for a notice the policy table rates likely
	// (its match or date rests on an inference the parser marked): the same
	// tier as a scope-implied cancellation, never a strike. Both can be set
	// when two notices reach the session; Cancelled is the stronger answer.
	LikelyCancelled bool
	// TimeChange means a time-change notice affects this session ("will end
	// at 6 pm", "schedule change").
	TimeChange bool
	// NewStart/NewEnd are the session's derived effective time, valid only
	// when NewTime: a single-ended time-change mention strictly inside the
	// slot trims it ("will end at 6 pm" against 1 to 7 pm gives 1 to 6 pm).
	// Anything less clear-cut only sets TimeChange.
	NewStart, NewEnd int
	NewTime          bool
}

// trust is the most this consumer does with an object, given its ambiguity
// markers: the weakest of its markers' rows in markerPolicy. A marker with no
// row (a newer parser than this consumer) is trustWarn, which is what the
// schema says an unrecognized marker means.
type trust int

const (
	trustStated trust = iota // strikes sessions, cancels a scope as stated, adds sessions
	trustLikely              // the same, one tier down: LikelyCancelled, the implied scope tier, Uncertain adds
	trustWarn                // warns only; never strikes, cancels a scope or adds
)

// markerPolicy has a row for every marker the parser can emit
// (enrich.Markers; TestMarkerPolicyCoversParser keeps the two in step). A
// row marked "cannot" is a marker that never reaches a strike or an add; the
// row records what it means rather than changing anything.
var markerPolicy = map[string]trust{
	// dates
	"date-unparsed":         trustWarn,   // no date resolved (cannot)
	"date-garbled":          trustLikely, // a garbled range repaired from its ends, both weekdays agreeing
	"weekday-mismatch":      trustLikely, // the written weekday fits no year: a typo'd weekday or a stale year
	"date-year-unconfirmed": trustLikely, // half a year or more from the anchor, the year not confirmed by the weekday alone
	"date-year-ambiguous":   trustWarn,   // two years fit the weekday (cannot)
	"date-range-invalid":    trustWarn,   // the range did not resolve (cannot)
	"date-month-only":       trustStated, // an end bounded by its month; see monthOnlyEnd
	"date-end-unstated":     trustLikely, // a start with no end found; the open end is the parser's, not the city's
	"date-outside-schedule": trustStated, // no schedule listing the activity covers the date: never a strike, and where an add is expected
	"date-only-item":        trustWarn,   // a date with nothing said about it (cannot)
	// clocks
	"meridiem-inferred":  trustStated, // the only reading, or the one a slot confirms
	"meridiem-ambiguous": trustWarn,   // several readings fit and no slot decides
	// subjects
	"activity-unmatched":           trustWarn,   // (cannot)
	"activity-multiple-candidates": trustWarn,   // (cannot)
	"activity-typo-match":          trustLikely, // one edit away from a label
	"activity-time-disambiguated":  trustStated, // one of several candidates, the only one with the exact slot (invariant 2's deterministic exception)
	"activity-narrowed-to-amenity": trustStated, // narrowing off a row is the conservative direction
	"matched-other-group":          trustLikely, // posted under another group
	"class-unmatched":              trustWarn,   // (cannot)
	"closed-part-unmatched":        trustWarn,   // (cannot)
	"class-title-partial":          trustStated, // the class names part of the title of the group it was posted under
	"class-matched-by-vocabulary":  trustLikely, // a class the page never spells, from the ice taxonomy
	"skating-widened-to-window":    trustLikely, // siblings the notice does not name
	"dog-swim-session":             trustStated, // a classification, not a doubt
	"facility-except-programs":     trustLikely, // a facility closure with an exception: closed to visitors, maybe not to its programs
	// structure
	"head-unparsed":                trustLikely, // the item's head was not understood
	"no-subject":                   trustWarn,   // (cannot)
	"hours-context-unknown":        trustWarn,   // (cannot)
	"possible-activity-time":       trustWarn,   // (cannot)
	"freeform-item":                trustWarn,   // (cannot)
	"no-slot-overlap":              trustWarn,   // a cancellation whose time meets no slot
	"added-time-already-scheduled": trustWarn,   // an added time the schedule already has
}

// objectTrust is the weakest trust among the object's markers.
func objectTrust(o *epb.Object) trust {
	t := trustStated
	for _, m := range o.GetAmbiguities() {
		p, ok := markerPolicy[m]
		if !ok {
			p = trustWarn
		}
		t = max(t, p)
	}
	return t
}

// Ref is an indexed enrichment output. The zero Ref is valid and empty.
type Ref struct {
	facilities map[string]*facility
}

// FacilityRef is one facility's enrichment. The zero FacilityRef is valid and
// empty.
type FacilityRef struct {
	f *facility
}

// GroupRef is one schedule group's enrichment. The zero GroupRef is valid and
// empty.
type GroupRef struct {
	g *group
}

type facility struct {
	objects []*epb.Object
	groups  map[string]*group
}

type group struct {
	direct  []*epb.Object // objects placed at the group node itself
	objects []*epb.Object // whole subtree, deduped
	marks   map[sessKey]SessionNotices
	added   []AddedSession
}

// sessKey identifies a concrete session; day is YYYYMMDD (weekday stripped).
type sessKey struct {
	label      string
	day        int32
	start, end int32
}

// Join indexes an enrichment output. A nil output yields an empty Ref.
func Join(out *epb.Output) Ref {
	if out == nil {
		return Ref{}
	}
	byID := make(map[string]*epb.Object, len(out.GetObjects()))
	for _, o := range out.GetObjects() {
		byID[o.GetId()] = o
	}
	resolve := func(dst []*epb.Object, seen map[string]bool, ids []string) []*epb.Object {
		for _, id := range ids {
			if o := byID[id]; o != nil && !seen[id] {
				seen[id] = true
				dst = append(dst, o)
			}
		}
		return dst
	}

	r := Ref{facilities: make(map[string]*facility, len(out.GetFacilities()))}
	for _, ef := range out.GetFacilities() {
		f := &facility{groups: make(map[string]*group, len(ef.GetGroups()))}
		f.objects = resolve(nil, map[string]bool{}, ef.GetObjects())
		for _, eg := range ef.GetGroups() {
			g := &group{marks: map[sessKey]SessionNotices{}}
			seen := map[string]bool{}
			g.direct = resolve(nil, seen, eg.GetObjects())
			g.objects = slices.Clip(g.direct)
			for _, ea := range eg.GetActivities() {
				g.objects = resolve(g.objects, seen, ea.GetObjects())
				for _, es := range ea.GetSessions() {
					g.objects = resolve(g.objects, seen, es.GetObjects())
					g.objects = resolve(g.objects, seen, es.GetAdded())
					key := sessKey{
						label: ea.GetLabel(),
						day:   es.GetDate() / 10,
						start: es.GetStart(),
						end:   es.GetEnd(),
					}
					if m := sessionNotices(byID, es); m != (SessionNotices{}) {
						g.marks[key] = m
					}
					for _, id := range es.GetAdded() {
						if ok, uncertain := addsSession(byID[id]); ok {
							g.added = append(g.added, AddedSession{
								ActivityLabel: ea.GetLabel(),
								Novel:         ea.GetNovel(),
								Date:          schema.Date(es.GetDate()),
								Start:         int(es.GetStart()),
								End:           int(es.GetEnd()),
								Uncertain:     uncertain,
							})
						}
					}
				}
			}
			f.groups[eg.GetLabel()] = g
		}
		r.facilities[ef.GetName()] = f
	}
	return r
}

// OK reports whether the Ref holds an enrichment output (even an empty one),
// as opposed to being the zero "no enrichment" value.
func (r Ref) OK() bool { return r.facilities != nil }

// Facility returns the enrichment for the facility with the given raw dataset
// name. A facility with no source blocks is absent, which is the same as
// having nothing posted.
func (r Ref) Facility(name string) FacilityRef {
	return FacilityRef{f: r.facilities[name]}
}

// Group returns the enrichment for the schedule group with the given raw
// label.
func (f FacilityRef) Group(label string) GroupRef {
	if f.f == nil {
		return GroupRef{}
	}
	return GroupRef{g: f.f.groups[label]}
}

// Warning classifies the facility-scoped objects (from the facility's special
// hours and notifications) against the inclusive date window [from, to].
// Objects placed under a specific group are not included; query the group.
func (f FacilityRef) Warning(from, to schema.Date) Warning {
	if f.f == nil {
		return WarnNone
	}
	return warning(f.f.objects, from, to)
}

// Warning classifies everything associated with the group (its own objects
// and those of its activities and sessions, including objects from other
// blocks that were matched into this group) against the inclusive date window
// [from, to].
func (g GroupRef) Warning(from, to schema.Date) Warning {
	if g.g == nil {
		return WarnNone
	}
	return warning(g.g.objects, from, to)
}

// ScopeCancelled reports whether a facility-scoped whole-scope notice cancels
// or closes the facility's programming on the given date, overlapping the
// clock range [start, end) in minutes ("The facility is closed and all
// programs cancelled.", "All drop-in skating and ice sports, cancelled"). See
// [scopeCancelled] for what qualifies.
func (f FacilityRef) ScopeCancelled(date schema.Date, start, end int) bool {
	if f.f == nil {
		return false
	}
	return scopeCancelled(f.f.objects, date, start, end, false)
}

// ScopeCancelledStated is [FacilityRef.ScopeCancelled] restricted to notices
// whose own text states the cancellation, which is the difference between "the
// facility is closed and all programs cancelled" and a bare closure the scope
// only implies.
func (f FacilityRef) ScopeCancelledStated(date schema.Date, start, end int) bool {
	if f.f == nil {
		return false
	}
	return scopeCancelled(f.f.objects, date, start, end, true)
}

// ScopeCancelled reports whether a whole-scope notice placed at the group
// level cancels or closes the group's programming on the given date,
// overlapping the clock range [start, end) in minutes ("All drop-in skating,
// cancelled"). Objects that descended to the group's activities or sessions
// are not included (those report through Session).
func (g GroupRef) ScopeCancelled(date schema.Date, start, end int) bool {
	if g.g == nil {
		return false
	}
	return scopeCancelled(g.g.direct, date, start, end, false)
}

// ScopeCancelledStated is [GroupRef.ScopeCancelled] restricted to notices whose
// own text states the cancellation.
func (g GroupRef) ScopeCancelledStated(date schema.Date, start, end int) bool {
	if g.g == nil {
		return false
	}
	return scopeCancelled(g.g.direct, date, start, end, true)
}

// scopeCancelled reports whether a notice among objs is a whole-scope
// cancellation or closure that may apply on date, overlapping [start, end).
// Tree position guarantees the level; this additionally requires the subject
// to be a scope phrase ("all drop-in skating", "the facility") or absent (a
// bare dated "cancelled"/"closed" item), so activity-subject notices that
// merely failed to match (NONE, MULTIPLE) never implicate the whole scope.
//
// A cancelled effect always claims the scope (including through an amenity
// subject: "the pool is closed and all programs cancelled"). A closure-only
// notice claims it only when the parser extracted no residual subject: whole-
// scope sentences ("The facility is closed until further notice.") and bare
// dated "closed" items leave the phrase empty, while a closure of some named
// part ("The pool is closed for maintenance", "The Great Lawn ... closed")
// carries its subject and says nothing about the rest of the scope's
// programming, however the parser leveled it. The notice must also carry a
// resolved DateSpan (open-ended "until further notice" counts): an undated
// object applies to every date, which is right for the coarse Warning tier
// but would paint the whole feed red for list heads like "The facility is
// not available on the following dates:" whose dates live in the child
// items (each of which claims its own dates here). Unknown match qualities
// and effect kinds never add the claim; the Warning tier already covers all
// of the above, so anything skipped here degrades to that, never below.
//
// A scope phrase is still an inference: "all drop-in skating" was matched
// against the group's title, not each activity, so a true hit means "likely
// cancelled", not the per-session guarantee SessionNotices.Cancelled carries.
//
// stated drops the closure-only half, leaving the notices that say a
// cancellation happened rather than the ones a closure implies. Those still
// generalize from a scope phrase to its sessions, but the cancellation itself
// is the city's word and not an inference, which is what lets a consumer treat
// a whole-facility closure as cancelling the sessions under it.
//
// The object's markers can lower it: rated likely, a stated cancellation
// answers only the implied tier; rated warn, the object is left to Warning.
func scopeCancelled(objs []*epb.Object, date schema.Date, start, end int, stated bool) bool {
	for _, o := range objs {
		if o.GetKind() != epb.Object_NOTICE {
			continue
		}
		switch o.GetMatchQuality() {
		case epb.Object_SCOPE_PHRASE, epb.Object_MATCH_QUALITY_UNSPECIFIED:
		default:
			continue
		}
		var cancelled, closure bool
		for _, e := range o.GetEffects() {
			switch e.WhichEffect() {
			case epb.Effect_Cancelled_case:
				cancelled = true
			case epb.Effect_Closure_case:
				closure = true
			}
		}
		if stated && !cancelled {
			continue
		}
		if !cancelled && (!closure || o.GetAmenity() != "" || o.GetPhrase() != "") {
			continue
		}
		// a marker rated likely drops a stated cancellation to the implied
		// tier; one rated warn leaves the object to Warning
		if t := objectTrust(o); t == trustWarn || (stated && t != trustStated) {
			continue
		}
		if o.HasDates() && clockOverlaps(o, start, end) && applies(o, date, date) && !monthOnlyEnd(o, date) {
			return true
		}
	}
	return false
}

// monthOnlyEnd reports whether the object's end date was given only as a
// month ("until September 2026", marked date-month-only) and date falls in
// that month: the closure is certain up to the month before and only bounded
// within it, so it still warns there but does not strike sessions.
func monthOnlyEnd(o *epb.Object, date schema.Date) bool {
	if !slices.Contains(o.GetAmbiguities(), "date-month-only") || !o.GetDates().HasTo() {
		return false
	}
	return int(date)/1000 == int(o.GetDates().GetTo())/1000 // same YYYYMM
}

// clockOverlaps reports whether the object's extracted clock window (when it
// has one) intersects [start, end). Single-ended mentions are already stored
// as half-day windows ("closed until noon" is 0 to 720); slot-only time
// associations carry no clock and constrain nothing.
func clockOverlaps(o *epb.Object, start, end int) bool {
	if !o.HasTime() {
		return true
	}
	t := o.GetTime()
	if !t.HasStart() || !t.HasEnd() {
		return true
	}
	return int32(start) < t.GetEnd() && t.GetStart() < int32(end)
}

// SeeSchedule reports whether a facility-scoped notice deferring to another
// schedule ("See Canada Day schedule") may apply within the inclusive date
// window [from, to].
func (f FacilityRef) SeeSchedule(from, to schema.Date) bool {
	if f.f == nil {
		return false
	}
	return seeSchedule(f.f.objects, from, to)
}

// SeeSchedule reports whether a notice associated with the group defers to
// another schedule within the inclusive date window [from, to].
func (g GroupRef) SeeSchedule(from, to schema.Date) bool {
	if g.g == nil {
		return false
	}
	return seeSchedule(g.g.objects, from, to)
}

// seeSchedule reports whether any notice with a see-schedule effect may apply
// within the window.
func seeSchedule(objs []*epb.Object, from, to schema.Date) bool {
	for _, o := range objs {
		if o.GetKind() != epb.Object_NOTICE {
			continue
		}
		for _, e := range o.GetEffects() {
			if e.WhichEffect() == epb.Effect_SeeSchedule_case && applies(o, from, to) {
				return true
			}
		}
	}
	return false
}

// Session returns what validated session-level notices say about the
// published session (raw activity label, concrete date, exact published clock
// range in minutes).
func (g GroupRef) Session(activityLabel string, date schema.Date, start, end int) SessionNotices {
	if g.g == nil {
		return SessionNotices{}
	}
	return g.g.marks[sessKey{
		label: activityLabel,
		day:   int32(date) / 10,
		start: int32(start),
		end:   int32(end),
	}]
}

// Added returns the sessions added by notices within the inclusive date
// window [from, to], ordered by date then start time.
func (g GroupRef) Added(from, to schema.Date) []AddedSession {
	if g.g == nil {
		return nil
	}
	var out []AddedSession
	for _, a := range g.g.added {
		if day := int(a.Date) / 10; day >= int(from)/10 && day <= int(to)/10 {
			out = append(out, a)
		}
	}
	slices.SortStableFunc(out, func(a, b AddedSession) int {
		if a.Date/10 != b.Date/10 {
			return int(a.Date/10) - int(b.Date/10)
		}
		return a.Start - b.Start
	})
	return out
}

// Item is one posted object surfaced for a chronological listing (e.g. a
// category page's upcoming cancellations and notices): the source text as
// posted, how it classifies for listing, and the first date on or after the
// query date it may apply. Ordering and grouping are left to the consumer.
type Item struct {
	// ID is the object id, unique within the output, for deduplicating an
	// object reachable through more than one scope.
	ID string
	// Text is the source text as posted. A list head completed by a child
	// line is the two lines joined by a newline.
	Text string
	// Reading is the sentence the parser read when it differs from Text,
	// else "".
	Reading string
	// Unparsed marks freeform text nothing could be extracted from (or an
	// object kind this consumer doesn't recognize); it can never be ruled out
	// and carries no further classification.
	Unparsed bool
	// Cancelled marks a notice carrying a cancellation effect or a closure
	// broader than a named amenity.
	Cancelled bool
	// Dated reports the posted dates resolved; Date is then the first date on
	// or after the query date the object may apply.
	Dated bool
	Date  schema.Date
	// The resolved span as posted, for labeling: explicit Dates (which may
	// include past ones), or a From/To range (either side may be zero),
	// possibly OpenEnded ("until further notice") and/or restricted to
	// Weekdays. WeekdaysPartial reports that some posted weekday values did
	// not resolve, so Weekdays understates the restriction; a consumer that
	// can't express the span faithfully should fall back to DateText.
	Dates           []schema.Date
	From, To        schema.Date
	OpenEnded       bool
	Weekdays        []time.Weekday
	WeekdaysPartial bool
	// DateText is the raw date-context text as posted (may be ""), the
	// fallback when the resolved span can't be expressed faithfully. It is
	// set even when nothing resolved.
	DateText string
	// EndInexact reports that the resolved end is not the posted one: a
	// month taken as its last day ("until September 2026"), or a start with
	// no end found ("from August 22 to spring 2028"). A label should use
	// DateText rather than the resolved span.
	EndInexact bool
}

// Items lists the facility-scoped objects that may still be relevant on or
// after the given date. Objects placed under a specific group are not
// included; query the group.
func (f FacilityRef) Items(from schema.Date) []Item {
	if f.f == nil {
		return nil
	}
	return items(f.f.objects, from)
}

// Items lists everything associated with the group (its own objects and those
// of its activities and sessions) that may still be relevant on or after the
// given date.
func (g GroupRef) Items(from schema.Date) []Item {
	if g.g == nil {
		return nil
	}
	return items(g.g.objects, from)
}

// items builds the listing for objs: IGNORED objects are dropped, UNPARSED
// (and unknown-kind) objects are always kept since nothing can rule them out,
// and parsed notices are kept unless their resolved dates all fall before
// from. A dated object with no applicable day within a year of from is
// dropped as stale rather than listed undated.
func items(objs []*epb.Object, from schema.Date) []Item {
	var out []Item
	for _, o := range objs {
		it := Item{
			ID:       o.GetId(),
			Text:     strings.TrimSpace(o.GetRawText()),
			Reading:  strings.TrimSpace(o.GetReading()),
			DateText: strings.TrimSpace(o.GetDateText()),
		}
		if it.Text == "" {
			continue
		}
		switch o.GetKind() {
		case epb.Object_IGNORED:
			continue
		case epb.Object_NOTICE:
			var cancelled, closure bool
			for _, e := range o.GetEffects() {
				switch e.WhichEffect() {
				case epb.Effect_Cancelled_case:
					cancelled = true
				case epb.Effect_Closure_case:
					closure = true
				}
			}
			// an amenity closure (hot tub, sauna, ...) reads as a plain
			// notice; any broader closure reads as a cancellation
			it.Cancelled = cancelled || (closure && o.GetAmenity() == "")
		default:
			// UNPARSED, or a kind this consumer doesn't recognize
			it.Unparsed = true
		}
		if dated(o) {
			d, ok := firstApplies(o, from)
			if !ok {
				continue
			}
			it.Dated, it.Date = true, d
			it.EndInexact = slices.ContainsFunc(o.GetAmbiguities(), func(a string) bool {
				return a == "date-month-only" || a == "date-end-unstated"
			})
			ds := o.GetDates()
			for _, x := range ds.GetDates() {
				it.Dates = append(it.Dates, schema.Date(x))
			}
			if ds.HasFrom() {
				it.From = schema.Date(ds.GetFrom())
			}
			if ds.HasTo() {
				it.To = schema.Date(ds.GetTo())
			}
			it.OpenEnded = ds.GetOpenEnded()
			for _, x := range ds.GetWeekdays() {
				if wd, ok := schema.Date(x).Weekday(); ok {
					if !slices.Contains(it.Weekdays, wd) {
						it.Weekdays = append(it.Weekdays, wd)
					}
				} else {
					it.WeekdaysPartial = true
				}
			}
		}
		out = append(out, it)
	}
	return out
}

// dated reports whether the object carries any resolved dates, mirroring what
// [applies] can actually rule in or out.
func dated(o *epb.Object) bool {
	if !o.HasDates() {
		return false
	}
	d := o.GetDates()
	return len(d.GetDates()) > 0 || d.HasFrom() || d.HasTo() || d.GetOpenEnded() || len(d.GetWeekdays()) > 0
}

// firstApplies returns the first date on or after from the object may apply,
// probing day by day (matching the [applies] semantics exactly) for a year.
func firstApplies(o *epb.Object, from schema.Date) (schema.Date, bool) {
	t, ok := from.GoTime(time.UTC)
	if !ok {
		return from, true // can't probe; keep the object rather than dropping it
	}
	for range 366 {
		d := schema.MakeDateFromGo(t)
		if applies(o, d, d) {
			return d, true
		}
		t = t.AddDate(0, 0, 1)
	}
	return 0, false
}

// warning returns the highest severity among objects that may apply within
// the window.
func warning(objs []*epb.Object, from, to schema.Date) Warning {
	w := WarnNone
	for _, o := range objs {
		if sev := severity(o); sev > w && applies(o, from, to) {
			if w = sev; w == WarnChanges {
				break
			}
		}
	}
	return w
}

// severity classifies an object by what it could do to the schedule,
// independent of dates. Anything the parser could not fully account for, and
// anything from a newer schema than this consumer, classifies as WarnChanges:
// unknowns can never rule anything out.
func severity(o *epb.Object) Warning {
	switch o.GetKind() {
	case epb.Object_IGNORED:
		// headings, date context, boilerplate, service-desk notes, collapsed
		// duplicate stubs (the surviving copy is classified on its own)
		return WarnNone
	case epb.Object_NOTICE:
	default:
		// UNPARSED, or a kind this consumer doesn't recognize
		return WarnChanges
	}
	effects := o.GetEffects()
	if len(effects) == 0 {
		// a parsed notice stating no recognized effect; effects are only ever
		// set from trigger words, so this cannot affect the schedule
		return WarnNotice
	}
	amenity := o.GetAmenity() != ""
	for _, e := range effects {
		switch e.WhichEffect() {
		case epb.Effect_SeasonalHours_case:
			// seasonal operating ranges duplicate the schedules' own
			// effective date ranges
		case epb.Effect_Closure_case:
			// an amenity closure (hot tub, sauna, ...) never claims schedule
			// effects; any broader closure does
			if !amenity {
				return WarnChanges
			}
		default:
			// cancelled/added/time change/restriction/
			// see-schedule/modified hours, or an effect kind this consumer is
			// too old to understand (unset oneof)
			return WarnChanges
		}
	}
	return WarnNotice
}

// applies reports whether the object's resolved dates may fall within the
// inclusive window [from, to]. Objects without resolved dates always apply
// (they cannot be ruled out).
func applies(o *epb.Object, from, to schema.Date) bool {
	if !o.HasDates() {
		return true
	}
	d := o.GetDates()
	fromDay, toDay := int(from)/10, int(to)/10

	if dd := d.GetDates(); len(dd) > 0 {
		for _, x := range dd {
			if day := int(x) / 10; day >= fromDay && day <= toDay {
				return true
			}
		}
		return false
	}

	if !d.HasFrom() && !d.HasTo() && !d.GetOpenEnded() && len(d.GetWeekdays()) == 0 {
		return true // a DateSpan with nothing resolved
	}
	if d.HasFrom() && int(d.GetFrom())/10 > toDay {
		return false
	}
	if d.HasTo() && int(d.GetTo())/10 < fromDay {
		return false
	}

	// weekday restriction (possibly combined with a range): some day of the
	// window must satisfy both
	if wds := d.GetWeekdays(); len(wds) > 0 {
		var set [7]bool
		for _, x := range wds {
			if wd, ok := schema.Date(x).Weekday(); ok {
				set[wd] = true
			}
		}
		t, ok := from.GoTime(time.UTC)
		if !ok {
			return true
		}
		for range 62 {
			day := int(schema.MakeDateFromGo(t)) / 10
			if day > toDay {
				return false
			}
			if (!d.HasFrom() || day >= int(d.GetFrom())/10) &&
				(!d.HasTo() || day <= int(d.GetTo())/10) &&
				set[t.Weekday()] {
				return true
			}
			t = t.AddDate(0, 0, 1)
		}
		return true // window too long to enumerate; assume it applies
	}
	return true
}

// sessionNotices merges a session's referenced notices into SessionNotices.
func sessionNotices(byID map[string]*epb.Object, es *epb.Session) SessionNotices {
	var m SessionNotices
	for _, id := range es.GetObjects() {
		o := byID[id]
		if o == nil || o.GetKind() != epb.Object_NOTICE {
			continue
		}
		trust := objectTrust(o)
		if cancelsWholeSlot(o) {
			switch trust {
			case trustStated:
				m.Cancelled = true
			case trustLikely:
				m.LikelyCancelled = true
			}
		}
		for _, e := range o.GetEffects() {
			switch e.WhichEffect() {
			case epb.Effect_TimeChange_case:
				m.TimeChange = true
				// derive the effective time only from a single-ended mention
				// strictly inside the slot: open-end trims the end ("will end
				// at 6 pm"), open-start trims the start. Everything else
				// (e.g. a bare "schedule change" whose time equals the slot)
				// stays a flag with the details in the raw text. A trimmed
				// time is an assertion, so only a stated notice makes one.
				if t := o.GetTime(); o.HasTime() && !m.NewTime && trust == trustStated {
					start, end := es.GetStart(), es.GetEnd()
					switch {
					case t.GetOpenEnd() && t.HasStart() && t.GetStart() > start && t.GetStart() < end:
						m.NewStart, m.NewEnd, m.NewTime = int(start), int(t.GetStart()), true
					case t.GetOpenStart() && t.HasEnd() && t.GetEnd() > start && t.GetEnd() < end:
						m.NewStart, m.NewEnd, m.NewTime = int(t.GetEnd()), int(end), true
					}
				}
			}
		}
	}
	return m
}

// cancelsWholeSlot reports whether a session-referenced notice cancels or
// closes the whole published slot: it must carry a cancelled/closure effect
// and, when it has an extracted time, that time must equal or cover the slot
// (partial-slot and unvalidated relations only warn, never strike).
func cancelsWholeSlot(o *epb.Object) bool {
	var cancels bool
	for _, e := range o.GetEffects() {
		switch e.WhichEffect() {
		case epb.Effect_Cancelled_case, epb.Effect_Closure_case:
			cancels = true
		}
	}
	if !cancels {
		return false
	}
	if !o.HasTime() {
		// a whole-activity notice placed on its concrete sessions
		return true
	}
	switch o.GetTime().GetRelation() {
	case epb.TimeAssoc_EXACT, epb.TimeAssoc_COVERS:
		return true
	}
	return false
}

// addsSession reports whether a notice referenced from a Session.added list
// should inject that session, and whether only as an uncertain one: it must
// carry an added effect and no marker the policy table rates warn (which is
// where "added-time-already-scheduled" lives).
func addsSession(o *epb.Object) (ok, uncertain bool) {
	if o == nil || o.GetKind() != epb.Object_NOTICE {
		return false, false
	}
	t := objectTrust(o)
	if t == trustWarn {
		return false, false
	}
	for _, e := range o.GetEffects() {
		if e.WhichEffect() == epb.Effect_Added_case {
			return true, t == trustLikely
		}
	}
	return false, false
}
