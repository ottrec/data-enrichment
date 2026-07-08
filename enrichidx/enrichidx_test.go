package enrichidx

import (
	"testing"
	"time"

	epb "github.com/ottrec/data-enrichment/schema"
)

// TestSessionNotices covers the session-mark derivations against outputs
// shaped like real corpus cases.
func TestSessionNotices(t *testing.T) {
	i32 := func(v int32) *int32 { return &v }
	out := epb.Output_builder{
		Objects: []*epb.Object{
			// exact-slot cancel (Glen Cairn shape)
			epb.Object_builder{
				Id:   "cancel",
				Kind: epb.Object_NOTICE,
				Effects: []*epb.Effect{
					epb.Effect_builder{Cancelled: &epb.Effect_Cancelled{}}.Build(),
				},
				Time: epb.TimeAssoc_builder{
					Start: i32(540), End: i32(720),
					Relation: epb.TimeAssoc_EXACT,
				}.Build(),
			}.Build(),
			// end-early time change (Entrance Pool "Public swim will end at
			// 6 pm." against a 1 to 7 pm slot)
			epb.Object_builder{
				Id:   "endearly",
				Kind: epb.Object_NOTICE,
				Effects: []*epb.Effect{
					epb.Effect_builder{TimeChange: &epb.Effect_TimeChange{}}.Build(),
				},
				Time: epb.TimeAssoc_builder{
					Start: i32(1080), End: i32(1440), OpenEnd: true,
					Relation: epb.TimeAssoc_OVERLAPS,
				}.Build(),
			}.Build(),
			// partial-slot cancel: warns, never strikes
			epb.Object_builder{
				Id:   "partial",
				Kind: epb.Object_NOTICE,
				Effects: []*epb.Effect{
					epb.Effect_builder{Cancelled: &epb.Effect_Cancelled{}}.Build(),
				},
				Time: epb.TimeAssoc_builder{
					Start: i32(870), End: i32(960),
					Relation: epb.TimeAssoc_WITHIN,
				}.Build(),
			}.Build(),
		},
		Facilities: []*epb.Facility{
			epb.Facility_builder{
				Name: "Fac",
				Groups: []*epb.Group{
					epb.Group_builder{
						Label: "Grp",
						Activities: []*epb.Activity{
							epb.Activity_builder{
								Label: "Act",
								Sessions: []*epb.Session{
									epb.Session_builder{Date: 202607061, Start: 540, End: 720, Objects: []string{"cancel"}}.Build(),
									epb.Session_builder{Date: 202607132, Start: 780, End: 1140, Objects: []string{"endearly"}}.Build(),
									epb.Session_builder{Date: 202607083, Start: 780, End: 1020, Objects: []string{"partial"}}.Build(),
								},
							}.Build(),
						},
					}.Build(),
				},
			}.Build(),
		},
	}.Build()

	g := Join(out).Facility("Fac").Group("Grp")

	if m := g.Session("Act", 202607061, 540, 720); !m.Cancelled {
		t.Errorf("exact cancel: got %+v", m)
	}
	if m := g.Session("Act", 202607132, 780, 1140); !m.TimeChange || !m.NewTime || m.NewStart != 780 || m.NewEnd != 1080 || m.Cancelled {
		t.Errorf("end-early: got %+v", m)
	}
	if m := g.Session("Act", 202607083, 780, 1020); m.Cancelled {
		t.Errorf("partial cancel must not strike: got %+v", m)
	}
	if m := g.Session("Act", 202607061, 540, 721); m != (SessionNotices{}) {
		t.Errorf("wrong key must miss: got %+v", m)
	}
}

