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

## Structural review (2026-09-30)

Five fixes landed on 2026-09-29 and 30 (c531ea9, 7132ea1, 647596b, 58dacdd,
501115d), all of the same kinds: a layout the city uses fell through a ladder
of special cases, or a string rewrite earlier in the pipeline hid something
from a rule later in it, and no test could see either. A structural review
followed: a regression oracle first, then six areas explored in parallel in
throwaway worktrees, each measured against the full corpus, then one
synthesis. The memos live outside the repo; this section is the record of
what was decided and why. The queue is in hacking.md "Next steps".

Two facts frame the decisions. All five incidents were gaps (a layout never
handled, a rule that never fired), not regressions, so a change detector
cannot prevent one; what the oracle changes is that a fix is verified in a
second and a gap's symptoms (an effect word with no effect, a cancellation
with no date, a kind that never occurs) are listed where a reviewer reads
them. And of the five, only 501115d ever produced a strike: the Sawmill
undated cancellation and the prose-dated closures were placed with no
session refs and no dates, so they warned everywhere rather than struck. The
trust model is about 84c17cd and 501115d.

### Weaknesses

- W1 no regression oracle: a two-minute full run and a person reading the
  diff; the restriction rule was dead for a year.
- W2 string surgery on the working sentence: every finder rewrites
  `working`, so later regexes see a synthetic sentence (the lost commas, the
  dangling preposition, the colon).
- W3 shape ladders: eight `<li>` shapes and a load-bearing check order; a
  layout not in the ladder loses its head's context (three of five
  incidents).
- W4 uncertainty invisible to the consumer: enrichidx honoured two markers
  and let the other inference markers strike as if certain.
- W5 hand-grown vocabularies: a word per incident, no test that the
  motivating phrase still resolves, nothing derived from the dataset.
- W6 date semantics spread out: year anchoring, the 45 day cap, To-only and
  From-only spans, OpenEnded meaning two things, month-only ends.
- W7 output stability: ids renumber on any emission change, `RawText` is
  sometimes synthesized, `collapse` keys on the parse result.
- W8 facility, part, amenity, activity decided by regexes in sequence; the
  wrong answer strikes sessions (84c17cd).

### Decisions

One row per area; the deciding number is the full-corpus effect over 497
versions unless it says golden (the 2,511-fixture corpus).

| area | accepted | rejected | deferred | deciding numbers |
| --- | --- | --- | --- | --- |
| oracle (W1) | O1 golden fixtures, one trimmed facility protobuf per distinct (facility, blocks, schedule structure); O3 corpus properties with one hard assertion; O4 consumer golden | O2 digest per version | | 2,511 fixtures, 212 KB packed, 1 s; K2 catches six object variants K1 misses; O3 lists every incident's symptom at its parent commit; O4 is the only artefact that sees a trust-rule change |
| A sentence (W2) | A1 spans over the original, first as a compat refactor, then with natural comma rules plus an A2-lite typed clause list; A3's contract tests as the guard | A3 as a design (restates the bug as a property) | A2 dispatcher, after A1 natural | compat 0 objects, stats byte-identical; natural 372 objects in 256 versions, 12 texts, every one a modification |
| B walk (W3) | B2 flatten first: (context chain, leaf) pairs, then parse; `completeHead` for the inverted forms | B1 (same rule, chain not data); B3 grammar over the tree (keeps the ladder) | B1-strict marker semantics, with A2 | 7 objects, 1 version (Minto mixed list); every `li/*` stat reproduced; both invented layouts go from lost to resolved |
| C trust (W4) | C1 marker policy table in enrichidx, `LikelyCancelled` and `Uncertain`, a registry test that every marker has a row | C1a no API change; C2 confidence on the object; C3 structured claims | | 0 parser objects; ~140 session-days struck to likely, 16 adds uncertain, 2 dropped, none gains a strike; C3 trades 132 right strikes for 33 wrong on 84c17cd |
| D subject (W5, W8) | D1c other facilities' names as amenities; D2 one resolver with kind and reason, part and grammar variants; D3 motivation table and dead entries deleted; a `facility-except-programs` marker | D1a amenity words from labels (282 golden worse); D1b generic words from names; D2 multi, unify-clause, actfirst, atstrip; a museum rule | | D2 as a whole 1,701 objects in 487 versions, 1,508 placement only; `activity-unmatched` 271 to 78; 85 dead entries |
| E dates (W6) | E1b far marker at 183 days; E1c range containing the anchor wins; E2a `date-end-unstated`; E3a per-date clip; E3b no fixed cap; E4 "until further notice" under a head date is a From; E5 latent fixes | E1a history anchor (0 objects, needs history); E1d 120 days (108 right resolutions marked); E3c keep the cap | E2b end kind on the DateSpan; E6 one span type | E4 246 objects, 0 strike changes; E3b 3 objects (Kanata); E1b and E1c 0 objects today |
| F output (W7) | F2 verbatim `RawText` plus a `reading` field; F3c collapse keeps a notice its survivors do not cover; F3d effect kinds in the key | F3a collapse on text (2,754 objects, 647596b worse) | F1 content ids (nothing keys on ids); F3b guarded | F2 121 objects, `raw_text` only; F3c 238 objects, 133 cancellations survive and 320 session refs return |

