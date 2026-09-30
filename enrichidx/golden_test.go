package enrichidx_test

import (
	"errors"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ottrec/data-enrichment/internal/golden"
)

var update = flag.Bool("update", false, "rewrite the golden files from the current answers")

const (
	corpusDir   = "../enrich/testdata/corpus"
	consumerDir = "testdata/consumer"
)

// TestConsumerGolden runs the parser over every fixture of the enrich golden
// corpus and compares enrichidx's answers for the fixture's published
// sessions (golden.Consumer) with testdata/consumer/<fixture>.golden. A
// fixture with no answers has no golden file. This is what shows a change to
// the trust rules, which no parser golden can.
//
//	go test ./enrichidx                              # check
//	go test ./enrichidx -run ConsumerGolden -update  # rewrite after a reviewed change
func TestConsumerGolden(t *testing.T) {
	names, err := golden.Fixtures(corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Skip("no fixtures; run go run ./cmd/mkcorpus")
	}
	fixtures, err := golden.Load(corpusDir, names)
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		removeStale(t, names)
	}
	for _, fx := range fixtures {
		t.Run(fx.Name, func(t *testing.T) {
			t.Parallel()
			got := golden.Consumer(fx.Data, fx.Out)
			path := filepath.Join(consumerDir, fx.Name+".golden")
			if *update {
				if got == "" {
					if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
						t.Fatal(err)
					}
					return
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o666); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				t.Fatal(err)
			}
			if d := golden.Diff(string(want), got); d != "" {
				t.Errorf("%s changed (-want +got):\n%s", path, d)
			}
		})
	}
}

// removeStale deletes golden files whose fixture is gone.
func removeStale(t testing.TB, fixtures []string) {
	filepath.WalkDir(consumerDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".golden") {
			return nil
		}
		rel, _ := filepath.Rel(consumerDir, p)
		if _, ok := slices.BinarySearch(fixtures, strings.TrimSuffix(rel, ".golden")); !ok {
			if err := os.Remove(p); err != nil {
				t.Error(err)
			}
		}
		return nil
	})
}
