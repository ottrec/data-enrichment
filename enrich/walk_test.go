package enrich

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ottrec/scraper/schema"
	"github.com/ottrec/website/pkg/ottrecidx"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// testBlock is a blockCtx with just what flatten needs: an anchor for the
// date parser and a stats map.
func testBlock(anchor time.Time) *blockCtx {
	return &blockCtx{facCtx: &facCtx{out: &builder{Stats: map[string]int{}}, anchor: anchor}}
}

// flattenHTML flattens the first list of an HTML block, checking that every
// unit's parent precedes it, which is what bounds nearestStmt.
func flattenHTML(t *testing.T, b *blockCtx, html string) []unit {
	t.Helper()
	parts, ok := splitBlock(html)
	if !ok || len(parts) != 1 || parts[0].Kind != "list" {
		t.Fatalf("%s: not one list", html)
	}
	units := b.flatten(parts[0].Items)
	for i, u := range units {
		if u.parent >= i {
			t.Fatalf("unit %d %q: parent %d does not precede it", i, u.r.text, u.parent)
		}
	}
	return units
}

// shape names a reading the way b.md's census does.
func (r reading) shape() string {
	switch {
	case r.garbled != nil:
		return "garbled"
	case r.spec == nil && r.clock != "":
		return "clock"
	case r.spec == nil:
		return "stmt"
	case r.clock != "":
		return "date+clock"
	case r.stmt != "":
		return "date+stmt"
	}
	return "date"
}

