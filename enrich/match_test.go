package enrich

import (
	"strings"
	"testing"
)

// TestStemSkatings covers the Tom Brown Arena spelling: one item of a December
// list writes "skatings" where its siblings write "skating", and before the
// stem the whole notice resolved to class-unmatched.
func TestStemSkatings(t *testing.T) {
	for _, s := range []string{"All drop-in skating", "All drop-in skatings", "all skates"} {
		ts := tokenSet(s)
		if len(ts) != 1 || !ts["skate"] {
			t.Errorf("tokenSet(%q) = %v, want {skate}", s, ts)
		}
	}
}

// TestIceClassVocab pins the hard-coded taxonomy. It fires on no version of the
// dataset so far, so this is the only coverage it has; claude-qc's
// city-ice-class-vocabulary is what reports drift in the city's own tables.
func TestIceClassVocab(t *testing.T) {
	covers := func(seg string, act string) bool {
		st, at := tokenSet(seg), tokenSet(act)
		for _, v := range iceClassVocab {
			if v.Selects(st) && v.Covers(at) {
				return true
			}
		}
		return false
	}
	for _, tt := range []struct {
		seg, act string
		want     bool
	}{
		// every activity label an "ice sports" group has ever published
		{"ice sports", "Hockey 35+", true},
		{"ice sports", "Pick-up hockey 50+", true},
		{"ice sports", "Child hockey (6 to 12 years)", true},
		{"ice sports", "Ringette (10 to 14 years)", true},
		{"ice sports", "Stick and puck - youth and adult (13+)", true},
		{"ice sports", "Stick & Puck Preschool/Child (3 to 12 years)", true},
		{"ice sports", "Figure skating", true},
		{"ice sports", "Speed skating (6+)", true},
		// the city files these under skating, so an "all ice sports" notice
		// must not reach them
		{"ice sports", "Public skating", false},
		{"ice sports", "Family skate", false},
		{"ice sports", "Adult 18+ skating", false},
		{"ice sports", "Skating 50+", false},
		// "all skating" takes every skate variant, and nothing else
		{"skating", "Public skating", true},
		{"skating", "Family skate", true},
		{"skating", "Adult skating (ages 18+)", true},
		{"skating", "50+ skate", true},
		{"skating", "Figure skating", true},
		{"skating", "Pick-up hockey 18+", false},
		{"skating", "Ringette (10 to 14 years)", false},
		// a segment naming neither class selects nothing
		{"swimming", "Lane swim", false},
		{"gymnasium sports", "Basketball", false},
	} {
		if got := covers(tt.seg, tt.act); got != tt.want {
			t.Errorf("class %q covering %q = %v, want %v", tt.seg, tt.act, got, tt.want)
		}
	}
}

// testMatcher builds a groupMatcher from plain labels, the way
// newGroupMatcher does from the dataset, so match/split behaviour can be
// exercised without an ottrecidx index.
func testMatcher(labels ...string) *groupMatcher {
	m := &groupMatcher{label: "Drop-in schedule - test"}
	for _, l := range labels {
		e := &actEntry{name: foldText(l), labels: []string{l},
			folds: map[string]bool{}, toks: map[string]bool{}}
		e.folds[foldText(l)] = true
		for _, t := range tokens(l) {
			e.toks[t] = true
		}
		m.acts = append(m.acts, e)
	}
	return m
}

func TestSubjectParts(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want []string
	}{
		{"Figure skating and hockey 35+", []string{"Figure skating", "hockey 35+"}},
		{"Family skating, public skating and figure skating",
			[]string{"Family skating", "public skating", "figure skating"}},
		{"Weight and cardio room", []string{"Weight", "cardio room"}},
		{"Public skating", []string{"Public skating"}},
		{"", nil},
	} {
		got := subjectParts(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("subjectParts(%q) = %v, want %v", tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("subjectParts(%q) = %v, want %v", tt.in, got, tt.want)
				break
			}
		}
	}
}

