// Package enrich derives structured schedule-change/special-hours records
// from the freeform HTML fields of an ottrec dataset version. It is
// deliberately conservative: effect flags are only set when backed by a
// trigger word, and anything short of certain carries an ambiguity marker
// instead of a guess (see notes/approaches.md).
package enrich

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ottrec/website/pkg/ottrecidx"
)

// Ambiguity markers beyond the date/clock ones.
const (
	ambActivityUnmatched  = "activity-unmatched"
	ambActivityMultiple   = "activity-multiple-candidates"
	ambClassUnmatched     = "class-unmatched"
	ambNoSlotOverlap      = "no-slot-overlap"
	ambAddedScheduled     = "added-time-already-scheduled"
	ambTimeDisambiguated  = "activity-time-disambiguated"
	ambHeadUnparsed       = "head-unparsed"
	ambDateOnlyItem       = "date-only-item"
	ambNoSubject          = "no-subject"
	ambHoursContext       = "hours-context-unknown"
	ambFreeformItem       = "freeform-item"
	ambDateOutsideSched   = "date-outside-schedule"
	ambTimeChangeUnparsed = "time-change-unparsed"
)

const producedByParser = "parser"

// EnrichVersion builds the enrichment output for one dataset version.
func EnrichVersion(version string, data ottrecidx.DataRef) *Output {
	out := &Output{
		Version:   version,
		Generated: time.Now(),
		Stats:     map[string]int{},
	}
	for fac := range data.Facilities() {
		fc := &facCtx{out: out, fac: fac, facStart: len(out.Notices)}
		fc.anchor = fac.GetSourceDate()
		if fc.anchor.IsZero() {
			fc.anchor = data.Index().Updated()
		}
		for grp := range fac.ScheduleGroups() {
			fc.matchers = append(fc.matchers, newGroupMatcher(grp))
		}
		for _, m := range fc.matchers {
			fc.processBlock(m.grp.GetScheduleChangesHTML(), "schedule_changes", m)
		}
		fc.processBlock(fac.GetSpecialHoursHTML(), "special_hours", nil)
		fc.processBlock(fac.GetNotificationsHTML(), "notifications", nil)
		fc.dedupe()
	}
	return out
}

type facCtx struct {
	out      *Output
	fac      ottrecidx.FacilityRef
	anchor   time.Time
	matchers []*groupMatcher
	facStart int // index of this facility's first notice, for dedupe
}

type blockCtx struct {
	*facCtx
	grp       *groupMatcher // nil for facility-level sources
	source    string
	blockHash string
}

// walkState is the running context while walking a block's parts.
type walkState struct {
	section string
	head    *dateSpec
	headRaw string
	// closureContext is set by an intro line like "The facility is not
	// available on the following dates ...:" so bare date items that follow
	// inherit the closure instead of looking like hours.
	closureContext bool
}

func (fc *facCtx) processBlock(blockHTML, source string, grp *groupMatcher) {
	if strings.TrimSpace(blockHTML) == "" {
		return
	}
	fc.out.Stats["block/"+source]++
	sum := sha256.Sum256([]byte(blockHTML))
	b := &blockCtx{facCtx: fc, grp: grp, source: source, blockHash: hex.EncodeToString(sum[:8])}
	st := &walkState{}
	for _, part := range splitBlock(blockHTML) {
		switch part.Kind {
		case "heading":
			st.section = part.Text
			st.head, st.headRaw = nil, ""
			st.closureContext = false
			// a heading can itself be the date context ("Wednesday, July 1",
			// "Notice: December 5")
			ht := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(part.Text, "Notice:"), "Notice"))
			if spec, rest, ok := parseLeadingDate(ht, fc.anchor); ok && restIsTrivial(rest) {
				st.head, st.headRaw = &spec, part.Text
			}
		case "para":
			for line := range strings.SplitSeq(part.Text, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				if spec, rest, ok := parseLeadingDate(line, fc.anchor); ok && restIsTrivial(rest) {
					st.head, st.headRaw = &spec, line
					continue
				}
				b.processItem(st, line, part.HTML, part.Links, nil)
			}
		case "list":
			for _, li := range part.Items {
				b.processLi(st, li)
			}
			st.closureContext = false
		}
	}
}

