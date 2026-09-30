package enrichidx

import (
	"slices"
	"testing"

	"github.com/ottrec/data-enrichment/enrich"
	epb "github.com/ottrec/data-enrichment/schema"
)

// TestMarkerPolicyCoversParser keeps the policy table and the parser's
// marker registry in step: a new marker is a decision about what the
// consumer may still do, made where the trust rules live.
func TestMarkerPolicyCoversParser(t *testing.T) {
	emitted := enrich.Markers()
	for _, m := range emitted {
		if _, ok := markerPolicy[m]; !ok {
			t.Errorf("marker %q has no policy row", m)
		}
	}
	for m := range markerPolicy {
		if !slices.Contains(emitted, m) {
			t.Errorf("policy row %q for a marker the parser does not emit", m)
		}
	}
}

// TestTrustTiers pins what each tier does to a session-level cancel, a
// stated scope cancel and an add.
func TestTrustTiers(t *testing.T) {
	i32 := func(v int32) *int32 { return &v }
	cancel := func(id string, amb ...string) *epb.Object {
		return epb.Object_builder{
			Id: id, Kind: epb.Object_NOTICE,
			Effects:     []*epb.Effect{epb.Effect_builder{Cancelled: &epb.Effect_Cancelled{}}.Build()},
			Time:        epb.TimeAssoc_builder{Start: i32(540), End: i32(720), Relation: epb.TimeAssoc_EXACT}.Build(),
			Ambiguities: amb,
		}.Build()
	}
	scope := func(id string, amb ...string) *epb.Object {
		return epb.Object_builder{
			Id: id, Kind: epb.Object_NOTICE, MatchQuality: epb.Object_SCOPE_PHRASE,
			Dates:       epb.DateSpan_builder{Dates: []int32{202607051}}.Build(),
			Effects:     []*epb.Effect{epb.Effect_builder{Cancelled: &epb.Effect_Cancelled{}}.Build()},
			Ambiguities: amb,
		}.Build()
	}
	add := func(id string, amb ...string) *epb.Object {
		return epb.Object_builder{
			Id: id, Kind: epb.Object_NOTICE,
			Effects:     []*epb.Effect{epb.Effect_builder{Added: &epb.Effect_Added{}}.Build()},
			Ambiguities: amb,
		}.Build()
	}
	byID := map[string]*epb.Object{}
	for _, o := range []*epb.Object{
		cancel("stated"), cancel("likely", "activity-typo-match"), cancel("warn", "meridiem-ambiguous"), cancel("unknown", "some-new-marker"), cancel("disambiguated", "activity-time-disambiguated"),
		add("addstated"), add("addlikely", "matched-other-group"), add("addwarn", "added-time-already-scheduled"),
	} {
		byID[o.GetId()] = o
	}
	sess := func(ids ...string) *epb.Session {
		return epb.Session_builder{Date: 202607051, Start: 540, End: 720, Objects: ids}.Build()
	}
	if m := sessionNotices(byID, sess("stated")); !m.Cancelled || m.LikelyCancelled {
		t.Errorf("stated: %+v", m)
	}
	if m := sessionNotices(byID, sess("likely")); m.Cancelled || !m.LikelyCancelled {
		t.Errorf("likely: %+v", m)
	}
	if m := sessionNotices(byID, sess("warn")); m.Cancelled || m.LikelyCancelled {
		t.Errorf("warn: %+v", m)
	}
	if m := sessionNotices(byID, sess("unknown")); m.Cancelled || m.LikelyCancelled {
		t.Errorf("unknown marker must not strike: %+v", m)
	}
	if m := sessionNotices(byID, sess("disambiguated")); !m.Cancelled {
		t.Errorf("activity-time-disambiguated is stated: %+v", m)
	}
	if !scopeCancelled([]*epb.Object{scope("s")}, 202607051, 540, 720, true) {
		t.Error("stated scope cancel")
	}
	if !scopeCancelled([]*epb.Object{scope("s", "class-title-partial")}, 202607051, 540, 720, true) {
		t.Error("class-title-partial is stated")
	}
	objs := []*epb.Object{scope("l", "weekday-mismatch")}
	if scopeCancelled(objs, 202607051, 540, 720, true) || !scopeCancelled(objs, 202607051, 540, 720, false) {
		t.Error("a likely-trust stated cancel is the implied tier")
	}
	objs = []*epb.Object{scope("w", "no-slot-overlap")}
	if scopeCancelled(objs, 202607051, 540, 720, false) {
		t.Error("a warn-trust cancel never cancels a scope")
	}
	if ok, unc := addsSession(byID["addstated"]); !ok || unc {
		t.Error("stated add")
	}
	if ok, unc := addsSession(byID["addlikely"]); !ok || !unc {
		t.Error("likely add is uncertain")
	}
	if ok, _ := addsSession(byID["addwarn"]); ok {
		t.Error("warn add is no add")
	}
}
