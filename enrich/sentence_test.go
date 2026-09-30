package enrich

import (
	"slices"
	"testing"
	"time"
)

// claimSpans reads s as the parser does: the walk takes a leading date off,
// then the finders claim what they find in processSentence's order, the
// embedded date (a weekday set is not one), the clock ranges, the
// single-ended mentions.
func claimSpans(s string, anchor time.Time) *sentence {
	if _, rest, ok := parseLeadingDate(s, anchor); ok {
		s = rest
	}
	sent := &sentence{src: s}
	if em, sp, ok := findEmbeddedDate(s, anchor); ok && len(em.Weekdays) == 0 {
		sent.claim(sp)
	}
	for _, cm := range findClockRanges(sent.masked()) {
		sent.claim(cm.Span)
	}
	_, claimed := findSingleEnded(sent.masked())
	for _, sp := range claimed {
		sent.claim(sp)
	}
	return sent
}

// TestRemainder pins what the sentence reads as without its spans: every
// comma stays where the city put it, a clock span takes its preposition, a
// date span takes its preposition, and a single-ended mention leaves its
// keyword.
func TestRemainder(t *testing.T) {
	anchor := anchorAt(2026, 3, 2)
	for _, tc := range []struct {
		in, want string
	}{
		// a comma is a boundary wherever it is
		{"Aquafit, 8:05 to 9 am, cancelled", "Aquafit,, cancelled"},
		{"Public swim, 1 to 3 pm, 25m pool only", "Public swim,, 25m pool only"},
		{"Lane Swim, daily 11:30 am to 1:30 pm, will have shared space", "Lane Swim, daily, will have shared space"},
		{"Public swim, 1:30 to 3 pm only", "Public swim, only"},
		{"Lane swim, 12:30 to 1 pm, and 8 to 9 pm.", "Lane swim,, and."},
		{"Lane swim, 8 to 9 am, 10 to 11 am, cancelled", "Lane swim,,, cancelled"},
		{"8 to 9 am", ""},
		// the preposition goes with its clock (the leading weekday set and
		// date below are the walk's)
		{"From 11 am to 2 pm, all drop-in programs are cancelled", ", all drop-in programs are cancelled"},
		{"Saturdays and Sundays from 10 am to 5 pm", ""},
		{"Sunday, May 10 to Friday, October 9 from 9 am to 4 pm.", ""},
		{"Monday, July 27 to Friday, July 31, between 9 am and 4 pm", ""},
		{"Emergency cooling centre will be open from 9 am to 8 pm", "Emergency cooling centre will be open"},
		{"The 25 m pool is closed between 7:30 and 10:30 am.", "The 25 m pool is closed."},
		// and with its date
		{"The pool is closed from Monday, March 23 to Sunday, April 12.", "The pool is closed."},
		{"The rink is closed until December 1 for ice installation.", "The rink is closed for ice installation."},
		{"Public swim, Monday, March 23, cancelled", "Public swim,, cancelled"},
		// a single-ended mention leaves its keyword
		{"The pool is closed until noon.", "The pool is closed."},
		{"The hot tub and steam room is closed at 7:30 pm.", "The hot tub and steam room is closed"},
		{"Public swim will end at 6 pm.", "Public swim"},
		// a mention across a date: the pieces around the date are claimed
		{"Facility is closed between Thursday, May 21 at 5 pm and Friday, May 22 at 5:30 pm.", "Facility is closed"},
		{"The pool is closed for maintenance until Monday, September 21 at 4 pm.", "The pool is closed for maintenance at 4 pm."},
		// nothing claimed
		{"See Winter Break schedule.", "See Winter Break schedule."},
	} {
		sent := claimSpans(tc.in, anchor)
		if got := sent.remainder(); got != tc.want {
			t.Errorf("remainder(%q) = %q, want %q (spans %v)", tc.in, got, tc.want, sent.spans)
		}
	}
}