// renderUnits prints one unit per line, indented by depth: kind, shape,
// text, and what the walk decided.
func renderUnits(units []unit) string {
	var sb strings.Builder
	for i, u := range units {
		depth := 0
		for p := u.parent; p >= 0; p = units[p].parent {
			depth++
		}
		kind := [...]string{"context", "head", "item", "leaf"}[u.kind]
		fmt.Fprintf(&sb, "%s%s %s %q", strings.Repeat("  ", depth), kind, u.r.shape(), u.r.text)
		if !u.first {
			sb.WriteString(" br")
		}
		switch {
		case u.kind == uHead && u.completed:
			sb.WriteString(" completed")
		case u.kind == uHead && u.supp:
			sb.WriteString(" supplementary")
		case u.kind == uHead:
			sb.WriteString(" unparsed")
		case u.completes:
			fmt.Fprintf(&sb, " completes %d", u.stmt)
			if u.withHead {
				sb.WriteString(" with-head")
			}
		}
		if i < len(units)-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func statsOf(b *blockCtx) string {
	var out []string
	for k, v := range b.out.Stats {
		if strings.HasPrefix(k, "li/") {
			out = append(out, fmt.Sprintf("%s=%d", strings.TrimPrefix(k, "li/"), v))
		}
	}
	slices.Sort(out)
	return strings.Join(out, " ")
}

// TestFlatten pins what the walk decides for each list shape the census in
// hacking.md names, plus the layouts it invented, without running the
// sentence parser. The example lines are the corpus's.
func TestFlatten(t *testing.T) {
	anchor := anchorAt(2026, time.September, 25)
	for _, tc := range []struct {
		name, html string
		anchor     time.Time
		want       string
		stats      string
	}{
		{
			name: "date over statements",
			html: `<ul><li>Wednesday, September 30<ul><li>Pickleball, 11:45 am to 12:45 pm, cancelled</li><li>All gymnasium activities are cancelled.</li></ul></li></ul>`,
			want: `context date "Wednesday, September 30"
  leaf stmt "Pickleball, 11:45 am to 12:45 pm, cancelled"
  leaf stmt "All gymnasium activities are cancelled."`,
			stats: "date-head=1 leaf=2",
		},
		{
			name: "leaves of every shape are items with nothing above them",
			html: `<ul><li>Sunday, August 23, 5 to 6 pm</li><li>Monday, August 3, 8 am to 4 pm, facility hours</li><li>The sauna is closed.</li><li>Wednesday, September 30</li><li>11:45 am to 12:45 pm</li></ul>`,
			want: `leaf date+clock "Sunday, August 23, 5 to 6 pm"
leaf date+stmt "Monday, August 3, 8 am to 4 pm, facility hours"
leaf stmt "The sauna is closed."
leaf date "Wednesday, September 30"
leaf clock "11:45 am to 12:45 pm"`,
			stats: "leaf=5",
		},
		{
			name: "leaf whose first line is a date for the lines after it",
			html: `<ul><li>Wednesday, September 30<br>Pickleball, 11:45 am to 12:45 pm, cancelled<br>Badminton cancelled.</li></ul>`,
			want: `context date "Wednesday, September 30"
  leaf stmt "Pickleball, 11:45 am to 12:45 pm, cancelled" br
  leaf stmt "Badminton cancelled." br`,
			stats: "leaf-date-line=1 leaf=1",
		},
		{
			name: "date over bare clocks with no statement above",
			html: `<ul><li>Wednesday, September 30<ul><li>11:45 am to 12:45 pm</li><li><a href="/pools">See Outdoor Pools for more information.</a></li></ul></li></ul>`,
			want: `context date "Wednesday, September 30"
  leaf clock "11:45 am to 12:45 pm"
  leaf stmt "See Outdoor Pools for more information."`,
			stats: "date-head=1 leaf=2",
		},
		{
			name: "date and clock head with children: an item that dates them",
			html: `<ul><li>Sunday, August 23, 5 to 6 pm<ul><li><a href="/pools">Details: Outdoor pools</a></li></ul></li></ul>`,
			want: `item date+clock "Sunday, August 23, 5 to 6 pm"
  leaf stmt "Details: Outdoor pools"`,
			stats: "dated-head-item=1 leaf=1",
		},
		{
			name: "date and statement head with children: its statement takes clock children",
			html: `<ul><li>Friday, October 2, Pickleball cancelled:<ul><li>3 to 4 pm</li></ul></li></ul>`,
			want: `item date+stmt "Friday, October 2, Pickleball cancelled:"
  leaf clock "3 to 4 pm" completes 0`,
			stats: "dated-head-item=1 head-times=1",
		},
		{
			name: "date head carrying more than the date over statements",
			html: `<ul><li>Monday, July 27 to Friday, July 31, between 9 am and 4 pm<ul><li>All drop-in programs are cancelled.</li></ul></li></ul>`,
			want: `item date+stmt "Monday, July 27 to Friday, July 31, between 9 am and 4 pm"
  leaf stmt "All drop-in programs are cancelled."`,
			stats: "dated-head-item=1 leaf=1",
		},
		{
			name:   "garbled date head",
			html:   `<ul><li>Monday, July 6 to 10 Friday, July 10<ul><li>Lane swim cancelled.</li></ul></li></ul>`,
			anchor: anchorAt(2025, time.July, 1),
			want: `context garbled "Monday, July 6 to 10 Friday, July 10"
  leaf stmt "Lane swim cancelled."`,
			stats: "garbled-head=1 leaf=1",
		},
		{
			name: "statement over dates: the inverted form",
			html: `<ul><li>The facility is closed, and all programs cancelled:<ul><li>Friday, August 7</li><li>August 10 to 12</li></ul></li></ul>`,
			want: `head stmt "The facility is closed, and all programs cancelled:" completed
  leaf date "Friday, August 7" completes 0 with-head
  leaf date "August 10 to 12" completes 0 with-head`,
			stats: "head-dates=1",
		},
		{
			name: "statement over a date and clock",
			html: `<ul><li>PD Day Public Swim - training and whale pools only<ul><li>Friday, October 2, 8:30 to 10 am</li></ul></li></ul>`,
			want: `head stmt "PD Day Public Swim - training and whale pools only" completed
  leaf date+clock "Friday, October 2, 8:30 to 10 am" completes 0`,
			stats: "head-dated-times=1 head-dates=1",
		},
		{
			name: "statement over a date carrying more: not completed",
			html: `<ul><li>Civic Holiday<ul><li>Monday, August 3, 8 am to 4 pm, facility hours</li></ul></li></ul>`,
			want: `head stmt "Civic Holiday" unparsed
  leaf date+stmt "Monday, August 3, 8 am to 4 pm, facility hours"`,
			stats: "head-unparsed=1 leaf=1",
		},
		{
			name: "date over a statement over clocks",
			html: `<ul><li>Wednesday, September 30<ul><li>Pickleball cancelled:<ul><li>11:45 am to 12:45 pm</li><li>12:50 to 1:50 pm</li></ul></li></ul></li></ul>`,
			want: `context date "Wednesday, September 30"
  head stmt "Pickleball cancelled:" completed
    leaf clock "11:45 am to 12:45 pm" completes 1
    leaf clock "12:50 to 1:50 pm" completes 1`,
			stats: "date-head=1 head-times=1",
		},
		{
			name: "statement over cross-references only",
			html: `<ul><li>Dogs swim free, 4:30 to 5:30 pm<ul><li><a href="/pools">See Outdoor Pools for more information.</a></li></ul></li></ul>`,
			want: `head stmt "Dogs swim free, 4:30 to 5:30 pm" supplementary
  leaf stmt "See Outdoor Pools for more information."`,
			stats: "head-supplementary=1 leaf=1",
		},
		{
			name: "statement over a statement",
			html: `<ul><li>Wednesday, September 30<ul><li>The 25 m pool is closed between 7:30 and 10:30 am.<ul><li>Lane swim, 7:30 to 8:30 am, cancelled</li></ul></li></ul></li><li>The rink is closed from noon to 4 pm<ul><li>Public skating cancelled.</li></ul></li></ul>`,
			want: `context date "Wednesday, September 30"
  head stmt "The 25 m pool is closed between 7:30 and 10:30 am." unparsed
    leaf stmt "Lane swim, 7:30 to 8:30 am, cancelled"
head stmt "The rink is closed from noon to 4 pm" unparsed
  leaf stmt "Public skating cancelled."`,
			stats: "date-head=1 head-unparsed=2 leaf=2",
		},
		{
			name: "date over a weekday set and clock",
			html: `<ul><li>June 20 to 28<ul><li>Monday to Friday, 1:45 to 7 pm</li></ul></li></ul>`,
			want: `context date "June 20 to 28"
  leaf date+clock "Monday to Friday, 1:45 to 7 pm"`,
			stats: "date-head=1 leaf=1",
		},
		{
			name: "the mixed list: clocks complete the statement, the rest is its own",
			html: `<ul><li>Wednesday, September 30<ul><li>Pickleball drop-ins:<ul><li>8:45 to 9:45 am</li><li>9:50 to 10:50 am</li><li>10:55 to 11:55 am</li><li>Noon 1 pm</li></ul></li></ul></li></ul>`,
			want: `context date "Wednesday, September 30"
  head stmt "Pickleball drop-ins:" completed
    leaf clock "8:45 to 9:45 am" completes 1
    leaf clock "9:50 to 10:50 am" completes 1
    leaf clock "10:55 to 11:55 am" completes 1
    leaf stmt "Noon 1 pm"`,
			stats: "date-head=1 head-times=1 leaf=1",
		},
		{
			name: "invented: statement over dates over clocks",
			html: `<ul><li>Pickleball cancelled:<ul><li>Wednesday, September 30<ul><li>11:45 am to 12:45 pm</li></ul></li><li>Thursday, October 1<ul><li>3 to 4 pm</li></ul></li></ul></li></ul>`,
			want: `head stmt "Pickleball cancelled:" completed
  context date "Wednesday, September 30"
    leaf clock "11:45 am to 12:45 pm" completes 0
  context date "Thursday, October 1"
    leaf clock "3 to 4 pm" completes 0`,
			stats: "date-head=2 head-nested=1 head-times=1",
		},
		{
			name: "invented: statement over dates over items",
			html: `<ul><li>Gymnasium closures:<ul><li>Wednesday, September 30<ul><li>Pickleball, 11:45 am to 12:45 pm, cancelled</li></ul></li></ul></li></ul>`,
			want: `head stmt "Gymnasium closures:" unparsed
  context date "Wednesday, September 30"
    leaf stmt "Pickleball, 11:45 am to 12:45 pm, cancelled"`,
			stats: "date-head=1 head-unparsed=1 leaf=1",
		},
		{
			name: "invented: statement over mixed children",
			html: `<ul><li>Pickleball cancelled:<ul><li>Wednesday, September 30</li><li>Thursday, October 1, 3 to 4 pm</li><li>Badminton, 6:35 to 7:35 pm, cancelled</li></ul></li></ul>`,
			want: `head stmt "Pickleball cancelled:" completed
  leaf date "Wednesday, September 30" completes 0 with-head
  leaf date+clock "Thursday, October 1, 3 to 4 pm" completes 0
  leaf stmt "Badminton, 6:35 to 7:35 pm, cancelled"`,
			stats: "head-dated-times=1 head-dates=1 leaf=1",
		},
		{
			name: "a head's <br> lines are under it",
			html: `<ul><li>Pickleball cancelled:<br>11:45 am to 12:45 pm<ul><li>12:50 to 1:50 pm</li></ul></li></ul>`,
			want: `head stmt "Pickleball cancelled:" completed
  leaf clock "11:45 am to 12:45 pm" br completes 0
  leaf clock "12:50 to 1:50 pm" completes 0`,
			stats: "head-times=1",
		},
		{
			name:  "an <li> with no text of its own",
			html:  `<ul><li><ul><li>Public skating cancelled.</li></ul></li></ul>`,
			want:  `leaf stmt "Public skating cancelled."`,
			stats: "headless=1 leaf=1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.anchor
			if a.IsZero() {
				a = anchor
			}
			b := testBlock(a)
			got := renderUnits(flattenHTML(t, b, tc.html))
			if got != tc.want {
				t.Errorf("units:\n%s\nwant:\n%s", got, tc.want)
			}
			if s := statsOf(b); s != tc.stats {
				t.Errorf("stats %q, want %q", s, tc.stats)
			}
		})
	}
}

// TestNearestStmt pins which statement a leaf would complete: the nearest
// head or dated head above it through date contexts, whether or not the
// leaf then qualifies to complete it.
func TestNearestStmt(t *testing.T) {
	b := testBlock(anchorAt(2026, time.September, 25))
	for _, tc := range []struct {
		html string
		want []string // per leaf, in order: the statement, or "" for none
	}{
		// a statement leaf still has a nearest statement; it is an item of
		// its own because of what it carries, not where it sits
		{`<ul><li>Gymnasium closures:<ul><li>Wednesday, September 30<ul><li>Pickleball, 11:45 am to 12:45 pm, cancelled</li></ul></li></ul></li></ul>`,
			[]string{"Gymnasium closures:"}},
		// through two date contexts, one of them a leaf's date line
		{`<ul><li>Pickleball cancelled:<ul><li>October 1 to 3<ul><li>Thursday, October 1<br>11:45 am to 12:45 pm</li></ul></li></ul></li></ul>`,
			[]string{"Pickleball cancelled:"}},
		// a dated head offers its statement, not its date (the date parser's
		// rest has no trailing punctuation)
		{`<ul><li>Friday, October 2, Pickleball cancelled:<ul><li>3 to 4 pm</li></ul></li></ul>`,
			[]string{"Pickleball cancelled"}},
		// a dated head whose rest is a clock has nothing to complete
		{`<ul><li>Sunday, August 23, 5 to 6 pm<ul><li>6 to 7 pm</li></ul></li></ul>`,
			[]string{""}},
		// a date context alone is no statement
		{`<ul><li>Wednesday, September 30<ul><li>11:45 am to 12:45 pm</li></ul></li><li>3 to 4 pm</li></ul>`,
			[]string{"", ""}},
	} {
		units := flattenHTML(t, b, tc.html)
		var got []string
		for i, u := range units {
			if u.kind != uLeaf {
				continue
			}
			s := ""
			if h := nearestStmt(units, i); h >= 0 {
				s = units[h].statement()
			}
			got = append(got, s)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.html, got, tc.want)
		}
	}

	// the walk goes strictly up, so a malformed chain ends instead of looping
	cyclic := []unit{{kind: uContext, parent: 1}, {kind: uContext, parent: 0}, {kind: uLeaf, parent: 1}}
	if got := nearestStmt(cyclic, 2); got != -1 {
		t.Errorf("cyclic chain: got %d, want -1", got)
	}
}

