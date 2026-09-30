package enrich

import (
	"fmt"
	"html"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	epb "github.com/ottrec/data-enrichment/schema"
	"github.com/ottrec/website/pkg/ottrecidx"
	"google.golang.org/protobuf/encoding/protojson"
)

// vocabRow says why one vocabulary entry exists. An entry a corpus phrase
// needs names the fixture and the phrase. An entry no fixture needs states
// the reading it exists for and a probe that resolves that case: a guard
// against a wrong reading, or a rule a unit test pins. want is what the
// phrase or the probe resolves to with the entry in.
type vocabRow struct {
	entry   string // list:word; iceClassVocab entries by index
	fixture string // corpus fixture, relative to testdata/corpus
	phrase  string // raw text of the fixture's objects the entry decides
	reason  string // for an entry no fixture needs
	probe   func() string
	want    string
}

// vocabRows is the record of why each entry of the word lists is there
// (matching.md, "The word lists"). A new entry lands with its row: the
// phrase that needed it and what the phrase should resolve to. A phrase's
// want is the closure subject's reason with a colon, where the closure path
// decides (subject.go), then per notice the scope level, match quality,
// amenity, and activities or groups (phraseResolution).
var vocabRows = []vocabRow{
	{entry: "amenityCore:arena", fixture: "bob-macquarrie-recreation-complex-orleans/2026-09-19-tze5bk",
		phrase: "Roger Sénécal Arena is closed for annual maintenance.",
		want:   `amenity-core: amenity none "roger sénécal arena"`},
	{entry: "amenityCore:arenas", fixture: "minto-recreation-complex-barrhaven/2026-06-11-ns7p56",
		phrase: "The arenas are closed until further notice.",
		want:   `amenity-core: amenity none "arenas"`},
	{entry: "amenityCore:board", fixture: "ray-friel-recreation-complex/2026-09-28-xw6lza",
		phrase: "The diving board is closed until further notice.",
		want:   `amenity-core: amenity none "diving board"`},
	{entry: "amenityCore:boards", fixture: "walter-baker-sports-centre/2025-12-29-mw7dxs",
		phrase: "The 1m and 3m diving boards are closed until further notice.",
		want:   `amenity-core: amenity none "1m 3m diving boards"`},
	{entry: "amenityCore:centre", fixture: "terry-fox-athletic-facility/2026-01-09-a4qieg",
		phrase: "Mooney's Bay Cross Country Ski Centre is closed until further notice.",
		want:   `amenity-core: amenity none "mooney's bay cross country ski centre"`},
	{entry: "amenityCore:changeroom", fixture: "francois-dupuis-recreation-centre/2026-01-11-w5zhys",
		phrase: "Men's pool changeroom, closed for maintenance.",
		want:   `amenity-core: amenity none "men's pool changeroom"`},
	{entry: "amenityCore:changerooms", fixture: "nepean-sportsplex/2026-07-18-4bkyp6",
		phrase: "The athletics changerooms will be closed.",
		want:   `amenity-core: amenity none "athletics changerooms"`},
	{entry: "amenityCore:court", fixture: "bob-macquarrie-recreation-complex-orleans/2026-08-30-zdg6s2",
		phrase: "Squash court 3 is closed until further notice.",
		want:   `unit-of-row: amenity none "squash court"`},
	{entry: "amenityCore:courts", fixture: "walter-baker-sports-centre/2025-10-28-lqlr2u",
		phrase: "Squash courts 3 and 4 are closed until further notice.",
		want:   `unit-of-row: amenity none "squash courts"`},
	{entry: "amenityCore:elevator", fixture: "ray-friel-recreation-complex/2026-03-06-uu6ink",
		phrase: "The elevator is closed for maintenance.",
		want:   `amenity-core: amenity none "elevator"`},
	// the second sentence is an instruction, read as an amenity notice with
	// no effect
	{entry: "amenityCore:entrance", fixture: "bob-macquarrie-recreation-complex-orleans/2026-09-27-r4sgp6",
		phrase: "The Main Entrance will be closed due to construction. Please use the West Entrance.",
		want:   `amenity-core: amenity none "main entrance"; amenity none "please use west entrance"`},
	{entry: "amenityCore:field", fixture: "terry-fox-athletic-facility/2025-11-10-zersph",
		phrase: "Track and field is now closed for the season and the facility will reopen in January for cross-country skiing.",
		want:   `amenity-core: amenity none "track field"`},
	{entry: "amenityCore:gym", fixture: "nepean-sportsplex/2026-06-29-u2mdwc",
		phrase: "Gym, weight room and fitness, 7 am to 4 pm",
		want:   `amenity none "gym"`},
	{entry: "amenityCore:gymnasium", fixture: "st-laurent-complex/2026-06-24-h3tbjz",
		phrase: "The gymnasium is closed",
		want:   `amenity-core: amenity none "gymnasium"`},
	{entry: "amenityCore:hill", fixture: "lansdowne-park/2026-09-29-3n6eyt",
		phrase: "The Great Lawn and the sledding hill are closed until further notice.",
		want:   `amenity-core: amenity none "great lawn sledding hill"`},
	{entry: "amenityCore:pool", fixture: "walter-baker-sports-centre/2026-06-30-mucucz",
		phrase: "The whale pool is closed.",
		want:   `amenity-core: amenity none "whale pool"`},
	{entry: "amenityCore:pools", fixture: "nepean-sportsplex/2026-09-08-qf5txy",
		phrase: "The pools are closed for annual maintenance.",
		want:   `amenity-core: amenity none "pools"`},
	{entry: "amenityCore:ramp", fixture: "st-laurent-complex/2026-08-21-dbt34c",
		phrase: "The pool's wheelchair ramp is currently unavailable. We apologize for the inconvenience.",
		want:   `amenity-core: amenity none "pool's wheelchair ramp"`},
	{entry: "amenityCore:rink", fixture: "nepean-sportsplex/2026-02-13-4raexg",
		phrase: "The curling rink is closed.",
		want:   `amenity-core: amenity none "curling rink"`},
	{entry: "amenityCore:room", fixture: "walter-baker-sports-centre/2026-08-23-jc67sf",
		phrase: "The steam room will be closed until further notice.",
		want:   `amenity-core: amenity none "steam room"`},
	{entry: "amenityCore:rooms", fixture: "nepean-sportsplex/2026-08-25-i7balt",
		phrase: "The Athletics change rooms are closed until further notice.",
		want:   `amenity-core: amenity none "athletics change rooms"`},
	{entry: "amenityCore:sauna", fixture: "ray-friel-recreation-complex/2025-12-16-bnj253",
		phrase: "The sauna is closed for maintenance.",
		want:   `amenity-core: amenity none "sauna"`},
	{entry: "amenityCore:saunas", fixture: "nepean-sportsplex/2026-09-28-xw6lza",
		phrase: "The saunas are closed until further notice.",
		want:   `amenity-core: amenity none "saunas"`},
	{entry: "amenityCore:slide", fixture: "cardelrec-recreation-complex-goulbourn/2025-12-13-ul36t5",
		phrase: "The slide is closed.",
		want:   `amenity-core: amenity none "slide"`},
	{entry: "amenityCore:studio", fixture: "st-laurent-complex/2026-09-11-pjyild",
		phrase: "The dance studio is closed. All group fitness drop-ins are cancelled.",
		want:   `amenity-core: amenity none "dance studio"; group scope-phrase ["Drop-in schedule - group fitness"]`},
	{entry: "amenityCore:track", fixture: "minto-recreation-complex-barrhaven/2026-09-29-3n6eyt",
		phrase: "The walking track is closed from 10 am to 8 pm.",
		want:   `amenity-core: amenity none "walking track"`},
	{entry: "amenityCore:tub", fixture: "walter-baker-sports-centre/2026-04-05-jo2me4",
		phrase: "The hot tub is closed until further notice.",
		want:   `amenity-core: amenity none "hot tub"`},
	{entry: "amenityCore:wall", fixture: "francois-dupuis-recreation-centre/2025-10-24-5hu6st",
		phrase: "The rock wall is closed for annual maintenance until further notice.",
		want:   `amenity-core: amenity none "rock wall"`},
	{entry: "amenityCore:whirlpool", fixture: "walter-baker-sports-centre/2026-01-11-w5zhys",
		phrase: "The whirlpool is temporarily closed.",
		want:   `amenity-core: amenity none "whirlpool"`},
	{entry: "amenityQualifier:athletics", fixture: "nepean-sportsplex/2026-08-20-v4rnus",
		phrase: "The Athletics change rooms are now open.",
		want:   `amenity none "athletics change rooms"`},
	{entry: "amenityQualifier:cardio", fixture: "st-laurent-complex/2026-09-02-wnazvw",
		phrase: "The Weight and Cardio rooms will be partially inaccessible.",
		want:   `amenity none "weight cardio rooms"`},
	{entry: "amenityQualifier:change", fixture: "nepean-sportsplex/2026-08-20-v4rnus",
		phrase: "The Athletics change rooms are now open.",
		want:   `amenity none "athletics change rooms"`},
	{entry: "amenityQualifier:diving", fixture: "nepean-sportsplex/2026-08-07-sytkfb",
		phrase: "The diving boards are open during Public Swim.",
		want:   `amenity none "diving boards"`},
	{entry: "amenityQualifier:lap", fixture: "splash-wave-pool/2025-12-15-x25n3h",
		phrase: "The lap pool heater is out of order, the water will feel colder.",
		want:   `amenity none "lap pool"`},
	{entry: "amenityQualifier:squash", fixture: "bob-macquarrie-recreation-complex-orleans/2026-09-27-r4sgp6",
		phrase: "Squash court 3 is closed until further notice.",
		want:   `unit-of-row: amenity none "squash court"`},
	{entry: "amenityQualifier:weight", fixture: "st-laurent-complex/2026-09-02-wnazvw",
		phrase: "The Weight and Cardio rooms will be partially inaccessible.",
		want:   `amenity none "weight cardio rooms"`},
	{entry: "genericFacility:arena",
		reason: "keeps a word facility names share out of the name-token match: another arena at a facility called an arena is not the facility",
		probe:  facilityProbe("roger sénécal arena", "Brewer Pool and Arena"),
		want:   `not the facility`},
	{entry: "genericFacility:building",
		reason: "keeps a word facility names share out of the name-token match",
		probe:  facilityProbe("school building", "Heron Park Community Building"),
		want:   `not the facility`},
	// the facility, though the text says the arenas stay open (matching.md);
	// the only phrase either entry decides
	{entry: "genericFacility:centre", fixture: "cardelrec-recreation-complex-goulbourn/2026-09-08-7bptcz",
		phrase: "August 31 to September 7: the community centre is closed for annual maintenance. The arenas are open.",
		want:   `facility-generic: facility scope-phrase; amenity none "arenas"`},
	{entry: "genericFacility:community", fixture: "cardelrec-recreation-complex-goulbourn/2026-09-08-7bptcz",
		phrase: "August 31 to September 7: the community centre is closed for annual maintenance. The arenas are open.",
		want:   `facility-generic: facility scope-phrase; amenity none "arenas"`},
	{entry: "genericFacility:complex", fixture: "tony-graham-recreation-complex-kanata/2026-09-25-2gsk5c",
		phrase: "The complex and Client Services remain closed.",
		want:   `facility-list-with-desk: facility scope-phrase`},
	{entry: "genericFacility:dome",
		reason: "keeps a word facility names share out of the name-token match",
		probe:  facilityProbe("dome washrooms", "Belltown Dome"),
		want:   `not the facility`},
	{entry: "genericFacility:facility",
		reason: "the facility wherever it is posted; the facility sentences (facilityRe) read it first, so no fixture reaches the subject resolver with it (TestSubjectIsFacility)",
		probe:  facilityProbe("facility", "Bob MacQuarrie Recreation Complex - Orléans"),
		want:   `facility-generic`},
	{entry: "genericFacility:hall",
		reason: "keeps a word facility names share out of the name-token match",
		probe:  facilityProbe("upper hall", "Carp Memorial Hall"),
		want:   `not the facility`},
	{entry: "genericFacility:park",
		reason: "keeps a word facility names share out of the name-token match: a skate park at a park's community centre is not the facility",
		probe:  facilityProbe("skate park", "Fisher Park Community Centre"),
		want:   `not the facility`},
	{entry: "genericFacility:pool", fixture: "walter-baker-sports-centre/2026-06-27-ih3oon",
		phrase: "the pool is closed for annual maintenance.",
		want:   `part-groups: group scope-phrase ["Drop-in schedule - swim and aquafitness"]`},
	{entry: "genericFacility:recreation",
		reason: "keeps a word facility names share out of the name-token match",
		probe:  facilityProbe("recreation room", "Kars Recreation Hall"),
		want:   `not the facility`},
	{entry: "genericFacility:rink", fixture: "jim-tubman-chevrolet-rink/2026-04-03-qrqtar",
		phrase: "The rink is closed for the season.",
		want:   `facility-generic: facility scope-phrase`},
	{entry: "iceClassVocab:0",
		reason: "\"all skating\" at a facility whose titles and labels do not spell the class; fires on no version yet (matching.md, TestIceClassVocab)",
		probe:  classVocabProbe("skating", "Public skating", "Pick-up hockey 18+"),
		want:   `["Public skating"]`},
	{entry: "iceClassVocab:1",
		reason: "\"all ice sports\" at a facility whose titles and labels do not spell the class; fires on no version yet (matching.md, TestIceClassVocab)",
		probe:  classVocabProbe("ice sports", "Hockey 35+", "Public skating"),
		want:   `["Hockey 35+"]`},
	{entry: "partGenericTokens:court",
		reason: "the part rule (84c17cd): a closed court names the group whose title does not say court (TestGroupsForPart)",
		probe:  partProbe("squash court"),
		want:   `["Drop-in schedule - squash and racquetball"]`},
	{entry: "partGenericTokens:courts",
		reason: "the part rule (84c17cd): \"squash and racquetball courts\" names the group whose title does not say courts (TestGroupsForPart)",
		probe:  partProbe("racquetball courts"),
		want:   `["Drop-in schedule - squash and racquetball"]`},
	{entry: "partGenericTokens:room",
		reason: "the part rule: a bare room names no group (TestGroupsForPart)",
		probe:  partProbe("room"),
		want:   `[]`},
	{entry: "partGenericTokens:rooms",
		reason: "the part rule: \"weight and cardio rooms\" (St. Laurent's spelling) names the weight and cardio room group",
		probe:  partProbe("cardio rooms"),
		want:   `["Drop-in schedule - weight and cardio room"]`},
	{entry: "stemMap:aqua", fixture: "richcraft-recreation-complex-kanata/2026-08-27-cyice5",
		phrase: "Aqua Zumba, cancelled",
		want:   `activity normalized ["Aquafit Zumba®"]`},
	{entry: "stemMap:aquafitness", fixture: "cardelrec-recreation-complex-goulbourn/2026-07-02-vjnexu",
		phrase: "The pool is closed until further notice. All swim and aquafitness drop-ins are cancelled.",
		want:   `part-groups: group scope-phrase ["Drop-in schedule - swim and aquafitness"]; class scope-phrase ["50+ swim *Reservations required." "Aquafit General - Shallow/deep combo *Reservations required." "Lane swim *Reservations required." "Preschool swim" "Preschool swim (shared pool)" "Public swim with slide"]`},
	{entry: "stemMap:skates", fixture: "jack-charron-arena/2026-03-25-y7zqs5",
		phrase: "All skates cancelled.",
		want:   `class scope-phrase ["Family skating" "Public skating" "Skating 50+"]`},
	{entry: "stemMap:skating", fixture: "jim-durrell-recreation-centre/2025-12-11-ryinoz",
		phrase: "Adult 18 + skate, 11 to 11:50 am, cancelled",
		want:   `activity normalized ["Adult 18+ skating"]`},
	{entry: "stemMap:skatings", fixture: "tom-brown-arena/2026-08-28-prxevz",
		phrase: "All drop-in skatings, cancelled",
		want:   `group scope-phrase ["Drop-in schedule - skating"]`},
	{entry: "stemMap:swimming", fixture: "lowertown-community-centre-and-pool/2026-09-08-qf5txy",
		phrase: "Pool closed for annual maintenance August 17 to September 8.",
		want:   `part-groups: group scope-phrase ["Drop-in schedule - swimming"]`},
	{entry: "stopTokens:activities", fixture: "routhier-community-centre/2026-07-02-5fevp5",
		phrase: "All drop in activities are cancelled.",
		want:   `group scope-phrase ["Drop-in schedule - gymnasium sports"]`},
	{entry: "stopTokens:all", fixture: "minto-recreation-complex-barrhaven/2026-01-20-hkjdje",
		phrase: "Pickleball, all drop-ins, cancelled",
		want:   `activity normalized ["Pickleball"]`},
	{entry: "stopTokens:and", fixture: "kanata-leisure-centre-and-wave-pool/2025-10-31-7u7ejv",
		phrase: "The hot tub and sauna are closed.",
		want:   `activity-exact: activity exact ["Hot tub and sauna"]`},
	{entry: "stopTokens:at", fixture: "ben-franklin-place/2026-08-04-33i5k2",
		phrase: "Meridian Theatres @ Centrepointe will remain closed for continued recovery and restoration efforts",
		want:   `other-facility: amenity none "Meridian Theatres at Centrepointe"`},
	{entry: "stopTokens:drop", fixture: "w-erskine-johnston-arena/2025-12-14-r22prp",
		phrase: "All skating and ice sports drop-ins are cancelled.",
		want:   `group scope-phrase ["Drop-in schedule - ice sports" "Drop-in schedule - skating"]`},
	{entry: "stopTokens:in", fixture: "w-erskine-johnston-arena/2026-09-14-z4jnge",
		phrase: "All drop-in skating, cancelled",
		want:   `group scope-phrase ["Drop-in schedule - skating"]`},
	{entry: "stopTokens:ins", fixture: "minto-recreation-complex-barrhaven/2026-09-28-xw6lza",
		phrase: "Pickleball drop-ins:\n8:45 to 9:45 am",
		want:   `activity normalized ["Pickleball"]`},
	{entry: "stopTokens:of", fixture: "hintonburg-community-centre/2026-06-27-kmfxkd",
		phrase: "Thursday, June 25, all sessions of pickleball, cancelled",
		want:   `class scope-phrase ["Pickleball"]`},
	{entry: "stopTokens:programming", fixture: "heron-road-community-centre/2026-01-15-djdw7q",
		phrase: "All gymnasium programming is cancelled.",
		want:   `group scope-phrase ["Drop-in schedule - gymnasium sports"]`},
	{entry: "stopTokens:programs", fixture: "hintonburg-community-centre/2026-09-07-qznjeu",
		phrase: "All drop-in programs are cancelled",
		want:   `group scope-phrase ["Drop-in schedule - gymnasium sports"]`},
	{entry: "stopTokens:schedule", fixture: "sandy-hill-arena/2026-09-10-6ixdci",
		phrase: "All drop-in skating, cancelled",
		want:   `group scope-phrase ["Drop-in schedule - skating"]`},
	{entry: "stopTokens:sessions", fixture: "hintonburg-community-centre/2026-06-27-kmfxkd",
		phrase: "Thursday, June 25, all sessions of pickleball, cancelled",
		want:   `class scope-phrase ["Pickleball"]`},
	{entry: "stopTokens:the", fixture: "st-laurent-complex/2026-09-02-wnazvw",
		phrase: "The Weight and Cardio rooms will be partially inaccessible.",
		want:   `amenity none "weight cardio rooms"`},
	{entry: "stopTokens:times", fixture: "canterbury-recreation-complex/2026-01-29-4urzf3",
		phrase: "Table Tennis, all times, cancelled",
		want:   `activity normalized ["Table tennis"]`},
	{entry: "stopTokens:to",
		reason: "age ranges: labels write \"10 to 14\", and \"10-14\" reads as the same spelling (TestTokens)",
		probe:  matchProbe("Ringette 10-14 years", "Ringette (10 to 14 years)"),
		want:   `normalized ["Ringette (10 to 14 years)"]`},
}

