// Command check-coverage verifies the enrichment's total-accounting
// guarantee: every word of text in every source HTML block must appear in
// the raw text of at least one output object for that block.
//
//	go run ./cmd/check-coverage [-versions 0]
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/ottrec/data-enrichment/enrich"
	"github.com/ottrec/data-enrichment/internal/dataver"
)

var (
	cachePath = flag.String("cache", "/tmp/ottrec-data.db", "ottrecdata cache path")
	nVersions = flag.Int("versions", 0, "number of most recent versions to check (0 = all)")
	maxShow   = flag.Int("show", 30, "max uncovered blocks to print")
)

func words(s string) map[string]bool {
	out := map[string]bool{}
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	for _, w := range strings.Fields(b.String()) {
		out[w] = true
	}
	return out
}

func main() {
	flag.Parse()
	ctx := context.Background()

	versions, blocks, bad, shown := 0, 0, 0, 0
	var err error
	for ver, data := range dataver.Each(ctx, *cachePath)(&err) {
		if *nVersions > 0 && versions >= *nVersions {
			break
		}
		versions++

		out := enrich.EnrichVersion(ver.ID, data)
		covered := map[string]map[string]bool{} // facility\x00blockHash -> words
		for _, o := range out.Objects {
			key := o.Facility + "\x00" + o.BlockHash
			m := covered[key]
			if m == nil {
				m = map[string]bool{}
				covered[key] = m
			}
			for w := range words(o.RawText + " " + o.DateText) {
				m[w] = true
			}
		}

		check := func(fac, source, group, blockHTML string) {
			if strings.TrimSpace(blockHTML) == "" {
				return
			}
			blocks++
			sum := sha256.Sum256([]byte(blockHTML))
			key := fac + "\x00" + hex.EncodeToString(sum[:8])
			have := covered[key]
			var missing []string
			for w := range words(enrich.BlockText(blockHTML)) {
				if !have[w] {
					missing = append(missing, w)
				}
			}
			if len(missing) > 0 {
				bad++
				if shown < *maxShow {
					shown++
					fmt.Printf("MISSING %v\n  %s | %s | %s [%s]\n  %.200s\n", missing, ver.ID, fac, source, group, blockHTML)
				}
			}
		}

		for fac := range data.Facilities() {
			name := fac.GetName()
			check(name, "special_hours", "", fac.GetSpecialHoursHTML())
			check(name, "notifications", "", fac.GetNotificationsHTML())
			for grp := range fac.ScheduleGroups() {
				check(name, "schedule_changes", grp.GetLabel(), grp.GetScheduleChangesHTML())
			}
		}
	}
	if err != nil {
		panic(err)
	}

	fmt.Fprintf(os.Stderr, "checked %d versions, %d blocks: %d with uncovered text\n", versions, blocks, bad)
	if bad > 0 {
		os.Exit(1)
	}
}
