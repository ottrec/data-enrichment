// Package golden renders enrichment output as the text the golden corpus
// tests compare and the full-corpus diff reads. One rendering, used by
// enrich's TestGolden, cmd/enrich -format golden and the corpus properties,
// so that a reviewed golden diff and a full-corpus diff show the same thing.
package golden

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	epb "github.com/ottrec/data-enrichment/schema"
	"github.com/ottrec/scraper/schema"
)

// Render renders an output's objects in output order, grouped by source
// block, each followed by where the tree places it. Ids, sequence numbers,
// block hashes, offsets and raw HTML are left out so that a renumbering alone
// is not a change; the reference tree is in, as placement lines.
func Render(out *epb.Output) string {
	placed := placements(out)
	byID := map[string]*epb.Object{}
	for _, o := range out.GetObjects() {
		byID[o.GetId()] = o
	}

	var b strings.Builder
	var block string
	for _, o := range out.GetObjects() {
		if o.GetBlockHash() != block {
			block = o.GetBlockHash()
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			fmt.Fprintf(&b, "== %s %s", o.GetFacility(), sourceName(o.GetSource()))
			if g := o.GetSourceGroup(); g != "" {
				fmt.Fprintf(&b, " %q", g)
			}
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "- %s", o.GetKind())
		if r := o.GetReason(); r != "" {
			fmt.Fprintf(&b, " %s", r)
		}
		fmt.Fprintf(&b, " %q\n", o.GetRawText())
		field := func(k, format string, args ...any) {
			fmt.Fprintf(&b, "    %s: %s\n", k, fmt.Sprintf(format, args...))
		}
		if s := o.GetReading(); s != "" {
			field("reading", "%q", s)
		}
		if s := o.GetSection(); s != "" {
			field("section", "%q", s)
		}
		if s := o.GetDateText(); s != "" {
			field("date text", "%q", s)
		}
		if o.HasDates() {
			field("dates", "%s", fmtDates(o.GetDates()))
		}
		if o.HasTime() {
			field("time", "%s", fmtTime(o.GetTime()))
		}
		if len(o.GetEffects()) > 0 {
			var es []string
			for _, e := range o.GetEffects() {
				es = append(es, fmtEffect(e))
			}
			field("effects", "%s", strings.Join(es, ", "))
		}
		if q := o.GetMatchQuality(); q != epb.Object_MATCH_QUALITY_UNSPECIFIED {
			field("match", "%s", q)
		}
		if s := o.GetPhrase(); s != "" {
			field("phrase", "%q", s)
		}
		if s := o.GetAmenity(); s != "" {
			field("amenity", "%q", s)
		}
		if len(o.GetCandidates()) > 0 {
			field("candidates", "%s", quoteAll(o.GetCandidates()))
		}
		if len(o.GetAmbiguities()) > 0 {
			field("ambiguities", "%s", strings.Join(o.GetAmbiguities(), " "))
		}
		if len(o.GetSources()) > 0 {
			var ss []string
			for _, s := range o.GetSources() {
				ss = append(ss, sourceName(s))
			}
			field("sources", "%s", strings.Join(ss, " "))
		}
		for _, id := range o.GetDuplicateOf() {
			if d := byID[id]; d != nil {
				field("duplicate of", "%s %q %q", sourceName(d.GetSource()), d.GetSourceGroup(), d.GetRawText())
			} else {
				field("duplicate of", "missing object")
			}
		}
		if s := o.GetProducedBy(); s != "" && s != "parser" {
			field("produced by", "%s", s)
		}
		for _, p := range placed[o.GetId()] {
			field("at", "%s", p)
		}
	}
	return b.String()
}

// placements inverts the facility > group > activity > session tree into the
// places each object id is referenced from. Sessions are listed per activity
// on one line.
func placements(out *epb.Output) map[string][]string {
	at := map[string][]string{}
	for _, f := range out.GetFacilities() {
		for _, id := range f.GetObjects() {
			at[id] = append(at[id], "facility")
		}
		for _, g := range f.GetGroups() {
			for _, id := range g.GetObjects() {
				at[id] = append(at[id], fmt.Sprintf("group %q", g.GetLabel()))
			}
			for _, a := range g.GetActivities() {
				where := fmt.Sprintf("group %q activity %q", g.GetLabel(), a.GetLabel())
				if a.GetNovel() {
					where += " novel"
				}
				for _, id := range a.GetObjects() {
					at[id] = append(at[id], where)
				}
				sessions := map[string][]string{}
				var order []string
				ses := slices.SortedFunc(slices.Values(a.GetSessions()), func(x, y *epb.Session) int {
					return cmp.Or(cmp.Compare(x.GetDate(), y.GetDate()), cmp.Compare(x.GetStart(), y.GetStart()), cmp.Compare(x.GetEnd(), y.GetEnd()))
				})
				for _, s := range ses {
					st := Date(s.GetDate()) + " " + fmtClock(s.GetStart()) + "-" + fmtClock(s.GetEnd())
					for _, id := range s.GetObjects() {
						if sessions[id] == nil {
							order = append(order, id)
						}
						sessions[id] = append(sessions[id], st)
					}
					for _, id := range s.GetAdded() {
						if sessions[id] == nil {
							order = append(order, id)
						}
						sessions[id] = append(sessions[id], st+" added")
					}
				}
				for _, id := range order {
					at[id] = append(at[id], where+" sessions "+strings.Join(sessions[id], ", "))
				}
			}
		}
	}
	return at
}

