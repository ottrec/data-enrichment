// Command dump-context dumps special hours, notifications, and schedule
// change HTML blocks in full, with the schedule structure they belong to, for
// inspection. Unlike dump-special, blocks are kept whole (newlines escaped)
// so per-block structure is visible.
//
//	go run ./cmd/dump-context [-versions n] [-blocks] > context.txt
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"iter"
	"strings"

	"github.com/ottrec/website/pkg/ottrecdata"
	"github.com/ottrec/website/pkg/ottrecidx"
)

var (
	nVersions  = flag.Int("versions", 1, "number of most recent versions to dump (0 = all)")
	blocksOnly = flag.Bool("blocks", false, "dump only unique whole blocks (for uniq -c), no schedule context")
)

func main() {
	flag.Parse()
	ctx := context.Background()

	var err error
	n := 0
	for ver, data := range versions(ctx, "/tmp/ottrec-data.db")(&err) {
		if *nVersions > 0 && n >= *nVersions {
			break
		}
		n++

		if *blocksOnly {
			for fac := range data.Facilities() {
				if s := fac.GetSpecialHoursHTML(); s != "" {
					fmt.Printf("SPECIAL\t%s\n", oneline(s))
				}
				if s := fac.GetNotificationsHTML(); s != "" {
					fmt.Printf("NOTIF\t%s\n", oneline(s))
				}
			}
			for grp := range data.ScheduleGroups() {
				if s := grp.GetScheduleChangesHTML(); s != "" {
					fmt.Printf("CHANGES\t%s\n", oneline(s))
				}
			}
			continue
		}

		for fac := range data.Facilities() {
			sh, nh := fac.GetSpecialHoursHTML(), fac.GetNotificationsHTML()
			hasChanges := false
			for grp := range fac.ScheduleGroups() {
				if grp.GetScheduleChangesHTML() != "" {
					hasChanges = true
				}
			}
			if sh == "" && nh == "" && !hasChanges {
				continue
			}

			fmt.Printf("=== %s [%s]\n", fac.GetName(), ver.ID)
			if sh != "" {
				fmt.Printf("  SPECIAL: %s\n", oneline(sh))
			}
			if nh != "" {
				fmt.Printf("  NOTIF: %s\n", oneline(nh))
			}
			for grp := range fac.ScheduleGroups() {
				ch := grp.GetScheduleChangesHTML()
				if ch == "" {
					continue
				}
				fmt.Printf("  GROUP %q (title %q)\n", grp.GetLabel(), grp.GetTitle())
				fmt.Printf("    CHANGES: %s\n", oneline(ch))
				for sched := range grp.Schedules() {
					dr, _ := sched.GetDateRange()
					fmt.Printf("    SCHED caption=%q name=%q date=%q range=%q days=", sched.GetCaption(), sched.GetName(), sched.GetDate(), dr)
					for i := range sched.NumDays() {
						if i > 0 {
							fmt.Print("|")
						}
						fmt.Print(sched.GetDay(i))
					}
					fmt.Println()
					for act := range sched.Activities() {
						fmt.Printf("      ACT label=%q name=%q\n", act.GetLabel(), act.GetName())
						for tm := range act.Times() {
							wd, wdOK := tm.GetWeekday()
							r, rOK := tm.GetRange()
							fmt.Printf("        TIME day=%q wd=%v(%v) range=%v(%v) label=%q\n", tm.GetScheduleDay(), wd, wdOK, r, rOK, tm.GetLabel())
						}
					}
				}
			}
			fmt.Println()
		}
	}
	if err != nil {
		panic(err)
	}
}

func oneline(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

func versions(ctx context.Context, cachePath string) func(*error) iter.Seq2[ottrecdata.DataVersion, ottrecidx.DataRef] {
	return func(err *error) iter.Seq2[ottrecdata.DataVersion, ottrecidx.DataRef] {
		return func(yield func(ottrecdata.DataVersion, ottrecidx.DataRef) bool) {
			*err = func() error {
				cache, err := ottrecdata.OpenCacheReadOnly(cachePath)
				if err != nil {
					return fmt.Errorf("open cache %q: %w", cachePath, err)
				}
				defer cache.Close()

				for ver := range cache.DataVersions(ctx)(&err) {
					var pbh string
					for hash, fmt := range cache.DataFormats(ctx, ver.ID)(&err) {
						if fmt == "pb" {
							pbh = hash
							break
						}
					}
					if err != nil {
						return fmt.Errorf("list %s formats: %w", ver.ID, err)
					}
					if pbh == "" {
						return fmt.Errorf("read %s: missing pb", ver.ID)
					}

					var pb []byte
					cache.ReadBlob(ctx, pbh, false, func(r io.Reader, i int64) (err error) {
						pb, err = io.ReadAll(r)
						return
					})
					if err != nil {
						return fmt.Errorf("read %s: %w", ver.ID, err)
					}

					idx, err := new(ottrecidx.Indexer).Load(pb)
					if err != nil {
						return fmt.Errorf("load %s: %w", ver.ID, err)
					}

					if !yield(ver, idx.Data()) {
						break
					}
				}
				if err != nil {
					return fmt.Errorf("list versions: %w", err)
				}
				return nil
			}()
		}
	}
}
