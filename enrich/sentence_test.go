package enrich

import (
	"fmt"
	"slices"
	"strings"
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
	sent.claimClockRanges()
	sent.claimSingleEnded()
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
		// a clock on the end of a range goes with the date
		{"Facility is closed between Thursday, May 21 at 5 pm and Friday, May 22 at 5:30 pm.", "Facility is closed."},
		{"The pool is closed for maintenance until Monday, September 21 at 4 pm.", "The pool is closed for maintenance."},
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

// TestSegments pins the unclaimed segments and that a claim keeps the
// spans sorted; no claim can overlap, since a finder matches inside one
// segment.
func TestSegments(t *testing.T) {
	sent := &sentence{src: "abcdefghij"}
	sent.claim(span{6, 8, spanClock})
	sent.claim(span{3, 6, spanDate})
	sent.claim(span{9, 9, spanSingle}) // empty: nothing
	if want := []span{{3, 6, spanDate}, {6, 8, spanClock}}; !slices.Equal(sent.spans, want) {
		t.Errorf("spans = %v, want %v", sent.spans, want)
	}
	if want := []span{{start: 0, end: 3}, {start: 8, end: 10}}; !slices.Equal(sent.segments(), want) {
		t.Errorf("segments = %v, want %v", sent.segments(), want)
	}
	if got := sent.masked(spanDate); got != "abc   ghij" {
		t.Errorf("masked(date) = %q", got)
	}
	if got := sent.masked(); got != "abc     ij" {
		t.Errorf("masked() = %q", got)
	}
}

// TestNoMatchAcrossSpan pins that a finder cannot match across a claimed
// span: with the date claimed, "closed [on Monday, October 12] at 5 pm"
// is not "closed at 5 pm" and "from 5 pm [on Monday, October 12] to 7 pm"
// is not a range, though each pattern's \s+ would take the blank.
func TestNoMatchAcrossSpan(t *testing.T) {
	anchor := anchorAt(2026, 10, 1)
	for _, tc := range []struct {
		in    string
		spans []spanKind
		rest  string
	}{
		{"The pool is closed on Monday, October 12 at 5 pm.", []spanKind{spanDate}, "The pool is closed at 5 pm."},
		{"The pool is closed from 5 pm on Monday, October 12 to 7 pm.", []spanKind{spanDate}, "The pool is closed from 5 pm to 7 pm."},
		// the same words with nothing between them are the mentions
		{"The pool is closed at 5 pm.", []spanKind{spanSingle}, "The pool is closed"},
		{"The pool is closed from 5 pm to 7 pm.", []spanKind{spanClock}, "The pool is closed."},
	} {
		sent := claimSpans(tc.in, anchor)
		var kinds []spanKind
		for _, sp := range sent.spans {
			kinds = append(kinds, sp.kind)
		}
		if !slices.Equal(kinds, tc.spans) {
			t.Errorf("%q: spans %v, want kinds %v", tc.in, sent.spans, tc.spans)
		}
		if got := sent.remainder(); got != tc.rest {
			t.Errorf("%q: remainder %q, want %q", tc.in, got, tc.rest)
		}
	}
}

// TestEndClockNotices pins the notices a range with a clock on an end
// makes (the Canterbury sentence): the start day from its clock, open-end;
// the end day until its clock, open-start; the days between, when there
// are any, whole; the To-only form the same way. Both days are facility
// closures, so the consumer strikes May 21 after 5 pm and May 22 before
// 5:30 pm and nothing else.
func TestEndClockNotices(t *testing.T) {
	for _, tc := range []struct {
		html string
		want []string
	}{
		{
			`<p>Facility is closed between Thursday, May 21 at 5 pm and Friday, May 22 at 5:30 pm.</p>`,
			[]string{
				`closure facility 20260521 "at 5 pm" 1020-1440 open-end [between Thursday, May 21 at 5 pm and Friday, May 22 at 5:30 pm]`,
				`closure facility 20260522 "at 5:30 pm" 0-1050 open-start [between Thursday, May 21 at 5 pm and Friday, May 22 at 5:30 pm]`,
			},
		},
		{
			`<p>Facility is closed between Thursday, May 21 at 5 pm and Monday, May 25 at 5:30 pm.</p>`,
			[]string{
				`closure facility 20260521 "at 5 pm" 1020-1440 open-end [between Thursday, May 21 at 5 pm and Monday, May 25 at 5:30 pm]`,
				`closure facility 20260522-20260524 [between Thursday, May 21 at 5 pm and Monday, May 25 at 5:30 pm]`,
				`closure facility 20260525 "at 5:30 pm" 0-1050 open-start [between Thursday, May 21 at 5 pm and Monday, May 25 at 5:30 pm]`,
			},
		},
		{
			`<p>The pool is closed for maintenance until Monday, September 21 at 4 pm.</p>`,
			[]string{
				`closure facility until 20260920 [until Monday, September 21 at 4 pm]`,
				`closure facility 20260921 "at 4 pm" 0-960 open-start [until Monday, September 21 at 4 pm]`,
			},
		},
	} {
		fac := testFacility(t, tc.html)
		fc := &facCtx{out: &builder{Stats: map[string]int{}}, fac: fac, anchor: fac.GetSourceDate()}
		fc.processBlock(tc.html, "special_hours", nil)
		var got []string
		for _, r := range fc.recs {
			if r.kind != "notice" {
				continue
			}
			s := ""
			if r.n.Effects.Closure {
				s += "closure "
			}
			s += r.n.Scope.Level
			if d := r.n.Dates; d != nil {
				for _, x := range d.Dates {
					s += fmt.Sprintf(" %d", x/10)
				}
				switch {
				case !d.From.IsZero() && !d.To.IsZero():
					s += fmt.Sprintf(" %d-%d", d.From/10, d.To/10)
				case !d.To.IsZero():
					s += fmt.Sprintf(" until %d", d.To/10)
				case !d.From.IsZero():
					s += fmt.Sprintf(" from %d", d.From/10)
				}
			}
			if tm := r.n.Time; tm != nil {
				s += fmt.Sprintf(" %q %d-%d", tm.Text, tm.StartMin, tm.EndMin)
				if tm.OpenStart {
					s += " open-start"
				}
				if tm.OpenEnd {
					s += " open-end"
				}
			}
			s += " [" + r.n.DateText + "]"
			got = append(got, s)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s:\n  %s\nwant:\n  %s", tc.html, strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
		}
	}
}
