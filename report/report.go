// Package report renders a self-contained HTML debugging report for one
// enriched dataset version: source HTML blocks on the left (with each
// object's extracted byte range highlighted), the enrichment objects on the
// right, paired by hover. Intended for eyeballing parser behavior, not for
// publishing.
package report

import (
	"fmt"
	"hash/fnv"
	"html"
	"slices"
	"strings"

	"github.com/ottrec/data-enrichment/enrich"
	epb "github.com/ottrec/data-enrichment/schema"
	"github.com/ottrec/scraper/schema"
	"github.com/ottrec/website/pkg/ottrecidx"
)

// Build runs the enrichment for the version and renders the report page.
func Build(version string, data ottrecidx.DataRef) []byte {
	out := enrich.EnrichVersion(version, data)

	// objects by (facility, sourceGroup, blockHash), in seq order (output
	// order already is block order)
	type blockKey struct{ fac, group, hash string }
	byBlock := map[blockKey][]*epb.Object{}
	for _, o := range out.GetObjects() {
		k := blockKey{o.GetFacility(), o.GetSourceGroup(), o.GetBlockHash()}
		byBlock[k] = append(byBlock[k], o)
	}

	placements := placementIndex(out)

	var b strings.Builder
	b.WriteString("<!doctype html>\n<meta charset=\"utf-8\">\n<title>enrichment report ")
	b.WriteString(html.EscapeString(version))
	b.WriteString("</title>\n")
	b.WriteString(pageCSS)

	b.WriteString(`<div class="grid">`)

	// header row: version left, stats right
	fmt.Fprintf(&b, `<div class="cell left meta"><h1>enrichment report</h1><p>version %s &middot; generated %s</p></div>`,
		html.EscapeString(version), html.EscapeString(out.GetGenerated().AsTime().Format("2006-01-02 15:04:05 MST")))
	b.WriteString(`<div class="cell right"><details class="stats" open><summary>stats</summary><table>`)
	for _, k := range slices.Sorted(func(yield func(string) bool) {
		for k := range out.GetStats() {
			if !yield(k) {
				return
			}
		}
	}) {
		fmt.Fprintf(&b, "<tr><td>%d</td><td>%s</td></tr>", out.GetStats()[k], html.EscapeString(k))
	}
	b.WriteString(`</table></details></div>`)

	// one row per source block, grouped by facility in dataset order
	for fac := range data.Facilities() {
		name := fac.GetName()
		var blocks []struct {
			source, group, html string
		}
		add := func(source, group, blockHTML string) {
			if strings.TrimSpace(blockHTML) != "" {
				blocks = append(blocks, struct{ source, group, html string }{source, group, blockHTML})
			}
		}
		for grp := range fac.ScheduleGroups() {
			add("schedule_changes", grp.GetLabel(), grp.GetScheduleChangesHTML())
		}
		add("special_hours", "", fac.GetSpecialHoursHTML())
		add("notifications", "", fac.GetNotificationsHTML())
		if len(blocks) == 0 {
			continue
		}

		fmt.Fprintf(&b, `<div class="cell fac" style="grid-column: 1 / -1"><h2>%s</h2></div>`, html.EscapeString(name))
		for _, blk := range blocks {
			objs := byBlock[blockKey{name, blk.group, enrich.BlockHash(blk.html)}]

			label := blk.source
			if blk.group != "" {
				label += " [" + blk.group + "]"
			}
			segs, colors := segmentsFor(blk.html, objs)
			b.WriteString(`<div class="cell left"><div class="blockhead">` + html.EscapeString(label) + `</div><pre class="src">`)
			writeSegs(&b, blk.html, 0, len(blk.html), segs)
			b.WriteString(`</pre></div><div class="cell right">`)
			for _, o := range objs {
				writeCard(&b, o, colors[o.GetId()], placements)
			}
			b.WriteString(`</div>`)
		}
	}
	b.WriteString(`</div>`)
	b.WriteString(pageJS)
	return []byte(b.String())
}

