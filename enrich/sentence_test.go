package enrich

import "testing"

// TestRemainder pins what each kind of span takes with it (see
// sentence.remainder): the comma a clock range that was a clause of its own
// leaves behind, the space that joins the halves of a clause around one,
// the preposition and conjunction it leaves, the date's seam, and the
// single-ended mention's keyword.
func TestRemainder(t *testing.T) {
	anchor := anchorAt(2026, 3, 2)
	for _, tc := range []struct {
		in, want string
	}{
		// a clock range that was a clause of its own keeps the clauses apart
		{"Aquafit, 8:05 to 9 am, cancelled", "Aquafit, cancelled"},
		{"Public swim, 1 to 3 pm, 25m pool only", "Public swim, 25m pool only"},
		// one inside a clause joins the halves
		{"Lane Swim, daily 11:30 am to 1:30 pm, will have shared space", "Lane Swim, daily will have shared space"},
		{"Public swim, 1:30 to 3 pm only", "Public swim only"},
		// what introduced it stays: the preposition, the conjunction
		{"From 11 am to 2 pm, all drop-in programs are cancelled", "From all drop-in programs are cancelled"},
		{"Lane swim, 12:30 to 1 pm, and 8 to 9 pm.", "Lane swim, and ."},
		{"Lane swim, 8 to 9 am, 10 to 11 am, cancelled", "Lane swim,  cancelled"},
		{"8 to 9 am", ""},
		// a date leaves one space and closes up before a period
		{"The pool is closed from Monday, March 23 to Sunday, April 12.", "The pool is closed."},
		{"The rink is closed until December 1 for ice installation.", "The rink is closed for ice installation."},
		{"Public swim, Monday, March 23, cancelled", "Public swim, cancelled"},
		// a single-ended mention leaves its keyword
		{"The pool is closed until noon.", "The pool is closed."},
		{"The hot tub and steam room is closed at 7:30 pm.", "The hot tub and steam room is closed"},
		{"Public swim will end at 6 pm.", "Public swim"},
		// a mention across a date: the pieces around the date are claimed
		{"Facility is closed between Thursday, May 21 at 5 pm and Friday, May 22 at 5:30 pm.", "Facility is closed"},
		{"The pool is closed for maintenance until Monday, September 21 at 4 pm.", "The pool is closed for maintenance at 4 pm."},
		// nothing claimed
		{"See Winter Break schedule.", "See Winter Break schedule."},
	} {
		sent := &sentence{src: tc.in}
		if _, sp, ok := findEmbeddedDate(tc.in, anchor); ok {
			sent.claim(sp)
		}
		for _, cm := range findClockRanges(sent.masked()) {
			sent.claim(cm.Span)
		}
		_, claimed := findSingleEnded(sent.masked())
		for _, sp := range claimed {
			sent.claim(sp)
		}
		if got := sent.remainder(); got != tc.want {
			t.Errorf("remainder(%q) = %q, want %q (spans %v)", tc.in, got, tc.want, sent.spans)
		}
	}
}

func TestSentenceClaim(t *testing.T) {
	sent := &sentence{src: "abcdefghij"}
	sent.claim(span{3, 6, spanDate})
	sent.claim(span{1, 8, spanClock})  // across the date: the pieces around it
	sent.claim(span{4, 5, spanSingle}) // inside the date: nothing
	want := []span{{1, 3, spanClock}, {3, 6, spanDate}, {6, 8, spanClock}}
	if len(sent.spans) != len(want) {
		t.Fatalf("spans = %v, want %v", sent.spans, want)
	}
	for i := range want {
		if sent.spans[i] != want[i] {
			t.Errorf("spans = %v, want %v", sent.spans, want)
		}
	}
	if got := sent.masked(spanDate); got != "abc   ghij" {
		t.Errorf("masked(date) = %q", got)
	}
	if got := sent.masked(); got != "a       ij" {
		t.Errorf("masked() = %q", got)
	}
}
