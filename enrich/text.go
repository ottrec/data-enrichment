package enrich

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// normText cleans text extracted from HTML for display, keeping newlines as
// line separators for later splitting.
func normText(s string) string {
	return normalizeText(s, true, false)
}

// normalizeText performs various transformations on s:
//   - remove invisible characters
//   - collapse some kinds of consecutive whitespace (excluding newlines unless requested, but including nbsp)
//   - replace all kinds of dashes with "-"
//   - perform unicode NFKC normalization
//   - optionally lowercase the string
//   - remove leading and trailing whitespace
//
// Copied verbatim from the scraper (scraper/scraper/main.go) so both sides
// normalize text identically.
func normalizeText(s string, newlines, lower bool) string {
	// normalize the string
	s = norm.NFKC.String(s)

	// transform characters
	s = strings.Map(func(r rune) rune {

		// remove zero-width spaces
		switch r {
		case '\u200b', '\ufeff', '\u200d', '\u200c':
			return -1
		}

		// replace some whitespace for collapsing later
		switch r {
		case '\n':
			if newlines {
				return r
			}
			fallthrough
		case ' ', '\t', '\v', '\f', '\u00a0':
			return ' '
		}
		if unicode.Is(unicode.Zs, r) {
			return ' '
		}

		// replace smart punctuation
		switch r {
		case '“', '”', '‟':
			return '"'
		case '\u2018', '\u2019', '\u201b':
			return '\''
		case '\u2039':
			return '<'
		case '\u203a':
			return '>'
		}

		// normalize all kinds of dashes
		if unicode.Is(unicode.Pd, r) {
			return '-'
		}

		// remove invisible characters
		if !unicode.IsGraphic(r) {
			return -1
		}

		// lowercase (or not)
		if lower {
			return unicode.ToLower(r)
		}
		return r
	}, s)

	// collapse consecutive whitespace
	s = string(slices.CompactFunc([]rune(s), func(a, b rune) bool {
		return a == ' ' && a == b
	}))

	// remove leading/trailing whitespace
	return strings.TrimSpace(s)
}

// foldText lowercases s and folds punctuation to spaces for matching, keeping
// letters, digits, '+' (age qualifiers), and apostrophes.
func foldText(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "’", "'")
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '+' || r == '\'' {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// stopTokens are dropped when tokenizing phrases for matching: articles,
// glue, and the drop-in boilerplate that varies freely between the schedule
// and the handwritten changes.
var stopTokens = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true, "of": true,
	"in": true, "on": true, "at": true, "to": true, "for": true, "with": true,
	"drop": true, "ins": true, "all": true, "s": true,
	"session": true, "sessions": true, "program": true, "programs": true,
	"programming": true,
	"activity":    true, "activities": true, "times": true,
	"schedule": true, "schedules": true,
}

// stemMap folds trivial variants the city alternates between.
var stemMap = map[string]string{
	"skating": "skate", "skates": "skate",
	"swimming": "swim", "swims": "swim",
	"canceled": "cancelled",
	"aqua":     "aquafit", "aquafitness": "aquafit",
}

// tokens splits s into matching tokens: folded, stemmed, stopwords dropped.
func tokens(s string) []string {
	var out []string
	for _, w := range strings.Fields(foldText(s)) {
		if w == "+" && len(out) > 0 {
			// "18 +" is "18+"
			out[len(out)-1] += "+"
			continue
		}
		if st, ok := stemMap[w]; ok {
			w = st
		}
		if stopTokens[w] {
			continue
		}
		out = append(out, w)
	}
	return out
}

func tokenSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, t := range tokens(s) {
		m[t] = true
	}
	return m
}

func subset(a, b map[string]bool) bool {
	if len(a) > len(b) {
		return false
	}
	for t := range a {
		if !b[t] {
			return false
		}
	}
	return true
}

func setsEqual(a, b map[string]bool) bool {
	return len(a) == len(b) && subset(a, b)
}

// splitSentences splits multi-sentence text at ". " boundaries followed by
// an uppercase letter, so each sentence gets its own parse ("The 25 m pool
// is closed between ... am. Lane swim, 7:30 to 8:30 am, cancelled").
// Abbreviations like "a.m." don't split (the period before the letter).
func splitSentences(s string) []string {
	var out []string
	start := 0
	for i := 1; i+1 < len(s); i++ {
		if s[i] != '.' || s[i+1] != ' ' {
			continue
		}
		if p := s[i-1]; !(p >= 'a' && p <= 'z' || p == ')') {
			continue
		}
		if i >= 2 && s[i-2] == '.' {
			continue
		}
		j := i + 1
		for j < len(s) && s[j] == ' ' {
			j++
		}
		r, _ := utf8.DecodeRuneInString(s[j:])
		if !unicode.IsUpper(r) {
			continue
		}
		if part := strings.TrimSpace(s[start : i+1]); part != "" {
			out = append(out, part)
		}
		start = j
	}
	if part := strings.TrimSpace(s[start:]); part != "" {
		out = append(out, part)
	}
	return out
}

func joinedLen(m map[string]bool) int {
	n := 0
	for t := range m {
		n += len(t)
	}
	return n
}
