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
4. **`ambiguities` weaken, never strengthen.** Any unrecognized marker =
   reduced confidence. Markers worth branching on today:
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
   `duplicate_of`.

## Suggested /today mapping

Today /today shows a coarse per-group `Changes` warning flag. With the
enrichment:

- session-level `objects` with a `cancelled`/`closure` effect: style the
  matching feed session as cancelled (this is the high-confidence tier:
  slot-validated, date-resolved).
- session-level `added` refs: inject a new feed session (activity label from
  the tree; `novel` activities have no dataset row).
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
  added joins) against one version, anchored at that version's date; dumps
  the objects behind every warning downgrade. Try a holiday-week version
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

## Open decisions

- Generation/publishing pipeline (see above).
- Per-version files vs a rolling latest (only latest is needed by /today;
  the timemachine could use history).
- The LLM residue pass (approach C in approaches.md) is on hold pending
  option exploration; the residue is ~90 unique items over the whole
  history, nearly all museum/season prose.
