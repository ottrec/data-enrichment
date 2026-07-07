// Command enrichcheck sanity-checks enrichidx joins for one dataset version,
// mimicking the /today feed's join keys and window: per-group warning levels
// vs the old changes-HTML flag (with the objects behind every downgrade and
// sampled upgrades/notices), see-schedule hits, per-session cancellation
// hits, and added sessions. The window is anchored at the version's updated
// date, so historical versions replay what /today would have shown.
//
//	go run ./notes/scripts [version-spec]
//
// version-spec is anything ottrecdata.ResolveVersion takes: "latest"
// (default), "latest-8", "2026-06-29", or a version id. Needs
// /tmp/ottrec-data.db.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ottrec/data-enrichment/enrich"
	"github.com/ottrec/data-enrichment/enrichidx"
	epb "github.com/ottrec/data-enrichment/schema"
	"github.com/ottrec/scraper/schema"
	"github.com/ottrec/website/pkg/ottrecdata"
	"github.com/ottrec/website/pkg/ottrecidx"
)

func main() {
	ctx := context.Background()
	cache, err := ottrecdata.OpenCacheReadOnly("/tmp/ottrec-data.db")
	if err != nil {
		panic(err)
	}
	defer cache.Close()

	spec := "latest"
	if len(os.Args) > 1 {
		spec = os.Args[1]
	}
	id, _, ok, err := cache.ResolveVersion(ctx, spec)
	if err != nil {
		panic(err)
	}
	if !ok || id == "" {
		fmt.Fprintf(os.Stderr, "no version matching %q\n", spec)
		os.Exit(1)
	}
	var pbh string
	for hash, format := range cache.DataFormats(ctx, id)(&err) {
		if format == "pb" {
			pbh = hash
			break
		}
	}
	if err != nil {
		panic(err)
	}
	var pb []byte
	if _, err := cache.ReadBlob(ctx, pbh, false, func(r io.Reader, _ int64) (err error) {
		pb, err = io.ReadAll(r)
		return
	}); err != nil {
		panic(err)
	}
	idx, err := new(ottrecidx.Indexer).Load(pb)
	if err != nil {
		panic(err)
	}
	check(id, idx.Data())
}

