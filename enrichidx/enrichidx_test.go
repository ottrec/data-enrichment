package enrichidx

import (
	"testing"

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