// testFacility builds a one-facility dataset around a special hours block,
// so the walk can be run end to end without a fixture.
func testFacility(t *testing.T, specialHTML string) ottrecidx.FacilityRef {
	t.Helper()
	buf, err := proto.Marshal(schema.Data_builder{Facilities: []*schema.Facility{
		schema.Facility_builder{
			Name:             "Test Arena",
			Source:           schema.Source_builder{XDate: timestamppb.New(anchorAt(2026, time.August, 1))}.Build(),
			SpecialHoursHtml: specialHTML,
		}.Build(),
	}}.Build())
	if err != nil {
		t.Fatal(err)
	}
	idx, err := new(ottrecidx.Indexer).Load(buf)
	if err != nil {
		t.Fatal(err)
	}
	for fac := range idx.Data().Facilities() {
		return fac
	}
	t.Fatal("no facility")
	return ottrecidx.FacilityRef{}
}

// TestCompleteHead pins the notices a statement head's date children
// produce: single dates merge into one, and a range, a To-only "Until"
// child, a weekday set each get their own (F11: the ladder's inverted form
// dropped the last two, and a lone "Until" child left the head unparsed).
func TestCompleteHead(t *testing.T) {
	for _, tc := range []struct {
		name, html string
		want       []string
	}{
		{
			"single dates merge, a range is its own",
			`<ul><li>The facility is closed, and all programs cancelled:<ul><li>Friday, August 7</li><li>Saturday, August 8</li><li>August 10 to 12</li></ul></li></ul>`,
			[]string{
				`"The facility is closed, and all programs cancelled:" reads "The facility is closed, and all programs cancelled" 20260810-20260812 [August 10 to 12]`,
				`"The facility is closed, and all programs cancelled:" reads "The facility is closed, and all programs cancelled" 20260807 20260808 [Friday, August 7 Saturday, August 8]`,
			},
		},
		{
			"a To-only child completes the head beside a single date",
			`<ul><li>The arena is closed.<ul><li>Friday, August 7</li><li>Until August 21</li></ul></li></ul>`,
			[]string{
				`"The arena is closed." until 20260821 [Until August 21]`,
				`"The arena is closed." 20260807 [Friday, August 7]`,
			},
		},
		{
			"a lone To-only child completes the head",
			`<ul><li>The arena is closed.<ul><li>Until August 21</li></ul></li></ul>`,
			[]string{`"The arena is closed." until 20260821 [Until August 21]`},
		},
		{
			"a weekday set child completes the head",
			`<ul><li>The arena is closed.<ul><li>Friday, August 7</li><li>Saturdays</li></ul></li></ul>`,
			[]string{
				`"The arena is closed." Saturday [Saturdays]`,
				`"The arena is closed." 20260807 [Friday, August 7]`,
			},
		},
		{
			"a clock child times the statement under the inherited date",
			`<ul><li>Wednesday, August 5<ul><li>The arena is closed:<ul><li>11:45 am to 12:45 pm</li></ul></li></ul></li></ul>`,
			[]string{`"The arena is closed:\n11:45 am to 12:45 pm" reads "The arena is closed, 11:45 am to 12:45 pm" 20260805 [Wednesday, August 5]`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fac := testFacility(t, tc.html)
			fc := &facCtx{out: &builder{Stats: map[string]int{}}, fac: fac, anchor: fac.GetSourceDate()}
			fc.processBlock(tc.html, "special_hours", nil)
			var got []string
			for _, r := range fc.recs {
				if r.kind != "notice" {
					continue
				}
				s := fmt.Sprintf("%q", r.n.RawText)
				if r.n.Reading != "" {
					s += fmt.Sprintf(" reads %q", r.n.Reading)
				}
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
					for _, wd := range d.Weekdays {
						s += " " + wd.String()
					}
				}
				s += " [" + r.n.DateText + "]"
				if slices.Contains(r.n.Ambiguities, ambHeadUnparsed) {
					s += " head-unparsed"
				}
				got = append(got, s)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("notices:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

// TestCompletionText pins what a completion keeps and what it reads: the
// lines as posted in RawText, the composed sentence in Reading, the head's
// <li> once in RawHTML, and the colon off the head on both paths, so a
// colon head over a bare date carries its cancellation.
func TestCompletionText(t *testing.T) {
	for _, tc := range []struct {
		name, html       string
		raw, reading     string
		cancelled        bool
		htmlHasChildOnce string
	}{
		{
			"a clock completion keeps both lines",
			`<ul><li>Pickleball cancelled:<ul><li>Friday, August 7, 11:45 am to 12:45 pm</li></ul></li></ul>`,
			"Pickleball cancelled:\nFriday, August 7, 11:45 am to 12:45 pm",
			"Pickleball cancelled, 11:45 am to 12:45 pm",
			true, "11:45 am",
		},
		{
			"a date completion reads the head without its colon",
			`<ul><li>Pickleball cancelled:<ul><li>Friday, August 7</li></ul></li></ul>`,
			"Pickleball cancelled:",
			"Pickleball cancelled",
			true, "",
		},
		{
			"an item of its own has no reading",
			`<ul><li>Friday, August 7<ul><li>Pickleball, 11:45 am to 12:45 pm, cancelled</li></ul></li></ul>`,
			"Pickleball, 11:45 am to 12:45 pm, cancelled",
			"",
			true, "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fac := testFacility(t, tc.html)
			fc := &facCtx{out: &builder{Stats: map[string]int{}}, fac: fac, anchor: fac.GetSourceDate()}
			fc.processBlock(tc.html, "special_hours", nil)
			var n []notice
			for _, r := range fc.recs {
				if r.kind == "notice" {
					n = append(n, r.n)
				}
			}
			if len(n) != 1 {
				t.Fatalf("got %d notices, want 1", len(n))
			}
			if n[0].RawText != tc.raw || n[0].Reading != tc.reading {
				t.Errorf("text %q reading %q, want %q and %q", n[0].RawText, n[0].Reading, tc.raw, tc.reading)
			}
			if n[0].Effects.Cancelled != tc.cancelled {
				t.Errorf("cancelled %v, want %v", n[0].Effects.Cancelled, tc.cancelled)
			}
			if d := n[0].Dates; d == nil || len(d.Dates) != 1 || d.Dates[0]/10 != 20260807 {
				t.Errorf("dates %+v, want August 7", d)
			}
			if s := tc.htmlHasChildOnce; s != "" && strings.Count(n[0].RawHTML, s) != 1 {
				t.Errorf("html %q has %q %d times, want once", n[0].RawHTML, s, strings.Count(n[0].RawHTML, s))
			}
		})
	}
}
