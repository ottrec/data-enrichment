package enrich

import (
	"slices"
	"strings"
)

// A sentence is one sentence of an item together with the byte spans its
// finders have claimed over it: the embedded date, the clock ranges, the
// single-ended clock mentions. The text is never rewritten. A finder runs
// over the sentence with the spans claimed so far blanked out (masked), so
// every match is at its offset in the source, and the rules after the
// finders read the masked text, remainder() (the text with the spans taken
// out) or clauses() (the remainder split at its commas and typed).
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
	spanClock                      // a clock range, with its preposition
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

// remainder returns the sentence without its spans: the masked text with
// its blanks closed up. A span takes nothing but itself, so every comma
// the city wrote stays where it was and is a clause boundary wherever it
// is ("Public swim, 1 to 3 pm, 25m pool only" reads "Public swim,, 25m
// pool only"; "Lane Swim, daily 11:30 am to 1:30 pm, will have shared
// space" reads "Lane Swim, daily, will have shared space"), and nothing
// dangles, since a date span and a clock span hold the preposition that
// introduced them ("The pool is closed from March 23 to April 12." reads
// "The pool is closed."; "From 11 am to 2 pm, all drop-in programs are
// cancelled" reads ", all drop-in programs are cancelled"). A single-ended
// mention leaves its keyword ("The pool is closed until noon." reads "The
// pool is closed.").
func (s *sentence) remainder() string {
	return clauseText(s.masked())
}

// clauseText closes up the blanks the spans left in a piece of the masked
// text: runs of spaces become one, and a space before a period or a comma
// goes.
func clauseText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.NewReplacer(" .", ".", " ,", ",").Replace(s)
}

// A clause is one comma segment of the remainder, typed by what it says.
type clauseKind uint8

const (
	clauseSubject     clauseKind = iota // what the notice is about
	clauseKeyword                       // an effect keyword ("cancelled", "added", "closed"), with or without a reason
	clauseTimeChange                    // "schedule change"
	clauseHours                         // an hours label ("Modified hours", "facility hours")
	clauseRestriction                   // "25m pool only", "moved to 25m warm pool", or a bare "only" after a clock
	clauseConjunction                   // "and" or "or" alone, or punctuation alone: nothing to read
)

type clause struct {
	kind clauseKind
	text string // the segment with its blanks closed up; for a restriction, the restriction text
	kw   string // the keyword, for a clauseKeyword
}

// clauses splits the sentence at its commas, spans blanked, and types each
// segment; an empty segment (one that was only a span) is left out. A
// keyword, a time change, an hours label and a conjunction are what they
// say. A restriction needs a subject before it; that covers "X only" and
// the note clauses, and a bare "only" left beside a clock span ("Public
// swim, 1:30 to 3 pm only"), which read as a subject would be matched as
// an activity of its own. Everything else is a subject.
func (s *sentence) clauses() []clause {
	var out []clause
	subjects := 0
	masked := s.masked()
	for start := 0; start <= len(masked); {
		end := len(masked)
		if i := strings.IndexByte(masked[start:], ','); i >= 0 {
			end = start + i
		}
		text := clauseText(masked[start:end])
		clockBefore := slices.ContainsFunc(s.spans, func(sp span) bool { return sp.kind == spanClock && sp.start < end })
		start = end + 1
		if text == "" {
			continue
		}
		c := clause{text: text}
		switch fc := foldText(text); {
		case fc == "" || fc == "and" || fc == "or":
			c.kind = clauseConjunction
		case keywordRe.MatchString(fc):
			c.kind, c.kw = clauseKeyword, keywordRe.FindStringSubmatch(fc)[1]
		case fc == "schedule change" || fc == "schedule changes":
			c.kind = clauseTimeChange
		case hoursClauseRe.MatchString(fc):
			c.kind = clauseHours
		case subjects > 0 && (strings.HasSuffix(fc, " only") || (fc == "only" && clockBefore) || noteClauseRe.MatchString(fc)):
			c.kind, c.text = clauseRestriction, strings.Trim(normText(text), " .")
		default:
			c.kind = clauseSubject
			subjects++
		}
		out = append(out, c)
	}
	return out
}
