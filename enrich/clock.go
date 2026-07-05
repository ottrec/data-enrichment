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

var clockRangeRe = regexp.MustCompile(`(?i)(?:\b|^)(` + clockTokenPat + `)\s*(?:to|until|through|and|-|–|—)\s*(` + clockTokenPat + `)\b`)

var clockSideRe = regexp.MustCompile(`(?i)^(?:(\d{1,2})(?::(\d{2}))?\s*(a\.?m\.?|p\.?m\.?)?|(noon)|(midnight))$`)

// clockMention is one clock range found in text, with all candidate
// interpretations when meridiems are missing.
type clockMention struct {
	Text     string
	Cands    []schema.ClockRange
	Inferred bool // a meridiem was missing and had to be inferred
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

// findClockRanges extracts all clock ranges from s, returning the mentions
// and s with the matches removed. A match must have an explicit meridiem,
// noon/midnight, or minutes on at least one side (so "December 13 and 14"
// is not a clock range).
func findClockRanges(s string) ([]clockMention, string) {
	var out []clockMention
	var kept strings.Builder
	rest := s
	for rest != "" {
		loc := clockRangeRe.FindStringSubmatchIndex(rest)
		if loc == nil {
			kept.WriteString(rest)
			break
		}
		a, b := rest[loc[2]:loc[3]], rest[loc[4]:loc[5]]
		var cands []schema.ClockRange
		var inferred bool
		if clockish(a) || clockish(b) {
			cands, inferred = clockCandidates(a, b)
		}
		if len(cands) == 0 {
			// not a clock range; keep the text and continue after it
			kept.WriteString(rest[:loc[1]])
			rest = rest[loc[1]:]
			continue
		}
		out = append(out, clockMention{Text: strings.TrimSpace(rest[loc[0]:loc[1]]), Cands: cands, Inferred: inferred})
		kept.WriteString(strings.TrimRight(rest[:loc[0]], " ,"))
		kept.WriteByte(' ')
		rest = strings.TrimLeft(rest[loc[1]:], " ,")
	}
	return out, strings.TrimSpace(kept.String())
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
