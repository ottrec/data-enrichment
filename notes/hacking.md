# Hacking on the enrichment parser

Continuation notes for `enrich/` + `cmd/enrich`. Read
[implementation.md](implementation.md) first for the pipeline overview and
corpus numbers; this file is the code map, the invariants, and the workflow.

## Code map

- `record.go` — output JSON types (`Output` > `Notice`/`Unparsed`). Effects
  booleans are only ever set from literal trigger words. `TimeAssoc`
  Text/StartMin/EndMin are omitted for whole-activity notices where only the
  affected slots are known.
- `text.go` — `normText` (display text; keeps `\n` from `<br>`, strips
  zero-width/nbsp), `foldText` (lowercase, punctuation folded; kills colons,
  so clock/date parsing must run on normText and only keyword/token work on
  folded), `tokens`/`tokenSet` (stemmed + stopworded). The `stemMap`
  (skating→skate, aqua→aquafit, ...) and `stopTokens` (drop/ins/all/
  programming/...) feed activity matching, class segments, and
  `subjectIsFacility` alike; edit with care.
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
  empties skipped ("Wednesday , November 26").
- `clock.go` — `findClockRanges(s) ([]clockMention, remainder)`. A match
  needs a meridiem/noon/midnight/colon on at least one side ("December 13
  and 14" is not a clock). Missing meridiems produce candidates: >12h
  readings dropped when a shorter exists, sorted shortest-first, `Inferred`
  set. Note the remainder loses commas adjacent to removed ranges
  ("Aquafit, 8:05 to 9 am, cancelled" → "Aquafit cancelled"); the clause
  code downstream tolerates that.
- `match.go` — `groupMatcher` (one per schedule group; actEntry per
  normalized activity name with folded spellings + token sets from label and
  name). `match`: exact folded string → equal token sets → subset either
  direction; 2+ candidates always come back as `multiple`, never picked.
  `coversGroup` (group title tokens ⊆ class segment tokens) and
  `matchClass` (segment tokens ⊆ activity tokens) drive "all X" phrases;
  `classSegments` splits on commas and " and ".
- `item.go` — `processItem` is the heart; **the order of checks is load-
  bearing**: boilerplate → item's own leading date (beats head context) →
  see-schedule → facilityRe (whole-facility sentences; sets
  st.closureContext) → "closed for the season" → "Regular season, <range>"
  → findClockRanges → subjectClosedRe ("X is closed", skipped for "all "
  prefixes; subject resolved facility-name → activity → amenity → none) →
  comma-clause loop (keyword / moved to / changed to / schedule change /
  trailing "only" restriction / phrase parts) → trailing keyword glued
  without comma → allDropinsRe → allClassRe → empty-phrase branch (bare
  effects, date+clock hours items, date-only items; closureContext and an
  "hours" section heading decide Closure vs ModifiedHours vs
  `hours-context-unknown`) → activity match → amenity → freeform.
  Also here: `resolveClass` (empty segments = "all drop-in activities" ⇒
  whole scope), `gatherSlots` (fixed-date times via `SingleDate` ymd
  equality; weekday times filtered by the spec's weekdays and by schedule
  effective ranges as negative-only evidence; ranges enumerate ≤45 days,
  longer ⇒ all weekdays), `clockRelation` (exact > within > covers >
  overlaps), `maybeDisambiguate` (a `multiple` match narrowed only when
  exactly one candidate has an exact slot), `emitTimesWithSlots` (one
  notice per clock mention; picks the best-relating meridiem candidate).
- `enrich.go` — version loop, walkState lifetimes (head reset by headings;
  closureContext reset by headings and after each list), the four `<li>`
  shapes (leaf with `<br>` lines; date head + children; garbled head —
  children processed with the marked spec; inverted form: statement head
  whose children are all dates, a range child only accepted alone;
  otherwise head emitted with `head-unparsed` and children processed), and
  `dedupe` (per facility: special_hours notices matching a
  schedule_changes notice on dates+effects+scope key get
  DuplicateOfGroups; group/class/facility levels share one "broad" key so
  the city's merged phrasing matches the per-group copies).
- `cmd/enrich` — `-versions n` (0=all), `-o` stdout/dir/stats-only; stats
  to stderr, aggregated over versions. `internal/dataver` is the shared
  version-cache iterator (same as the dump tools).

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
   cancelled" upgrades to broad scope with the amenity noted; a bare
   amenity closure (Roger Sénécal Arena) cancels nothing.
6. Dates: no year is guessed against a written weekday without a marker;
   garbled heads produce no dates at all.

## Workflow

```sh
go test ./enrich/                    # grammar unit tests (corpus nasties)
go run ./cmd/enrich | less           # latest version, eyeball JSON
# full corpus: ~2 min; run under a cap (a runaway loop here once OOM'd the box)
go build -o /tmp/enrich-bin ./cmd/enrich
systemd-run --user --scope -p MemoryMax=8G env GOMEMLIMIT=6GiB \
    /tmp/enrich-bin -versions 0 -o "" 2> stats.txt
```

The aggregate stats are the regression signal: diff them between runs.
Watch `unparsed/*`, `scope/none`, and the `amb/*` counts; a new city
phrasing shows up as a bump there. To inspect a marker class, write per-
version files (`-o dir`) and sample with a few lines of python (glob the
JSONs, collect notices by marker, print facility/dateText/rawText/scope) —
that loop found every bug so far. `cmd/dump-context` shows raw blocks with
their schedule/activity/time context when you need to see what the parser
saw.

## Things that bit us already

- `parseWeekdaySet`'s range expansion once looped forever ("Monday to
  Friday") and OOM'd the machine — anything iterating weekday/date math
  deserves a bounds check and a capped corpus run.
- `foldText` removes colons; never fold before clock parsing.
- Item dates override head dates by design ("December 27, 8:30 am to 9 pm"
  under a "Winter Break" range head).
- The same block HTML can resolve differently under a different anchor year
  or schedule; don't cache on block hash alone (see matching.md).
- Stats keys are ad-hoc, not API.

## Next steps (rough order)

1. Decide delivery: where the JSON goes (published next to the dataset vs
   consumed directly by the website) and what key the website joins on
   (facility name + group label today; consider source URL).
2. Golden tests: embed a handful of raw block HTML fixtures (Terry Fox,
   Glen Cairn, Eva James inverted form, Fred Barrett) and assert full
   Notice output, so refactors are safe without the corpus.
3. Single-ended times ("closed until noon", "will end at 6 pm") as
   half-open TimeAssocs.
4. Sentence splitting so multi-sentence items don't lose the second
   sentence's structure.
5. Guarded edit-distance-1 activity matching ("Baddminton").
6. The LLM residue pass (approach C in approaches.md) over
   unparsed-freeform + activity-unmatched, behind the same validators.
