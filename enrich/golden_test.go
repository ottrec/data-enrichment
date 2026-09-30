package enrich_test

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ottrec/data-enrichment/enrich"
	"github.com/ottrec/data-enrichment/internal/golden"
	epb "github.com/ottrec/data-enrichment/schema"
	"github.com/ottrec/website/pkg/ottrecidx"
)

var update = flag.Bool("update", false, "rewrite the golden files from the current parser output")

const corpusDir = "testdata/corpus"

// TestGolden runs the parser over every fixture in testdata/corpus (written
// by cmd/mkcorpus) and compares a text rendering of its objects and their
// placement with the .golden file beside it. Ids, sequence numbers, block
// hashes, offsets and raw HTML are left out, as in the full-corpus diff, so
// that a renumbering alone is not a change.
//
//	go test ./enrich                          # check
//	go test ./enrich -run Golden -update      # rewrite after a reviewed change
//	go test ./enrich -run 'Golden/minto-.*'   # one facility
//
// The output also depends on ottrecidx (effective date ranges, slots), so the
// golden files follow the website module the build resolves: the workspace's
// under go.work, the go.mod pin otherwise.
func TestGolden(t *testing.T) {
	fixtures := corpusFixtures(t)
	if len(fixtures) == 0 {
		t.Skip("no fixtures; run go run ./cmd/mkcorpus")
	}
	if *update {
		removeStale(t, fixtures)
	}
	outs := corpusOutputs(t)
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := golden.Render(outs[name].out)
			path := filepath.Join(corpusDir, name+".golden")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o666); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run go test ./enrich -run Golden -update)", err)
			}
			if d := golden.Diff(string(want), got); d != "" {
				t.Errorf("%s changed (-want +got):\n%s", path, d)
			}
		})
	}
}

// corpusFixtures lists the fixture names, relative to corpusDir and without
// the extension.
func corpusFixtures(t testing.TB) []string {
	var names []string
	err := filepath.WalkDir(corpusDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".pb") {
			rel, _ := filepath.Rel(corpusDir, p)
			names = append(names, strings.TrimSuffix(rel, ".pb"))
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	slices.Sort(names)
	return names
}

// removeStale deletes golden files whose fixture is gone.
func removeStale(t testing.TB, fixtures []string) {
	filepath.WalkDir(corpusDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".golden") {
			return err
		}
		rel, _ := filepath.Rel(corpusDir, p)
		if _, ok := slices.BinarySearch(fixtures, strings.TrimSuffix(rel, ".golden")); !ok {
			if err := os.Remove(p); err != nil {
				t.Error(err)
			}
		}
		return nil
	})
}

type fixtureOutput struct {
	data ottrecidx.DataRef
	out  *epb.Output
}

var corpus struct {
	once sync.Once
	outs map[string]fixtureOutput
	err  error
}

// corpusOutputs runs the parser over every fixture once per test binary.
func corpusOutputs(t testing.TB) map[string]fixtureOutput {
	corpus.once.Do(func() {
		names := corpusFixtures(t)
		outs := make([]fixtureOutput, len(names))
		errs := make([]error, len(names))
		var wg sync.WaitGroup
		sem := make(chan struct{}, runtime.GOMAXPROCS(0))
		for i, name := range names {
			wg.Go(func() {
				sem <- struct{}{}
				defer func() { <-sem }()
				buf, err := os.ReadFile(filepath.Join(corpusDir, name+".pb"))
				if err != nil {
					errs[i] = err
					return
				}
				idx, err := new(ottrecidx.Indexer).Load(buf)
				if err != nil {
					errs[i] = fmt.Errorf("load %s: %w", name, err)
					return
				}
				outs[i] = fixtureOutput{idx.Data(), enrich.EnrichVersion(name, idx.Data())}
			})
		}
		wg.Wait()
		corpus.outs = map[string]fixtureOutput{}
		for i, name := range names {
			corpus.outs[name] = outs[i]
		}
		corpus.err = errors.Join(errs...)
	})
	if corpus.err != nil {
		t.Fatal(corpus.err)
	}
	return corpus.outs
}
