package enrich

import (
	"slices"
	"strings"
	"testing"

	"github.com/ottrec/scraper/schema"
)

func TestFindClockRanges(t *testing.T) {
	for _, tc := range []struct {
		in       string
		first    schema.ClockRange // best (first) candidate of the first mention
		spans    []string          // the text of each mention's span
		text     string            // the first mention's text, when not its span's words
		cands    int               // candidates of the first mention
		inferred bool
	}{
		{in: "Aquafit, 8:05 to 9 am, cancelled", first: schema.ClockRange{Start: 8*60 + 5, End: 9 * 60}, spans: []string{"8:05 to 9 am"}, cands: 1, inferred: true},
		{in: "Noon to 5 pm", first: schema.ClockRange{Start: 12 * 60, End: 17 * 60}, spans: []string{"Noon to 5 pm"}, cands: 1},
		{in: "4:15 to 5:15 pm", first: schema.ClockRange{Start: 16*60 + 15, End: 17*60 + 15}, spans: []string{"4:15 to 5:15 pm"}, cands: 1, inferred: true},
		{in: "1 to 5 pm", first: schema.ClockRange{Start: 13 * 60, End: 17 * 60}, spans: []string{"1 to 5 pm"}, cands: 1, inferred: true},
		{in: "8:30 to 10:30", first: schema.ClockRange{Start: 8*60 + 30, End: 10*60 + 30}, spans: []string{"8:30 to 10:30"}, cands: 2, inferred: true},
		{in: "10 pm to midnight", first: schema.ClockRange{Start: 22 * 60, End: 24 * 60}, spans: []string{"10 pm to midnight"}, cands: 1},
		{in: "December 13 and 14"},
		{in: "Lane swim, 12:30 to 1 pm, and 8 to 9 pm.", first: schema.ClockRange{Start: 12*60 + 30, End: 13 * 60}, spans: []string{"12:30 to 1 pm", "8 to 9 pm"}, cands: 1, inferred: true},
		// the span takes the preposition that introduced the range; the
		// text is the range
		{in: "From 11 am to 2 pm, all drop-in programs are cancelled", first: schema.ClockRange{Start: 11 * 60, End: 14 * 60}, spans: []string{"From 11 am to 2 pm"}, text: "11 am to 2 pm", cands: 1},
		{in: "Saturdays and Sundays from 10 am to 5 pm", first: schema.ClockRange{Start: 10 * 60, End: 17 * 60}, spans: []string{"from 10 am to 5 pm"}, text: "10 am to 5 pm", cands: 1},
		// "and" joins a range only after "between"
		{in: "The 25 m pool is closed between 7:30 and 10:30 am.", first: schema.ClockRange{Start: 7*60 + 30, End: 10*60 + 30}, spans: []string{"between 7:30 and 10:30 am"}, text: "7:30 and 10:30 am", cands: 1, inferred: true},
		{in: "Lane swim at 7 and 8 pm"},
		{in: "Lane swim at 7 and 8 pm, 9 to 10 pm", first: schema.ClockRange{Start: 21 * 60, End: 22 * 60}, spans: []string{"9 to 10 pm"}, cands: 1, inferred: true},
		// a match across a blank (a claimed date) is one mention
		{in: "closed from 5 pm                  to 7 pm", first: schema.ClockRange{Start: 17 * 60, End: 19 * 60}, spans: []string{"from 5 pm                  to 7 pm"}, text: "5 pm to 7 pm", cands: 1},
	} {
		t.Run(tc.in, func(t *testing.T) {
			ms := findClockRanges(tc.in)
			if got := spanTexts(tc.in, ms); !slices.Equal(got, tc.spans) {
				t.Fatalf("spans = %q, want %q", got, tc.spans)
			}
			if len(ms) > 0 {
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
				want := tc.text
				if want == "" {
					want = strings.Join(strings.Fields(tc.spans[0]), " ")
				}
				if m.Text != want {
					t.Errorf("text = %q, want %q", m.Text, want)
				}
			}
		})
	}
}

func spanTexts(s string, ms []clockMention) []string {
	var out []string
	for _, m := range ms {
		out = append(out, s[m.Span.start:m.Span.end])
	}
	return out
}

func TestOnlyClocks(t *testing.T) {
	for in, want := range map[string]bool{
		"11:45 am to 12:45 pm":         true,
		"8 to 9 am, 10 to 11 am.":      true,
		"Noon 1 pm":                    false,
		"Lane swim, 8 to 9 am":         false,
		"8 to 9 am and 10 to 11 am":    false,
		"from 8 to 9 am":               true,
		"between 9 am and 4 pm":        true,
		"Friday, October 2, 8 to 9 am": false,
	} {
		if got := onlyClocks(in); got != want {
			t.Errorf("onlyClocks(%q) = %v, want %v", in, got, want)
		}
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
		span      string // the text of the mention's span
		first     schema.ClockRange
		openStart bool
		openEnd   bool
		endEarly  bool
	}{
		{in: "The pool is closed until noon.", span: " until noon", first: schema.ClockRange{Start: 0, End: 720}, openStart: true},
		{in: "The hot tub and steam room is closed at 7:30 pm.", span: " at 7:30 pm.", first: schema.ClockRange{Start: 19*60 + 30, End: 1440}, openEnd: true},
		{in: "Public swim will end at 6 pm.", span: "will end at 6 pm.", first: schema.ClockRange{Start: 18 * 60, End: 1440}, openEnd: true, endEarly: true},
		{in: "The pool will open at 10 am.", span: " at 10 am.", first: schema.ClockRange{Start: 0, End: 10 * 60}, openStart: true},
		{in: "closed until further notice"},
		{in: "Lane swim, cancelled"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			ms, spans := findSingleEnded(tc.in)
			var want []string
			if tc.span != "" {
				want = []string{tc.span}
			}
			if got := spanTexts(tc.in, ms); !slices.Equal(got, want) {
				t.Fatalf("spans = %q, want %q", got, want)
			}
			if len(spans) != len(ms) {
				t.Errorf("claimed %d spans for %d mentions", len(spans), len(ms))
			}
			if len(ms) > 0 {
				m := ms[0]
				if m.Cands[0] != tc.first || m.OpenStart != tc.openStart || m.OpenEnd != tc.openEnd || m.EndEarly != tc.endEarly {
					t.Errorf("got %+v, want first=%v openStart=%v openEnd=%v endEarly=%v", m, tc.first, tc.openStart, tc.openEnd, tc.endEarly)
				}
			}
		})
	}
	// a time with no plausible reading yields no mention but is still claimed
	ms, spans := findSingleEnded("The pool is closed until midnight.")
	if len(ms) != 0 || len(spans) != 1 || spans[0] != (span{18, 33, spanSingle}) {
		t.Errorf("midnight: mentions %v, spans %v", ms, spans)
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
