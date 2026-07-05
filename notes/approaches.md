# Candidate approaches

Constraints: no false positives (mark ambiguity instead), reuse the
`ottrecidx` helpers (data access, date/time semantics), keep the scraper
untouched, output a derived data file per dataset version. The website
integration (/today) is separate and out of scope here.

Shared by every option below: a **deterministic validation layer** in Go on
top of `ottrecidx`. Whatever produces candidate records, a record is only
emitted as confident if:

- the claimed activity exists in the claimed group (or the scope rule is a
  recognized whole-group/class phrase),
- the resolved date's weekday agrees with the written weekday, and the date
  falls in/near the schedule's effective range (`ComputeEffectiveDateRange`),
- a claimed time range overlaps actual slots for cancel/close/change, or is
  novel for "added",
- an effect flag is backed by its trigger word in the raw text.

Anything failing a check degrades to ambiguous/unresolved with the raw text
kept. This layer is what actually guarantees the no-false-positives
property, independent of the extraction method.

## A. Pure deterministic parser (Go)

Structural parse (x/net/html): normalize text, split blocks into sections by
heading, top-level items into (date head, leaf items), then small grammars
for date heads, clock ranges, tail keywords, scope phrases, plus the token
matcher for activities.

Measured coverage if built exactly as prototyped: the canonical `<ul>` shape
is 704/706 unique CHANGES blocks and ~55% of SPECIAL; date heads parse for
~95% of items; tail keywords classify ~85% of leaf items; activity
resolution (exact + safe fuzzy + scope rules) lands ~90%+. The residue is
freeform prose ("The pool is closed until noon.", "Public swim will end at
6 pm.") which stays unresolved-with-raw-text, which is acceptable
(/today can still show it as a facility/group note on the resolved dates).

- Pros: reproducible, testable against 10.5 months of history (golden
  corpus already dumped), zero runtime deps, runs in the daily pipeline,
  conservative by construction, one language (Go) with ottrecidx used
  directly.
- Cons: a rules codebase that grows with each new city phrasing; freeform
  prose never gets parsed; typo/variant matching needs care to stay safe;
  someone has to notice when a new shape appears (mitigate: emit stats and
  a diff of unparsed items per run).

## B. LLM extraction with deterministic validation

Go program dumps per-block context JSON (block HTML, group activities +
slots, schedule ranges, source date) using ottrecidx; an extraction step
(any language, or Go calling the API) prompts a model to emit records in the
output schema; the Go validator then accepts/degrades each record. Cache by
content+context hash: only ~3-5 new blocks/day, so cost and latency are
negligible after backfilling ~1,650 blocks once.

- Pros: handles the freeform residue, typos, reworded activity names, and
  future phrasing changes without new code; one mechanism for all three
  fields; the validator bounds the false-positive risk.
- Cons: nondeterministic (rerun can differ; cache mitigates but backfills
  or context changes reopen it); the validator cannot catch everything (a
  fabricated-but-plausible date on a dateless notice passes the weekday
  check ~1/7 of the time; an invented "cancelled" flag passes only if the
  word appears, so effects are safe, dates less so); external dependency
  and secrets in the pipeline; harder to test; failure mode is silent
  quality drift rather than a visible parse error.
- Note: for dates specifically, the LLM should only be allowed to *select*
  from deterministically pre-parsed candidates, not produce its own; same
  for activity ids (choose from the group's list or "none"). Constraining
  the output to references into supplied context removes most hallucination
  surface.

## C. Hybrid: deterministic core, LLM only for the residue (recommended)

Run A; whatever it marks unresolved/ambiguous (measured: roughly 10-15% of
items, nearly all freeform prose) optionally goes through B with the
reference-only prompting, and through the same validators. The enrichment
file records which path produced each record (`parser` vs `llm+validated`),
so consumers can choose a trust floor. With the LLM step disabled the
pipeline still produces the regular-shape majority; nothing breaks, coverage
just drops.

- Pros: precision of A where the data is regular (the vast majority),
  coverage of B where it is not, tiny LLM bill, graceful degradation,
  the golden-corpus tests only need to pin the deterministic part.
- Cons: two mechanisms to maintain; the residue classifier (what gets sent
  to the LLM) is one more piece; still needs the B caveats for the records
  it does produce.

## D. Deterministic + curated overrides

A with a small reviewed overrides file (keyed by block hash) for the
freeform tail; a report per run lists new unparsed blocks (median 3/day) for
occasional manual triage.

- Pros: zero false positives attainable; no runtime AI dependency; the
  review burden is provably small.
- Cons: ongoing manual chore, coverage lags until reviewed, overrides rot
  when blocks change. Works better as an optional layer on top of A or C
  (a corrections file the validator consults first) than as the plan.

## Recommendation

Build A now as the core (Go, module `data-enrichment`, consuming
`ottrecidx` directly and the version cache like the dump tools). Design the
record format and validators so B can be slotted in behind the same
interface, and add it (as C) only if the freeform residue turns out to
matter for /today. Ship D's reporting (per-run stats + new-unparsed-blocks
diff) regardless, since it is nearly free and is the early-warning system
for phrasing drift.

## Output sketch

One file per dataset version (JSON now; protobuf later if the website wants
it), records keyed to facility/group by name+label as in the dataset. Raw
text always kept; flags only ever true when validated; every inference that
fell short of certain gets a marker instead of a guess.

```jsonc
{
  "version": "OSCMZ...",            // dataset version ID
  "generated": "2026-07-05T...",
  "notices": [
    {
      "facility": "Sandy Hill Arena",
      "group": "Drop-in schedule - skating", // absent for SPECIAL/NOTIF
      "source": "schedule_changes",  // special_hours | notifications
      "blockHash": "sha256:...",     // cache/dedup key
      "rawHTML": "<li>...</li>",     // the item's own fragment
      "rawText": "All drop-in skating, cancelled",
      "dateText": "Friday, July 3",
      "dates": { "from": "2026-07-03", "to": "2026-07-03", "openEnded": false },
      "scope": {
        "level": "group",            // slot|activity|class|group|facility|amenity|none
        "activities": ["public skate", "family skate", "adult skate 18+"],
        "matchQuality": "scope-phrase" // exact|fuzzy|multiple|scope-phrase|none
      },
      "time": null,                  // {"start":..., "end":..., "slots":[...], "relation":"exact|within|spans|novel"}
      "effects": { "cancelled": true, "added": false, "timeChange": false,
                   "closure": false, "movedTo": null, "seeSchedule": null,
                   "modifiedHours": null },
      "ambiguities": [],             // e.g. ["activity-multiple-candidates",
                                     //  "year-unconfirmed", "meridiem-inferred",
                                     //  "date-unparsed", "duplicate-of-group-changes"]
      "producedBy": "parser"         // parser | llm
    }
  ],
  "unparsed": [ /* blocks/items nothing could be extracted from, raw */ ],
  "stats": { /* per-run counters for drift monitoring */ }
}
```

Open questions:

- Whether /today wants per-version files or a single rolling file for the
  latest version only (history is nice for the timemachine, but /today only
  needs current).
- Stable keys: facility name + group label match how ottrecidx consumers
  look things up today, but renames break history joins; consider also
  carrying source URL.
- Whether "modified hours"/"regular season" facility-hours items (SPECIAL
  class 2/3) belong in the same record stream or a separate `hours` section;
  they attach to a facility+date but not to activities, and /today may want
  them rendered differently.