// vocabLists are the word lists an entry of vocabRows belongs to.
var vocabLists = []string{"amenityCore", "amenityQualifier", "genericFacility", "partGenericTokens", "stopTokens", "stemMap", "iceClassVocab"}

// vocabEntries lists every entry of the word lists as list:word.
func vocabEntries() []string {
	var out []string
	for _, list := range vocabLists {
		var words []string
		switch list {
		case "amenityCore":
			words = slices.Collect(maps.Keys(amenityCore))
		case "amenityQualifier":
			words = slices.Collect(maps.Keys(amenityQualifier))
		case "genericFacility":
			words = slices.Collect(maps.Keys(genericFacility))
		case "partGenericTokens":
			words = slices.Collect(maps.Keys(partGenericTokens))
		case "stopTokens":
			words = slices.Collect(maps.Keys(stopTokens))
		case "stemMap":
			words = slices.Collect(maps.Keys(stemMap))
		case "iceClassVocab":
			for i := range iceClassVocab {
				words = append(words, strconv.Itoa(i))
			}
		}
		for _, w := range words {
			out = append(out, list+":"+w)
		}
	}
	slices.Sort(out)
	return out
}

// ablate removes one entry and returns the function that restores it.
func ablate(t *testing.T, entry string) func() {
	t.Helper()
	list, word, _ := strings.Cut(entry, ":")
	var m map[string]bool
	switch list {
	case "amenityCore":
		m = amenityCore
	case "amenityQualifier":
		m = amenityQualifier
	case "genericFacility":
		m = genericFacility
	case "partGenericTokens":
		m = partGenericTokens
	case "stopTokens":
		m = stopTokens
	case "stemMap":
		v, ok := stemMap[word]
		if !ok {
			t.Fatalf("%s: no such entry", entry)
		}
		delete(stemMap, word)
		return func() { stemMap[word] = v }
	case "iceClassVocab":
		i, err := strconv.Atoi(word)
		if err != nil || i < 0 || i >= len(iceClassVocab) {
			t.Fatalf("%s: no such entry", entry)
		}
		saved := iceClassVocab
		iceClassVocab = append(slices.Clone(saved[:i]), saved[i+1:]...)
		return func() { iceClassVocab = saved }
	default:
		t.Fatalf("%s: unknown list", entry)
	}
	if !m[word] {
		t.Fatalf("%s: no such entry", entry)
	}
	delete(m, word)
	return func() { m[word] = true }
}

