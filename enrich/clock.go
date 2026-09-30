package enrich

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ottrec/scraper/schema"
)

const (
	ambMeridiemInferred  = "meridiem-inferred"  // missing am/pm resolved by context
	ambMeridiemAmbiguous = "meridiem-ambiguous" // missing am/pm, several readings fit
)

const clockTokenPat = `(?:\d{1,2}(?::\d{2})?(?:\s*(?:a\.?m\.?|p\.?m\.?))?|noon|midnight)`

// clockRangeRe matches a clock range together with the preposition that
// introduces it, when one does ("from 11 am to 2 pm", "between 7:30 and
// 10:30 am"). "and" joins a range only after "between" (findClockRanges
// checks): "7 and 8 pm" is two times.
var clockRangeRe = regexp.MustCompile(`(?i)(?:\b(from|between)\s+)?\b(` + clockTokenPat + `)\s*(to|until|through|and|-|–|—)\s*(` + clockTokenPat + `)\b`)

var clockSideRe = regexp.MustCompile(`(?i)^(?:(\d{1,2})(?::(\d{2}))?\s*(a\.?m\.?|p\.?m\.?)?|(noon)|(midnight))$`)

// clockMention is one clock range found in text, with all candidate
// interpretations when meridiems are missing. Single-ended mentions
// ("closed until noon") synthesize the affected portion of the day and set
// OpenStart/OpenEnd.
type clockMention struct {
	Text      string
	Span      span // where in the text it was found
	Cands     []schema.ClockRange
	Inferred  bool // a meridiem was missing and had to be inferred
	OpenStart bool // "until X": affected from start of day to X
	OpenEnd   bool // "at/from X on": affected from X to end of day
	EndEarly  bool // "will end at X": a time change, not a closure
}

// parseClockSide parses one side of a clock range into minutes from midnight
// and whether the meridiem was explicit.
func parseClockSide(s string) (minutes int, explicit bool, ok bool) {
	m := clockSideRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false, false
	}
	switch {
	case m[4] != "":
		return 12 * 60, true, true
	case m[5] != "":
		return 0, true, true
	}
	h, _ := strconv.Atoi(m[1])
	var mm int
	if m[2] != "" {
		mm, _ = strconv.Atoi(m[2])
	}
	if h < 1 || h > 12 || mm > 59 {
		return 0, false, false
	}
	switch strings.ToLower(strings.ReplaceAll(m[3], ".", "")) {
	case "am":
		if h == 12 {
			h = 0
		}
		return h*60 + mm, true, true
	case "pm":
		if h != 12 {
			h += 12
		}
		return h*60 + mm, true, true
	}
	return h%12*60 + mm, false, true
}

// findClockRanges finds the clock ranges in s and returns them with their
// spans; s is left alone. A match must have an explicit meridiem,
// noon/midnight, or minutes on at least one side (so "December 13 and 14"
// is not a clock range), and "and" joins the two sides only after
// "between" ("between 7:30 and 10:30 am"; "Lane swim at 7 and 8 pm" has no
// range). The span takes the "from" or "between" that introduced the range
// with it, so the preposition goes with its object; the mention's text is
// the range's words. s is one unclaimed segment of a sentence
// (claimClockRanges), so a span is an offset into that segment.
func findClockRanges(s string) []clockMention {
	var out []clockMention
	pos := 0
	for pos < len(s) {
		loc := clockRangeRe.FindStringSubmatchIndex(s[pos:])
		if loc == nil {
			break
		}
		prep, a, joiner, b := "", s[pos+loc[4]:pos+loc[5]], s[pos+loc[6]:pos+loc[7]], s[pos+loc[8]:pos+loc[9]]
		if loc[2] >= 0 {
			prep = strings.ToLower(s[pos+loc[2] : pos+loc[3]])
		}
		var cands []schema.ClockRange
		var inferred bool
		if (clockish(a) || clockish(b)) && (strings.ToLower(joiner) != "and" || prep == "between") {
			cands, inferred = clockCandidates(a, b)
		}
		start, clockStart, end := pos+loc[0], pos+loc[4], pos+loc[1]
		pos = end
		if len(cands) == 0 {
			continue // not a clock range
		}
		out = append(out, clockMention{
			Text:     strings.Join(strings.Fields(s[clockStart:end]), " "),
			Span:     span{start, end, spanClock},
			Cands:    cands,
			Inferred: inferred,
		})
	}
	return out
}

// onlyClocks reports whether s is clock ranges and nothing else, punctuation
// aside ("11:45 am to 12:45 pm", "8 to 9 am, 10 to 11 am", "from 8 to 9
// am").
func onlyClocks(s string) bool {
	sent := &sentence{src: s}
	if len(sent.claimClockRanges()) == 0 {
		return false
	}
	return strings.Trim(sent.masked(), " .,") == ""
}

// clockish reports whether one side of a range looks unambiguously like a
// clock time on its own.
func clockish(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.Contains(s, ":") || strings.Contains(s, "am") || strings.Contains(s, "pm") ||
		strings.Contains(s, "a.m") || strings.Contains(s, "p.m") || s == "noon" || s == "midnight"
}

