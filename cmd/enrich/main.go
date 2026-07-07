// Command enrich derives structured schedule-change/special-hours records
// from dataset versions in an ottrecdata cache.
//
//	go run ./cmd/enrich                     # latest version, JSON to stdout
//	go run ./cmd/enrich -versions 0 -o dir  # all versions, one file each
//	go run ./cmd/enrich -versions 0 -o ""   # stats only
package main

import (
	"context"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/ottrec/data-enrichment/enrich"
	"github.com/ottrec/data-enrichment/internal/dataver"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var (
	cachePath = flag.String("cache", "/tmp/ottrec-data.db", "ottrecdata cache path")
	nVersions = flag.Int("versions", 1, "number of most recent versions to process (0 = all)")
	outPath   = flag.String("o", "-", `output: "-" for stdout (single version only), a directory for one file per version, "" for stats only`)
	format    = flag.String("format", "json", `output format: "json" (protojson) or "pb" (binary protobuf)`)
)

func main() {
	flag.Parse()
	ctx := context.Background()

	agg := map[string]int{}
	n := 0
	var err error
	for ver, data := range dataver.Each(ctx, *cachePath)(&err) {
		if *nVersions > 0 && n >= *nVersions {
			break
		}
		n++

		out := enrich.EnrichVersion(ver.ID, data)
		for k, v := range out.GetStats() {
			agg[k] += int(v)
		}

		marshal := func() []byte {
			if *format == "pb" {
				buf, err := proto.Marshal(out)
				if err != nil {
					panic(err)
				}
				return buf
			}
			buf, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(out)
			if err != nil {
				panic(err)
			}
			return append(buf, '\n')
		}
		switch {
		case *outPath == "":
		case *outPath == "-":
			if *nVersions != 1 {
				fmt.Fprintln(os.Stderr, "stdout output requires -versions 1; use -o dir")
				os.Exit(2)
			}
			os.Stdout.Write(marshal())
		default:
			if err := os.MkdirAll(*outPath, 0o777); err != nil {
				panic(err)
			}
			if err := os.WriteFile(filepath.Join(*outPath, ver.ID+"."+*format), marshal(), 0o666); err != nil {
				panic(err)
			}
		}
	}
	if err != nil {
		panic(err)
	}

	fmt.Fprintf(os.Stderr, "processed %d versions\n", n)
	for _, k := range slices.Sorted(maps.Keys(agg)) {
		fmt.Fprintf(os.Stderr, "%8d %s\n", agg[k], k)
	}
}