// TestVocabularyCovered requires a row for every entry of every list, and
// an entry for every row: a word added without its phrase fails here.
func TestVocabularyCovered(t *testing.T) {
	rows := map[string]int{}
	for _, r := range vocabRows {
		rows[r.entry]++
	}
	entries := vocabEntries()
	for _, e := range entries {
		switch rows[e] {
		case 0:
			t.Errorf("%s has no row in vocabRows: name the phrase that needs it, or the reading it exists for", e)
		case 1:
		default:
			t.Errorf("%s has %d rows", e, rows[e])
		}
	}
	for e := range rows {
		if !slices.Contains(entries, e) {
			t.Errorf("row for %s, which is in no list", e)
		}
	}
}

// TestVocabularyMotivation resolves each row's phrase (or probe) with the
// entry in, which must give want, and with the entry removed, which must
// change the phrase's objects in the fixture or what it resolves to.
func TestVocabularyMotivation(t *testing.T) {
	for _, r := range vocabRows {
		t.Run(r.entry, func(t *testing.T) {
			var resolve func() (objects, resolved string)
			switch {
			case r.probe != nil && r.fixture == "":
				resolve = func() (string, string) { return "", r.probe() }
			case r.probe == nil && r.fixture != "" && r.phrase != "":
				resolve = func() (string, string) { return fixturePhrase(t, r.fixture, r.phrase) }
			default:
				t.Fatalf("a row names a fixture and a phrase, or a reason and a probe")
			}
			if r.fixture == "" && r.reason == "" {
				t.Fatalf("a probe row states the reading the entry exists for")
			}
			objs, got := resolve()
			if got != r.want {
				t.Errorf("resolves to %q, want %q", got, r.want)
			}
			restore := ablate(t, r.entry)
			defer restore()
			objs2, got2 := resolve()
			if objs == objs2 && got == got2 {
				t.Errorf("removing %s changes nothing: the entry is dead or the phrase resolves another way", r.entry)
			}
		})
	}
}

