package enrich

import (
	"slices"
	"strings"

	"github.com/ottrec/website/pkg/ottrecidx"
)

const (
	matchExact       = "exact"
	matchNormalized  = "normalized"
	matchFuzzy       = "fuzzy"
	matchMultiple    = "multiple"
	matchScopePhrase = "scope-phrase"
	matchNone        = "none"
)

// actEntry is one distinct activity (by normalized name) within a group.
type actEntry struct {
	name  string          // normalized display name
	folds map[string]bool // folded label and name spellings
	toks  map[string]bool // union of label/name tokens
	refs  []ottrecidx.ActivityRef
}

// groupMatcher matches phrases against one schedule group's activities.
type groupMatcher struct {
	grp       ottrecidx.ScheduleGroupRef
	label     string
	titleToks map[string]bool
	acts      []*actEntry
}

func newGroupMatcher(grp ottrecidx.ScheduleGroupRef) *groupMatcher {
	m := &groupMatcher{
		grp:       grp,
		label:     grp.GetLabel(),
		titleToks: tokenSet(grp.GetTitle() + " " + grp.GetLabel()),
	}
	byName := map[string]*actEntry{}
	for act := range grp.Activities() {
		name := act.GetName()
		if name == "" {
			name = foldText(act.GetLabel())
		}
		e := byName[name]
		if e == nil {
			e = &actEntry{name: name, folds: map[string]bool{}, toks: map[string]bool{}}
			byName[name] = e
			m.acts = append(m.acts, e)
		}
		e.refs = append(e.refs, act)
		for _, s := range []string{act.GetLabel(), name} {
			if f := foldText(s); f != "" {
				e.folds[f] = true
			}
			for _, t := range tokens(s) {
				e.toks[t] = true
			}
		}
	}
	return m
}

// matchResult is the outcome of matching a phrase against activities.
type matchResult struct {
	Quality string
	Acts    []*actEntry
	Typo    bool // matched only via edit-distance-1 token pairing
}

// match matches a subject phrase against the group's activities: exact folded
// spelling, then equal token sets, then token subset either way. Multiple
// candidates are returned as ambiguous, never picked from.
func (m *groupMatcher) match(phrase string) matchResult {
	f := foldText(phrase)
	if f == "" {
		return matchResult{Quality: matchNone}
	}
	for _, e := range m.acts {
		if e.folds[f] {
			return matchResult{Quality: matchExact, Acts: []*actEntry{e}}
		}
	}
	pt := tokenSet(phrase)
	if len(pt) == 0 || joinedLen(pt) < 4 {
		return matchResult{Quality: matchNone}
	}
	var eq, sub []*actEntry
	for _, e := range m.acts {
		switch {
		case setsEqual(pt, e.toks):
			eq = append(eq, e)
		case subset(pt, e.toks) || subset(e.toks, pt):
			sub = append(sub, e)
		}
	}
	switch {
	case len(eq) == 1:
		return matchResult{Quality: matchNormalized, Acts: eq}
	case len(eq) > 1:
		return matchResult{Quality: matchMultiple, Acts: eq}
	case len(sub) == 1:
		return matchResult{Quality: matchFuzzy, Acts: sub}
	case len(sub) > 1:
		return matchResult{Quality: matchMultiple, Acts: sub}
	}
	// typo tolerance ("Baddminton"): token pairing within edit distance 1,
	// only when it identifies exactly one activity
	var typo []*actEntry
	for _, e := range m.acts {
		okPA, t1 := typoCovers(pt, e.toks)
		okAP, t2 := typoCovers(e.toks, pt)
		if (okPA && t1) || (okAP && t2) {
			typo = append(typo, e)
		}
	}
	if len(typo) == 1 {
		return matchResult{Quality: matchFuzzy, Acts: typo, Typo: true}
	}
	return matchResult{Quality: matchNone}
}

// typoCovers reports whether every token in xs pairs with a token in ys,
// exactly or (for tokens of 5+ chars on both sides) within edit distance 1;
// usedTypo is true when at least one pair needed the tolerance.
func typoCovers(xs, ys map[string]bool) (ok, usedTypo bool) {
	for x := range xs {
		if ys[x] {
			continue
		}
		found := false
		if len(x) >= 5 {
			for y := range ys {
				if len(y) >= 5 && dlLE1(x, y) {
					found, usedTypo = true, true
					break
				}
			}
		}
		if !found {
			return false, false
		}
	}
	return true, usedTypo
}

// dlLE1 reports whether the Damerau-Levenshtein distance between a and b is
// at most 1 (one insertion, deletion, substitution, or transposition).
func dlLE1(a, b string) bool {
	if a == b {
		return true
	}
	ra, rb := []rune(a), []rune(b)
	if len(ra) > len(rb) {
		ra, rb = rb, ra
	}
	la, lb := len(ra), len(rb)
	if lb-la > 1 {
		return false
	}
	i := 0
	for i < la && ra[i] == rb[i] {
		i++
	}
	if la == lb {
		if i == la {
			return true
		}
		if slices.Equal(ra[i+1:], rb[i+1:]) {
			return true // substitution
		}
		return i+1 < la && ra[i] == rb[i+1] && ra[i+1] == rb[i] &&
			slices.Equal(ra[i+2:], rb[i+2:]) // transposition
	}
	return slices.Equal(ra[i:], rb[i+1:]) // insertion
}

// coversGroup reports whether one class segment ("skating", "ice sports")
// covers this whole group, i.e. the group's title tokens are a subset of the
// segment's.
func (m *groupMatcher) coversGroup(segToks map[string]bool) bool {
	return len(m.titleToks) > 0 && subset(m.titleToks, segToks)
}

// matchClass matches one class segment against the group's activities:
// activities whose tokens include all segment tokens.
func (m *groupMatcher) matchClass(segToks map[string]bool) []*actEntry {
	if len(segToks) == 0 {
		return nil
	}
	var out []*actEntry
	for _, e := range m.acts {
		if subset(segToks, e.toks) {
			out = append(out, e)
		}
	}
	return out
}

// classSegments splits an "all X and Y" class phrase into per-class token
// sets ("skating and ice sports" -> {skate}, {ice sports}).
func classSegments(phrase string) []map[string]bool {
	var segs []map[string]bool
	for _, part := range strings.FieldsFunc(phrase, func(r rune) bool { return r == ',' }) {
		for _, seg := range strings.Split(part, " and ") {
			if ts := tokenSet(seg); len(ts) > 0 {
				segs = append(segs, ts)
			}
		}
	}
	return segs
}

func actNames(acts []*actEntry) []string {
	names := make([]string, len(acts))
	for i, e := range acts {
		names[i] = e.name
	}
	slices.Sort(names)
	return slices.Compact(names)
}
