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
	return matchResult{Quality: matchNone}
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