var vocabFixtures sync.Map // fixture name -> ottrecidx.DataRef

func loadFixture(t *testing.T, name string) ottrecidx.DataRef {
	t.Helper()
	if d, ok := vocabFixtures.Load(name); ok {
		return d.(ottrecidx.DataRef)
	}
	buf, err := os.ReadFile(filepath.Join("testdata/corpus", name+".pb"))
	if err != nil {
		t.Fatal(err)
	}
	idx, err := new(ottrecidx.Indexer).Load(buf)
	if err != nil {
		t.Fatal(err)
	}
	vocabFixtures.Store(name, idx.Data())
	return idx.Data()
}

// fixturePhrase enriches the fixture and returns the objects whose raw text
// is the phrase, rendered with their placement, and what the phrase
// resolves to posted alone where the fixture posted it (phraseResolution).
func fixturePhrase(t *testing.T, fixture, phrase string) (objects, resolved string) {
	t.Helper()
	data := loadFixture(t, fixture)
	out := EnrichVersion("", data)
	place := map[string][]string{}
	for _, f := range out.GetFacilities() {
		for _, id := range f.GetObjects() {
			place[id] = append(place[id], "facility")
		}
		for _, g := range f.GetGroups() {
			for _, id := range g.GetObjects() {
				place[id] = append(place[id], "group "+g.GetLabel())
			}
			for _, a := range g.GetActivities() {
				for _, id := range a.GetObjects() {
					place[id] = append(place[id], "activity "+a.GetLabel())
				}
				for _, ss := range a.GetSessions() {
					for _, id := range append(ss.GetObjects(), ss.GetAdded()...) {
						place[id] = append(place[id], "session "+a.GetLabel())
					}
				}
			}
		}
	}
	var objs []string
	var posted *epb.Object
	for _, o := range out.GetObjects() {
		if o.GetRawText() != phrase {
			continue
		}
		if posted == nil || posted.GetKind() != epb.Object_NOTICE && o.GetKind() == epb.Object_NOTICE {
			posted = o
		}
		at := strings.Join(place[o.GetId()], ", ")
		o.SetId("")
		o.SetSeq(0)
		b, _ := protojson.Marshal(o)
		objs = append(objs, at+" "+string(b))
	}
	if posted == nil {
		t.Fatalf("no object with raw text %q in %s", phrase, fixture)
	}
	return strings.Join(objs, "\n"), phraseResolution(t, data, posted)
}