// TestScopeCancelled covers the whole-scope cancellation query against
// outputs shaped like real corpus cases.
func TestScopeCancelled(t *testing.T) {
	i32 := func(v int32) *int32 { return &v }
	cancelled := []*epb.Effect{epb.Effect_builder{Cancelled: &epb.Effect_Cancelled{}}.Build()}
	closed := []*epb.Effect{epb.Effect_builder{Closure: &epb.Effect_Closure{}}.Build()}
	dates := func(d ...int32) *epb.DateSpan { return epb.DateSpan_builder{Dates: d}.Build() }
	out := epb.Output_builder{
		Objects: []*epb.Object{
			// group-scope dated cancel (Sandy Hill "All drop-in skating,
			// cancelled" shape)
			epb.Object_builder{
				Id: "grpcancel", Kind: epb.Object_NOTICE,
				MatchQuality: epb.Object_SCOPE_PHRASE,
				Dates:        dates(202607051),
				Effects:      cancelled,
			}.Build(),
			// bare dated "closed" item (no subject phrase)
			epb.Object_builder{
				Id: "bareclosed", Kind: epb.Object_NOTICE,
				Dates:   dates(202607062),
				Effects: closed,
			}.Build(),
			// facility-scope cancel via an amenity subject ("The pool is
			// closed and all programs cancelled.")
			epb.Object_builder{
				Id: "poolcancel", Kind: epb.Object_NOTICE,
				MatchQuality: epb.Object_SCOPE_PHRASE,
				Amenity:      "pool",
				Dates:        dates(202607073),
				Effects: []*epb.Effect{
					epb.Effect_builder{Closure: &epb.Effect_Closure{}}.Build(),
					epb.Effect_builder{Cancelled: &epb.Effect_Cancelled{}}.Build(),
				},
			}.Build(),
			// amenity closure only: never claims schedule effects
			epb.Object_builder{
				Id: "hottub", Kind: epb.Object_NOTICE,
				MatchQuality: epb.Object_SCOPE_PHRASE,
				Amenity:      "hot tub",
				Dates:        dates(202607051),
				Effects:      closed,
			}.Build(),
			// part closure leveled at the facility ("The pool is closed for
			// maintenance until further notice." at a multi-group complex):
			// the residual subject phrase must keep it out of the claim
			epb.Object_builder{
				Id: "poolclosed", Kind: epb.Object_NOTICE,
				MatchQuality: epb.Object_SCOPE_PHRASE,
				Phrase:       "pool",
				Dates:        epb.DateSpan_builder{OpenEnded: true}.Build(),
				Effects:      closed,
			}.Build(),
			// undated list head ("The facility is closed, and all programs
			// cancelled:"): the dates live in the child items, which claim
			// their own dates; the head itself must not claim every date
			epb.Object_builder{
				Id: "listhead", Kind: epb.Object_NOTICE,
				MatchQuality: epb.Object_SCOPE_PHRASE,
				Effects: []*epb.Effect{
					epb.Effect_builder{Closure: &epb.Effect_Closure{}}.Build(),
					epb.Effect_builder{Cancelled: &epb.Effect_Cancelled{}}.Build(),
				},
			}.Build(),
			// unmatched-subject cancel: scope unknown, must not implicate
			epb.Object_builder{
				Id: "unmatched", Kind: epb.Object_NOTICE,
				MatchQuality: epb.Object_NONE,
				Dates:        dates(202607051),
				Effects:      cancelled,
			}.Build(),
			// session-level cancel: reports through Session, not the scope
			epb.Object_builder{
				Id: "sesscancel", Kind: epb.Object_NOTICE,
				MatchQuality: epb.Object_EXACT,
				Dates:        dates(202607051),
				Effects:      cancelled,
			}.Build(),
			// partial-day closure ("The facility is closed until noon.")
			epb.Object_builder{
				Id: "morning", Kind: epb.Object_NOTICE,
				MatchQuality: epb.Object_SCOPE_PHRASE,
				Dates:        dates(202607095),
				Effects:      closed,
				Time: epb.TimeAssoc_builder{
					Start: i32(0), End: i32(720), OpenStart: true,
					Relation: epb.TimeAssoc_UNCHECKED,
				}.Build(),
			}.Build(),
			// whole-activity cancel with slot-only time association (Sandy
			// Hill shape: relation COVERS, no extracted clock)
			epb.Object_builder{
				Id: "slotsonly", Kind: epb.Object_NOTICE,
				MatchQuality: epb.Object_SCOPE_PHRASE,
				Dates:        dates(202607106),
				Effects:      cancelled,
				Time: epb.TimeAssoc_builder{
					Relation: epb.TimeAssoc_COVERS,
					Slots:    []string{"Friday 5:00 - 5:50pm"},
				}.Build(),
			}.Build(),
		},
		Facilities: []*epb.Facility{
			epb.Facility_builder{
				Name:    "Fac",
				Objects: []string{"poolcancel", "hottub", "unmatched", "poolclosed", "listhead"},
				Groups: []*epb.Group{
					epb.Group_builder{
						Label:   "Grp",
						Objects: []string{"grpcancel", "bareclosed", "morning", "slotsonly"},
						Activities: []*epb.Activity{
							epb.Activity_builder{
								Label: "Act",
								Sessions: []*epb.Session{
									epb.Session_builder{Date: 202607051, Start: 540, End: 720, Objects: []string{"sesscancel"}}.Build(),
								},
							}.Build(),
						},
					}.Build(),
					epb.Group_builder{Label: "Other"}.Build(),
				},
			}.Build(),
		},
	}.Build()

	f := Join(out).Facility("Fac")
	g := f.Group("Grp")

	if !g.ScopeCancelled(202607051, 540, 720) {
		t.Error("group-scope dated cancel must apply on its date")
	}
	if !g.ScopeCancelled(202607062, 540, 720) {
		t.Error("bare dated closed item must apply on its date")
	}
	if g.ScopeCancelled(202607084, 540, 720) {
		t.Error("group-scope cancels must not apply on other dates")
	}
	if !f.ScopeCancelled(202607073, 540, 720) {
		t.Error("closure with all-programs-cancelled must apply facility-wide")
	}
	if f.ScopeCancelled(202607051, 540, 720) {
		t.Error("amenity/part closures and unmatched-subject cancels must not implicate the facility")
	}
	if o := f.Group("Other"); o.ScopeCancelled(202607051, 540, 720) {
		t.Error("a sibling group must not inherit another group's cancel")
	}
	if zero := (GroupRef{}); zero.ScopeCancelled(202607051, 540, 720) {
		t.Error("zero GroupRef must report nothing")
	}
	if !g.ScopeCancelled(202607095, 540, 720) {
		t.Error("morning closure must apply to a morning session")
	}
	if g.ScopeCancelled(202607095, 1080, 1200) {
		t.Error("morning closure must not apply to an evening session")
	}
	if !g.ScopeCancelled(202607106, 1080, 1200) {
		t.Error("a slot-only time association must not constrain the clock range")
	}
}