func (b *blockCtx) processLi(st *walkState, li liNode) {
	lines := splitLines(li.Head)
	if len(li.Items) == 0 {
		// leaf: possibly "date<br>item<br>item"
		local := *st
		for i, line := range lines {
			if i == 0 && len(lines) > 1 {
				if spec, rest, ok := parseLeadingDate(line, b.anchor); ok && restIsTrivial(rest) {
					local.head, local.headRaw = &spec, line
					continue
				}
			}
			b.processItem(&local, line, li.HeadHTML, li.Links, nil)
		}
		return
	}

	head := ""
	if len(lines) > 0 {
		head = lines[0]
	}
	if spec, rest, ok := parseLeadingDate(head, b.anchor); ok && restIsTrivial(rest) {
		// date-headed: children are the items
		local := *st
		local.head, local.headRaw = &spec, head
		for _, line := range lines[1:] {
			b.processItem(&local, line, li.HeadHTML, li.Links, nil)
		}
		for _, sub := range li.Items {
			b.processLi(&local, sub)
		}
		return
	} else if len(spec.Ambig) > 0 {
		// garbled date head: children still get the head text, marked
		local := *st
		local.head, local.headRaw = &spec, head
		for _, sub := range li.Items {
			b.processLi(&local, sub)
		}
		return
	}

	// inverted form: a statement whose children are all dates
	var dates dateSpec
	allDates := len(li.Items) > 0
	for _, sub := range li.Items {
		if len(sub.Items) > 0 {
			allDates = false
			break
		}
		spec, rest, ok := parseLeadingDate(sub.Head, b.anchor)
		if !ok || !restIsTrivial(rest) {
			allDates = false
			break
		}
		dates.Dates = append(dates.Dates, spec.Dates...)
		dates.Ambig = append(dates.Ambig, spec.Ambig...)
		if !spec.From.IsZero() {
			// a range child: keep as range only if it's the sole child
			if len(li.Items) == 1 {
				dates.From, dates.To, dates.OpenEnded = spec.From, spec.To, spec.OpenEnded
			} else {
				allDates = false
				break
			}
		}
		dates.Raw = strings.TrimSpace(dates.Raw + " " + spec.Raw)
	}
	if allDates && !dates.empty() {
		local := *st
		local.head, local.headRaw = &dates, dates.Raw
		b.processItem(&local, head, li.HeadHTML, li.Links, nil)
		return
	}

	// unrecognized head: emit it as an item and process children normally
	if head != "" {
		b.processItem(st, head, li.HeadHTML, li.Links, []string{ambHeadUnparsed})
	}
	for _, sub := range li.Items {
		b.processLi(st, sub)
	}
}

func splitLines(s string) []string {
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

func (fc *facCtx) dedupe() {
	notices := fc.out.Notices[fc.facStart:]
	groupKeys := map[string][]string{} // dedupe key -> group labels
	for i := range notices {
		n := &notices[i]
		if n.Source == "schedule_changes" {
			for _, k := range dedupeKeys(n) {
				if !slices.Contains(groupKeys[k], n.Group) {
					groupKeys[k] = append(groupKeys[k], n.Group)
				}
			}
		}
	}
	if len(groupKeys) == 0 {
		return
	}
	for i := range notices {
		n := &notices[i]
		if n.Source != "special_hours" {
			continue
		}
		for _, k := range dedupeKeys(n) {
			if groups, ok := groupKeys[k]; ok {
				n.DuplicateOfGroups = groups
				fc.out.Stats["dedupe/special-duplicates-changes"]++
				break
			}
		}
	}
}

// dedupeKeys builds comparison keys for a notice: dates + effects + scope.
// Broad scopes (group/class/facility) share a key so the city's merged
// facility-level phrasing ("skating and ice sports") matches the per-group
// copies ("skating").
func dedupeKeys(n *Notice) []string {
	var d string
	if n.Dates != nil {
		d = strings.Join(n.Dates.Dates, ",") + "|" + n.Dates.From + "|" + n.Dates.To + "|" + fmt.Sprint(n.Dates.OpenEnded) + "|" + strings.Join(n.Dates.Weekdays, ",")
	}
	e := n.Effects
	e.SeeURL = ""
	eff := fmt.Sprintf("%+v", e)
	var scopes []string
	switch n.Scope.Level {
	case "group", "class", "facility":
		scopes = append(scopes, "broad")
	case "activity":
		var t string
		if n.Time != nil {
			t = fmt.Sprintf("%d-%d", n.Time.StartMin, n.Time.EndMin)
		}
		scopes = append(scopes, "act:"+strings.Join(n.Scope.Activities, ",")+"@"+t)
	case "amenity":
		scopes = append(scopes, "amenity:"+foldText(n.Scope.Phrase))
	default:
		scopes = append(scopes, "none:"+foldText(n.RawText))
	}
	keys := make([]string, len(scopes))
	for i, s := range scopes {
		keys[i] = d + "||" + eff + "||" + s
	}
	return keys
}
