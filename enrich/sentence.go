package enrich

import (
	"slices"
	"strings"
	"unicode"
)

// A sentence is one sentence of an item together with the byte spans its
// finders have claimed over it: the embedded date, the clock ranges, the
// single-ended clock mentions. The text is never rewritten. A finder runs
// over the sentence with the spans claimed so far blanked out (masked), so
// every match is at its offset in the source, and the rules after the
// finders read either the masked text or remainder(), the text with the
// spans taken out under the punctuation rules below.
type sentence struct {
	src   string
	spans []span // sorted by start, never overlapping
}

// span is a claimed byte range [start, end) of a sentence.
type span struct {
	start, end int
	kind       spanKind
}

type spanKind uint8

const (
	spanDate   spanKind = iota + 1 // an embedded date expression, with its preposition
	spanClock                      // a clock range
	spanSingle                     // a single-ended clock mention, without its keyword
)

// claim records the span. A finder scans the masked text, and a pattern
// with \s+ in it can match across a blank ("closed [between May 21 ...
// May 22] at 5:30 pm" matches "closed at 5:30 pm"), so a claim may overlap
// an earlier span; only its unclaimed pieces are recorded, and spans never
// overlap.
func (s *sentence) claim(sp span) {
	for _, have := range s.spans {
		if sp.start < have.end && have.start < sp.end {
			if sp.start < have.start {
				s.claim(span{sp.start, have.start, sp.kind})
			}
			if have.end < sp.end {
				s.claim(span{have.end, sp.end, sp.kind})
			}
			return
		}
	}
	if sp.start >= sp.end {
		return
	}
	s.spans = append(s.spans, sp)
	slices.SortFunc(s.spans, func(a, b span) int { return a.start - b.start })
}

// masked returns the source with the claimed spans of the given kinds (all
// kinds when none is given) blanked to spaces, so an offset into it is an
// offset into the source.
func (s *sentence) masked(kinds ...spanKind) string {
	b := []byte(s.src)
	for _, sp := range s.spans {
		if len(kinds) > 0 && !slices.Contains(kinds, sp.kind) {
			continue
		}
		for i := sp.start; i < sp.end; i++ {
			b[i] = ' '
		}
	}
	return string(b)
}

// remainder returns the sentence without its spans, for the clause split.
// Each kind of span takes a fixed amount of the punctuation around it with
// it, so the clauses around a span are what they were when the finders cut
// the text instead of claiming it:
//
//   - A clock range takes the spaces and commas on both sides. When it had
//     a comma on each side it was a clause of its own and leaves one comma,
//     so the clauses around it stay apart ("Public swim, 1 to 3 pm, 25m
//     pool only" reads "Public swim, 25m pool only"); otherwise the two
//     sides join with a space ("Lane Swim, daily 11:30 am to 1:30 pm, will
//     have shared space" reads "Lane Swim, daily will have shared space").
//     What introduced the range stays behind: a preposition ("From 11 am to
//     2 pm, all ..." reads "From all ...", which danglingPrepRe takes off
//     the phrase) or a conjunction ("Lane swim, 12:30 to 1 pm, and 8 to 9
//     pm" reads "Lane swim, and", a clause the loop skips).
//   - An embedded date takes the space before it and the spaces and commas
//     after it and leaves one space; a space left before a period closes up
//     ("The pool is closed from March 23 to April 12." reads "The pool is
//     closed.").
//   - A single-ended mention takes nothing: its keyword stays and the text
//     closes up over the span ("The pool is closed until noon." reads "The
//     pool is closed.").
func (s *sentence) remainder() string {
	out := ""
	pos := 0  // the next source byte not yet consumed
	seam := 0 // where in out the text since the last clock range begins
	for _, sp := range s.spans {
		seg := ""
		if sp.start > pos {
			seg = s.src[pos:sp.start]
		}
		after := s.src[sp.end:]
		switch sp.kind {
		case spanDate:
			out += strings.TrimRightFunc(seg, unicode.IsSpace) + " "
			pos = max(pos, sp.end+len(after)-len(strings.TrimLeft(after, " ,")))
		case spanClock:
			left := strings.TrimRight(out[seam:]+seg, " ")
			out = out[:seam] + strings.TrimRight(left, " ,")
			if strings.HasSuffix(left, ",") && strings.HasPrefix(strings.TrimLeft(after, " "), ",") {
				out += ","
			}
			out += " "
			seam = len(out)
			pos = max(pos, sp.end+len(after)-len(strings.TrimLeft(after, " ,")))
		case spanSingle:
			out += seg
			pos = max(pos, sp.end)
		}
	}
	out += s.src[pos:]
	if slices.ContainsFunc(s.spans, func(sp span) bool { return sp.kind == spanDate }) {
		out = strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(out, "  ", " ")), " .", ".")
	}
	return strings.TrimSpace(out)
}