// TestItems covers the listing derivation: which objects are kept, how they
// classify, and the first applicable date. Query date 2026-07-08 (Wednesday, encoded 202607084).
func TestItems(t *testing.T) {
	i32 := func(v int32) *int32 { return &v }
	cancelled := []*epb.Effect{epb.Effect_builder{Cancelled: &epb.Effect_Cancelled{}}.Build()}
	closed := []*epb.Effect{epb.Effect_builder{Closure: &epb.Effect_Closure{}}.Build()}
	out := epb.Output_builder{
		Objects: []*epb.Object{
			// future dated cancel: listed on its date
			epb.Object_builder{
				Id: "future", Kind: epb.Object_NOTICE,
				RawText: "Public swim cancelled on July 12.",
				Dates:   epb.DateSpan_builder{Dates: []int32{202607121}}.Build(),
				Effects: cancelled,
			}.Build(),
			// past dated cancel: determinably irrelevant, dropped
			epb.Object_builder{
				Id: "past", Kind: epb.Object_NOTICE,
				RawText: "Public swim cancelled on July 1.",
				Dates:   epb.DateSpan_builder{Dates: []int32{202607014}}.Build(),
				Effects: cancelled,
			}.Build(),
			// freeform text: always kept, never classified
			epb.Object_builder{
				Id: "free", Kind: epb.Object_UNPARSED,
				RawText: "Some freeform note about the facility.",
			}.Build(),
			// amenity closure: a plain notice, not a cancellation
			epb.Object_builder{
				Id: "hottub", Kind: epb.Object_NOTICE,
				RawText: "The hot tub is closed.",
				Amenity: "hot tub",
				Effects: closed,
			}.Build(),
			// boilerplate: dropped
			epb.Object_builder{
				Id: "head", Kind: epb.Object_IGNORED,
				RawText: "Notices",
			}.Build(),
			// range covering the query date: first applicable = query date
			epb.Object_builder{
				Id: "range", Kind: epb.Object_NOTICE,
				RawText: "Lane swim cancelled until July 20.",
				Dates:   epb.DateSpan_builder{To: i32(202607202)}.Build(),
				Effects: cancelled,
			}.Build(),
			// weekday-restricted range: span + raw date text exposed for
			// faithful labeling; first applicable = first Friday in range
			epb.Object_builder{
				Id: "fridays", Kind: epb.Object_NOTICE,
				RawText:  "Aquafit cancelled Fridays until July 24.",
				DateText: "Fridays until July 24",
				Dates:    epb.DateSpan_builder{To: i32(202607246), Weekdays: []int32{6}}.Build(),
				Effects:  cancelled,
			}.Build(),
		},
		Facilities: []*epb.Facility{
			epb.Facility_builder{
				Name:    "Fac",
				Objects: []string{"future", "past", "free", "hottub", "head", "range", "fridays"},
			}.Build(),
		},
	}.Build()

	items := Join(out).Facility("Fac").Items(202607084)
	byID := map[string]Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if len(items) != 5 {
		t.Errorf("got %d items (%v), want 5", len(items), byID)
	}
	if it := byID["future"]; !it.Cancelled || !it.Dated || it.Date != 202607121 {
		t.Errorf("future cancel: got %+v", it)
	}
	if _, ok := byID["past"]; ok {
		t.Error("past-dated cancel must be dropped")
	}
	if it := byID["free"]; !it.Unparsed || it.Cancelled || it.Dated {
		t.Errorf("freeform: got %+v", it)
	}
	if it := byID["hottub"]; it.Cancelled || it.Unparsed || it.Dated {
		t.Errorf("amenity closure: got %+v", it)
	}
	if _, ok := byID["head"]; ok {
		t.Error("ignored object must be dropped")
	}
	if it := byID["range"]; !it.Cancelled || !it.Dated || it.Date != 202607084 || it.To != 202607202 || it.From != 0 || len(it.Dates) != 0 {
		t.Errorf("range cancel: got %+v", it)
	}
	if it := byID["future"]; len(it.Dates) != 1 || it.Dates[0] != 202607121 {
		t.Errorf("future cancel span: got %+v", it)
	}
	if it := byID["fridays"]; !it.Cancelled || !it.Dated || it.Date != 202607106 ||
		len(it.Weekdays) != 1 || it.Weekdays[0] != time.Friday || it.WeekdaysPartial ||
		it.To != 202607246 || it.DateText != "Fridays until July 24" {
		t.Errorf("weekday-restricted cancel: got %+v", it)
	}
	if got := (FacilityRef{}).Items(202607084); got != nil {
		t.Errorf("zero FacilityRef must list nothing, got %v", got)
	}
}