func check(version string, data ottrecidx.DataRef) {
	out := enrich.EnrichVersion(version, data)
	en := enrichidx.Join(out)

	// the /today feed window, anchored at the version date instead of the
	// wall clock so historical versions replay sensibly
	loc := ottrecidx.TZ
	now := data.Index().Updated().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	dates := make([]time.Time, 7)
	dayIndex := map[schema.Date]int{}
	for i := range dates {
		d := today.AddDate(0, 0, i)
		dates[i] = d
		dayIndex[schema.MakeDateFromGo(d)/10] = i
	}
	feedFrom := schema.MakeDateFromGo(dates[0])
	feedTo := schema.MakeDateFromGo(dates[6])
	badgeFrom := schema.MakeDateFromGo(today.AddDate(0, 0, -7))
	badgeTo := schema.MakeDateFromGo(today.AddDate(0, 0, 6+7))

	fmt.Printf("version %s, window %s..%s\n", version, feedFrom, feedTo)

	var (
		groups, oldChanges                  int
		warnNone, warnNotice, warnChanges   int
		downgraded, upgraded                int
		sessions, cancelled, changed, added int
	)
	for fac := range data.Facilities() {
		enFac := en.Facility(fac.GetName())
		facWarn := enFac.Warning(feedFrom, feedTo)
		for grp := range fac.ScheduleGroups() {
			groups++
			enGrp := enFac.Group(grp.GetLabel())
			warn := max(facWarn, enGrp.Warning(feedFrom, feedTo))
			old := grp.GetScheduleChangesHTML() != ""
			if old {
				oldChanges++
			}
			switch warn {
			case enrichidx.WarnNone:
				warnNone++
			case enrichidx.WarnNotice:
				warnNotice++
			case enrichidx.WarnChanges:
				warnChanges++
			}
			// every downgrade with the objects behind it (the
			// no-false-suppression check), plus sampled notices and upgrades
			if old && warn != enrichidx.WarnChanges {
				downgraded++
				fmt.Printf("  down %v: %s / %s\n", warn, fac.GetName(), grp.GetLabel())
				dumpObjects(out, fac.GetName(), grp.GetLabel())
			}
			if warn == enrichidx.WarnNotice && !old && warnNotice <= 6 {
				fmt.Printf("  notice: %s / %s\n", fac.GetName(), grp.GetLabel())
				dumpObjects(out, fac.GetName(), grp.GetLabel())
			}
			if !old && warn == enrichidx.WarnChanges {
				upgraded++
				if upgraded <= 6 {
					fmt.Printf("  up: %s / %s\n", fac.GetName(), grp.GetLabel())
					dumpObjects(out, fac.GetName(), grp.GetLabel())
				}
			}

			if enFac.SeeSchedule(badgeFrom, badgeTo) || enGrp.SeeSchedule(badgeFrom, badgeTo) {
				fmt.Printf("  seesched: %s / %s\n", fac.GetName(), grp.GetLabel())
			}

			for _, ad := range enGrp.Added(feedFrom, feedTo) {
				if _, in := dayIndex[ad.Date/10]; in {
					added++
					fmt.Printf("  added: %s / %s / %q novel=%v %s %d-%d\n",
						fac.GetName(), grp.GetLabel(), ad.ActivityLabel, ad.Novel, ad.Date, ad.Start, ad.End)
				}
			}

			// place sessions exactly like buildTodayFeed and probe Cancelled
			// with the same keys (raw activity label, concrete date, exact
			// published minutes)
			for sch := range grp.Schedules() {
				er, erOK := sch.ComputeEffectiveDateRange()
				for act := range sch.Activities() {
					for tm := range act.Times() {
						r, ok := tm.GetRange()
						if !ok {
							continue
						}
						hit := func(day schema.Date) {
							sessions++
							m := enGrp.Session(act.GetLabel(), day, int(r.Start), int(r.End))
							if m.Cancelled {
								cancelled++
								if cancelled <= 12 {
									fmt.Printf("  cancel: %s / %s / %s %s %d-%d\n",
										fac.GetName(), grp.GetLabel(), act.GetLabel(), day, r.Start, r.End)
								}
							}
							if m.TimeChange {
								changed++
								fmt.Printf("  change: %s / %s / %s %s %d-%d new=%v %d-%d\n",
									fac.GetName(), grp.GetLabel(), act.GetLabel(), day, r.Start, r.End,
									m.NewTime, m.NewStart, m.NewEnd)
							}
						}
						if d, ok := tm.SingleDate(); ok {
							if _, in := dayIndex[d/10]; in {
								hit(d)
							}
							continue
						}
						wd, ok := tm.GetWeekday()
						if !ok {
							continue
						}
						for _, d := range dates {
							if d.Weekday() != wd {
								continue
							}
							dd := schema.MakeDateFromGo(d)
							if erOK {
								if !er.From.IsZero() && int(er.From)/10 > int(dd)/10 {
									continue
								}
								if !er.To.IsZero() && int(er.To)/10 < int(dd)/10 {
									continue
								}
							}
							hit(dd)
						}
					}
				}
			}
		}
	}
	fmt.Printf("groups=%d oldChanges=%d -> none=%d notice=%d changes=%d (downgraded=%d upgraded=%d)\n",
		groups, oldChanges, warnNone, warnNotice, warnChanges, downgraded, upgraded)
	fmt.Printf("sessions=%d cancelled=%d changed=%d added=%d\n", sessions, cancelled, changed, added)
}

// dumpObjects prints the facility-level objects and the named group's subtree
// objects from the raw output, for eyeballing why a warning level was chosen.
func dumpObjects(out *epb.Output, facName, grpLabel string) {
	byID := map[string]*epb.Object{}
	for _, o := range out.GetObjects() {
		byID[o.GetId()] = o
	}
	show := func(pfx, id string) {
		o := byID[id]
		if o == nil || o.GetKind() == epb.Object_IGNORED {
			return
		}
		fmt.Printf("      %s %v dates=%v dateText=%q text=%.80q\n",
			pfx, o.GetKind(), o.GetDates(), o.GetDateText(), o.GetRawText())
	}
	for _, ef := range out.GetFacilities() {
		if ef.GetName() != facName {
			continue
		}
		for _, id := range ef.GetObjects() {
			show("fac", id)
		}
		for _, eg := range ef.GetGroups() {
			if eg.GetLabel() != grpLabel {
				continue
			}
			for _, id := range eg.GetObjects() {
				show("grp", id)
			}
			for _, ea := range eg.GetActivities() {
				for _, id := range ea.GetObjects() {
					show("act", id)
				}
				for _, es := range ea.GetSessions() {
					for _, id := range es.GetObjects() {
						show("sess", id)
					}
					for _, id := range es.GetAdded() {
						show("add", id)
					}
				}
			}
		}
	}
}
