package enrich

import (
	"fmt"
	"slices"
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
		// comma-enumerated days
		{in: "October 1, 2, 3, 4, 16, and 26", anchor: anchorAt(2026, 9, 29), ok: true, dates: []string{"2026-10-01", "2026-10-02", "2026-10-03", "2026-10-04", "2026-10-16", "2026-10-26"}},
		{in: "December 13, 14 and 21", anchor: anchorAt(2025, 12, 1), ok: true, dates: []string{"2025-12-13", "2025-12-14", "2025-12-21"}},
		{in: "December 24, 25, 26 and January 1", anchor: anchorAt(2025, 12, 1), ok: true, dates: []string{"2025-12-24", "2025-12-25", "2025-12-26", "2026-01-01"}},
		// a comma-led number that reads as a clock is not a day
		{in: "Monday, October 12, 7 am to 4 pm", anchor: anchorAt(2026, 9, 29), ok: true, dates: []string{"2026-10-12"}, rest: "7 am to 4 pm"},
		{in: "October 12, 7 to 9 pm", anchor: anchorAt(2026, 9, 29), ok: true, dates: []string{"2026-10-12"}, rest: "7 to 9 pm"},
		{in: "October 12, 7 and 8 pm", anchor: anchorAt(2026, 9, 29), ok: true, dates: []string{"2026-10-12"}, rest: "7 and 8 pm"},
		{in: "January 3 and 4, noon to 4 pm", anchor: anchorAt(2025, 12, 20), ok: true, dates: []string{"2026-01-03", "2026-01-04"}, rest: "noon to 4 pm"},
		{in: "July 3 - 10 am to 5 pm", anchor: anchorAt(2026, 7, 1), ok: true, dates: []string{"2026-07-03"}, rest: "- 10 am to 5 pm"},
		{in: "November 25 until further notice", anchor: anchorAt(2025, 11, 20), ok: true, from: "2025-11-25", open: true},
		{in: "Until August 21", anchor: anchorAt(2026, 8, 1), ok: true, to: "2026-08-21"},
		{in: "Until September 14", anchor: anchorAt(2026, 9, 1), ok: true, to: "2026-09-14"},
		{in: "Until further notice", anchor: anchorAt(2026, 9, 1), ok: false},
		{in: "Monday - Thursday: Closed", anchor: anchorAt(2026, 8, 1), ok: true, wds: 4, rest: "Closed"},
		{in: "Saturday and Sunday - 10 am to 5 pm", anchor: anchorAt(2026, 8, 1), ok: true, wds: 2, rest: "- 10 am to 5 pm"},
		{in: "Monday to Friday", anchor: anchorAt(2026, 1, 1), ok: true, wds: 5},
		{in: "Fridays, Saturdays, and Sundays", anchor: anchorAt(2026, 1, 1), ok: true, wds: 3},
		// From's weekday is a typo; anchor proximity must win over it
		{in: "Monday, June 7 to Sunday, June 28", anchor: anchorAt(2026, 6, 20), ok: true, from: "2026-06-07", to: "2026-06-28", ambig: []string{ambWeekdayMismatch}},
		{in: "Wednesday , November 26", anchor: anchorAt(2025, 11, 20), ok: true, dates: []string{"2025-11-26"}},
		{in: "Monday, February 16 (Family Day)", anchor: anchorAt(2026, 2, 1), ok: true, dates: []string{"2026-02-16"}, rest: "(Family Day)"},
		{in: "Thursday, June 25, 9 am to 8 pm", anchor: anchorAt(2026, 6, 20), ok: true, dates: []string{"2026-06-25"}, rest: "9 am to 8 pm"},
		// leading holiday label is stripped when a single date follows
		{in: "Civic Holiday, Monday, August 3", anchor: anchorAt(2026, 7, 20), ok: true, dates: []string{"2026-08-03"}},
		{in: "Civic Holiday, Monday, August 3, closed", anchor: anchorAt(2026, 7, 20), ok: true, dates: []string{"2026-08-03"}, rest: "closed"},
		{in: "Canada Day, Wednesday, July 1", anchor: anchorAt(2026, 6, 20), ok: true, dates: []string{"2026-07-01"}},
		{in: "Good Friday, April 3", anchor: anchorAt(2026, 3, 20), ok: true, dates: []string{"2026-04-03"}},
		{in: "Easter Monday, April 6", anchor: anchorAt(2026, 3, 20), ok: true, dates: []string{"2026-04-06"}},
		// not a holiday label: capitalized non-holiday lead stays unparsed
		{in: "Closed Monday, August 3", anchor: anchorAt(2026, 7, 20), ok: false},
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

func TestFindEmbeddedDate(t *testing.T) {
	for _, tc := range []struct {
		in       string
		anchor   time.Time
		ok       bool
		dates    []string
		from, to string
		open     bool
		wds      int
		span     string // the text of the span: the expression with its preposition
		ambig    []string
		notAmbig []string
	}{
		{in: "The pool is closed from Monday, March 23 to Sunday, April 12.", anchor: anchorAt(2026, 3, 2), ok: true, from: "2026-03-23", to: "2026-04-12", span: "from Monday, March 23 to Sunday, April 12"},
		{in: "The pool is closed between November 3, 2025 and February 1, 2026.", anchor: anchorAt(2025, 10, 24), ok: true, from: "2025-11-03", to: "2026-02-01", span: "between November 3, 2025 and February 1, 2026"},
		{in: "The facility will be closed starting May 1 until September 2026.", anchor: anchorAt(2026, 4, 25), ok: true, from: "2026-05-01", to: "2026-09-30", span: "starting May 1 until September 2026", ambig: []string{ambDateMonthOnly}},
		{in: "The pool is closed for maintenance until Monday, September 21 at 4 pm.", anchor: anchorAt(2026, 9, 1), ok: true, to: "2026-09-21", span: "until Monday, September 21"},
		{in: "The rink is closed until December 1 for ice installation.", anchor: anchorAt(2025, 11, 1), ok: true, to: "2025-12-01", span: "until December 1"},
		{in: "The facility is closed until July 19.", anchor: anchorAt(2026, 7, 1), ok: true, to: "2026-07-19", span: "until July 19"},
		{in: "Facility is closed between Thursday, May 21 at 5 pm and Friday, May 22 at 5:30 pm.", anchor: anchorAt(2026, 5, 1), ok: true, from: "2026-05-21", to: "2026-05-22", span: "between Thursday, May 21 at 5 pm and Friday, May 22"},
		{in: "Pool closed for annual maintenance August 17 to September 8.", anchor: anchorAt(2026, 8, 1), ok: true, from: "2026-08-17", to: "2026-09-08", span: "August 17 to September 8"},
		{in: "The facility is closed from August 22 to spring 2028 for renovations.", anchor: anchorAt(2026, 9, 1), ok: true, from: "2026-08-22", open: true, span: "from August 22", ambig: []string{ambDateEndUnstated}},
		{in: "Regular season ends August 23.", anchor: anchorAt(2026, 8, 1), ok: true, to: "2026-08-23", span: "August 23"},
		{in: "Public swim is cancelled on Monday, October 12.", anchor: anchorAt(2026, 10, 1), ok: true, dates: []string{"2026-10-12"}, span: "on Monday, October 12"},
		{in: "Programs may be cancelled without notice.", anchor: anchorAt(2026, 10, 1), ok: false},
		{in: "The facility will close at 4:30 pm and return to regular hours Friday, June 12.", anchor: anchorAt(2026, 6, 1), ok: false},
		{in: "The museum will reopen to daily visitors beginning Sunday, May 10, 2026.", anchor: anchorAt(2026, 4, 1), ok: false},
		{in: "beginning May 24 - 10 am to 5 pm", anchor: anchorAt(2026, 5, 1), ok: true, from: "2026-05-24", open: true, span: "beginning May 24", ambig: []string{ambDateEndUnstated}},
		{in: "Starting May 1, the pool is closed until further notice.", anchor: anchorAt(2026, 4, 25), ok: true, from: "2026-05-01", open: true, span: "Starting May 1", notAmbig: []string{ambDateEndUnstated}},
		{in: "The facility is closed until October.", anchor: anchorAt(2026, 8, 7), ok: true, to: "2026-10-31", span: "until October", ambig: []string{ambDateMonthOnly}},
		{in: "The facility is closed until October.", anchor: anchorAt(2026, 11, 2), ok: true, to: "2027-10-31", span: "until October", ambig: []string{ambDateMonthOnly}},
		{in: "The facility is closed until October for repairs.", anchor: anchorAt(2026, 10, 2), ok: true, to: "2026-10-31", span: "until October", ambig: []string{ambDateMonthOnly}},
		{in: "Lane swim, 1 to 3 pm, cancelled", anchor: anchorAt(2026, 10, 1), ok: false},
		{in: "The pool is closed until further notice.", anchor: anchorAt(2026, 10, 1), ok: false},
	} {
		t.Run(tc.in, func(t *testing.T) {
			spec, sp, ok := findEmbeddedDate(tc.in, tc.anchor)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (spec %+v span %v)", ok, tc.ok, spec, sp)
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
			}
			for i := range min(len(gotDates), len(tc.dates)) {
				if gotDates[i] != tc.dates[i] {
					t.Errorf("dates = %v, want %v", gotDates, tc.dates)
				}
			}
			got := func(x time.Time) string {
				if x.IsZero() {
					return ""
				}
				return iso(x)
			}
			if got(spec.From) != tc.from || got(spec.To) != tc.to || spec.OpenEnded != tc.open || len(spec.Weekdays) != tc.wds {
				t.Errorf("span = %s..%s open=%v wds=%v, want %s..%s open=%v wds=%d", got(spec.From), got(spec.To), spec.OpenEnded, spec.Weekdays, tc.from, tc.to, tc.open, tc.wds)
			}
			if got := tc.in[sp.start:sp.end]; got != tc.span || spec.Raw != tc.span {
				t.Errorf("span = %q, raw = %q, want %q", got, spec.Raw, tc.span)
			}
			for _, a := range tc.ambig {
				if !slices.Contains(spec.Ambig, a) {
					t.Errorf("ambig = %v, want to contain %q", spec.Ambig, a)
				}
			}
			for _, a := range tc.notAmbig {
				if slices.Contains(spec.Ambig, a) {
					t.Errorf("ambig = %v, want no %q", spec.Ambig, a)
				}
			}
		})
	}
}

