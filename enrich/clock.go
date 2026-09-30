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
// the range's words. s may be a masked sentence, and a match may run
// across a blank.
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
	clocks := findClockRanges(s)
	if len(clocks) == 0 {
		return false
	}
	sent := &sentence{src: s}
	for _, cm := range clocks {
		sent.claim(cm.Span)
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

var (
	endAtRe       = regexp.MustCompile(`(?i)\b(?:will end|ends|ending)\s+(?:at|by)\s+(` + clockTokenPat + `)`)
	closedUntilRe = regexp.MustCompile(`(?i)\b(closed|will be closed)\s+until\s+(` + clockTokenPat + `)`)
	closedAtRe    = regexp.MustCompile(`(?i)\b(closed|closes|closing|will close)\s+at\s+(` + clockTokenPat + `)`)
	// a late opening is a closure until then
	openAtRe = regexp.MustCompile(`(?i)\b(will open|opens|opening)\s+at\s+(` + clockTokenPat + `)`)
)

// findSingleEnded finds single-ended time mentions in s ("The pool is
// closed until noon", "closed at 7:30 pm", "Public swim will end at 6 pm"),
// synthesizing the affected part of the day. The span of a closure mention
// leaves the keyword out (it drives the effect); "will end at X" is spanned
// wholesale and flagged EndEarly. s is scanned again with each span blanked,
// so a mention is found once and a later pattern may match across an earlier
// span. The second result is every span found, including one whose time has
// no plausible reading and yields no mention.
func findSingleEnded(s string) ([]clockMention, []span) {
	var out []clockMention
	var spans []span
	buf := []byte(s)
	for {
		var m []int
		var openStart, endEarly, keepKeyword bool
		if m = closedUntilRe.FindStringSubmatchIndex(s); m != nil {
			openStart, keepKeyword = true, true
		} else if m = closedAtRe.FindStringSubmatchIndex(s); m != nil {
			keepKeyword = true
		} else if m = openAtRe.FindStringSubmatchIndex(s); m != nil {
			openStart, keepKeyword = true, true
		} else if m = endAtRe.FindStringSubmatchIndex(s); m != nil {
			endEarly = true
		} else {
			break
		}
		clockStr := s[m[len(m)-2]:m[len(m)-1]]
		v, explicit, ok := parseClockSide(clockStr)
		var cands []schema.ClockRange
		if ok && v > 0 && v < 24*60 {
			vs := []int{v}
			if !explicit && v+12*60 < 24*60 {
				vs = append(vs, v+12*60)
			}
			for _, x := range vs {
				if openStart {
					cands = append(cands, schema.ClockRange{Start: 0, End: schema.ClockTime(x)})
				} else {
					cands = append(cands, schema.ClockRange{Start: schema.ClockTime(x), End: 24 * 60})
				}
			}
		}
		sp := span{m[0], m[1], spanSingle}
		if keepKeyword {
			sp.start = m[3]
		}
		text := strings.Join(strings.Fields(s[m[0]:m[1]]), " ")
		for i := sp.start; i < sp.end; i++ {
			buf[i] = ' '
		}
		s = string(buf)
		spans = append(spans, sp)
		if len(cands) == 0 {
			continue
		}
		out = append(out, clockMention{
			Text:      text,
			Span:      sp,
			Cands:     cands,
			Inferred:  !explicit,
			OpenStart: openStart,
			OpenEnd:   !openStart,
			EndEarly:  endEarly,
		})
	}
	return out, spans
}