// placementIndex maps object id to human-readable positions in the tree.
func placementIndex(out *epb.Output) map[string][]string {
	idx := map[string][]string{}
	add := func(ids []string, where string) {
		for _, id := range ids {
			idx[id] = append(idx[id], where)
		}
	}
	for _, f := range out.GetFacilities() {
		add(f.GetObjects(), "facility")
		for _, g := range f.GetGroups() {
			add(g.GetObjects(), "group "+g.GetLabel())
			for _, a := range g.GetActivities() {
				where := "activity " + a.GetLabel()
				if a.GetNovel() {
					where += " (novel)"
				}
				add(a.GetObjects(), where)
				for _, s := range a.GetSessions() {
					sess := fmt.Sprintf("%s %s-%s [%s]",
						schema.Date(s.GetDate()),
						schema.ClockTime(s.GetStart()).Format(true),
						schema.ClockTime(s.GetEnd()).Format(true),
						a.GetLabel())
					add(s.GetObjects(), "session "+sess)
					add(s.GetAdded(), "added session "+sess)
				}
			}
		}
	}
	return idx
}

// seg is one highlighted byte range of a block (multiple objects can share
// an identical range, e.g. a date head and the items of one <li>).
type seg struct {
	start, end int
	ids        []string
}

// segmentsFor groups the objects' extracted byte ranges into highlight
// segments (objects sharing an identical range, e.g. a date head and the
// items of one <li>, share a segment and its color) and returns them
// outermost-first for the recursive writer, plus each object's segment
// color. Ranges come from element boundaries so they either nest or are
// disjoint; anything else is skipped defensively by writeSegs.
func segmentsFor(blockHTML string, objs []*epb.Object) ([]seg, map[string]string) {
	byRange := map[[2]int][]string{}
	for _, o := range objs {
		if !o.HasHtmlStart() {
			continue
		}
		r := [2]int{int(o.GetHtmlStart()), int(o.GetHtmlEnd())}
		if r[0] < 0 || r[1] > len(blockHTML) || r[0] >= r[1] {
			continue
		}
		byRange[r] = append(byRange[r], o.GetId())
	}
	segs := make([]seg, 0, len(byRange))
	colors := map[string]string{}
	for r, ids := range byRange {
		segs = append(segs, seg{r[0], r[1], ids})
		for _, id := range ids {
			colors[id] = segColor(ids)
		}
	}
	// sort outermost-first so a simple recursive walk nests them
	slices.SortFunc(segs, func(a, b seg) int {
		if a.start != b.start {
			return a.start - b.start
		}
		return b.end - a.end
	})
	return segs, colors
}

// writeSegs emits blockHTML[pos:end], wrapping each top-level seg and
// recursing into the segs it contains.
func writeSegs(b *strings.Builder, src string, pos, end int, segs []seg) {
	for i := 0; i < len(segs); i++ {
		s := segs[i]
		if s.start < pos {
			continue // overlaps something already emitted; skip
		}
		// children are the following segs contained in s
		j := i + 1
		for j < len(segs) && segs[j].start < s.end {
			j++
		}
		b.WriteString(html.EscapeString(src[pos:s.start]))
		fmt.Fprintf(b, `<span class="seg" data-ids="%s" style="background:%s">`,
			html.EscapeString(strings.Join(s.ids, " ")), segColor(s.ids))
		writeSegs(b, src, s.start, s.end, segs[i+1:j])
		b.WriteString(`</span>`)
		pos = s.end
		i = j - 1
	}
	b.WriteString(html.EscapeString(src[pos:end]))
}

// segColor derives a stable pastel from the ids sharing a range.
func segColor(ids []string) string {
	h := fnv.New32a()
	for _, id := range ids {
		h.Write([]byte(id))
	}
	return fmt.Sprintf("hsl(%d, 70%%, 86%%)", h.Sum32()%360)
}

