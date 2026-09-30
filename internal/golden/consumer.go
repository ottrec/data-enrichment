package golden

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ottrec/data-enrichment/enrichidx"
	epb "github.com/ottrec/data-enrichment/schema"
	"github.com/ottrec/scraper/schema"
	"github.com/ottrec/website/pkg/ottrecidx"
)

// The consumer window, in days around each facility's anchor (its source
// date, else the dataset's update time, as the parser anchors). /today shows
// the week from the day it renders and a posting stays up for weeks after it
// first appears, so the window runs six weeks forward; the week before it
// shows dates that resolved behind the anchor.
const (
	ConsumerBefore = 7
	ConsumerAfter  = 42
)

// Consumer renders what enrichidx answers for the dataset's published
// sessions in the consumer window: per day, the facility and group Warning;
// per session, Session and the facility and group ScopeCancelled and
// ScopeCancelledStated; per group, Added. Only non-empty answers are written,
// so a facility the enrichment says nothing about renders as "".
//
// Sessions are enumerated with ottrecidx: a fixed-date time on its date, a
// weekday time on each matching date inside its schedule's effective range
// (negative-only, as the website reads it). Which answers /today strikes, marks
// or warns on is left to the reader; this records the answers, not a copy of
// the website's use of them.
func Consumer(data ottrecidx.DataRef, out *epb.Output) string {
	idx := enrichidx.Join(out)
	var b strings.Builder
	for fac := range data.Facilities() {
		anchor := fac.GetSourceDate()
		if anchor.IsZero() {
			anchor = data.Index().Updated()
		}
		anchor = anchor.In(ottrecidx.TZ)
		d0 := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, ottrecidx.TZ).AddDate(0, 0, -ConsumerBefore)
		days := make([]schema.Date, ConsumerBefore+ConsumerAfter+1)
		for i := range days {
			days[i] = schema.MakeDateFromGo(d0.AddDate(0, 0, i))
		}
		from, to := days[0], days[len(days)-1]

		ef := idx.Facility(fac.GetName())
		var fb strings.Builder
		if s := warningRuns(days, ef.Warning); s != "" {
			fmt.Fprintf(&fb, "warning: %s\n", s)
		}
		for grp := range fac.ScheduleGroups() {
			eg := ef.Group(grp.GetLabel())
			var gb strings.Builder
			if s := warningRuns(days, eg.Warning); s != "" {
				fmt.Fprintf(&gb, "  warning: %s\n", s)
			}
			var lines []sessionLine
			for _, s := range sessions(grp, days) {
				var ans []string
				m := eg.Session(s.label, s.date, s.start, s.end)
				if m.Cancelled {
					ans = append(ans, "cancelled")
				}
				if m.LikelyCancelled {
					ans = append(ans, "likely cancelled")
				}
				if m.TimeChange {
					ans = append(ans, "time-change")
				}
				if m.NewTime {
					ans = append(ans, "new time "+fmtClock(int32(m.NewStart))+"-"+fmtClock(int32(m.NewEnd)))
				}
				for _, sc := range []struct {
					name          string
					scope, stated func(schema.Date, int, int) bool
				}{
					{"facility", ef.ScopeCancelled, ef.ScopeCancelledStated},
					{"group", eg.ScopeCancelled, eg.ScopeCancelledStated},
				} {
					switch {
					case sc.stated(s.date, s.start, s.end):
						ans = append(ans, sc.name+" scope stated")
					case sc.scope(s.date, s.start, s.end):
						ans = append(ans, sc.name+" scope")
					}
				}
				if len(ans) > 0 {
					s.answer = strings.Join(ans, ", ")
					lines = append(lines, s)
				}
			}
			for _, a := range eg.Added(from, to) {
				s := sessionLine{label: a.ActivityLabel, date: a.Date, start: a.Start, end: a.End, answer: "added"}
				if a.Novel {
					s.answer += " novel"
				}
				if a.Uncertain {
					s.answer += " uncertain"
				}
				lines = append(lines, s)
			}
			slices.SortStableFunc(lines, func(x, y sessionLine) int {
				return cmp.Or(cmp.Compare(x.date/10, y.date/10), cmp.Compare(x.start, y.start), cmp.Compare(x.end, y.end), cmp.Compare(x.label, y.label))
			})
			for _, s := range lines {
				fmt.Fprintf(&gb, "  %s %s-%s %q: %s\n", Date(int32(s.date)), fmtClock(int32(s.start)), fmtClock(int32(s.end)), s.label, s.answer)
			}
			if gb.Len() > 0 {
				fmt.Fprintf(&fb, "group %q\n%s", grp.GetLabel(), gb.String())
			}
		}
		if fb.Len() > 0 {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			fmt.Fprintf(&b, "== %s, %s to %s\n%s", fac.GetName(), Date(int32(from)), Date(int32(to)), fb.String())
		}
	}
	return b.String()
}

type sessionLine struct {
	label      string
	date       schema.Date
	start, end int
	answer     string
}

// sessions enumerates the group's published sessions on days, each once.
func sessions(grp ottrecidx.ScheduleGroupRef, days []schema.Date) []sessionLine {
	type key struct {
		label      string
		day        int
		start, end int
	}
	seen := map[key]bool{}
	var out []sessionLine
	add := func(label string, d schema.Date, r schema.ClockRange) {
		k := key{label, int(d) / 10, int(r.Start), int(r.End)}
		if !seen[k] {
			seen[k] = true
			out = append(out, sessionLine{label: label, date: d, start: k.start, end: k.end})
		}
	}
	for sch := range grp.Schedules() {
		er, erOK := sch.ComputeEffectiveDateRange()
		for act := range sch.Activities() {
			for tm := range act.Times() {
				r, ok := tm.GetRange()
				if !ok {
					continue
				}
				if d, ok := tm.SingleDate(); ok {
					if int(d)/10 >= int(days[0])/10 && int(d)/10 <= int(days[len(days)-1])/10 {
						add(act.GetLabel(), d, r)
					}
					continue
				}
				wd, ok := tm.GetWeekday()
				if !ok {
					continue
				}
				for _, d := range days {
					if w, _ := d.Weekday(); w != wd {
						continue
					}
					if erOK && (!er.From.IsZero() && int(d)/10 < int(er.From)/10 || !er.To.IsZero() && int(d)/10 > int(er.To)/10) {
						continue
					}
					add(act.GetLabel(), d, r)
				}
			}
		}
	}
	return out
}

// warningRuns renders the per-day warnings as runs of equal levels, leaving
// out WarnNone.
func warningRuns(days []schema.Date, warning func(from, to schema.Date) enrichidx.Warning) string {
	ws := make([]enrichidx.Warning, len(days))
	for i, d := range days {
		ws[i] = warning(d, d)
	}
	var runs []string
	for i := 0; i < len(days); {
		w := ws[i]
		j := i + 1
		for j < len(days) && ws[j] == w {
			j++
		}
		if w != enrichidx.WarnNone {
			s := warningName(w) + " " + Date(int32(days[i]))
			if j-1 > i {
				s += " to " + Date(int32(days[j-1]))
			}
			runs = append(runs, s)
		}
		i = j
	}
	return strings.Join(runs, ", ")
}

func warningName(w enrichidx.Warning) string {
	switch w {
	case enrichidx.WarnNotice:
		return "notice"
	case enrichidx.WarnChanges:
		return "changes"
	}
	return fmt.Sprintf("warning(%d)", int(w))
}