// TestUntilFurtherNoticeStarts pins the head-date reading of "until further
// notice": under a single date it is a start, not the one day it applies;
// under a range or several dates the dates stand.
func TestUntilFurtherNoticeStarts(t *testing.T) {
	anchor := anchorAt(2026, time.January, 20)
	for _, tc := range []struct {
		head, want string
	}{
		{"Saturday, January 24", "from 20260124 open"},
		{"January 24 to 26", "20260124-20260126 open"},
		{"January 24 and 25", "dates 2 open"},
	} {
		spec, _, ok := parseLeadingDate(tc.head, anchor)
		if !ok {
			t.Fatalf("%q: no date", tc.head)
		}
		ds := toDateSpan(&spec, true)
		var got string
		switch {
		case len(ds.Dates) > 0:
			got = fmt.Sprintf("dates %d", len(ds.Dates))
		case ds.To.IsZero():
			got = fmt.Sprintf("from %d", ds.From/10)
		default:
			got = fmt.Sprintf("%d-%d", ds.From/10, ds.To/10)
		}
		if ds.OpenEnded {
			got += " open"
		}
		if got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.head, got, tc.want)
		}
	}
}

// TestYearUnderAnchorShift pins year resolution for a notice left up past
// its dates: a range containing the anchor keeps its year, and a
// weekday-agreed date half a year or more away is marked.
func TestYearUnderAnchorShift(t *testing.T) {
	for _, tc := range []struct {
		text   string
		anchor time.Time
		want   string
		amb    []string
	}{
		// St. Laurent, posted in October; still up in December
		{"June 2 to December 31", anchorAt(2026, time.October, 1), "20260602-20261231", nil},
		{"June 2 to December 31", anchorAt(2026, time.December, 10), "20260602-20261231", nil},
		// a typo for Friday, May 8, 2026: near the posting it is a mismatch
		// on the near date; read in August it agrees only with 2027
		{"Saturday, May 8", anchorAt(2026, time.May, 1), "20260508", []string{ambWeekdayMismatch}},
		{"Saturday, May 8", anchorAt(2026, time.August, 1), "20270508", []string{ambYearUnconfirmed}},
		{"Friday, May 8", anchorAt(2026, time.May, 1), "20260508", nil},
		// a weekday-validated range far from the anchor
		{"Saturday, May 8 to Sunday, May 9", anchorAt(2026, time.August, 1), "20270508-20270509", []string{ambYearUnconfirmed}},
	} {
		spec, _, ok := parseLeadingDate(tc.text, tc.anchor)
		if !ok {
			t.Fatalf("%q: no date", tc.text)
		}
		ymd := func(x time.Time) string { return x.Format("20060102") }
		var got string
		if len(spec.Dates) > 0 {
			got = ymd(spec.Dates[0])
		} else {
			got = ymd(spec.From) + "-" + ymd(spec.To)
		}
		if got != tc.want || !slices.Equal(spec.Ambig, tc.amb) {
			t.Errorf("%q at %s: got %s %v, want %s %v", tc.text, ymd(tc.anchor), got, spec.Ambig, tc.want, tc.amb)
		}
	}
}