// writeCard emits one object card; color is the object's segment color in
// the source pane ("" when it has no tracked range).
func writeCard(b *strings.Builder, o *epb.Object, color string, placements map[string][]string) {
	if color == "" {
		color = "transparent"
	}
	kind := strings.ToLower(o.GetKind().String())
	fmt.Fprintf(b, `<div class="card kind-%s" id="obj-%s" data-id="%s" style="border-left-color:%s">`,
		kind, html.EscapeString(o.GetId()), html.EscapeString(o.GetId()), color)

	badge := kind
	if o.GetReason() != "" {
		badge += "/" + o.GetReason()
	}
	fmt.Fprintf(b, `<div class="cardhead"><span class="badge %s">%s</span> <code>%s</code> seq %d`,
		kind, html.EscapeString(badge), html.EscapeString(o.GetId()), o.GetSeq())
	if q := o.GetMatchQuality(); q != "" {
		fmt.Fprintf(b, ` &middot; match: %s`, html.EscapeString(q))
	}
	b.WriteString(`</div>`)

	row := func(label, val string) {
		if val != "" {
			fmt.Fprintf(b, `<div class="row"><span class="lbl">%s</span> %s</div>`, label, val)
		}
	}
	esc := html.EscapeString

	row("text", esc(o.GetRawText()))
	if dt := o.GetDateText(); dt != "" && dt != o.GetRawText() {
		row("date text", esc(dt))
	}
	row("section", esc(o.GetSection()))
	row("dates", esc(dateSpanText(o.GetDates())))
	row("time", esc(timeAssocText(o.GetTime())))
	row("effects", esc(effectsText(o.GetEffects())))
	row("phrase", esc(o.GetPhrase()))
	row("amenity", esc(o.GetAmenity()))
	row("candidates", esc(strings.Join(o.GetCandidates(), "; ")))
	row("ambiguities", esc(strings.Join(o.GetAmbiguities(), ", ")))
	row("placed", esc(strings.Join(placements[o.GetId()], "; ")))
	row("sources", esc(strings.Join(o.GetSources(), ", ")))
	if dup := o.GetDuplicateOf(); len(dup) > 0 {
		var links []string
		for _, id := range dup {
			links = append(links, fmt.Sprintf(`<a href="#obj-%s"><code>%s</code></a>`, esc(id), esc(id)))
		}
		row("duplicate of", strings.Join(links, " "))
	}
	b.WriteString(`</div>`)
}

func dateSpanText(d *epb.DateSpan) string {
	if d == nil {
		return ""
	}
	var parts []string
	for _, x := range d.GetDates() {
		parts = append(parts, schema.Date(x).String())
	}
	if d.HasFrom() || d.HasTo() {
		parts = append(parts, schema.DateRange{From: schema.Date(d.GetFrom()), To: schema.Date(d.GetTo())}.String())
	}
	for _, x := range d.GetWeekdays() {
		parts = append(parts, schema.Date(x).String())
	}
	if d.GetOpenEnded() {
		parts = append(parts, "(open-ended)")
	}
	return strings.Join(parts, ", ")
}

func timeAssocText(t *epb.TimeAssoc) string {
	if t == nil {
		return ""
	}
	var parts []string
	if t.HasStart() {
		r := fmt.Sprintf("%s - %s", schema.ClockTime(t.GetStart()).Format(true), schema.ClockTime(t.GetEnd()).Format(true))
		switch {
		case t.GetOpenStart():
			r = "until " + schema.ClockTime(t.GetEnd()).Format(true)
		case t.GetOpenEnd():
			r = "from " + schema.ClockTime(t.GetStart()).Format(true)
		}
		parts = append(parts, r)
	}
	if rel := t.GetRelation(); rel != "" {
		parts = append(parts, "rel="+rel)
	}
	if s := t.GetSlots(); len(s) > 0 {
		parts = append(parts, "slots: "+strings.Join(s, "; "))
	}
	return strings.Join(parts, " · ")
}

