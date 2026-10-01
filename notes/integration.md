# Consuming the enrichment (website integration)

How a consumer (the /today page) should read the output. The schema itself
is `schema/enrichment.proto`; read its comments first, they are the
contract. This file adds the usage rules that don't fit in field comments.

## Where the data comes from

`cmd/enrich` produces one `Output` per dataset version (protojson or binary
pb). **Not yet decided**: where generation runs and where the file is
published (candidates: alongside the dataset at data.ottrec.ca, or generated
by/for the website directly). The website side should consume the pb via the
generated Go package (`github.com/ottrec/data-enrichment/schema`).

## Joining to the dataset

All join keys are the raw dataset identifiers, never normalized forms:

- `Facility.name` = ottrec.v1 `Facility.name`
- `Group.label` = `ScheduleGroup.label`
- `Activity.label` = `Schedule.Activity.label` (raw; `novel: true` means the
  activity is not in the published schedule and the label is the notice's
  raw subject phrase)
- `Session.date` is a full YYYYMMDDW `schema.Date`; `start`/`end` are
  minutes from midnight. For cancels/closures/changes they equal the
  affected published slot's `TimeRange._start`/`_end` (so a session maps to
  a concrete `todaySession`); for `added` refs they are the added time
  itself.

## Trust rules (the point of the whole design)

1. **Tree position is the guarantee.** An object referenced from a session
   is safe to apply to exactly that session; from an activity, to all of
   that activity's sessions on the object's dates; from a group/facility,
   to that whole scope. Nothing needs re-validation downstream.
2. **Only `NOTICE` objects drive behavior.** `UNPARSED` should be surfaced
   as raw text at its posted level (facility/group note); `IGNORED` can be
   skipped entirely (headings, date-context, boilerplate, service-desk,
   collapsed duplicates) unless reconstructing full blocks.
3. **Unknown-proofing**: an `Effect` with an unset oneof, or an
   unrecognized enum value (`kind`, `source`, `match_quality`, `relation`),
   means the consumer is older than the data. Fall back to showing the
   object's `raw_text`; never drop it and never guess.
4. **`ambiguities` weaken, never strengthen.** enrichidx reads them through
   `markerPolicy` (hacking.md "Marker vocabulary"): a marker rated likely
   turns a strike into `SessionNotices.LikelyCancelled` or the implied
   `ScopeCancelled` tier and an add into `AddedSession.Uncertain`; one rated
   warn, or one with no row (a newer parser), blocks strikes and adds and
   leaves only the warning. A consumer of enrichidx gets this for free; one
   reading the raw output should treat any unrecognized marker as reduced
   confidence. Markers worth branching on when reading the output directly:
   - `possible-activity-time`: a bare date+time that may be an orphaned
     activity change; show as a note, don't treat as facility hours.
   - `no-slot-overlap`: a cancel whose time matches no published slot
     (usually an unpublished holiday-schedule time); show at activity level.
   - `activity-multiple-candidates` (match_quality MULTIPLE): group-level
     object with `candidates`; show as a group note, never pick one.
   - `date-garbled`, `weekday-mismatch`, `meridiem-inferred/-ambiguous`:
     date/time caveats; the resolved values are still the best reading.
5. **Duplicates are pre-collapsed**: render the surviving notice once
   (`sources` says it appeared in both the group changes and the facility
   special hours); `ignored/duplicate` stubs link to survivors via
   `duplicate_of`. A special-hours notice naming more groups than its
   group copies ("All drop-in skating and ice sports, cancelled" against a
   skating-only copy) is not collapsed, so the same cancellation can be
   listed twice.

## Suggested /today mapping

Today /today shows a coarse per-group `Changes` warning flag. With the
enrichment:

- session-level `objects` with a `cancelled`/`closure` effect: style the
  matching feed session as cancelled (this is the high-confidence tier:
  slot-validated, date-resolved). Through enrichidx that is
  `SessionNotices.Cancelled`; `LikelyCancelled` is the same notice resting on
  a marked inference (a repaired range, a mismatched weekday, a wrong-group
  posting) and gets the "may be affected" tier, not the strike. /today folds
  it into `EnrichedScopeCancelled`.