// TestClauses pins the typed clause list: the keyword, time change, hours
// and conjunction clauses, the restriction that needs a subject before it,
// the bare "only" beside a clock that is a restriction and not a subject,
// and the subject clauses with their blanks closed up.
func TestClauses(t *testing.T) {
	anchor := anchorAt(2026, 3, 2)
	kinds := map[clauseKind]string{clauseSubject: "subject", clauseKeyword: "keyword", clauseTimeChange: "timechange", clauseHours: "hours", clauseRestriction: "restriction", clauseConjunction: "conjunction"}
	for _, tc := range []struct {
		in   string
		want []string // kind:text
	}{
		{"Aquafit, 8:05 to 9 am, cancelled", []string{"subject:Aquafit", "keyword:cancelled"}},
		{"Public swim, 1 to 3 pm, 25m pool only", []string{"subject:Public swim", "restriction:25m pool only"}},
		{"Public swim, 1:30 to 3 pm only", []string{"subject:Public swim", "restriction:only"}},
		{"Pick-up hockey 18+, noon to 12:50 pm only", []string{"subject:Pick-up hockey 18+", "restriction:only"}},
		// a bare "only" with no clock, or with nothing before it, is not a restriction
		{"Public swim, only", []string{"subject:Public swim", "subject:only"}},
		{"1:30 to 3 pm only", []string{"subject:only"}},
		{"Lane swim, 12:30 to 1 pm, and 8 to 9 pm.", []string{"subject:Lane swim", "conjunction:and."}},
		{"Lane swim, 8 to 9 am, 10 to 11 am, cancelled due to maintenance", []string{"subject:Lane swim", "keyword:cancelled due to maintenance"}},
		{"Lane Swim, daily 11:30 am to 1:30 pm, will have shared space", []string{"subject:Lane Swim", "subject:daily", "subject:will have shared space"}},
		{"Pickleball - rotations, 2:30-3:30 pm - cancelled", []string{"subject:Pickleball - rotations", "keyword:- cancelled"}},
		{"From 11 am to 2 pm, all drop-in programs are cancelled", []string{"subject:all drop-in programs are cancelled"}},
		{"Modified hours, 6 am to 8 pm", []string{"hours:Modified hours"}},
		{"Monday, August 3, 8 am to 4 pm, facility hours", []string{"hours:facility hours"}},
		{"Monday, July 27 to Friday, July 31, between 9 am and 4 pm", nil},
		{"Lane swim, schedule change", []string{"subject:Lane swim", "timechange:schedule change"}},
		{"Aquafit, moved to 25m warm pool", []string{"subject:Aquafit", "restriction:moved to 25m warm pool"}},
		{"Women's only swim, 8:15 to 9:15 pm, added", []string{"subject:Women's only swim", "keyword:added"}},
		{"Public swim, Monday, March 23, cancelled", []string{"subject:Public swim", "keyword:cancelled"}},
		{"8 to 9 am", nil},
	} {
		var got []string
		for _, c := range claimSpans(tc.in, anchor).clauses() {
			got = append(got, kinds[c.kind]+":"+c.text)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("clauses(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// a keyword clause carries its keyword
	cs := claimSpans("Lane swim, 8 to 9 am, cancelled due to maintenance", anchor).clauses()
	if len(cs) != 2 || cs[1].kind != clauseKeyword || cs[1].kw != "cancelled" {
		t.Errorf("keyword clause = %+v", cs)
	}
}

// TestBareOnlyClaimsNoActivity pins why the bare "only" is typed: read as a
// subject it matches an activity of its own ("Women's only swim"), and the
// notice would claim two activities.
func TestBareOnlyClaimsNoActivity(t *testing.T) {
	fac := testFacility(t, "")
	fc := &facCtx{out: &builder{Stats: map[string]int{}}, fac: fac, anchor: fac.GetSourceDate()}
	b := &blockCtx{facCtx: fc, grp: testMatcher("Public swim", "Women's only swim")}
	fc.matchers = []*groupMatcher{b.grp}
	if q, acts, _, _ := b.matchActivity("only"); q == matchNone || len(acts) != 1 || acts[0].labels[0] != "Women's only swim" {
		t.Fatalf("matchActivity(\"only\") = %v %v, want Women's only swim", q, actNames(acts))
	}
	b.processItem(&walkState{}, b.read("Monday, August 3, Public swim, 1:30 to 3 pm only"), "", "", [2]int{}, nil, nil)
	var notices []notice
	for _, r := range fc.recs {
		if r.kind == "notice" {
			notices = append(notices, r.n)
		}
	}
	if len(notices) != 1 {
		t.Fatalf("got %d notices, want 1", len(notices))
	}
	n := notices[0]
	if got := n.Scope.Activities; !slices.Equal(got, []string{"Public swim"}) || n.Scope.MatchQuality != matchExact {
		t.Errorf("scope = %v %v, want Public swim exact", got, n.Scope.MatchQuality)
	}
	if n.Effects.Restriction != "only" || n.Scope.Phrase != "Public swim" {
		t.Errorf("restriction %q phrase %q, want \"only\" and \"Public swim\"", n.Effects.Restriction, n.Scope.Phrase)
	}
}

func TestSentenceClaim(t *testing.T) {
	sent := &sentence{src: "abcdefghij"}
	sent.claim(span{3, 6, spanDate})
	sent.claim(span{1, 8, spanClock})  // across the date: the pieces around it
	sent.claim(span{4, 5, spanSingle}) // inside the date: nothing
	want := []span{{1, 3, spanClock}, {3, 6, spanDate}, {6, 8, spanClock}}
	if len(sent.spans) != len(want) {
		t.Fatalf("spans = %v, want %v", sent.spans, want)
	}
	for i := range want {
		if sent.spans[i] != want[i] {
			t.Errorf("spans = %v, want %v", sent.spans, want)
		}
	}
	if got := sent.masked(spanDate); got != "abc   ghij" {
		t.Errorf("masked(date) = %q", got)
	}
	if got := sent.masked(); got != "a       ij" {
		t.Errorf("masked() = %q", got)
	}
}