// clockCandidates enumerates the plausible readings of a clock range,
// resolving missing meridiems. Explicit both sides yields one candidate
// (overnight allowed); otherwise start<end readings, preferring spans of at
// most 12h (the conventional reading of "4:15 to 5:15 pm") ordered shortest
// first.
func clockCandidates(a, b string) ([]schema.ClockRange, bool) {
	av, aok, ok1 := parseClockSide(a)
	bv, bok, ok2 := parseClockSide(b)
	if !ok1 || !ok2 {
		return nil, false
	}
	if aok && bok {
		e := bv
		if e <= av {
			e += 24 * 60 // overnight
		}
		return []schema.ClockRange{{Start: schema.ClockTime(av), End: schema.ClockTime(e)}}, false
	}
	avs := []int{av}
	if !aok && av+12*60 < 24*60 {
		avs = append(avs, av+12*60)
	}
	bvs := []int{bv}
	if !bok && bv+12*60 < 24*60 {
		bvs = append(bvs, bv+12*60)
	}
	var cands []schema.ClockRange
	for _, s := range avs {
		for _, e := range bvs {
			if e > s && e-s <= 18*60 {
				cands = append(cands, schema.ClockRange{Start: schema.ClockTime(s), End: schema.ClockTime(e)})
			}
		}
	}
	// when any reading fits in 12h, drop the implausible longer ones
	short := slices.DeleteFunc(slices.Clone(cands), func(r schema.ClockRange) bool {
		return r.End-r.Start > 12*60
	})
	if len(short) > 0 {
		cands = short
	}
	slices.SortFunc(cands, func(a, b schema.ClockRange) int {
		return int((a.End - a.Start) - (b.End - b.Start))
	})
	return cands, true
}

// claimClockRanges finds the clock ranges in each unclaimed segment of the
// sentence and claims them.
func (s *sentence) claimClockRanges() []clockMention {
	var out []clockMention
	for _, seg := range s.segments() {
		for _, cm := range findClockRanges(s.src[seg.start:seg.end]) {
			cm.Span.start += seg.start
			cm.Span.end += seg.start
			s.claim(cm.Span)
			out = append(out, cm)
		}
	}
	return out
}

var (
	endAtRe       = regexp.MustCompile(`(?i)\b(?:will end|ends|ending)\s+(?:at|by)\s+(` + clockTokenPat + `)`)
	closedUntilRe = regexp.MustCompile(`(?i)\b(closed|will be closed)\s+until\s+(` + clockTokenPat + `)`)
	closedAtRe    = regexp.MustCompile(`(?i)\b(closed|closes|closing|will close)\s+at\s+(` + clockTokenPat + `)`)
	// a late opening is a closure until then
	openAtRe = regexp.MustCompile(`(?i)\b(will open|opens|opening)\s+at\s+(` + clockTokenPat + `)`)
)

// singleEndedPats are the single-ended mention patterns in the order they
// are tried. keepKeyword leaves the keyword out of the span (it drives the
// effect); "will end at X" is spanned wholesale and flagged EndEarly.
var singleEndedPats = []struct {
	re                               *regexp.Regexp
	openStart, endEarly, keepKeyword bool
}{
	{closedUntilRe, true, false, true},
	{closedAtRe, false, false, true},
	{openAtRe, true, false, true},
	{endAtRe, false, true, false},
}

// claimSingleEnded finds the single-ended time mentions in the unclaimed
// segments of the sentence ("The pool is closed until noon", "closed at
// 7:30 pm", "Public swim will end at 6 pm") and claims them. Each pattern
// is tried in every segment before the next pattern, the first match is
// claimed, and the search starts over on what is left, so a mention is
// found once and no pattern reads across a span. A mention whose time has
// no plausible reading is claimed and yields nothing.
func (s *sentence) claimSingleEnded() []clockMention {
	var out []clockMention
	for {
		cm, sp, ok := s.firstSingleEnded()
		if !ok || sp.start >= sp.end {
			break
		}
		s.claim(sp)
		if len(cm.Cands) > 0 {
			out = append(out, cm)
		}
	}
	return out
}

// firstSingleEnded returns the first single-ended mention in the unclaimed
// segments, pattern by pattern, with the span to claim for it.
func (s *sentence) firstSingleEnded() (clockMention, span, bool) {
	for _, p := range singleEndedPats {
		for _, seg := range s.segments() {
			m := p.re.FindStringSubmatchIndex(s.src[seg.start:seg.end])
			if m == nil {
				continue
			}
			for i := range m {
				if m[i] >= 0 {
					m[i] += seg.start
				}
			}
			sp := span{m[0], m[1], spanSingle}
			if p.keepKeyword {
				sp.start = m[3]
			}
			text := strings.Join(strings.Fields(s.src[m[0]:m[1]]), " ")
			cm, _ := singleEndedMention(text, s.src[m[len(m)-2]:m[len(m)-1]], p.openStart, p.endEarly)
			cm.Span = sp
			return cm, sp, true
		}
	}
	return clockMention{}, span{}, false
}

// singleEndedMention builds the mention for a single-ended time: the part
// of the day from clockStr to its end (open-end) or from the start of the
// day to it (open-start), with both meridiems as candidates when it has
// none. ok is false when the clock has no plausible reading (midnight, an
// hour past 12), and the mention then has no candidates.
func singleEndedMention(text, clockStr string, openStart, endEarly bool) (clockMention, bool) {
	cm := clockMention{Text: text, OpenStart: openStart, OpenEnd: !openStart, EndEarly: endEarly}
	v, explicit, ok := parseClockSide(clockStr)
	if !ok || v <= 0 || v >= 24*60 {
		return cm, false
	}
	vs := []int{v}
	if !explicit && v+12*60 < 24*60 {
		vs = append(vs, v+12*60)
	}
	for _, x := range vs {
		if openStart {
			cm.Cands = append(cm.Cands, schema.ClockRange{Start: 0, End: schema.ClockTime(x)})
		} else {
			cm.Cands = append(cm.Cands, schema.ClockRange{Start: schema.ClockTime(x), End: 24 * 60})
		}
	}
	cm.Inferred = !explicit
	return cm, true
}
