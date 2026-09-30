package enrich

import (
	"strings"
	"testing"
)

// Contracts of the span finders: what the clause code after them relies
// on. Each is over claimSpans's remainder for sentences the city writes.

var contractSentences = []string{
	"Aquafit, 8:05 to 9 am, cancelled",
	"Public swim, 1 to 3 pm, 25m pool only",
	"Public swim, 1:30 to 3 pm only",
	"Lane swim, 12:30 to 1 pm, and 8 to 9 pm.",
	"Lane swim, 12:30 to 1 pm, 8 to 9 pm, cancelled",
	"Pickleball - rotations, 2:30-3:30 pm - cancelled",
	"From 11 am to 2 pm, all drop-in programs are cancelled",
	"Lane Swim, daily 11:30 am to 1:30 pm, will have shared space",
	"Modified hours, 6 am to 8 pm",
	"Women's only swim, 8:15 to 9:15 pm, added",
	"The baby pool will be closed from 1 to 5 pm.",
	"Saturdays and Sundays from 10 am to 5 pm",
	"Sunday, May 10 to Friday, October 9 from 9 am to 4 pm.",
	"Emergency cooling centre will be open from 10 am to 8 pm",
	"Monday, July 27 to Friday, July 31, between 9 am and 4 pm",
	"The 25 m pool is closed between 7:30 and 10:30 am.",
	"The pool is closed until noon.",
	"The hot tub and steam room is closed at 7:30 pm.",
	"Public swim will end at 6 pm.",
	"The facility will open at 10:45 am.",
	"The pool is closed from Monday, March 23 to Sunday, April 12.",
	"The facility will be closed starting May 1 until September 2026.",
	"The rink is closed until December 1 for ice installation.",
	"Public swim is cancelled on Monday, October 12.",
	"Facility is closed between Thursday, May 21 at 5 pm and Friday, May 22 at 5:30 pm.",
	"The pool is closed for maintenance until Monday, September 21 at 4 pm.",
}

func countClauses(s string) int {
	n := 0
	for c := range strings.SplitSeq(s, ",") {
		if strings.Trim(c, " .-") != "" {
			n++
		}
	}
	return n
}

func words(s string) []string {
	return strings.Fields(foldText(s))
}

// minusWords returns a's words with b's words removed, in order.
func minusWords(a, b []string) []string {
	drop := map[string]int{}
	for _, w := range b {
		drop[w]++
	}
	var out []string
	for _, w := range a {
		if drop[w] > 0 {
			drop[w]--
			continue
		}
		out = append(out, w)
	}
	return out
}

// A comma is a clause boundary: the spans take exactly the clauses they
// cover with them, a span inside a clause takes none, and every other
// clause survives.
func TestSpansKeepClauses(t *testing.T) {
	anchor := anchorAt(2026, 9, 1)
	for _, in := range contractSentences {
		sent := claimSpans(in, anchor)
		masked, pos, taken := sent.masked(), 0, 0
		for c := range strings.SplitSeq(sent.src, ",") {
			if strings.Trim(c, " .-") != "" && strings.Trim(masked[pos:pos+len(c)], " .-") == "" {
				taken++
			}
			pos += len(c) + 1
		}
		rest := sent.remainder()
		if got, want := countClauses(rest), countClauses(sent.src)-taken; got != want {
			t.Errorf("%q: remainder %q has %d clauses, want %d", in, rest, got, want)
		}
	}
}

// The words of the remainder are the words of the sentence minus the words
// of the spans, in order.
func TestSpansKeepWords(t *testing.T) {
	anchor := anchorAt(2026, 9, 1)
	for _, in := range contractSentences {
		sent := claimSpans(in, anchor)
		want := words(sent.src)
		for _, sp := range sent.spans {
			want = minusWords(want, words(sent.src[sp.start:sp.end]))
		}
		if got := words(sent.remainder()); strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%q: words %v, want %v", in, got, want)
		}
	}
}

// A preposition goes with its object: no "from" or "between" is left in the
// remainder once its clock or date is claimed.
func TestPrepositionGoesWithSpan(t *testing.T) {
	anchor := anchorAt(2026, 9, 1)
	for _, in := range contractSentences {
		rest := claimSpans(in, anchor).remainder()
		for _, w := range words(rest) {
			if w == "from" || w == "between" {
				t.Errorf("%q: %q left in %q", in, w, rest)
			}
		}
	}
}

// The spans lie inside the sentence and never overlap: each finder matched
// inside one unclaimed segment.
func TestSpansDisjoint(t *testing.T) {
	anchor := anchorAt(2026, 9, 1)
	for _, in := range append(contractSentences, "The pool is closed on Monday, October 12 at 5 pm.") {
		sent := claimSpans(in, anchor)
		pos := 0
		for _, sp := range sent.spans {
			if sp.start < pos || sp.end <= sp.start || sp.end > len(sent.src) {
				t.Errorf("%q: span %v out of order or bounds in %v", in, sp, sent.spans)
			}
			pos = sp.end
		}
	}
}
