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
// schedule effects.
package enrichidx

import (
	"slices"
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
	objects   []*epb.Object // whole subtree, deduped
	cancelled map[sessKey]bool
	added     []AddedSession
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
			g := &group{cancelled: map[sessKey]bool{}}
			seen := map[string]bool{}
			g.objects = resolve(g.objects, seen, eg.GetObjects())
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
					for _, id := range es.GetObjects() {
						if cancelsWholeSlot(byID[id]) {
							g.cancelled[key] = true
						}
					}
					for _, id := range es.GetAdded() {
						if addsSession(byID[id]) {
							g.added = append(g.added, AddedSession{
								ActivityLabel: ea.GetLabel(),
								Novel:         ea.GetNovel(),
								Date:          schema.Date(es.GetDate()),
								Start:         int(es.GetStart()),
								End:           int(es.GetEnd()),
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

// Cancelled reports whether the published session (raw activity label,
// concrete date, exact published clock range in minutes) is cancelled or
// closed for its whole time by a validated session-level notice.
func (g GroupRef) Cancelled(activityLabel string, date schema.Date, start, end int) bool {
	if g.g == nil {
		return false
	}
	return g.g.cancelled[sessKey{
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
			// cancelled/added/time change/moved/changed/restriction/
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

// cancelsWholeSlot reports whether a session-referenced notice cancels or
// closes the whole published slot: it must carry a cancelled/closure effect
// and, when it has an extracted time, that time must equal or cover the slot
// (partial-slot and unvalidated relations only warn, never strike).
func cancelsWholeSlot(o *epb.Object) bool {
	if o == nil || o.GetKind() != epb.Object_NOTICE {
		return false
	}
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
// should inject that session: it must carry an added effect and not be
// flagged as duplicating an already-published time.
func addsSession(o *epb.Object) bool {
	if o == nil || o.GetKind() != epb.Object_NOTICE {
		return false
	}
	if slices.Contains(o.GetAmbiguities(), "added-time-already-scheduled") {
		return false
	}
	for _, e := range o.GetEffects() {
		if e.WhichEffect() == epb.Effect_Added_case {
			return true
		}
	}
	return false
}