- group/facility-level whole-scope cancellations split into two tiers.
  `enrichidx.ScopeCancelledStated` is the half whose text **states** the
  cancellation ("The facility is closed and all programs cancelled.", "All
  drop-in skating, cancelled"): the scope is still an inference but the
  cancellation is the city's own word, so /today strikes those sessions. The
  rest, a closure the scope only implies, keeps the softer per-session "may be
  affected" warning via `ScopeCancelled`, one tier below the strike, because
  the scope phrase was matched against the group title rather than each
  activity.

  **Most of it arrives at group level, so consult both tiers.** The city posts
  "The facility is closed and all programs cancelled." into each group's
  schedule_changes, so that is where the enrichment usually places it, and the
  facility-level copy often says only "closed" and stays soft. Simulated over
  the sessions actually in effect on each day:

  | day | struck, facility tier | struck, group tier | left soft | sessions |
  | --- | --- | --- | --- | --- |
  | 2026-09-07 Labour Day | 0 | 103 | 1 | 241 |
  | 2026-04-03 Good Friday | 0 | 166 | 25 | 395 |
  | 2026-07-01 Canada Day | 47 | 115 | 21 | 363 |
  | 2026-11-11 ordinary Wednesday | 0 | 0 | 0 | 377 |

  A consumer that consults only the facility tier gets nothing on two of those
  three holidays. The Canada Day facility-tier figure predates the
  part-of-facility scoping (hacking.md): most of it was Bob MacQuarrie's and
  Plant's `The pool is closed and all programs cancelled.` striking every
  group at the facility. Re-measured with the in-effect count in
  `claude-qc/scratch/scopecancel`, which runs slightly below this table's,
  Canada Day moves from 60 facility-tier and 125 group-tier strikes to 0 and
  153, and the other three days do not move. The ordinary Wednesday is the negative control: the tier adds
  no strikes on a day with no notices.
  The query requires a dated (or open-ended) notice with a scope-phrase or
  absent subject, and skips closure-only notices with a residual subject
  ("The pool is closed for maintenance" at a multi-group complex says
  nothing about the other groups' programming); everything skipped still
  reports through the Warning tier.
- session-level `added` refs: inject a new feed session (activity label from
  the tree; `novel` activities have no dataset row). `AddedSession.Uncertain`
  marks an add resting on a marked inference; /today says "may have been
  added" and the activity pages' chip reads "added?".
- activity-level objects: a note line on all of that activity's sessions on
  the object's dates (`dates` may be open-ended or weekday-restricted).
- group/facility-level objects with `closure`+dates: a banner on the
  facility's sessions those days; `see_schedule` effects link the referenced
  schedule; `modified_hours`/`seasonal_hours` are facility-hours info,
  ignorable for the schedule itself.
- unparsed + none-scope notices: keep the existing raw-HTML modal as the
  fallback surface; `block_hash` + `seq` reconstruct block reading order,
  and `html_start/html_end` locate fragments in the source block.

Display text: `raw_text` is always present and normalized the same way the
scraper normalizes (`normalizeText`); `dates`/`time` are already resolved,
so avoid re-parsing.

## Verification workflow (before/after integration changes)

- `go run ./notes/scripts [version-spec]` (enrichcheck): replays the /today
  consumer (enrichidx warnings, see-schedule, session cancel/time-change/
  added/scope-cancelled joins) against one version, anchored at that
  version's date; dumps the objects behind every warning downgrade. Try a holiday-week version
  (e.g. `2026-06-29`, `2025-12-29`) to exercise the see-schedule and
  cancel/added paths.
- `go run ./cmd/report -o report.html`: visual QA for one version (source
  blocks beside objects, hover-paired).
- `go run ./cmd/check-coverage`: total-accounting invariant over all
  versions.
- `go run ./cmd/dump-residue > notes/residue.txt`: what the parser still
  can't resolve; diff against the checked-in snapshot.
- Aggregate stats (`cmd/enrich -versions 0 -o ""`) are the regression
  signal; see hacking.md.

## API changes and website bumps

The website consumes enrichidx in-process, so an API change lands there
with a go.mod bump:

- `SessionNotices.LikelyCancelled` and `AddedSession.Uncertain` (the marker
  policy table). `Cancelled` and `ScopeCancelledStated` became stated-only;
  without the website change the likely-rated sessions lose the strike and
  show only the group's changes warning. `today.go` adds `LikelyCancelled`
  to `EnrichedScopeCancelled` and carries `Uncertain` into the added note and
  the "added?" chip.
- `Item.EndInexact`: the resolved end is a month taken as its last day
  (`date-month-only`) or a start with no end found (`date-end-unstated`).
  The activity pages' date chip falls back to `DateText`, so "until
  September 2026" and "from August 22 to spring 2028" show as posted rather
  than as "to Wed, Sep 30" and "from Sat, Aug 22".
- `Item.Text` is the source text as posted, as its doc always said, and
  `Item.Reading` the sentence the parser read when it differs (from the new
  `Object.reading`; the website does not use it). A list head completed by
  a clock child was the composed sentence and is now the two lines joined
  by a newline, so the activity pages' copy changes for those postings:
  Walter Baker's "PD Day Public Swim - training and whale pools only" over
  each of its three dates, and Minto's Pickleball drop-ins, cancelled and
  added times. `.activity-change-text` has `white-space: pre-line` so the
  two lines show as posted. Arrives with the bump after this change; the
  CSS rule does not depend on it.
- The subject resolver (entry 14 of the structural review; no API or
  schema change, the bump alone). A closure-only "the pool is closed" at a
  complex is placed at the swim group instead of the facility, so /today's
  changes warning narrows to that group at 15 facilities (Canterbury,
  Walter Baker, Kanata Leisure, CARDELREC, François Dupuis, Richcraft,
  Minto, Bob MacQuarrie, Pinecrest, Brewer, Ray Friel, Plant, Jack
  Purcell, Lowertown, St. Laurent) and the activity pages stop listing a
  pool closure under skating or the gym. Ben Franklin Place's Meridian
  Theatres notices and St. Laurent's wheelchair ramp become amenity
  closures (WarnNotice, the notices list) instead of unmatched changes
  warnings; Tony Graham's "The complex and Client Services remain closed."
  is a facility closure. Pinecrest's "The pool is closed and programs are
  cancelled until further notice." strikes the swim group's sessions
  again. `facility-except-programs` (likely) marks a facility closure
  with an exception, at Cumberland and Fairfields today, which publish no
  drop-ins.
- A place named for an activity (no API or schema change, the bump alone).
  Bob MacQuarrie's "Elizabeth Manley Figure Skating Arena is closed for
  annual maintenance." is an amenity closure (WarnNotice, the notices list)
  instead of a closure of Figure skating, so the skating group's changes
  warning for those dates goes and the skating pages stop listing it. No
  session's answer changes.

## Open decisions

- Generation/publishing pipeline (see above).
- Per-version files vs a rolling latest (only latest is needed by /today;
  the timemachine could use history).
- The LLM residue pass (approach C in approaches.md) is on hold pending
  option exploration; the residue is ~90 unique items over the whole
  history, nearly all museum/season prose.
