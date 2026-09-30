package enrich

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ottrec/scraper/schema"
	"github.com/ottrec/website/pkg/ottrecidx"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// testGroup builds a groupMatcher with the label's title tokens, which is
// what groupsForPart reads, and the given activity labels.
func testGroup(label string, acts ...string) *groupMatcher {
	m := testMatcher(acts...)
	m.label = label
	m.titleToks = tokenSet(label)
	return m
}

// testClosureCtx builds a facility-level blockCtx for a facility of the
// given name and groups, in a dataset that also holds the other facilities
// by name alone, as the golden fixtures do.
func testClosureCtx(t *testing.T, name string, groups []*groupMatcher, others ...string) *blockCtx {
	t.Helper()
	facs := []*schema.Facility{schema.Facility_builder{
		Name:   name,
		Source: schema.Source_builder{XDate: timestamppb.New(anchorAt(2026, time.August, 1))}.Build(),
	}.Build()}
	for _, o := range others {
		facs = append(facs, schema.Facility_builder{Name: o}.Build())
	}
	buf, err := proto.Marshal(schema.Data_builder{Facilities: facs}.Build())
	if err != nil {
		t.Fatal(err)
	}
	idx, err := new(ottrecidx.Indexer).Load(buf)
	if err != nil {
		t.Fatal(err)
	}
	fc := &facCtx{out: &builder{Stats: map[string]int{}}, matchers: groups}
	var names []string
	for fac := range idx.Data().Facilities() {
		names = append(names, fac.GetName())
		if fac.GetName() == name {
			fc.fac = fac
		}
	}
	fc.others = otherFacilities(names)
	fc.anchor = fc.fac.GetSourceDate()
	return &blockCtx{facCtx: fc}
}

// TestResolveClosureSubject pins the kind and reason of the closure
// subjects behind the incidents and the residue d.md classified. The
// subject is what subjectClosedRe leaves after "the".
func TestResolveClosureSubject(t *testing.T) {
	bobMacQuarrie := testClosureCtx(t, "Bob MacQuarrie Recreation Complex - Orléans", []*groupMatcher{
		testGroup("Drop-in schedule - swim", "Lane swim", "Public swim"),
		testGroup("Drop-in schedule - squash and racquetball", "Squash courts 1, 2, 3, 5, 7 and 9", "Racquetball"),
		testGroup("Drop-in schedule - skating", "Public skating"),
	})
	kirwan := testClosureCtx(t, "Deborah Anne Kirwan Pool", []*groupMatcher{
		testGroup("Drop-in schedule - swim", "Lane swim", "Public swim"),
	})
	splash := testClosureCtx(t, "Splash Wave Pool", []*groupMatcher{
		testGroup("Drop-in schedule - swim", "Public swim - wave tank and warm pool", "Lane swim"),
	})
	nepean := testClosureCtx(t, "Nepean Sportsplex", []*groupMatcher{
		testGroup("Drop-in schedule - squash", "Squash court 3", "Squash - courts 1, 2, and 4"),
	})
	kanata := testClosureCtx(t, "Kanata Leisure Centre and Wave Pool", []*groupMatcher{
		testGroup("Drop-in schedule - swim", "Hot tub and steam room", "Sauna and steam room", "Public swim"),
	})
	benFranklin := testClosureCtx(t, "Ben Franklin Place", nil, "Meridian Theatres at Centrepointe", "Nepean Sportsplex", "Entrance Pool")
	stLaurent := testClosureCtx(t, "St. Laurent Complex", []*groupMatcher{
		testGroup("Drop-in schedule - swim", "Lane swim"),
		testGroup("Drop-in schedule - skating", "Public skating"),
	})
	tonyGraham := testClosureCtx(t, "Tony Graham Recreation Complex - Kanata", []*groupMatcher{
		testGroup("Drop-in schedule - swim", "Lane swim"),
	})
	cardelrec := testClosureCtx(t, "CARDELREC Recreation Complex - Goulbourn", []*groupMatcher{
		testGroup("Drop-in schedule - swim", "Lane swim"),
		testGroup("Drop-in schedule - skating", "Public skating"),
	})
	lansdowne := testClosureCtx(t, "Lansdowne Park", []*groupMatcher{
		testGroup("Drop-in schedule - skating", "Roller skating"),
	})
	cumberland := testClosureCtx(t, "Cumberland Heritage Village Museum", nil)
	fairfields := testClosureCtx(t, "Fairfields Heritage House", nil)
	billings := testClosureCtx(t, "Billings Estate National Historic Site", nil)
	pinhey := testClosureCtx(t, "Pinhey's Point Historic Site", nil)

	for _, tc := range []struct {
		name      string
		b         *blockCtx
		subject   string
		cancelled bool
		kind      subjectKind
		reason    string
		detail    string // the groups, activities or amenity the kind carries
	}{
		// the incidents
		{"Bob MacQuarrie's courts cancel their own groups (84c17cd)", bobMacQuarrie, "squash and racquetball courts", true, subjPart, "part-groups", "Drop-in schedule - squash and racquetball"},
		{"the pool of a complex, cancelling", bobMacQuarrie, "pool", true, subjPart, "part-groups", "Drop-in schedule - swim"},
		{"the pool of a complex, closure only", bobMacQuarrie, "pool", false, subjPart, "part-groups", "Drop-in schedule - swim"},
		{"the pool of a pool", kirwan, "pool", false, subjFacility, "facility-generic", ""},
		{"the pool of a pool, cancelling", kirwan, "pool", true, subjFacility, "facility-generic", ""},
		{"one arena of two", bobMacQuarrie, "roger sénécal arena", false, subjAmenity, "amenity-core", "roger sénécal arena"},
		{"one court of a six-court row", bobMacQuarrie, "squash court 3", false, subjUnit, "unit-of-row", "squash court"},
		{"a court that is a row of its own", nepean, "squash court 3", false, subjActivity, "activity-exact", "Squash court 3"},
		{"the wave pool of a wave pool", splash, "wave pool", false, subjFacility, "facility-name-token", ""},
		{"a multiple match is an amenity, not candidates", kanata, "steam room", false, subjAmenity, "amenity-core", "steam room"},
		{"another facility", benFranklin, "meridian theatres centrepointe", false, subjOtherFacility, "other-facility", "Meridian Theatres at Centrepointe"},
		{"another facility and a library", benFranklin, "meridian theatres centrepointe and the nepean centrepointe branch of the ottawa public library", false, subjOtherFacility, "other-facility", "Meridian Theatres at Centrepointe"},
		{"a facility name with one distinctive word names nothing", benFranklin, "entrance pool", false, subjAmenity, "amenity-core", "entrance pool"},
		{"the ramp (647596b)", stLaurent, "pool's wheelchair ramp", false, subjAmenity, "amenity-core", "pool's wheelchair ramp"},
		{"the complex and the desk", tonyGraham, "complex and client services", false, subjFacility, "facility-list-with-desk", ""},
		{"the desk alone is not the facility", tonyGraham, "client services", false, subjNone, "unmatched", ""},
		{"a list with something else in it", tonyGraham, "complex and the parking lot", false, subjNone, "unmatched", ""},
		// the residue
		{"the community centre of a complex", cardelrec, "community centre", false, subjFacility, "facility-generic", ""},
		{"the lawn and the hill", lansdowne, "great lawn and the sledding hill", false, subjAmenity, "amenity-core", "great lawn sledding hill"},
		{"the lawn and the hill at the park", lansdowne, "great lawn and the sledding hill at lansdowne park", false, subjFacility, "facility-name-token", ""},
		{"the museum that is the facility's name", cumberland, "museum", false, subjFacility, "facility-name-token", ""},
		{"the house by its name", fairfields, "fairfields heritage house", false, subjFacility, "facility-name-token", ""},
		{"the museum of a house", fairfields, "museum", false, subjNone, "unmatched", ""},
		{"the museum of an estate", billings, "museum", false, subjNone, "unmatched", ""},
		{"the museum at its site", pinhey, "museum at pinhey's point historic site", false, subjFacility, "facility-name-token", ""},
	} {
		s := tc.b.resolveClosureSubject(tc.subject, tc.cancelled, tc.subject+" is closed")
		if s.Kind != tc.kind || s.Reason != tc.reason {
			t.Errorf("%s: %q = %s %s, want %s %s", tc.name, tc.subject, s.Kind, s.Reason, tc.kind, tc.reason)
			continue
		}
		var detail string
		switch s.Kind {
		case subjPart:
			detail = strings.Join(s.Groups, ", ")
		case subjActivity:
			detail = strings.Join(actNames(s.Acts), ", ")
		case subjUnit, subjAmenity, subjOtherFacility:
			detail = s.Amenity
		}
		if detail != tc.detail {
			t.Errorf("%s: %q carries %q, want %q", tc.name, tc.subject, detail, tc.detail)
		}
	}
}

