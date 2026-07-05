package main

import (
	"context"
	"fmt"
	"io"
	"iter"

	"github.com/ottrec/website/pkg/ottrecdata"
	"github.com/ottrec/website/pkg/ottrecidx"
)

func main() {
	ctx := context.Background()

	var err error
	for ver, data := range versions(ctx, "/tmp/ottrec-data.db")(&err) {
		fmt.Println(ver.ID, ver.Updated, ver.Revision, data.Index())

		for fac := range data.Facilities() {
			fmt.Println(fac.GetSpecialHoursHTML())
			fmt.Println(fac.GetNotificationsHTML())
		}
		for grp := range data.ScheduleGroups() {
			fmt.Println(grp.GetScheduleChangesHTML())
		}
	}
	if err != nil {
		panic(err)
	}
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