Open questions settled by the user: `class-title-partial` and
`activity-time-disambiguated` are stated in the C1 table (every corpus
instance reads correctly, both deterministic); no museum rule, the
`facility-except-programs` marker instead; E2b deferred; the `reading` field
is added; the bare "only" restriction stays as written; heritage-site hours
become `modifiedHours` and the tier is a website question.

### Incidents under the accepted set

"Prevented" means the half of the incident in that area would not have
existed or would have been listed by the oracle before anyone looked. The
grammar halves are "same fix needed" everywhere: no design produces missing
grammar.

| commit | what it was | under the accepted set | what would catch it now |
| --- | --- | --- | --- |
| c531ea9 | comma-enumerated days (grammar); statement head over clock children (walk); the colon | grammar same fix; walk prevented, measured (Minto under B2 matches master); colon prevented by F2 plus the colon strip | O3 lists the undated gymnasium cancellation, "Pickleball cancelled:" as a head with a trigger, the enumerated days as a stray date |
| 7132ea1 | statement head over date+clock children (walk); `entrance` (vocabulary) | walk prevented, measured; `entrance` same fix, now with a D3 row | O3 no-effect lists the PD Day swim; the entrance shows only as an `activity-unmatched` count |
| 647596b | date head carrying more than a date (walk); "Until" heads, cross-references, hours clauses, seasons, dash ranges (grammar); studio, ramp, dance, wheelchair (vocabulary) | walk prevented, measured; grammar same fix; D2 grammar resolves the ramp without the two dead entries | O3 lists the undated Sawmill cancellation, the "Until" heads as stray dates, the cross-references as unparsed; D3 fails on a dead entry the day it is added |
| 58dacdd | prose-embedded dates (grammar); the restriction rule dead for a year (W2); dangling preposition, bare conjunction (W2) | grammar same fix; restriction prevented by A1 natural and by O3's hard assertion, which fails at every commit before it; preposition and conjunction prevented by A1 natural | the hard assertion "effect restriction occurs nowhere"; the clause contract fails 9 of 11 on the old finder |
| 501115d | month-only end struck sessions in the end month (trust rule) | prevented in the weak sense: C1's registry test fails until `date-month-only` has a row, which is where the question gets asked | O4 shows the struck-to-warned diff |
| 84c17cd | a closed part cancelling the facility (W8) | same fix needed everywhere; the parser was not uncertain, so no marker fired | O4: 541 lost strikes as one reviewed diff; D2's `subject/closure/<reason>` counters |

No accepted candidate makes any incident worse. The candidates that would
have (F3a on 647596b, A1 natural's bare "only" before typing, D2 multi, D1b
k=2) are rejected or fixed.

The LLM residue pass (approach C above) stays on hold. If it is taken up,
C1's marker table is what gates its objects too: an `llm` marker gets a row
like any other, and the registry test makes the trust decision explicit.
