// Command mkcorpus writes the golden corpus fixtures for enrich's golden test
// from an ottrecdata cache: one trimmed single-facility dataset per distinct
// combination of source blocks and schedule structure a facility has had,
// taken from the oldest version that has it.
//
//	go run ./cmd/mkcorpus                  # refresh enrich/testdata/corpus
//	go test ./enrich -run Golden -update   # write golden files for new fixtures
//
// A fixture keeps what the parser reads (name, source date, blocks, schedule
// groups and their schedules) and drops the rest (description, address,
// coordinates, errors, links). Keying on the schedule as well as the blocks
// keeps the variants where the same block matched different slots. Picking
// the oldest version keeps existing fixtures stable as the cache grows, and
// puts the anchor closest to when the city posted the block.
package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ottrec/data-enrichment/internal/dataver"
	"github.com/ottrec/scraper/schema"
	"github.com/ottrec/website/pkg/ottrecidx"
	"google.golang.org/protobuf/proto"
)

var (
	cachePath = flag.String("cache", "/tmp/ottrec-data.db", "ottrecdata cache path")
	outDir    = flag.String("o", "enrich/testdata/corpus", "fixture directory")
	blockOnly = flag.Bool("blocks-only", false, "key fixtures on the blocks alone, not also the schedule structure")
)

type fixture struct {
	name string
	pb   []byte
}

func main() {
	flag.Parse()
	ctx := context.Background()

	fixtures := map[string]fixture{}
	versions := 0
	var err error
	for ver, pb := range dataver.EachPB(ctx, *cachePath)(&err) {
		versions++
		var data schema.Data
		if err := proto.Unmarshal(pb, &data); err != nil {
			panic(fmt.Errorf("unmarshal %s: %w", ver.ID, err))
		}
		for _, fac := range data.GetFacilities() {
			key, ok := blockKey(fac)
			if !ok {
				continue
			}
			t := trim(fac)
			if !*blockOnly {
				key += "\x00" + scheduleKey(t)
			}
			if !t.GetSource().HasXDate() {
				// Index.Updated is the anchor fallback, and a single-facility
				// dataset would not reproduce it
				panic(fmt.Errorf("%s: %q has no source date", ver.ID, fac.GetName()))
			}
			buf, err := proto.MarshalOptions{Deterministic: true}.Marshal(schema.Data_builder{
				Facilities: []*schema.Facility{t},
			}.Build())
			if err != nil {
				panic(err)
			}
			// versions come newest first, so the last write is the oldest
			fixtures[key] = fixture{
				name: filepath.Join(slug(fac.GetName()), ver.Updated.In(ottrecidx.TZ).Format("2006-01-02")+"-"+strings.ToLower(ver.ID[:6])),
				pb:   buf,
			}
		}
	}
	if err != nil {
		panic(err)
	}

	// replace the .pb files; the test's -update removes golden files whose
	// fixture is gone
	want := map[string]bool{}
	for _, f := range fixtures {
		p := filepath.Join(*outDir, f.name+".pb")
		if want[p] {
			panic(fmt.Errorf("duplicate fixture name %s", p))
		}
		want[p] = true
	}
	err = filepath.WalkDir(*outDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".pb") && !want[p] {
			err = os.Remove(p)
		}
		return err
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		panic(err)
	}
	var size int
	for _, f := range fixtures {
		p := filepath.Join(*outDir, f.name+".pb")
		if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
			panic(err)
		}
		if err := os.WriteFile(p, f.pb, 0o666); err != nil {
			panic(err)
		}
		size += len(f.pb)
	}
	fmt.Fprintf(os.Stderr, "%d versions, %d fixtures, %d bytes\n", versions, len(fixtures), size)
}

// blockKey identifies the set of source blocks a facility carries, or false
// when it has none.
func blockKey(fac *schema.Facility) (string, bool) {
	var keys []string
	add := func(source, group, html string) {
		if strings.TrimSpace(html) != "" {
			sum := sha256.Sum256([]byte(html))
			keys = append(keys, fmt.Sprintf("%s\x00%s\x00%x", source, group, sum[:8]))
		}
	}
	add("special_hours", "", fac.GetSpecialHoursHtml())
	add("notifications", "", fac.GetNotificationsHtml())
	for _, grp := range fac.GetScheduleGroups() {
		add("schedule_changes", grp.GetLabel(), grp.GetScheduleChangesHtml())
	}
	if len(keys) == 0 {
		return "", false
	}
	slices.Sort(keys)
	return fac.GetName() + "\x00" + strings.Join(keys, "\x00"), true
}

// scheduleKey hashes the trimmed facility without its blocks and source date.
func scheduleKey(t *schema.Facility) string {
	s := proto.CloneOf(t)
	s.ClearSource()
	s.SetSpecialHoursHtml("")
	s.SetNotificationsHtml("")
	for _, grp := range s.GetScheduleGroups() {
		grp.SetScheduleChangesHtml("")
	}
	buf, err := proto.MarshalOptions{Deterministic: true}.Marshal(s)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(buf)
	return fmt.Sprintf("%x", sum[:8])
}

// trim copies the facility with only the fields the parser reads.
func trim(fac *schema.Facility) *schema.Facility {
	t := proto.CloneOf(fac)
	t.SetDescription("")
	t.SetAddress("")
	t.ClearXLnglat()
	t.SetXErrors(nil)
	if src := t.GetSource(); src != nil {
		src.SetUrl("")
	}
	for _, grp := range t.GetScheduleGroups() {
		grp.SetReservationLinks(nil)
	}
	return t
}

func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case r > 0x7f:
			// fold the accented letters the city uses (Sénécal, François)
			switch r {
			case 'é', 'è', 'ê', 'ë':
				r = 'e'
			case 'ç':
				r = 'c'
			case 'à', 'â':
				r = 'a'
			case 'ô':
				r = 'o'
			case 'î', 'ï':
				r = 'i'
			case 'û', 'ù':
				r = 'u'
			default:
				r = '-'
			}
			if r != '-' {
				b.WriteRune(r)
				dash = false
				continue
			}
			fallthrough
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}
