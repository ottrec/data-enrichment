# Hacking on the enrichment parser

Continuation notes for `enrich/` + `cmd/enrich`. Read
[implementation.md](implementation.md) first for the pipeline overview and
corpus numbers; this file is the code map, the invariants, and the workflow.

## Code map

- `schema/enrichment.proto` — the public output schema (flat objects +
  Facility > Group > Activity > Session id-reference tree; see
  implementation.md "Output shape"). Regenerate with `go generate ./schema`
  (buf + go tool protoc-gen-go, opaque API, same setup as the scraper).
- `record.go` — the internal intermediate representation (`notice`/`scope`,
  internal DateSpan/TimeAssoc/Effects) the item parser produces before
  placement converts it to protobuf. Effects booleans are only ever set from
  literal trigger words; scope.Activities carries raw activity labels (the
  canonical dataset join key).
- `text.go` — `normText` (display text; keeps `\n` from `<br>`, strips
  zero-width/nbsp), `foldText` (lowercase, punctuation folded; kills colons,
  so clock/date parsing must run on normText and only keyword/token work on
  folded), `tokens`/`tokenSet` (stemmed + stopworded). The `stemMap`
  (skating→skate, aqua→aquafit, ...) and `stopTokens` (drop/ins/all/
  programming/...) feed activity matching, class segments, and
  `subjectIsFacility` alike; edit with care. `skatings` is in the stemMap
  because Tom Brown Arena writes it (see matching.md).
- `html.go` — `splitBlock` → `blockPart` (heading/para/list) and nested
  `liNode`s. `nodeText` concatenates text nodes with no separator (the city
  splits bold mid-word: `<strong>T</strong>hursday`), `<br>` → `\n`. A
  liNode's Head excludes nested lists; Links collected per node (see-schedule
  URLs).
