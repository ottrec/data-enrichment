package enrich

import "testing"

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
