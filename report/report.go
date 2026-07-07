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

	// two independently scrolling columns: source blocks left, objects
	// right, each with its own facility headers
	var bl, br strings.Builder

	fmt.Fprintf(&bl, `<div class="meta"><h1>enrichment report</h1><p>version %s &middot; generated %s</p></div>`,
		html.EscapeString(version), html.EscapeString(out.GetGenerated().AsTime().Format("2006-01-02 15:04:05 MST")))
	br.WriteString(`<label class="toggle"><input type="checkbox" id="showignored" autocomplete="off"> show ignored</label>`)
	br.WriteString(`<details class="stats"><summary>stats</summary><table>`)
	for _, k := range slices.Sorted(func(yield func(string) bool) {
		for k := range out.GetStats() {
			if !yield(k) {
				return
			}
		}
	}) {
		fmt.Fprintf(&br, "<tr><td>%d</td><td>%s</td></tr>", out.GetStats()[k], html.EscapeString(k))
	}
	br.WriteString(`</table></details>`)

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

		fmt.Fprintf(&bl, `<h2>%s</h2>`, html.EscapeString(name))
		fmt.Fprintf(&br, `<h2>%s</h2>`, html.EscapeString(name))
		for _, blk := range blocks {
			objs := byBlock[blockKey{name, blk.group, enrich.BlockHash(blk.html)}]

			label := blk.source
			if blk.group != "" {
				label += " [" + blk.group + "]"
			}
			segs, colors := segmentsFor(blk.html, objs)
			bl.WriteString(`<div class="blockhead">` + html.EscapeString(label) + `</div><pre class="src">`)
			writeSegs(&bl, blk.html, 0, len(blk.html), segs)
			bl.WriteString(`</pre>`)
			br.WriteString(`<div class="blockhead">` + html.EscapeString(label) + `</div>`)
			for _, o := range objs {
				writeCard(&br, o, colors[o.GetId()], placements)
			}
		}
	}

	var b strings.Builder
	b.WriteString("<!doctype html>\n<meta charset=\"utf-8\">\n<title>enrichment report ")
	b.WriteString(html.EscapeString(version))
	b.WriteString("</title>\n")
	b.WriteString(pageCSS)
	b.WriteString(`<div class="col">` + bl.String() + `</div><div class="col">` + br.String() + `</div>`)
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
// an identical range, e.g. a date head and the items of one <li>). ignored
// is set when every object in the range is an ignored one, so the
// hide-ignored toggle can mute it.
type seg struct {
	start, end int
	ids        []string
	ignored    bool
	tip        string // ignore reasons, for the hover tooltip
}

// segmentsFor groups the objects' extracted byte ranges into highlight
// segments (objects sharing an identical range, e.g. a date head and the
// items of one <li>, share a segment and its color) and returns them
// outermost-first for the recursive writer, plus each object's segment
// color. Ranges come from element boundaries so they either nest or are
// disjoint; anything else is skipped defensively by writeSegs.
func segmentsFor(blockHTML string, objs []*epb.Object) ([]seg, map[string]string) {
	byRange := map[[2]int][]string{}
	kinds := map[string]epb.Object_Kind{}
	for _, o := range objs {
		kinds[o.GetId()] = o.GetKind()
		if !o.HasHtmlStart() {
			continue
		}
		r := [2]int{int(o.GetHtmlStart()), int(o.GetHtmlEnd())}
		if r[0] < 0 || r[1] > len(blockHTML) || r[0] >= r[1] {
			continue
		}
		byRange[r] = append(byRange[r], o.GetId())
	}
	tipFor := map[string]string{}
	for _, o := range objs {
		var t string
		switch o.GetKind() {
		case epb.Object_IGNORED, epb.Object_UNPARSED:
			t = strings.ToLower(o.GetKind().String()) + "/" + o.GetReason()
		default:
			if t = effectsText(o.GetEffects()); t == "" {
				t = "notice (no effects)"
			}
		}
		if amb := o.GetAmbiguities(); len(amb) > 0 {
			t += " (" + strings.Join(amb, ", ") + ")"
		}
		tipFor[o.GetId()] = t
	}
	segs := make([]seg, 0, len(byRange))
	colors := map[string]string{}
	for r, ids := range byRange {
		ignored := true
		var tips []string
		for _, id := range ids {
			if kinds[id] != epb.Object_IGNORED {
				ignored = false
			}
			if t := tipFor[id]; !slices.Contains(tips, t) {
				tips = append(tips, t)
			}
		}
		segs = append(segs, seg{start: r[0], end: r[1], ids: ids, ignored: ignored, tip: strings.Join(tips, ", ")})
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
		class := "seg"
		if s.ignored {
			class = "seg ig"
		}
		tip := ""
		if s.tip != "" {
			tip = fmt.Sprintf(` data-tip="%s"`, html.EscapeString(s.tip))
		}
		fmt.Fprintf(b, `<span class="%s" data-ids="%s"%s style="background:%s">`,
			class, html.EscapeString(strings.Join(s.ids, " ")), tip, segColor(s.ids))
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
	if q := o.GetMatchQuality(); q != epb.Object_MATCH_QUALITY_UNSPECIFIED {
		fmt.Fprintf(b, ` &middot; match: %s`, html.EscapeString(strings.ToLower(q.String())))
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
	var sources []string
	for _, s := range o.GetSources() {
		sources = append(sources, strings.ToLower(s.String()))
	}
	row("sources", esc(strings.Join(sources, ", ")))
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
	if rel := t.GetRelation(); rel != epb.TimeAssoc_RELATION_UNSPECIFIED {
		parts = append(parts, "rel="+strings.ToLower(rel.String()))
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
html, body { margin: 0; height: 100%; }
body { font: 13px/1.4 system-ui, sans-serif; color: #222; background: #fff;
  display: grid; grid-template-columns: 1fr 1fr; }
.col { overflow-y: scroll; min-width: 0; padding: 4px 8px; border-left: 1px solid #ccc; }
h2 { margin: 14px 0 0; font-size: 14px; border-bottom: 1px solid #ccc; }
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
.toggle { font-size: 12px; color: #555; user-select: none; }
.seg[data-tip] { position: relative; }
.seg[data-tip]:hover:not(:has(.seg:hover))::after {
  content: attr(data-tip); position: absolute; left: 0; top: 100%;
  z-index: 2; background: #333; color: #fff; padding: 1px 5px;
  font: 11px system-ui, sans-serif; white-space: nowrap; }
body.hide-ignored .card.kind-ignored { display: none; }
body.hide-ignored .seg.ig { background: #f0efed !important; color: #999; }
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
// ignored hidden by default; browsers restore checkbox state across
// reloads, so reset it explicitly
document.body.classList.add('hide-ignored')
const showIgnored = document.getElementById('showignored')
showIgnored.checked = false
showIgnored.addEventListener('change', e =>
  document.body.classList.toggle('hide-ignored', !e.target.checked))
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
  card.addEventListener('mouseenter', () => segs.forEach((s, i) => {
    s.classList.add('hl')
    if (i === 0) s.scrollIntoView({ block: 'nearest' })
  }))
  card.addEventListener('mouseleave', () => segs.forEach(s => s.classList.remove('hl')))
})
</script>
`