- `date.go` — `parseLeadingDate(s, anchor) (dateSpec, rest, ok)`. dateSpec
  carries exactly one form: enumerated Dates, From/To range, Weekdays set,
  or OpenEnded; `restIsTrivial` decides "the text was only a date"
  (tolerates a trailing parenthetical). Garbled text (date-like leftovers
  right after a parse, e.g. "July 6 to 10 Friday, July 10") returns ok=false
  with `date-garbled` in Ambig — callers keep the raw head and mark items.
  Year resolution: `resolveDate` (single; written weekday must agree, a
  weekday-matched candidate >300d out loses to a near one, marked) and
  `resolveRange` (joint: each candidate year places both endpoints, scored
  by weekday agreements, ties to anchor proximity — one typo'd endpoint
  can't drag the range a year off). Words are punctuation-trimmed and
  empties skipped ("Wednesday , November 26"); `commaAfter` looks at the
  source token, so comma-enumerated days ("October 1, 2, 3, 4, 16, and 26")
  extend the list while `clockAhead`/`clockAfterAnd` stop it at an hour
  ("October 12, 7 am to 4 pm", "October 12, 7 and 8 pm"); a comma before
  the clock word keeps the day ("January 3 and 4, noon to 4 pm"). "Until
  <date>" heads set only To (From zero; enrichidx `applies` and `dated`
  handle To-only spans). `parseWeekdaySet` takes "-" as well as "to" for a
  range, backing off a dangling dash ("Saturday and Sunday - 10 am to 5
  pm") so the set still parses. `findEmbeddedDate` finds a date written
  into a sentence ("The pool is closed from Monday, March 23 to Sunday,
  April 12.", "closed between November 3, 2025 and February 1, 2026",
  "closed until December 1", "starting May 1 until September 2026", which
  takes the month's last day with `date-month-only`): only expressions led
  by a weekday/month name, optionally introduced by a from/until/between
  word (which decides the side of the span), never one after a reopening
  cue (reopen/return/resume), and "July 3 - 10 am" is a date and a clock.
  It returns the sentence with the date and its preposition removed.
- `clock.go` — `findClockRanges(s) ([]clockMention, remainder)`. A match
  needs a meridiem/noon/midnight/colon on at least one side ("December 13
  and 14" is not a clock). Missing meridiems produce candidates: >12h
  readings dropped when a shorter exists, sorted shortest-first, `Inferred`
  set. A range that was a clause of its own leaves one comma behind
  ("Aquafit, 8:05 to 9 am, cancelled" → "Aquafit, cancelled"), so the
  clauses around it stay separate; that is what lets "Public swim, 1 to 3
  pm, 25m pool only" reach the restriction clause. The clause loop drops a
  bare conjunction left over ("Lane swim, 12:30 to 1 pm, and 8 to 9 pm").
- `match.go` — `groupMatcher` (one per schedule group; actEntry per
  normalized activity name with folded spellings + token sets from label and
  name). `match`: exact folded string → equal token sets → subset either
  direction; 2+ candidates always come back as `multiple`, never picked.
  `matchActivity` (item.go) wraps `matchActivityWhole` with
  `matchActivityParts`, which splits a subject naming several activities
  (`subjectParts`); matching.md has the three rules that keep it off a single
  activity whose label contains "and".
  `coversGroup` (group title tokens ⊆ class segment tokens) and
  `matchClass` (segment tokens ⊆ activity tokens) drive "all X" phrases;
  `classSegments` splits on commas and " and ". `iceClassVocab` +
  `matchClassVocab` are the last-resort hard-coded taxonomy for "all skating"
  and "all ice sports" (see matching.md); they run only after everything else
  has failed and always mark `class-matched-by-vocabulary`. `skateSiblings` +
  `owns` widen a matched skate activity to its group's other skate activities,
  for a notice whose clock window reaches past the row it names; `owns` is what
  makes the facility-level copy widen the same way its group-scoped twin does,
  so the two still collapse.
- `item.go` — `processItem` is the heart; **the order of checks is load-
  bearing**: boilerplate → item's own leading date (beats head context) →
  see-schedule → facilityRe (whole-facility sentences; sets
  st.closureContext; also "closing at X" and the late opening "will open
  at X", which `findSingleEnded` reads as closed until X) → "closed for the
  season" → "Regular/Pre/Post season, <range>". Before all of that, a bare
  cross-reference with a link ("See Outdoor Pools for more information.",
  "Details: Outdoor pools") is ignored/supplementary; "See X schedule" is
  still a SeeSchedule notice. Then the embedded date: a headless sentence
  (or one led by a weekday set, which merges) takes it as its own date; a
  head date context is never overridden ("...and return to regular hours
  Friday, June 12" under "Thursday, June 11" is about the 11th)
  → findClockRanges → subjectClosedRe ("X is closed", skipped for "all "
  prefixes; subject resolved facility-name → all-programs → part-of-a-row →
  activity → amenity → none; a generic facility word that names only some of
  the facility's groups, "the pool" at a complex, is a part and not the
  facility when the item cancels) →
  comma-clause loop (keyword / schedule change / `hoursClauseRe` ("Modified
  hours", "facility hours" ⇒ ModifiedHours) /
  trailing "only" restriction, or a `noteClauseRe` note ("moved to 25m
  warm pool", "reduced capacity", "no instructor") which is a Restriction
  too / phrase parts) → trailing keyword glued
  without comma → allDropinsRe → allClassRe → empty-phrase branch (bare
  effects, date+clock hours items, date-only items) → activity match →
  amenity → freeform. Bare date+clock items: closureContext ⇒ Closure; in a
  schedule_changes block always `possible-activity-time` (never dismissed as
  facility hours); in special_hours/notifications ⇒ ModifiedHours unless the
  range exactly equals an activity slot on those dates or is <4h.
  Also here: `resolveClass` (empty segments = "all drop-in activities" ⇒
  whole scope; both of its `class-unmatched` exits try `matchClassVocab`
  first), `gatherSlots` (fixed-date times via `SingleDate` ymd
  equality; weekday times filtered by the spec's weekdays and by schedule
  effective ranges as negative-only evidence; ranges enumerate ≤45 days,
  longer ⇒ all weekdays), `clockRelation` (exact > within > covers >
  overlaps), `maybeDisambiguate` (a `multiple` match narrowed only when
  exactly one candidate has an exact slot), `emitTimesWithSlots` (one
  notice per clock mention; picks the best-relating meridiem candidate).
- `enrich.go` — version loop, per-fragment `rec` collection (`blockCtx.add`
  assigns block seq + id and every heading/date-context/boilerplate fragment
  becomes an ignored object), walkState lifetimes (head reset by headings;
  closureContext reset by headings and after each list), the `<li>` shapes
  (leaf with `<br>` lines; date head + children; date head carrying more
  than the date ("Sunday, August 23, 5 to 6 pm", "Monday, July 27 to
  Friday, July 31, between 9 am and 4 pm") + children: the head is an item
  and the children still get its date, which is what keeps a child
  cancellation from going undated; garbled head — children
  processed with the marked spec; inverted form: statement head whose
  children are all dates, ranges emitted separately, and a date+clock child
  ("PD Day Public Swim" over "Friday, October 2, 8:30 to 10 am") emitted on
  its own as "<head>, <clock>" under that date; time analogue
  (`allClocks`): statement head whose children are all bare clock ranges
  ("Pickleball cancelled:" over "11:45 am to 12:45 pm", ...) is re-read as
  "<head sans colon>, <child>" per child, so the usual clause code applies; otherwise head emitted
  with `head-unparsed` and children processed, unless `allSupplementary` says
  the children are only cross-references and the head is therefore complete),
  `collapse` (special_hours
  notices matching a schedule_changes notice on dates+effects+scope key
  become ignored/duplicate stubs; survivors get Sources), and `place`
  (converts recs to Objects and builds the reference tree; sessions filled
  from rec.sessions, `added` vs `objects` split by Effects.Added).
- `cmd/enrich` — `-versions n` (0=all), `-o` stdout/dir/stats-only,
  `-format json|pb|golden`; stats to stderr, aggregated over versions.
  `internal/dataver` is the shared version-cache iterator (same as the dump
  tools); `EachPB` yields the raw protobuf, which `cmd/mkcorpus` uses.
- `internal/golden` — `Render` (the golden rendering: objects by block, only
  the fields that are set, one `at:` line per placement, sessions per
  activity; ids, seq, offsets, raw HTML and block hashes left out) and
  `Diff` (the changed region with the block and object headers above it).
  Shared by the golden test, the corpus properties and `-format golden`.
  `Fixtures` and `Load` read and enrich the corpus for both test binaries.
  `Consumer` renders enrichidx's answers for one fixture: its published
  sessions enumerated through ottrecidx over a window of a week before to
  six weeks after the anchor (weekday times inside their schedule's
  effective range, fixed-date times on their date), with per-day facility
  and group `Warning` runs, per-session `Session` and facility and group
  `ScopeCancelled` / `ScopeCancelledStated`, and `Added`; only non-empty
  answers are written. It records the answers, not /today's use of them.
- `enrich/golden_test.go` + `enrich/properties_test.go` — `TestGolden` (one
  parallel subtest per fixture, `-run 'Golden/minto-.*'` works) and
  `TestCorpusProperties` (the summary golden); `cmd/mkcorpus` writes the
  fixtures, oldest version per distinct facility snapshot, trimmed of
  description, address, coordinates, errors and links; `-blocks-only` drops
  the schedule structure from the key. See Workflow.
- `enrichidx/golden_test.go` — `TestConsumerGolden`, the consumer golden:
  `golden.Consumer` per fixture against `enrichidx/testdata/consumer`, a
  file only for fixtures with an answer (2,294 of 2,511). The one golden a
  trust-rule change in enrichidx shows up in.
- `enrichidx/` — the consumer API and trust rules: `Join` indexes an output
  by facility, group and session; `Session`, `ScopeCancelled` /
  `ScopeCancelledStated`, `Added`, `Warning` and `Items` are what the website
  asks. `markerPolicy` rates every marker stated, likely or warn and
  `objectTrust` takes the weakest of an object's rows (see Marker
  vocabulary); `monthOnlyEnd` is the one date-conditional marker rule beside
  the table.
- `report/` + `cmd/report` — the HTML debugging report (source blocks with
  highlighted extraction ranges beside their objects, hover-paired). The
  fastest way to eyeball parser behavior on a version.

## Invariants (the no-false-positive contract)

1. An Effects boolean is set only when its trigger word is in the raw text.
2. Never pick among multiple candidates; the one exception is
   `maybeDisambiguate`, which is deterministic (unique exact slot) and
   leaves `activity-time-disambiguated`.
3. Parse failures degrade to ambiguity markers with raw text kept, or to
   `Unparsed`; nothing is silently dropped except recognized boilerplate.
4. Schedule date ranges are negative-only evidence (they exclude, never
   include) — same as the CLAUDE.md dataset gotcha.
5. Amenity scope never claims activities. "X is closed and all programs
   cancelled" posted under a group scopes to that group with the amenity
   noted. Posted for the whole facility, "all programs" means the part's
   programs: a class named in the cancellation resolves as a class, otherwise
   the part claims the groups whose title names it (`groupsForPart`, pool read
   as swim), otherwise nothing (`closed-part-unmatched`). A bare amenity
   closure (Roger Sénécal Arena) cancels nothing. A subject naming
   part of what a row runs on is an amenity for the same reason:
   `subjectNamesUnitOfActivity` narrows "Squash court 3" back off the six-court
   row it matched.
6. Dates: no year is guessed against a written weekday without a marker;
   garbled heads produce no dates at all.
7. The consumer's side: a marker costs what its `markerPolicy` row says,
   and a marker with no row is warn, so a marker from a newer parser can
   never strike or add.

## Marker vocabulary

`enrich.Markers()` is the registry of every marker the parser can emit.
`TestMarkersRegistered` checks it against the package's `amb*` constants,
enrichidx's `TestMarkerPolicyCoversParser` against `markerPolicy`, and the
corpus summary lists the registered markers the golden corpus never shows
(`silent-marker`) and fails on one it shows that is not registered. A new
marker therefore needs a row, which is where its cost gets decided.

What each costs, from `markerPolicy` (the weakest row among an object's
markers applies):

- stated: strikes (`Cancelled`, `ScopeCancelledStated`), adds, trims a
  session's time.
- likely: one tier down. A session cancellation answers `LikelyCancelled`,
  a stated scope cancellation answers only `ScopeCancelled`, an add is
  `Uncertain`, no trimmed time.
- warn: none of those; the object reaches the consumer only through
  `Warning` and `Items`.

| marker | means | costs |
| --- | --- | --- |
| `meridiem-inferred` | missing am/pm, the only reading or the one a slot confirms | stated |
| `meridiem-ambiguous` | several readings fit, no slot decides | warn |
| `date-month-only` | an end given as a month, taken as its last day | stated; `monthOnlyEnd` keeps a scope cancellation from striking inside that month |
| `date-outside-schedule` | no schedule listing the activity covers the date | stated (never reaches a strike; an add is expected there) |
| `date-garbled` | a range repaired from its ends, both weekdays agreeing | likely |
| `weekday-mismatch` | the written weekday fits no year: a typo'd weekday or a stale year | likely |
| `date-year-unconfirmed` | no weekday to confirm a date far from the anchor | likely |
| `date-unparsed`, `date-year-ambiguous`, `date-range-invalid`, `date-only-item` | no usable date | warn |
| `activity-time-disambiguated` | one of several candidates, the only one with the exact slot (invariant 2) | stated |
| `class-title-partial` | the class names part of the title of the group it was posted under | stated |
| `activity-narrowed-to-amenity` | narrowed off a row to the courts it names | stated |
| `dog-swim-session` | a classification | stated |
| `activity-typo-match` | one edit from a label | likely |
| `matched-other-group` | posted under another group | likely |
| `class-matched-by-vocabulary` | a class from the ice taxonomy, never spelled on the page | likely |
| `skating-widened-to-window` | skate siblings the notice does not name | likely |
| `head-unparsed` | the item's list head was not understood | likely |
| `activity-unmatched`, `activity-multiple-candidates`, `class-unmatched`, `closed-part-unmatched`, `no-subject` | no single subject | warn |
| `no-slot-overlap` | a cancellation whose time meets no slot | warn |
| `added-time-already-scheduled` | an added time the schedule already has | warn |
| `hours-context-unknown`, `possible-activity-time`, `freeform-item` | not an effect on a session | warn |

`class-title-partial` and `activity-time-disambiguated` are stated because
every corpus instance reads correctly and both are deterministic; rated
likely they would move 356 more session-days off the strike (c.md's replay).
`weekday-mismatch` stays likely because it cannot tell a typo from a stale
year. The warn rows other than `meridiem-ambiguous` and
`added-time-already-scheduled` never reach a strike or an add; they record
what the marker means.

## Workflow

The oracle is the golden corpus: `enrich/testdata/corpus` holds one trimmed
single-facility dataset protobuf per distinct (facility, blocks, schedule
structure) over the cache's history (2,511 fixtures, written by
`cmd/mkcorpus`), each with a `.golden` rendering of its objects and their
placement beside it, and `corpus-summary.golden` with the counts and
property lists over all of them. `enrichidx/testdata/consumer` holds what
enrichidx answers for each fixture's sessions (see `golden.Consumer`), so a
trust-rule change is a golden diff too. `go test ./...` runs the lot in
about a second.

```sh
go test ./...                                     # goldens, properties, unit tests
go test ./enrich ./enrichidx -run 'Golden|CorpusProp' -update # rewrite after a reviewed change
git diff --stat enrich/testdata enrichidx/testdata # which fixtures moved
git diff enrich/testdata/corpus-summary.golden    # what the counts and lists say
go run ./cmd/mkcorpus                             # after the cache grows, then -update
```

A change is done when its golden diff has been read in full: for a
refactor the diff is empty; for a behaviour change every changed object,
and every changed consumer answer, is classified in the commit message.
`-update` is a flag of the enrich and enrichidx test binaries only, so it
goes with `./enrich ./enrichidx`, not `./...`. The summary's lists
(undated-effect, no-effect, stray-date, far-date, head-unparsed-trigger,
unparsed, session-outside-schedule) are where a gap shows before anyone
goes looking; its hard assertion is that every effect kind fires
somewhere. The goldens depend on ottrecidx (effective date ranges, slots),
so they follow the website module the build resolves: the workspace's
under go.work, the go.mod pin otherwise; keep the pin current enough that
both agree.

The full corpus is the pre-merge check for a behaviour change, since the
fixtures do not cover anchor-only variants (same blocks and schedule, a
different source date):

```sh
go build -o ~/src/ottrec/tmp/enrich-scratch/bin/enrich-x ./cmd/enrich
# ~2 min; run under a cap (a runaway loop here once OOM'd the box)
systemd-run --user --scope -p MemoryMax=8G env GOMEMLIMIT=6GiB \
    ~/src/ottrec/tmp/enrich-scratch/bin/enrich-x -versions 0 -o out-x -format golden 2> stats-x.txt
diff -r out-before out-x                          # the same rendering the goldens use
go run ./cmd/check-coverage -versions 0           # total accounting, 0 uncovered
```

`-format golden` writes the golden rendering per version, so `diff -r`
between two runs shows exactly what a golden diff shows, placement
included; `diff` the stats files too. `cmd/check-coverage` verifies that
every word of every source block appears in some object's raw text for
that block. To inspect a marker class, write JSON per version (`-o dir`)
and sample with a few lines of python (glob the JSONs, collect notices by
marker, print facility/dateText/rawText/scope). `cmd/dump-context` shows raw
blocks with their schedule/activity/time context when you need to see what
the parser saw; `cmd/report` renders one version as HTML.

## Things that bit us already

- `parseWeekdaySet`'s range expansion once looped forever ("Monday to
  Friday") and OOM'd the machine — anything iterating weekday/date math
  deserves a bounds check and a capped corpus run.
- `foldText` removes colons; never fold before clock parsing.
- The effect keyword can carry a reason after it ("cancelled due to annual
  maintenance", "closed for maintenance"), and `keywordRe`/`trailingKwRe` are
  end-anchored, so without `kwReason` the effect is lost entirely and the item
  resolves scope with no effect at all. Stripping the reason also takes the
  closure word off the phrase, which is why the `allClassRe` amenity branch
  accepts a bare amenity when `Effects.Closure` is already set: without that,
  "All changerooms closed for maintenance" stopped being an amenity closure and
  became 37 class-unmatched items. Both halves were caught by the corpus diff.
- Item dates override head dates by design ("December 27, 8:30 am to 9 pm"
  under a "Winter Break" range head).
- The same block HTML can resolve differently under a different anchor year
  or schedule; don't cache on block hash alone (see matching.md).
- Stats keys are ad-hoc, not API.

## Next steps

The implementation queue from the structural review (approaches.md
"Structural review"). One entry per commit series, in this order, each
behind the oracle: `go test ./...`, the golden diff read in full, `go vet`,
and for behaviour changes the full-corpus rendering diff against the
previous entry plus `check-coverage -versions 0`. Notes updated in the same
commit. "corpus" is the 497-version full run; "golden" the fixture corpus.
Parser logic runs on Fable, the rest on Opus.

| # | entry | contains | expected effect | website |
| --- | --- | --- | --- | --- |
| 0 | notes | this queue and the decisions in approaches.md | none | |
| 1 | oracle bookkeeping | website go.mod pin bumped so the goldens pass with `GOWORK=off`; golden renderer out of the test file; `cmd/enrich -format golden`; the corpus diff becomes `diff -r` of that rendering, placement included; Workflow below rewritten around `go test` | empty | |
| 2 | O4 consumer golden | per fixture, sessions enumerated over the schedules' effective ranges in a window around the anchor (via ottrecidx, not a copy of /today); `ScopeCancelled`, `ScopeCancelledStated`, `Session`, `Added`, `Warning` written for non-empty answers into `enrichidx/testdata` | empty | |
| 3 | C1 | `enrich.Markers()` registry; `time-change-unparsed` removed; `markerPolicy` table, `LikelyCancelled`, `AddedSession.Uncertain`; O3 property "every marker occurs or is listed"; `class-title-partial` and `activity-time-disambiguated` stated, `weekday-mismatch` likely | 0 objects; O4: ~140 session-days struck to likely, 16 adds uncertain, 2 dropped | bump; `today.go` strikes on `LikelyCancelled`, a chip for `Uncertain` |
| 4 | F3c + F3d | coverage check in `collapse` (a special_hours notice survives when no survivor covers its groups); effect kinds folded in the key | 248 objects, 96 versions; 133 cancellations survive, 320 session refs return; `session-outside-schedule` 37 to 50 until entry 6 | |
| 5 | E4 | "until further notice" under a single head date is a From; the flag lifted to item level | 246 objects, 120 versions, 8 texts; 47 warning raises, 0 strike changes | |
| 6 | E3a + E3b + F10 + placement provenance | `slotInfo` carries its schedule; `explode` clips per date; no fixed cap (bounded at 366 days); `explode` and `gatherSlots` honour `Weekdays`; a session hangs only on the label whose schedule produced the slot | 3 objects (Kanata) plus tree: -66 refs, +18; `session-outside-schedule` to 0 | |
| 7 | E1b + E1c | a weekday-agreed resolution more than 183 days from the anchor is marked; a range containing the anchor wins | 0 objects; anchor-shift tests | |
| 8 | E2a + "until <Month>" | `date-end-unstated` marker, row likely; `findEmbeddedDate` takes a bare month as a month-only To | 251 objects gain the marker; "closed until October" count read at landing | bump; chip falls back to `DateText` for `date-end-unstated` and `date-month-only` |
| 9 | A1 compat | `sentence{src, spans}`, span finders, `remainder()` with the old comma rules written down; wrapper tests rewritten to spans | empty, stats byte-identical | |
| 10 | B2 + F11 | `reading`, `flatten`, `resolve`, `completeHead`; To-only and weekday children complete a head as ranges do; four invented fixtures; `li/*` stats kept | 7 objects, 1 version (Minto mixed list) | |
| 11 | F2 + colon | `RawText` verbatim, `reading` field (`Object.reading = 26`); the completion strips the trailing colon; `RawHTML` no longer repeats the child; `dedupeKeys` keys on the reading | 121 objects, 27 versions, `raw_text` only | bump; `pre-line` on the activity change text |
| 12 | A1 natural + A2-lite + F12 | commas are boundaries, preposition absorbed; typed clause list; the bare "only" restriction rule (text as written); `rewrite_contract_test.go`; `clockRangeRe` takes "and" only after "between"; the walk hands `processItem` the reading | 372 objects, 256 versions, 12 texts, all modifications; 130 heritage hours objects become `modifiedHours` | |
| 13 | per-segment finders | no pattern crosses a claimed span; the two-sided span with a clock on each end (Canterbury) | 1 fixture | |
| 14 | D2 + `allProgramsRe` + `facility-except-programs` | `resolveClosureSubject` with kind and reason and `subject/closure/<reason>` stats; part, grammar and other-facility variants; "programs are cancelled" without "all"; mkcorpus keeps every facility's name; the marker (row likely) for a facility closure with an "except" clause | refactor 0; then 1,701 objects in 487 versions (1,508 placement); no strike changes | bump |
| 15 | D3 | motivation table with a `want` column, 58 rows; 85 dead entries deleted | empty | |

Deferred, in the order they would be taken up: the A2 dispatcher with E6's
span type and B1-strict (after entry 14, decided on what the A1 natural
review shows); E2b end kind on the DateSpan; F1b content ids (when a
consumer keeps ids across versions); F3b guarded collapse (if double
listing is wanted gone); C2 (if a second consumer appears); the
`weekday-mismatch` split into typo and stale year (when drift appears in
O3's far-date list). The LLM residue pass (approach C in approaches.md)
stays on hold.

Done since the first corpus run (verified against the full corpus, not
fixtures): single-ended times ("closed until noon" OpenStart, "will end at
6 pm" OpenEnd + TimeChange), per-sentence parsing of multi-sentence items
(sibling unparsed records suppressed), guarded edit-distance-1 typo matching
(`activity-typo-match`), the unit-of-a-row narrowing below, bare date+clock items resolved as facility hours
only outside schedule_changes blocks and only when not slot-exact/short
(`possible-activity-time`), cross-group fallbacks for wrong-group postings
(`matched-other-group`, `class-title-partial`), garbled-range repair with
double weekday validation, and inverted-form items with mixed range/single
date children. The driving check: `dump-residue` output should contain no
items mentioning cancellation.

Also done: the `skatings` stem and the `iceClassVocab` fallback (matching.md).
Corpus effect, 444 versions and 131,631 objects: exactly 18 objects changed,
all Tom Brown's `All drop-in skatings, cancelled` losing `class-unmatched` and
gaining their slot; `amb/class-unmatched` 76 → 58, those 18 moving to
`scope/group`. The vocabulary fallback itself fired **zero** times, so it is
an untested guard, and `claude-qc`'s `city-ice-class-vocabulary` is what will
say when the city's vocabulary drifts past it.

The skate widening (matching.md) is separate and does change output: 12 objects
over the corpus, in 2 cases, each marked `skating-widened-to-window`. It is
gated on a clock being present and on the widening actually changing the slot
set — `touchedBy` replays what `emitTimesWithSlots` would report, because the
unguarded version marked 2,549 objects to change 12.

The part-of-facility scoping (`groupsForPart`, `namesPartOfFacility`,
`closed-part-unmatched`) is the newest, and it fixes the same kind of
over-application one level up. A facility-level cancellation naming a part
(`Squash and racquetball courts are closed and all drop-ins cancelled.`,
`The pool is closed and all programs cancelled.`) resolved to the whole
facility, and since /today's stated tier strikes those, every session at Bob
MacQuarrie showed cancelled for the squash closure, September 14 to October 5.
Three routes led there and all three are closed:

- the `subjectClosedRe` cancelled case widened a facility-level item to the
  facility; it now takes the named class, else the part's groups, else nothing
- `subjectIsFacility` accepts `pool` (and `arena`, `rink`) as the facility,
  right for Deborah Anne Kirwan Pool and wrong for a complex; a cancelling item
  whose generic subject names a proper subset of the groups is now a part,
  group-posted items included (Bob MacQuarrie's July 1 swim-group copy was
  facility-wide)
- `resolveClass`'s facility path recorded no groups for activities it matched
  by class, so placement found no node and hung the object on the facility,
  where a scope phrase reads as the whole facility (Richcraft's
  `All swim drop-ins are cancelled.`, September 8 to 27); it now records them,
  and a facility-level class matching nothing is `NONE` rather than a scope
  phrase

Corpus effect, 485 versions: **279 objects changed and nothing was added or
removed**; the only fields that moved are placement, `time`, `amenity` and
`matchQuality`, and the only stats that moved are `scope/facility` 27,853 →
27,646, `scope/group` 29,979 → 30,111, `scope/class` 1,074 → 1,149.
check-coverage still reports 0 uncovered blocks over 63,212. Replaying /today's
marks over every version (`claude-qc/scratch/strikediff`), 541 sessions lose a
strike and none gains one or changes tier; every one is a session in a group
the notice did not name, except two `Sauna` rows in Richcraft's swim group
that `all swim and aquafitness drop-ins` does not name either. The new marker
fires nowhere on the corpus: Plant's `The pool and gymnasium are closed ...`,
the one list of parts, claims the swim group and skips the gymnasium, which
has no drop-in group there.

The group-title match is conservative by construction: every significant word
of the part must be in one title (`therapeutic pool`, `arena` match nothing),
and pool→swim is the one synonym, the only one the corpus needed. A
facility-level `The arena is closed and all programs cancelled.` at a complex
would now degrade to a notice rather than strike the skating.

The unit-of-a-row narrowing (`subjectNamesUnitOfActivity`,
`activity-narrowed-to-amenity`) came before, and it fixes a real
over-application rather than adding a marker. The city closes some of the
courts a single drop-in row runs on, the matcher lands on the row, and the
notice closed the whole row: Bob MacQuarrie's open-ended
`Squash court 3 is closed until further notice.` carried **93 slots** against
`Squash courts 1, 2, 3, 5, 7 and 9` on the newest version, so /today struck
every squash session for as long as the city left the notice up.

Corpus effect, 445 versions: **42 objects changed, in 42 versions, and nothing
else moved.** `scope/activity` 14,335 → 14,293 and `scope/amenity` 2,855 →
2,897, the same 42 either way; no object was added or removed; the only fields
that changed anywhere are the four this rule touches (`ambiguities`,
`amenity`, `matchQuality`, `time`). check-coverage still reports 0 uncovered
blocks over 58,405.

The 42 are one defect at two facilities, 21 versions each:

| facility | notice | row | slots dropped |
| --- | --- | --- | --- |
| Bob MacQuarrie | `Squash court 3 is closed until further notice.` | `Squash courts 1, 2, 3, 5, 7 and 9` | 15, then 93 once the fall table landed |
| Walter Baker | `Squash courts 3 and 4 are closed until further notice.` | `Squash Courts 2, 3, 4, and 5` | 7 |

**Do not gate this on the typo flag.** The two reach the row by different
routes: MacQuarrie through the edit-distance pairing of court with courts,
Walter Baker through the plain token-subset match, which carries no marker at
all. A first cut keyed on `activity-typo-match` and would have fixed half of
it. The test in `match_test.go` pins both shapes for that reason.

The negative cases matter as much: Nepean Sportsplex publishes `Squash court 3`
as a row of its own beside `Squash - courts 1, 2, and 4`, and closing that one
really does close the drop-in. The rule needs the subject's numbers to be a
**non-empty proper subset** of the row's, and the subject to be `isAmenity`, so
a subject naming every unit the row runs on, no units at all, or an age range
(`hockey 50+`) all still scope to the activity.