// TestSubjectClosedRe pins what the regex leaves as the subject: the verb
// and the adverb come off, so "will remain", "remains" and "is currently"
// do not hide the last word of the subject from the amenity list (the
// wheelchair ramp, 647596b) or the facility's name from the token match.
func TestSubjectClosedRe(t *testing.T) {
	for in, want := range map[string]string{
		"the pool is closed for maintenance":                                  "the pool",
		"squash court 3 is closed until further notice":                       "squash court 3",
		"the pool's wheelchair ramp is currently unavailable":                 "the pool's wheelchair ramp",
		"meridian theatres centrepointe will remain closed for continued":     "meridian theatres centrepointe",
		"meridian theatres centrepointe remains closed for flood restoration": "meridian theatres centrepointe",
		"the complex and client services remain closed":                       "the complex and client services",
		"the sauna is still closed":                                           "the sauna",
		"the arena will be temporarily closed":                                "the arena",
	} {
		m := subjectClosedRe.FindStringSubmatch(in)
		if m == nil {
			t.Errorf("%q: no match", in)
		} else if m[1] != want {
			t.Errorf("%q: subject %q, want %q", in, m[1], want)
		}
	}
	if subjectClosedRe.MatchString("the pool remains open") {
		t.Error("\"remains open\" is not a closure")
	}
}

// TestResolveClosureSubjectStats checks the reason is counted per decision.
func TestResolveClosureSubjectStats(t *testing.T) {
	fac := testFacility(t, "<p>The pool is closed for maintenance.</p><p>The sauna is closed.</p>")
	fc := &facCtx{out: &builder{Stats: map[string]int{}}, fac: fac, anchor: fac.GetSourceDate()}
	fc.matchers = []*groupMatcher{testGroup("Drop-in schedule - swim", "Lane swim")}
	fc.processBlock(fac.GetSpecialHoursHTML(), "special_hours", nil)
	want := map[string]int{"subject/closure/facility-generic": 1, "subject/closure/amenity-core": 1}
	for k, v := range want {
		if fc.out.Stats[k] != v {
			t.Errorf("%s = %d, want %d", k, fc.out.Stats[k], v)
		}
	}
	for k := range fc.out.Stats {
		if strings.HasPrefix(k, "subject/") && want[k] == 0 {
			t.Errorf("unexpected %s = %d", k, fc.out.Stats[k])
		}
	}
	_ = slices.Contains[[]string]
}
