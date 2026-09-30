package enrich_test

import (
	"cmp"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ottrec/data-enrichment/enrich"
	"github.com/ottrec/data-enrichment/internal/golden"
	epb "github.com/ottrec/data-enrichment/schema"
	"github.com/ottrec/scraper/schema"
	"github.com/ottrec/website/pkg/ottrecidx"
)

const summaryPath = "testdata/corpus-summary.golden"

// farDays is how far from its anchor a resolved date may fall before it is
// listed: past the 300 day window resolveDate allows a weekday-matched year.
const farDays = 300

// TestCorpusProperties checks properties of the parser output over the
// golden corpus and writes them to testdata/corpus-summary.golden, which
// -update regenerates like the golden files: the object, effect, marker and
// match counts, and the objects that break a property, deduplicated by
// facility and text. A property with known exceptions is listed rather than
// asserted, so a new exception shows up in the diff. Every effect kind
// occurring at least once is asserted: a rule that never fires is dead.
// Every registered marker either occurs or is listed as silent, and every
// marker that occurs must be registered.
func TestCorpusProperties(t *testing.T) {
	outs := corpusOutputs(t)
	if len(outs) == 0 {
		t.Skip("no fixtures; run go run ./cmd/mkcorpus")
	}

	counts := map[string]map[string]int{}
	count := func(table, key string) {
		if counts[table] == nil {
			counts[table] = map[string]int{}
		}
		counts[table][key]++
	}
	type violation struct{ facility, text, detail string }
	violations := map[string]map[violation][]string{}
	violate := func(property, fixture string, o *epb.Object, detail string) {
		if violations[property] == nil {
			violations[property] = map[violation][]string{}
		}
		v := violation{o.GetFacility(), o.GetRawText(), detail}
		violations[property][v] = append(violations[property][v], fixture)
	}
	for _, p := range properties {
		violations[p.name] = map[violation][]string{}
	}

	var objects int
	for _, name := range slices.Sorted(maps.Keys(outs)) {
		fo := outs[name]
		fx := &fixtureCtx{withEffect: map[string]bool{}}
		for fac := range fo.Data.Facilities() {
			fx.anchor = fac.GetSourceDate()
		}
		for _, o := range fo.Out.GetObjects() {
			if len(o.GetEffects()) > 0 {
				fx.withEffect[o.GetRawText()] = true
			}
		}
		byID := map[string]*epb.Object{}
		for _, o := range fo.Out.GetObjects() {
			objects++
			byID[o.GetId()] = o
			kind := strings.ToLower(o.GetKind().String())
			count("kind", kind)
			if r := o.GetReason(); r != "" {
				count("kind", kind+"/"+r)
			}
			for _, e := range o.GetEffects() {
				count("effect", golden.EffectKind(e))
			}
			for _, a := range o.GetAmbiguities() {
				count("marker", a)
			}
			if q := o.GetMatchQuality(); q != epb.Object_MATCH_QUALITY_UNSPECIFIED {
				count("match", strings.ToLower(q.String()))
			}
			if o.HasTime() && o.GetTime().GetRelation() != epb.TimeAssoc_RELATION_UNSPECIFIED {
				count("relation", strings.ToLower(o.GetTime().GetRelation().String()))
			}
			for _, p := range properties {
				if p.object != nil {
					if detail, bad := p.object(o, fx); bad {
						violate(p.name, name, o, detail)
					}
				}
			}
		}
		for _, v := range sessionsOutsideSchedule(fo) {
			violate("session-outside-schedule", name, byID[v.id], v.detail)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# golden corpus summary\n\n%d fixtures, %d objects\n", len(outs), objects)
	for _, table := range []string{"kind", "effect", "match", "relation", "marker"} {
		fmt.Fprintf(&b, "\n## %s\n\n", table)
		for _, k := range slices.Sorted(maps.Keys(counts[table])) {
			fmt.Fprintf(&b, "%7d %s\n", counts[table][k], k)
		}
	}
	var silent []string
	for _, m := range slices.Sorted(slices.Values(enrich.Markers())) {
		if counts["marker"][m] == 0 {
			silent = append(silent, m)
		}
	}
	fmt.Fprintf(&b, "\n## silent-marker: %d\n\nA marker the parser can emit (enrich.Markers) that occurs nowhere in the golden corpus.\n", len(silent))
	if len(silent) > 0 {
		b.WriteByte('\n')
	}
	for _, m := range silent {
		fmt.Fprintf(&b, "- %s\n", m)
	}
	for _, p := range properties {
		vs := violations[p.name]
		fmt.Fprintf(&b, "\n## %s: %d\n\n%s\n", p.name, len(vs), p.doc)
		keys := slices.SortedFunc(maps.Keys(vs), func(x, y violation) int {
			return cmp.Or(cmp.Compare(x.facility, y.facility), cmp.Compare(x.text, y.text), cmp.Compare(x.detail, y.detail))
		})
		if len(keys) > 0 {
			b.WriteByte('\n')
		}
		for _, v := range keys {
			fs := vs[v]
			fmt.Fprintf(&b, "- %s: %q", v.facility, v.text)
			if v.detail != "" {
				fmt.Fprintf(&b, " (%s)", v.detail)
			}
			fmt.Fprintf(&b, "\n    %s", slices.Min(fs))
			if len(fs) > 1 {
				fmt.Fprintf(&b, " and %d more", len(fs)-1)
			}
			b.WriteByte('\n')
		}
	}
	got := b.String()

	if *update {
		if err := os.WriteFile(summaryPath, []byte(got), 0o666); err != nil {
			t.Fatal(err)
		}
	} else if want, err := os.ReadFile(summaryPath); err != nil {
		t.Errorf("%v (run go test ./enrich -run Golden -update)", err)
	} else if d := golden.Diff(string(want), got); d != "" {
		t.Errorf("%s changed (-want +got):\n%s", summaryPath, d)
	}

	// every marker that occurs must be registered, or enrichidx has no row
	// for it
	for m := range counts["marker"] {
		if !slices.Contains(enrich.Markers(), m) {
			t.Errorf("marker %s occurs in the golden corpus but is not in enrich.Markers", m)
		}
	}

	// every effect kind must fire somewhere in the corpus
	oneof := (&epb.Effect{}).ProtoReflect().Descriptor().Oneofs().ByName("effect")
	for i := range oneof.Fields().Len() {
		k := strings.ReplaceAll(string(oneof.Fields().Get(i).Name()), "_", "-")
		if counts["effect"][k] == 0 {
			t.Errorf("effect %s occurs nowhere in the golden corpus", k)
		}
	}
}

type property struct {
	name, doc string
	// object reports whether o breaks the property, with a detail to list.
	object func(o *epb.Object, fx *fixtureCtx) (string, bool)
}

type fixtureCtx struct {
	anchor time.Time // the facility's source date
	// withEffect holds the raw text of every object with an effect; the
	// sentences of one item share it
	withEffect map[string]bool
}

var triggerRe = regexp.MustCompile(`(?i)\b(cancel\w*|clos(e|ed|ing|ure)|added)\b`)

var properties = []property{
	{
		name: "undated-effect",
		doc:  "A notice that cancels, closes or adds with no date, which applies on every day it is posted.",
		object: func(o *epb.Object, _ *fixtureCtx) (string, bool) {
			if o.GetKind() != epb.Object_NOTICE || hasDates(o) {
				return "", false
			}
			var ks []string
			for _, e := range o.GetEffects() {
				switch k := golden.EffectKind(e); k {
				case "cancelled", "closure", "added":
					ks = append(ks, k)
				}
			}
			return strings.Join(ks, " "), len(ks) > 0
		},
	},
	{
		name: "no-effect",
		doc:  "A notice or unparsed item that reads as an effect (claude-qc's enrich-no-effect word list) and has none, nor does any other sentence of the item.",
		object: func(o *epb.Object, fx *fixtureCtx) (string, bool) {
			if (o.GetKind() != epb.Object_NOTICE && o.GetKind() != epb.Object_UNPARSED) || fx.withEffect[o.GetRawText()] {
				return "", false
			}
			m := effectWordRe.FindString(o.GetRawText())
			return strings.ToLower(m), m != ""
		},
	},
	{
		name: "stray-date",
		doc:  "A notice or unparsed item that names a month and day its resolved dates do not include (claude-qc's enrich-stray-date).",
		object: func(o *epb.Object, _ *fixtureCtx) (string, bool) {
			if o.GetKind() == epb.Object_IGNORED {
				return "", false
			}
			var miss []string
			for _, x := range dateMentions(o.GetRawText()) {
				if !spanCovers(o.GetDates(), x) {
					miss = append(miss, fmt.Sprintf("%s %d", x.m.String()[:3], x.d))
				}
			}
			if len(miss) > 0 && !o.HasDates() {
				miss = append(miss, "no dates")
			}
			return strings.Join(miss, ", "), len(miss) > 0
		},
	},
	{
		name: "far-date",
		doc:  fmt.Sprintf("A resolved date more than %d days from the fixture's anchor (its source date).", farDays),
		object: func(o *epb.Object, fx *fixtureCtx) (string, bool) {
			if !o.HasDates() {
				return "", false
			}
			d := o.GetDates()
			ds := slices.Clone(d.GetDates())
			if d.HasFrom() {
				ds = append(ds, d.GetFrom())
			}
			if d.HasTo() {
				ds = append(ds, d.GetTo())
			}
			var far []string
			for _, x := range ds {
				tm, ok := schema.Date(x).GoTime(ottrecidx.TZ)
				if !ok {
					continue
				}
				if days := int(tm.Sub(fx.anchor).Hours() / 24); days > farDays || days < -farDays {
					far = append(far, fmt.Sprintf("%s is %+d days", golden.Date(x), days))
				}
			}
			return strings.Join(far, ", "), len(far) > 0
		},
	},
	{
		name: "head-unparsed-trigger",
		doc:  "A list head left unparsed although it carries an effect word, so its children lost what it says.",
		object: func(o *epb.Object, _ *fixtureCtx) (string, bool) {
			if !slices.Contains(o.GetAmbiguities(), "head-unparsed") {
				return "", false
			}
			m := triggerRe.FindString(o.GetRawText())
			return strings.ToLower(m), m != ""
		},
	},
	{
		name: "unparsed",
		doc:  "Objects of kind UNPARSED: the residue.",
		object: func(o *epb.Object, _ *fixtureCtx) (string, bool) {
			return o.GetReason(), o.GetKind() == epb.Object_UNPARSED
		},
	},
	{
		name: "session-outside-schedule",
		doc:  "A published session an object refers to on a date outside every effective range of the schedules that list its activity (ranges are negative-only evidence, so this should not happen).",
	},
}

// hasDates reports whether the object is bounded in time at all: enumerated
// dates, either side of a range, weekdays or an open end.
func hasDates(o *epb.Object) bool {
	if !o.HasDates() {
		return false
	}
	d := o.GetDates()
	return len(d.GetDates()) > 0 || d.HasFrom() || d.HasTo() || d.GetOpenEnded() || len(d.GetWeekdays()) > 0
}

type sessionViolation struct{ id, detail string }

// sessionsOutsideSchedule finds session refs (not added times) dated where no
// schedule listing the activity is in effect, when every such schedule has a
// known range.
func sessionsOutsideSchedule(fo golden.Fixture) []sessionViolation {
	type ranges struct {
		known bool
		rs    []schema.DateRange
	}
	byAct := map[[2]string]*ranges{}
	for grp := range fo.Data.ScheduleGroups() {
		for sched := range grp.Schedules() {
			er, ok := sched.ComputeEffectiveDateRange()
			for act := range sched.Activities() {
				k := [2]string{grp.GetLabel(), act.GetLabel()}
				r := byAct[k]
				if r == nil {
					r = &ranges{known: true}
					byAct[k] = r
				}
				if !ok {
					r.known = false
				}
				r.rs = append(r.rs, er)
			}
		}
	}
	var out []sessionViolation
	for _, f := range fo.Out.GetFacilities() {
		for _, g := range f.GetGroups() {
			for _, a := range g.GetActivities() {
				r := byAct[[2]string{g.GetLabel(), a.GetLabel()}]
				if r == nil || !r.known {
					continue
				}
				for _, s := range a.GetSessions() {
					d := schema.Date(s.GetDate())
					if slices.ContainsFunc(r.rs, func(er schema.DateRange) bool {
						return (er.From.IsZero() || dateKey(d) >= dateKey(er.From)) && (er.To.IsZero() || dateKey(d) <= dateKey(er.To))
					}) {
						continue
					}
					for _, id := range s.GetObjects() {
						out = append(out, sessionViolation{id, fmt.Sprintf("%q %s", a.GetLabel(), golden.Date(s.GetDate()))})
					}
				}
			}
		}
	}
	return out
}

// dateKey drops the weekday digit so dates compare as YYYYMMDD.
func dateKey(d schema.Date) int { return int(d) / 10 }

// effectWordRe, dateMentions and spanCovers are claude-qc's (checks/enrich.go),
// copied so the properties run with the parser's tests.

// effectWordRe matches the words that make a notice worth parsing: each names
// an effect the enrichment has a kind for.
var effectWordRe = regexp.MustCompile(`(?i)\b(cancel(?:l?ed|lations?)?|added|clos(?:ed|es|ing|ure)|will close|opens?|open (?:at|until|from)|only|no instructor|reduced|postponed|delayed|rescheduled|moved to|will (?:end|start|begin|finish))\b`)

type monthDay struct {
	m time.Month
	d int
}

var (
	mentionMonths = map[string]time.Month{"sept": time.September}
	// a month, a day, then any further ", d" / "and d" / "to d" days
	dateMentionRe = regexp.MustCompile(`(?i)\b(jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|june?|july?|aug(?:ust)?|sept?(?:ember)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)\.?\s+(\d{1,2})(?:st|nd|rd|th)?\b((?:\s*(?:,|,?\s*and|,?\s*to|-|–)\s*\d{1,2}(?:st|nd|rd|th)?\b)*)`)
	// clocks go first, or the 8 in "October 2, 8 am" reads as a day
	mentionClockRe = regexp.MustCompile(`(?i)\b\d{1,2}(?::\d{2})?\s*(?:(?:to|-|–)\s*\d{1,2}(?::\d{2})?\s*)?[ap]\.?m\.?|\b\d{1,2}:\d{2}\b|\bnoon\b`)
	mentionYearRe  = regexp.MustCompile(`,\s*\d{4}\b`)
	mentionDayRe   = regexp.MustCompile(`\d{1,2}`)
)

func init() {
	for m := time.January; m <= time.December; m++ {
		n := strings.ToLower(m.String())
		mentionMonths[n], mentionMonths[n[:3]] = m, m
	}
}

// dateMentions returns every month and day written in s.
func dateMentions(s string) []monthDay {
	s = mentionClockRe.ReplaceAllString(s, " @ ")
	s = mentionYearRe.ReplaceAllString(s, " ")
	var out []monthDay
	for _, x := range dateMentionRe.FindAllStringSubmatch(s, -1) {
		m := mentionMonths[strings.ToLower(x[1])]
		for _, d := range append([]string{x[2]}, mentionDayRe.FindAllString(x[3], -1)...) {
			n, _ := strconv.Atoi(d)
			out = append(out, monthDay{m, n})
		}
	}
	return out
}

// spanCovers reports whether a resolved span includes x, ignoring the year.
func spanCovers(sp *epb.DateSpan, x monthDay) bool {
	if sp == nil {
		return false
	}
	key := func(v int32) (int, bool) {
		d := schema.Date(v)
		m, ok1 := d.Month()
		dd, ok2 := d.Day()
		return int(m)*100 + dd, ok1 && ok2
	}
	want := int(x.m)*100 + x.d
	for _, v := range sp.GetDates() {
		if k, ok := key(v); ok && k == want {
			return true
		}
	}
	f, okf := key(sp.GetFrom())
	t, okt := key(sp.GetTo())
	switch {
	case okf && okt && f <= t:
		return f <= want && want <= t
	case okf && okt: // across a new year
		return want >= f || want <= t
	case okf:
		return want >= f
	case okt:
		return want <= t
	}
	return false
}