// TestMatchActivityParts is the split's real contract: it must fire on a phrase
// naming several activities and stay out of the way everywhere else, above all
// on a single activity whose own label contains "and".
func TestMatchActivityParts(t *testing.T) {
	for _, tt := range []struct {
		name      string
		labels    []string
		phrase    string
		wantSplit bool
		wantActs  []string
	}{
		{
			// the case this exists for: the phrase's tokens are a superset of
			// "Hockey 35+" and so match it alone, while the age qualifier
			// keeps "Figure Skating (6+)" out
			name:      "bernard grandmaitre",
			labels:    []string{"Figure Skating (6+)", "Hockey 35+", "Hockey 50+", "Speed skating (6+)"},
			phrase:    "Figure skating and hockey 35+",
			wantSplit: true,
			wantActs:  []string{"Figure Skating (6+)", "Hockey 35+"},
		},
		{
			name:      "three activities separated by comma and and",
			labels:    []string{"Family skating", "Public skating", "Figure skating"},
			phrase:    "Family skating, public skating and figure skating",
			wantSplit: true,
			wantActs:  []string{"Family skating", "Figure skating", "Public skating"},
		},
		{
			// one activity whose label contains "and", and the phrase names it
			// exactly, so it is not a list however many neighbours it has
			name:   "weight and cardio room",
			labels: []string{"Weight and cardio room"},
			phrase: "Weight and cardio room",
		},
		{
			// the over-reach this nearly shipped with: "Cardio and strength"
			// is one activity and the facility also runs a separate Strength,
			// so splitting cancelled a row the notice never named
			name:   "cardio and strength beside a separate strength",
			labels: []string{"Cardio and strength", "Strength", "Yoga"},
			phrase: "Cardio and strength",
		},
		{
			name:   "step and strength beside a separate strength",
			labels: []string{"Step and strength", "Strength 50+", "Cardio and strength"},
			phrase: "Step and strength",
		},
		{
			// same shape, matched by token set rather than spelling
			name:   "cardio and strength, different case and punctuation",
			labels: []string{"Cardio and Strength", "Strength"},
			phrase: "cardio and strength",
		},
		{
			// parts are ambiguous across two stick-and-puck rows
			name:   "stick and puck with two rows",
			labels: []string{"Stick and Puck - Preschool and Child (3 to 12 years)", "Stick and Puck - Youth and Adult (13+)"},
			phrase: "Stick and Puck - Preschool and Child (3 to 12 years)",
		},
		{
			// only one stick-and-puck row: the parts all resolve to it, so the
			// union is no bigger than the whole-phrase match
			name:   "stick and puck with one row",
			labels: []string{"Stick and Puck - Preschool and Child (3 to 12 years)"},
			phrase: "Stick and Puck - Preschool and Child (3 to 12 years)",
		},
		{
			// a part naming nothing in the schedule refuses the whole split,
			// rather than cancelling the half that did match
			name:   "one part unmatched",
			labels: []string{"Public skating"},
			phrase: "Public skating and pickleball",
		},
		{
			name:   "single activity, no split possible",
			labels: []string{"Public skating", "Family skating"},
			phrase: "Public skating",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fc := &facCtx{out: &builder{Stats: map[string]int{}}}
			b := &blockCtx{facCtx: fc, grp: testMatcher(tt.labels...)}
			fc.matchers = []*groupMatcher{b.grp}
			wq, whole, _, _ := b.matchActivityWhole(tt.phrase)
			_, acts, _, _, ok := b.matchActivityParts(tt.phrase, wq, whole)
			if ok != tt.wantSplit {
				t.Fatalf("split = %v, want %v (whole matched %v)", ok, tt.wantSplit, actNames(whole))
			}
			if !ok {
				return
			}
			got := actNames(acts)
			if len(got) != len(tt.wantActs) {
				t.Fatalf("split acts = %v, want %v", got, tt.wantActs)
			}
			for i := range got {
				if got[i] != tt.wantActs[i] {
					t.Fatalf("split acts = %v, want %v", got, tt.wantActs)
				}
			}
		})
	}
}

// TestDanglingPrep covers the preposition findClockRanges strands when it
// lifts a range out of the middle of a sentence. "beginning" and "starting"
// are deliberately not stripped: they introduce dates too.
func TestDanglingPrep(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"From all drop-in programs", "all drop-in programs"},
		{"Between all drop-in programs", "all drop-in programs"},
		{"beginning July to 5 pm", "beginning July to 5 pm"},
		{"starting July 3", "starting July 3"},
		{"all drop-in programs", "all drop-in programs"},
		{"Family skating", "Family skating"},
	} {
		if got := danglingPrepRe.ReplaceAllString(tt.in, ""); got != tt.want {
			t.Errorf("strip(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestKeywordReason covers an effect keyword followed by the reason the city
// sometimes gives for it, which the end-anchored patterns otherwise miss.
func TestKeywordReason(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want string // "" for no match
	}{
		{"All group fitness drop-ins are cancelled due to annual maintenance", "cancelled"},
		{"All changerooms closed for maintenance", "closed"},
		{"Lane swim are cancelled because of a meet", "cancelled"},
		{"All drop-in skating and ice sports cancelled", "cancelled"},
		{"Public skating", ""},
		{"The pool is closed for the season", "closed"},
	} {
		m := trailingKwRe.FindStringSubmatch(tt.in)
		got := ""
		if m != nil {
			got = strings.ToLower(m[1])
		}
		if got != tt.want {
			t.Errorf("trailingKwRe(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