// effectsText renders the effect list, showing unknown kinds explicitly the
// way an old consumer would have to.
func effectsText(effects []*epb.Effect) string {
	var parts []string
	for _, e := range effects {
		switch e.WhichEffect() {
		case epb.Effect_Cancelled_case:
			parts = append(parts, "cancelled")
		case epb.Effect_Added_case:
			parts = append(parts, "added")
		case epb.Effect_TimeChange_case:
			parts = append(parts, "time-change")
		case epb.Effect_Closure_case:
			parts = append(parts, "closure")
		case epb.Effect_SeasonalHours_case:
			parts = append(parts, "seasonal-hours")
		case epb.Effect_ModifiedHours_case:
			parts = append(parts, "modified-hours")
		case epb.Effect_MovedTo_case:
			parts = append(parts, "moved-to("+e.GetMovedTo().GetTo()+")")
		case epb.Effect_ChangedTo_case:
			parts = append(parts, "changed-to("+e.GetChangedTo().GetTo()+")")
		case epb.Effect_Restriction_case:
			parts = append(parts, "restriction("+e.GetRestriction().GetText()+")")
		case epb.Effect_SeeSchedule_case:
			parts = append(parts, "see-schedule("+e.GetSeeSchedule().GetName()+")")
		default:
			parts = append(parts, "UNKNOWN")
		}
	}
	return strings.Join(parts, ", ")
}

const pageCSS = `<style>
body { margin: 0; font: 13px/1.4 system-ui, sans-serif; color: #222; background: #fff; }
.grid { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 4px 12px; padding: 8px; }
.cell { min-width: 0; }
.cell.fac h2 { margin: 14px 0 0; font-size: 14px; border-bottom: 1px solid #ccc; }
.meta h1 { margin: 0; font-size: 14px; }
.meta p { margin: 0; color: #666; }
.blockhead { font-size: 11px; color: #666; margin-top: 4px; }
pre.src { margin: 0; padding: 4px; border: 1px solid #ddd;
  white-space: pre-wrap; word-break: break-all; font: 11px/1.5 ui-monospace, monospace; }
.seg { cursor: pointer; }
.seg.hl { outline: 2px solid #c00; }
.card { border: 1px solid #ddd; border-left: 4px solid transparent; padding: 2px 6px; margin: 2px 0; }
.card.hl { outline: 2px solid #c00; }
.cardhead { color: #555; font-size: 11px; }
.badge { font-weight: bold; }
.badge.unparsed { color: #a00; }
.badge.ignored { color: #888; font-weight: normal; }
.row { margin: 0; }
.lbl { display: inline-block; min-width: 78px; color: #999; font-size: 11px; }
.stats table { border-collapse: collapse; font-size: 11px; }
.stats td { padding: 0 8px 0 0; text-align: right; }
.stats td + td { text-align: left; color: #555; }
code { font-size: 11px; color: #875; }
</style>
`

const pageJS = `<script>
// hover a highlighted source segment: scroll to and outline its objects;
// hover a card: outline its source segment. mouseover with stopPropagation
// makes the innermost (most specific) segment win.
const byId = id => document.getElementById('obj-' + id)
document.querySelectorAll('.seg').forEach(seg => {
  const ids = seg.dataset.ids.split(' ')
  seg.addEventListener('mouseover', e => {
    e.stopPropagation()
    ids.forEach((id, i) => {
      const el = byId(id)
      if (!el) return
      el.classList.add('hl')
      if (i === 0) el.scrollIntoView({ block: 'nearest' })
    })
  })
  seg.addEventListener('mouseout', e => {
    e.stopPropagation()
    ids.forEach(id => byId(id)?.classList.remove('hl'))
  })
})
document.querySelectorAll('.card').forEach(card => {
  const id = card.dataset.id
  const segs = [...document.querySelectorAll('.seg')].filter(s => s.dataset.ids.split(' ').includes(id))
  card.addEventListener('mouseenter', () => segs.forEach(s => s.classList.add('hl')))
  card.addEventListener('mouseleave', () => segs.forEach(s => s.classList.remove('hl')))
})
</script>
`
