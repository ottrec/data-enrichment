package enrich

import (
	"testing"

	"github.com/ottrec/scraper/schema"
)

func TestFindClockRanges(t *testing.T) {
	for _, tc := range []struct {
		in       string
		first    schema.ClockRange // best (first) candidate of the first mention
		n        int               // number of mentions
		cands    int               // candidates of the first mention
		inferred bool
		rest     string
	}{
		{in: "Aquafit, 8:05 to 9 am, cancelled", first: schema.ClockRange{Start: 8*60 + 5, End: 9 * 60}, n: 1, cands: 1, inferred: true, rest: "Aquafit cancelled"},
		{in: "Noon to 5 pm", first: schema.ClockRange{Start: 12 * 60, End: 17 * 60}, n: 1, cands: 1},
		{in: "4:15 to 5:15 pm", first: schema.ClockRange{Start: 16*60 + 15, End: 17*60 + 15}, n: 1, cands: 1, inferred: true},
		{in: "1 to 5 pm", first: schema.ClockRange{Start: 13 * 60, End: 17 * 60}, n: 1, cands: 1, inferred: true},
		{in: "8:30 to 10:30", first: schema.ClockRange{Start: 8*60 + 30, End: 10*60 + 30}, n: 1, cands: 2, inferred: true},
		{in: "10 pm to midnight", first: schema.ClockRange{Start: 22 * 60, End: 24 * 60}, n: 1, cands: 1},
		{in: "December 13 and 14", n: 0, rest: "December 13 and 14"},
		{in: "Lane swim, 12:30 to 1 pm, and 8 to 9 pm.", first: schema.ClockRange{Start: 12*60 + 30, End: 13 * 60}, n: 2, cands: 1, inferred: true, rest: "Lane swim and ."},
	} {
		t.Run(tc.in, func(t *testing.T) {
			ms, rest := findClockRanges(tc.in)
			if len(ms) != tc.n {
				t.Fatalf("mentions = %d (%v), want %d", len(ms), ms, tc.n)
			}
			if tc.n > 0 {
				m := ms[0]
				if len(m.Cands) != tc.cands {
					t.Errorf("cands = %v, want %d", m.Cands, tc.cands)
				}
				if m.Cands[0] != tc.first {
					t.Errorf("first = %v, want %v", m.Cands[0], tc.first)
				}
				if m.Inferred != tc.inferred {
					t.Errorf("inferred = %v, want %v", m.Inferred, tc.inferred)
				}
			}
			if tc.rest != "" && rest != tc.rest {
				t.Errorf("rest = %q, want %q", rest, tc.rest)
			}
		})
	}
}

func TestTokens(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"All drop-in skating and ice sports", "skate ice sports"},
		{"Aqua Lite", "aquafit lite"},
		{"Ringette (10 to 14 years)", "ringette 10 14 years"},
		{"The Groove Method®", "groove method"},
		{"All gymnasium programming", "gymnasium"},
	} {
		got := ""
		for i, tok := range tokens(tc.in) {
			if i > 0 {
				got += " "
			}
			got += tok
		}
		if got != tc.want {
			t.Errorf("tokens(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAmenity(t *testing.T) {
	for in, want := range map[string]bool{
		"hot tub":                  true,
		"roger sénécal arena":      true,
		"1 metre diving board":     true,
		"men's pool changeroom":    true,
		"lap pool heater broken":   true,
		"aquafit":                  false,
		"public skating":           false,
		"emergency cooling centre": true, // ends with a core noun
	} {
		if got := isAmenity(in); got != want {
			t.Errorf("isAmenity(%q) = %v, want %v", in, got, want)
		}
	}
	if got := amenityName("lap pool heater broken pool temperature is colder"); got != "lap pool" {
		t.Errorf("amenityName = %q, want %q", got, "lap pool")
	}
}

func TestSubjectIsFacility(t *testing.T) {
	for _, tc := range []struct {
		subject, fac string
		want         bool
	}{
		{"facility", "Anything", true},
		{"canterbury community center", "Canterbury Recreation Complex", true},
		{"baby pool", "Kanata Leisure Centre and Wave Pool", false},
		{"roger sénécal arena", "Bob MacQuarrie Recreation Complex - Orléans", false},
		{"rink", "Jim Tubman Chevrolet Rink", true},
		{"mooney's bay cross country ski centre", "Mooney's Bay Park", true},
	} {
		if got := subjectIsFacility(tc.subject, tc.fac); got != tc.want {
			t.Errorf("subjectIsFacility(%q, %q) = %v, want %v", tc.subject, tc.fac, got, tc.want)
		}
	}
}

func TestFindSingleEnded(t *testing.T) {
	for _, tc := range []struct {
		in        string
		n         int
		first     schema.ClockRange
		openStart bool
		openEnd   bool
		endEarly  bool
		rest      string
	}{
		{in: "The pool is closed until noon.", n: 1, first: schema.ClockRange{Start: 0, End: 720}, openStart: true, rest: "The pool is closed."},
		{in: "The hot tub and steam room is closed at 7:30 pm.", n: 1, first: schema.ClockRange{Start: 19*60 + 30, End: 1440}, openEnd: true, rest: "The hot tub and steam room is closed"},
		{in: "Public swim will end at 6 pm.", n: 1, first: schema.ClockRange{Start: 18 * 60, End: 1440}, openEnd: true, endEarly: true, rest: "Public swim"},
		{in: "closed until further notice", n: 0, rest: "closed until further notice"},
		{in: "Lane swim, cancelled", n: 0, rest: "Lane swim, cancelled"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			ms, rest := findSingleEnded(tc.in)
			if len(ms) != tc.n {
				t.Fatalf("mentions = %v, want %d", ms, tc.n)
			}
			if tc.n > 0 {
				m := ms[0]
				if m.Cands[0] != tc.first || m.OpenStart != tc.openStart || m.OpenEnd != tc.openEnd || m.EndEarly != tc.endEarly {
					t.Errorf("got %+v, want first=%v openStart=%v openEnd=%v endEarly=%v", m, tc.first, tc.openStart, tc.openEnd, tc.endEarly)
				}
			}
			if rest != tc.rest {
				t.Errorf("rest = %q, want %q", rest, tc.rest)
			}
		})
	}
}

func TestSplitSentences(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"The 25 m pool is closed between 7:30 and 10:30 am. Lane swim, 7:30 to 8:30 am, cancelled", 2},
		{"Lap pool heater broken. Pool temperature is colder than normal until further notice.", 2},
		{"Lane swim, 11 am to 3 pm, cancelled", 1},
		{"See Winter Break schedule.", 1},
		{"Open 9 a.m. Monday", 1}, // abbreviation must not split
	} {
		if got := splitSentences(tc.in); len(got) != tc.want {
			t.Errorf("splitSentences(%q) = %q, want %d parts", tc.in, got, tc.want)
		}
	}
}

func TestDlLE1(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"baddminton", "badminton", true},
		{"therapuetic", "therapeutic", true}, // transposition
		{"badminton", "badminton", true},
		{"skating", "swimming", false},
		{"swim", "swims", true},
	} {
		if got := dlLE1(tc.a, tc.b); got != tc.want {
			t.Errorf("dlLE1(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