func sourceName(s epb.Object_Source) string {
	return strings.ToLower(s.String())
}

func fmtDates(d *epb.DateSpan) string {
	var parts []string
	if len(d.GetDates()) > 0 {
		var ds []string
		for _, x := range d.GetDates() {
			ds = append(ds, Date(x))
		}
		parts = append(parts, strings.Join(ds, " "))
	}
	if d.HasFrom() || d.HasTo() {
		from, to := "", ""
		if d.HasFrom() {
			from = Date(d.GetFrom())
		}
		if d.HasTo() {
			to = Date(d.GetTo())
		}
		parts = append(parts, strings.TrimSpace(from+" to "+to))
	}
	if d.GetOpenEnded() {
		parts = append(parts, "open-ended")
	}
	if len(d.GetWeekdays()) > 0 {
		var ws []string
		for _, x := range d.GetWeekdays() {
			ws = append(ws, Date(x))
		}
		parts = append(parts, "weekdays "+strings.Join(ws, " "))
	}
	if len(parts) == 0 {
		return "empty"
	}
	return strings.Join(parts, "; ")
}

// Date renders a YYYYMMDDW date as 2026-10-01 Thu, or the weekday alone
// for a weekday-only pattern.
func Date(v int32) string {
	d := schema.Date(v)
	y, yok := d.Year()
	m, mok := d.Month()
	day, dok := d.Day()
	wd, wok := d.Weekday()
	var s string
	if yok && mok && dok {
		s = fmt.Sprintf("%04d-%02d-%02d", y, m, day)
	} else if yok || mok || dok {
		s = fmt.Sprintf("%d?", v)
	}
	if wok {
		s = strings.TrimSpace(s + " " + wd.String()[:3])
	}
	return s
}

func fmtClock(m int32) string {
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

func fmtTime(t *epb.TimeAssoc) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%q", t.GetText())
	if t.HasStart() || t.HasEnd() {
		start, end := "", ""
		if t.HasStart() {
			start = fmtClock(t.GetStart())
		}
		if t.HasEnd() {
			end = fmtClock(t.GetEnd())
		}
		fmt.Fprintf(&b, " %s-%s", start, end)
	}
	if t.GetOpenStart() {
		b.WriteString(" open-start")
	}
	if t.GetOpenEnd() {
		b.WriteString(" open-end")
	}
	if r := t.GetRelation(); r != epb.TimeAssoc_RELATION_UNSPECIFIED {
		fmt.Fprintf(&b, " %s", strings.ToLower(r.String()))
	}
	if len(t.GetSlots()) > 0 {
		fmt.Fprintf(&b, " [%s]", strings.Join(t.GetSlots(), "; "))
	}
	return b.String()
}

func fmtEffect(e *epb.Effect) string {
	switch {
	case e.HasRestriction():
		return fmt.Sprintf("restriction %q", e.GetRestriction().GetText())
	case e.HasSeeSchedule():
		s := fmt.Sprintf("see-schedule %q", e.GetSeeSchedule().GetName())
		if u := e.GetSeeSchedule().GetUrl(); u != "" {
			s += fmt.Sprintf(" %q", u)
		}
		return s
	}
	return EffectKind(e)
}

// EffectKind names the effect's oneof case ("cancelled", "time-change").
func EffectKind(e *epb.Effect) string {
	c := e.WhichEffect()
	if c == epb.Effect_Effect_not_set_case {
		return "unknown"
	}
	return strings.ReplaceAll(string(e.ProtoReflect().WhichOneof(e.ProtoReflect().Descriptor().Oneofs().ByName("effect")).Name()), "_", "-")
}

func quoteAll(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, " ")
}

// Diff returns the changed region of two renderings with a few lines of
// context, or "" when equal. It trims the common prefix and suffix, which is
// enough to point at the block and object that changed.
func Diff(want, got string) string {
	if want == got {
		return ""
	}
	a, b := strings.Split(want, "\n"), strings.Split(got, "\n")
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	const ctx = 3
	var d strings.Builder
	// the nearest block and object headers above the change
	for i := p - 1; i >= 0; i-- {
		if strings.HasPrefix(a[i], "== ") {
			if i < p-ctx {
				fmt.Fprintf(&d, "  %s\n", a[i])
			}
			break
		}
	}
	for i := p - 1; i >= 0 && i >= p-ctx-1; i-- {
		if strings.HasPrefix(a[i], "- ") {
			if i < p-ctx {
				fmt.Fprintf(&d, "  %s\n  ...\n", a[i])
			}
			break
		}
	}
	for i := max(0, p-ctx); i < p; i++ {
		fmt.Fprintf(&d, "  %s\n", a[i])
	}
	for _, l := range a[p : len(a)-s] {
		fmt.Fprintf(&d, "- %s\n", l)
	}
	for _, l := range b[p : len(b)-s] {
		fmt.Fprintf(&d, "+ %s\n", l)
	}
	for i := len(a) - s; i < min(len(a), len(a)-s+ctx); i++ {
		fmt.Fprintf(&d, "  %s\n", a[i])
	}
	return d.String()
}
