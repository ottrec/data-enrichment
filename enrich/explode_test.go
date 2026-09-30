package enrich

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/ottrec/scraper/schema"
	"github.com/ottrec/website/pkg/ottrecidx"
)

func sessDays(ss []sessKey) []string {
	var out []string
	for _, s := range ss {
		out = append(out, fmt.Sprintf("%d %s", s.date/10, s.label))
	}
	return out
}

// TestExplode pins the enumeration: a weekday restriction on a range, the
// per-date clip by each slot's own schedule, and the bound on long ranges.
func TestExplode(t *testing.T) {
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, ottrecidx.TZ) }
	sd := func(y int, m time.Month, day int) schema.Date { return schema.MakeDateFromGo(d(y, m, day)) }
	r := schema.ClockRange{Start: 540, End: 600}

	// "August 3 to 16, Mondays": the Saturday slot is not selected
	spec := &dateSpec{From: d(2026, 8, 3), To: d(2026, 8, 16), Weekdays: []time.Weekday{time.Monday}}
	slots := []slotInfo{{r: r, wd: time.Monday, hasWd: true, act: "mon"}, {r: r, wd: time.Saturday, hasWd: true, act: "sat"}}
	if got, want := sessDays(explode(spec, slots)), []string{"20260803 mon", "20260810 mon"}; !slices.Equal(got, want) {
		t.Errorf("range with weekdays: got %v, want %v", got, want)
	}

	// "September 14 to October 5, all indoor cycling cancelled." (Bob
	// MacQuarrie): the Monday slot's schedule ends October 4, so October 5
	// is not a session of it; another schedule running that day keeps its own
	spec = &dateSpec{From: d(2026, 9, 14), To: d(2026, 10, 5)}
	slots = []slotInfo{
		{r: r, wd: time.Monday, hasWd: true, act: "fall", er: schema.DateRange{From: sd(2026, 9, 8), To: sd(2026, 10, 4)}, erOK: true},
		{r: r, wd: time.Monday, hasWd: true, act: "late", er: schema.DateRange{From: sd(2026, 10, 5)}, erOK: true},
	}
	want := []string{"20260914 fall", "20260921 fall", "20260928 fall", "20261005 late"}
	if got := sessDays(explode(spec, slots)); !slices.Equal(got, want) {
		t.Errorf("per-date clip: got %v, want %v", got, want)
	}

	// Kanata's hot tub and sauna, October 31 to March 13: past the old 45
	// day cap, bounded by the schedule rather than the notice
	spec = &dateSpec{From: d(2025, 10, 31), To: d(2026, 3, 13)}
	slots = []slotInfo{{r: r, wd: time.Friday, hasWd: true, act: "tub", er: schema.DateRange{To: sd(2025, 11, 21)}, erOK: true}}
	want = []string{"20251031 tub", "20251107 tub", "20251114 tub", "20251121 tub"}
	if got := sessDays(explode(spec, slots)); !slices.Equal(got, want) {
		t.Errorf("long range: got %v, want %v", got, want)
	}

	// past maxEnumDays nothing is enumerated
	spec = &dateSpec{From: d(2025, 1, 1), To: d(2026, 6, 1)}
	if got := explode(spec, slots); got != nil {
		t.Errorf("range past maxEnumDays: got %d sessions", len(got))
	}
}
