// Command report writes a self-contained HTML debugging report for one
// enriched dataset version: source blocks with highlighted extraction
// ranges beside the enrichment objects (see the report package).
//
//	go run ./cmd/report -o report.html   # latest version
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/ottrec/data-enrichment/internal/dataver"
	"github.com/ottrec/data-enrichment/report"
)

var (
	cachePath = flag.String("cache", "/tmp/ottrec-data.db", "ottrecdata cache path")
	version   = flag.String("version", "", "dataset version id (default: latest)")
	outPath   = flag.String("o", "report.html", "output file")
)

func main() {
	flag.Parse()
	ctx := context.Background()

	var err error
	for ver, data := range dataver.Each(ctx, *cachePath)(&err) {
		if *version != "" && ver.ID != *version {
			continue
		}
		if err := os.WriteFile(*outPath, report.Build(ver.ID, data), 0o666); err != nil {
			panic(err)
		}
		fmt.Fprintf(os.Stderr, "wrote %s for %s\n", *outPath, ver.ID)
		return
	}
	if err != nil {
		panic(err)
	}
	fmt.Fprintln(os.Stderr, "version not found")
	os.Exit(1)
}
