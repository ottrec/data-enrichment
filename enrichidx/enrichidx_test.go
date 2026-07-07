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
