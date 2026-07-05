package enrich

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// normText cleans text extracted from HTML for display: drops zero-width
// characters, folds whitespace runs (including nbsp) to single spaces, and
// trims. Newlines are preserved as line separators for later splitting.
func normText(s string) string {
	var b strings.Builder
	var space, nl bool
	for _, r := range s {
		switch {
		case r == '\u200b' || r == '\u200c' || r == '\u200d' || r == '\ufeff':
		case r == '\n':
			space, nl = true, true
		case unicode.IsSpace(r):
			space = true
		default:
			if space && b.Len() > 0 {
				if nl {
					b.WriteByte('\n')
				} else {
					b.WriteByte(' ')
				}
			}
			space, nl = false, false
			b.WriteRune(r)
		}
	}
	return b.String()
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