// phraseResolution posts the object's text (its reading, when the walk
// composed one) as a block of its own where the object was posted, at the
// fixture's facility as EnrichVersion sets it up, and renders what it
// resolves to: the closure subject's reason (subject.go), then each
// notice's scope level, match quality, amenity, and activities or groups.
func phraseResolution(t *testing.T, data ottrecidx.DataRef, o *epb.Object) string {
	t.Helper()
	var names []string
	var fc *facCtx
	for fac := range data.Facilities() {
		names = append(names, fac.GetName())
		if !fac.GetSourceDate().IsZero() {
			// the fixture's facility; the others are there by name alone
			fc = &facCtx{fac: fac, anchor: fac.GetSourceDate()}
		}
	}
	if fc == nil {
		t.Fatal("no facility with a source date")
	}
	fc.others = otherFacilities(names)
	fc.out = &builder{Stats: map[string]int{}}
	var grp *groupMatcher
	for g := range fc.fac.ScheduleGroups() {
		m := newGroupMatcher(g)
		fc.matchers = append(fc.matchers, m)
		if m.label == o.GetSourceGroup() {
			grp = m
		}
	}
	var source string
	for _, s := range []string{"special_hours", "notifications", "schedule_changes"} {
		if sourceToProto(s) == o.GetSource() {
			source = s
		}
	}
	if source == "schedule_changes" && grp == nil {
		t.Fatalf("posted under %q, which the fixture does not have", o.GetSourceGroup())
	}
	if source != "schedule_changes" {
		grp = nil
	}
	text := o.GetRawText()
	if o.GetReading() != "" {
		text = o.GetReading()
	}
	fc.processBlock("<p>"+html.EscapeString(text)+"</p>", source, grp)
	var reasons, notices []string
	for _, k := range slices.Sorted(maps.Keys(fc.out.Stats)) {
		if reason, ok := strings.CutPrefix(k, "subject/closure/"); ok {
			reasons = append(reasons, reason+":")
		}
	}
	for _, r := range fc.recs {
		if r.kind != "notice" {
			continue
		}
		sc := r.n.Scope
		s := sc.Level + " " + sc.MatchQuality
		if sc.Amenity != "" {
			s += fmt.Sprintf(" %q", sc.Amenity)
		}
		if len(sc.Activities) > 0 {
			s += fmt.Sprintf(" %q", sc.Activities)
		} else if len(sc.Groups) > 0 {
			s += fmt.Sprintf(" %q", sc.Groups)
		}
		notices = append(notices, s)
	}
	return strings.TrimSpace(strings.Join(reasons, " ") + " " + strings.Join(notices, "; "))
}

// facilityProbe resolves a subject at a facility of the given name.
func facilityProbe(subject, facility string) func() string {
	return func() string {
		if ok, reason := subjectIsFacility(subject, facility); ok {
			return reason
		}
		return "not the facility"
	}
}

// partProbe resolves a closed part against the groups of a complex.
func partProbe(part string) func() string {
	return func() string {
		ms := []*groupMatcher{
			testGroup("Drop-in schedule - swim"),
			testGroup("Drop-in schedule - squash and racquetball"),
			testGroup("Drop-in schedule - weight and cardio room"),
			testGroup("Drop-in schedule - group fitness"),
		}
		return fmt.Sprintf("%q", groupsForPart(ms, part))
	}
}

// matchProbe matches a subject against the labels.
func matchProbe(subject string, labels ...string) func() string {
	return func() string {
		r := testMatcher(labels...).match(subject)
		return fmt.Sprintf("%s %q", r.Quality, actNames(r.Acts))
	}
}

// classVocabProbe resolves a class through iceClassVocab against the labels.
func classVocabProbe(class string, labels ...string) func() string {
	return func() string {
		return fmt.Sprintf("%q", actNames(testMatcher(labels...).matchClassVocab(tokenSet(class))))
	}
}
