package golden

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/ottrec/data-enrichment/enrich"
	epb "github.com/ottrec/data-enrichment/schema"
	"github.com/ottrec/website/pkg/ottrecidx"
)

// Fixture is one golden corpus fixture (a single-facility dataset written by
// cmd/mkcorpus) and the parser's output for it.
type Fixture struct {
	Name string // path relative to the corpus directory, without .pb
	Data ottrecidx.DataRef
	Out  *epb.Output
}

// Fixtures lists the fixture names under dir, sorted. A missing dir is no
// fixtures.
func Fixtures(dir string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".pb") {
			rel, _ := filepath.Rel(dir, p)
			names = append(names, strings.TrimSuffix(rel, ".pb"))
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	slices.Sort(names)
	return names, nil
}

// Load reads and enriches the named fixtures under dir, in parallel.
func Load(dir string, names []string) ([]Fixture, error) {
	fx := make([]Fixture, len(names))
	errs := make([]error, len(names))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for i, name := range names {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			buf, err := os.ReadFile(filepath.Join(dir, name+".pb"))
			if err != nil {
				errs[i] = err
				return
			}
			idx, err := new(ottrecidx.Indexer).Load(buf)
			if err != nil {
				errs[i] = fmt.Errorf("load %s: %w", name, err)
				return
			}
			fx[i] = Fixture{name, idx.Data(), enrich.EnrichVersion(name, idx.Data())}
		})
	}
	wg.Wait()
	return fx, errors.Join(errs...)
}
