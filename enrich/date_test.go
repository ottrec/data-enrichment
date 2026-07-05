package enrich

import (
	"testing"
	"time"

	"github.com/ottrec/website/pkg/ottrecidx"
)

func anchorAt(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, ottrecidx.TZ)
}

func TestParseLeadingDate(t *testing.T) {
	for _, tc := range []struct {
		in     string
		anchor time.Time
		ok     bool
		dates  []string
		from   string
		to     string
		open   bool
		wds    int
		rest   string
		ambig  []string
	}{
		{in: "Friday, July 3", anchor: anchorAt(2026, 7, 1), ok: true, dates: []string{"2026-07-03"}},
		{in: "Monday, July 6 to Friday, July 10", anchor: anchorAt(2026, 7, 1), ok: true, from: "2026-07-06", to: "2026-07-10"},
		// garbled but repairable: both endpoint weekdays validate
		{in: "Monday, July 6 to 10 Friday, July 10", anchor: anchorAt(2026, 7, 1), ok: true, from: "2026-07-06", to: "2026-07-10", ambig: []string{ambDateGarbled}},
		{in: "May 31 to June 28", anchor: anchorAt(2026, 6, 10), ok: true, from: "2026-05-31", to: "2026-06-28"},
		{in: "December 20 to January 2", anchor: anchorAt(2025, 12, 19), ok: true, from: "2025-12-20", to: "2026-01-02"},
		{in: "October 31, 2025 to March 13, 2026", anchor: anchorAt(2025, 11, 1), ok: true, from: "2025-10-31", to: "2026-03-13"},
		{in: "December 13 and 14", anchor: anchorAt(2025, 12, 1), ok: true, dates: []string{"2025-12-13", "2025-12-14"}},
		{in: "Thursday, March 12 and Saturday, March 14", anchor: anchorAt(2026, 2, 20), ok: true, dates: []string{"2026-03-12", "2026-03-14"}},
		{in: "November 25 until further notice", anchor: anchorAt(2025, 11, 20), ok: true, from: "2025-11-25", open: true},
		{in: "Monday to Friday", anchor: anchorAt(2026, 1, 1), ok: true, wds: 5},
		{in: "Fridays, Saturdays, and Sundays", anchor: anchorAt(2026, 1, 1), ok: true, wds: 3},
		// From's weekday is a typo; anchor proximity must win over it
		{in: "Monday, June 7 to Sunday, June 28", anchor: anchorAt(2026, 6, 20), ok: true, from: "2026-06-07", to: "2026-06-28", ambig: []string{ambWeekdayMismatch}},
		{in: "Wednesday , November 26", anchor: anchorAt(2025, 11, 20), ok: true, dates: []string{"2025-11-26"}},
		{in: "Monday, February 16 (Family Day)", anchor: anchorAt(2026, 2, 1), ok: true, dates: []string{"2026-02-16"}, rest: "(Family Day)"},
		{in: "Thursday, June 25, 9 am to 8 pm", anchor: anchorAt(2026, 6, 20), ok: true, dates: []string{"2026-06-25"}, rest: "9 am to 8 pm"},
		{in: "Lane swim, 11 am to 3 pm, cancelled", anchor: anchorAt(2026, 6, 20), ok: false},
		{in: "The facility is closed.", anchor: anchorAt(2026, 6, 20), ok: false},
	} {
		t.Run(tc.in, func(t *testing.T) {
			spec, rest, ok := parseLeadingDate(tc.in, tc.anchor)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (spec %+v)", ok, tc.ok, spec)
			}
			if !ok {
				return
			}
			var gotDates []string
			for _, d := range spec.Dates {
				gotDates = append(gotDates, iso(d))
			}
			if len(gotDates) != len(tc.dates) {
				t.Errorf("dates = %v, want %v", gotDates, tc.dates)
			} else {
				for i := range gotDates {
					if gotDates[i] != tc.dates[i] {
						t.Errorf("dates = %v, want %v", gotDates, tc.dates)
						break
					}
				}
			}
			checkDate := func(name string, got time.Time, want string) {
				gotStr := ""
				if !got.IsZero() {
					gotStr = iso(got)
				}
				if gotStr != want {
					t.Errorf("%s = %q, want %q", name, gotStr, want)
				}
			}
			checkDate("from", spec.From, tc.from)
			checkDate("to", spec.To, tc.to)
			if spec.OpenEnded != tc.open {
				t.Errorf("openEnded = %v, want %v", spec.OpenEnded, tc.open)
			}
			if len(spec.Weekdays) != tc.wds {
				t.Errorf("weekdays = %v, want %d", spec.Weekdays, tc.wds)
			}
			if tc.rest != "" && rest != tc.rest {
				t.Errorf("rest = %q, want %q", rest, tc.rest)
			}
			for _, a := range tc.ambig {
				found := false
				for _, g := range spec.Ambig {
					if g == a {
						found = true
					}
				}
				if !found {
					t.Errorf("ambig = %v, want to contain %q", spec.Ambig, a)
				}
			}
		})
	}
}

func TestRestIsTrivial(t *testing.T) {
	for in, want := range map[string]bool{
		"": true, " .,": true, "(Family Day)": true,
		"9 am to 8 pm": false, "see Winter Break schedule": false,
	} {
		if got := restIsTrivial(in); got != want {
			t.Errorf("restIsTrivial(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestGarbledRangeRepair(t *testing.T) {
	spec, rest, ok := parseLeadingDate("Monday, July 6 to 10 Friday, July 10", anchorAt(2026, 7, 1))
	if !ok || rest != "" {
		t.Fatalf("ok=%v rest=%q spec=%+v", ok, rest, spec)
	}
	if iso(spec.From) != "2026-07-06" || iso(spec.To) != "2026-07-10" {
		t.Errorf("range = %s..%s, want 2026-07-06..2026-07-10", iso(spec.From), iso(spec.To))
	}
	if len(spec.Ambig) != 1 || spec.Ambig[0] != ambDateGarbled {
		t.Errorf("ambig = %v, want [date-garbled]", spec.Ambig)
	}
	// no weekday on the trailing mention: refuse the repair
	if _, _, ok := parseLeadingDate("Monday, July 6 to 10 July 10", anchorAt(2026, 7, 1)); ok {
		t.Errorf("repaired a garbled range without weekday validation")
	}
}

func TestRangeWithWeekdayRestriction(t *testing.T) {
	spec, rest, ok := parseLeadingDate("April 23 to June 15, Monday to Friday, 8 am to 4 pm", anchorAt(2026, 5, 1))
	if !ok {
		t.Fatalf("not ok: %+v", spec)
	}
	if iso(spec.From) != "2026-04-23" || iso(spec.To) != "2026-06-15" || len(spec.Weekdays) != 5 {
		t.Errorf("got from=%s to=%s wds=%v", iso(spec.From), iso(spec.To), spec.Weekdays)
	}
	if rest != "8 am to 4 pm" {
		t.Errorf("rest = %q", rest)
	}
}
