// Package dataver iterates over dataset versions in an ottrecdata cache.
package dataver

import (
	"context"
	"fmt"
	"io"
	"iter"

	"github.com/ottrec/website/pkg/ottrecdata"
	"github.com/ottrec/website/pkg/ottrecidx"
)

// Each iterates over the versions in the cache at cachePath, most recent
// first, yielding each version with its loaded data.
func Each(ctx context.Context, cachePath string) func(*error) iter.Seq2[ottrecdata.DataVersion, ottrecidx.DataRef] {
	return func(err *error) iter.Seq2[ottrecdata.DataVersion, ottrecidx.DataRef] {
		return func(yield func(ottrecdata.DataVersion, ottrecidx.DataRef) bool) {
			var lerr error
			for ver, pb := range EachPB(ctx, cachePath)(err) {
				idx, e := new(ottrecidx.Indexer).Load(pb)
				if e != nil {
					lerr = fmt.Errorf("load %s: %w", ver.ID, e)
					break
				}
				if !yield(ver, idx.Data()) {
					break
				}
			}
			if lerr != nil {
				*err = lerr
			}
		}
	}
}

// EachPB is Each without the indexing: it yields the raw binary protobuf of
// each version.
func EachPB(ctx context.Context, cachePath string) func(*error) iter.Seq2[ottrecdata.DataVersion, []byte] {
	return func(err *error) iter.Seq2[ottrecdata.DataVersion, []byte] {
		return func(yield func(ottrecdata.DataVersion, []byte) bool) {
			*err = func() error {
				cache, err := ottrecdata.OpenCacheReadOnly(cachePath)
				if err != nil {
					return fmt.Errorf("open cache %q: %w", cachePath, err)
				}
				defer cache.Close()

				for ver := range cache.DataVersions(ctx)(&err) {
					var pbh string
					for hash, format := range cache.DataFormats(ctx, ver.ID)(&err) {
						if format == "pb" {
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

					if !yield(ver, pb) {
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
