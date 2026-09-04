# Associating items with the schedule

An enrichment record should attach to the most specific target that can be
established without guessing, one of:

- a **time slot** (activity + weekday/date + clock range),
- an **activity** (all its slots on the given dates),
- an **activity class** ("all skating" applied across matching activities),
- a **schedule group** ("all drop-ins cancelled" on a group's CHANGES),
- a **facility** ("the facility is closed and all programs cancelled"),
- an **amenity** (hot tub, sauna, ... : no schedule effect unless the amenity
  is itself an activity),
- or **unresolved** (raw text only, optionally with resolved dates).

Everything below is measured on unique (group, CHANGES block) pairs across
all 315 versions (scripts in `scripts/`).

## Activity phrase match rates

Taking each leaf item's leading phrase (text before the time/keyword tail)
and matching against the group's activity labels and normalized names
(lowercase, punctuation stripped):

| bucket | count | notes |
|---|---|---|
| whole-scope/freeform ("all X", "the facility...", "see X schedule") | 1,255 | handled by scope rules, not name match |
| exact match | 650 | |
| substring match | 364 | often *ambiguous*, see below |
| no match | 80 | |

Substring matches are frequently one-to-many: "Lane swim" against a group
containing "Lane swim - 25m pool", "Lane swim - 50m long course", "Lane swim -
50m short course" (178 of the 364). Picking one would be a false positive;
the record must carry all candidates and an ambiguity marker. One-to-one
substring matches ("Aquafit lite" -> "Aquafit lite - 25m pool - shallow") are
safe to treat as matches with a `fuzzy` quality tag.

No-match causes, in rough order:

- Shorthand/renamed variants: "aqua lite" vs "Aquafit lite", "aqua zumba" vs
  "Aquafit - Zumba", "adult 18 + skate" vs "Adult skate 18+", "hockey child"
  vs "child hockey (6 to 12 years)".
- The activity genuinely isn't in the schedule (either it only exists in a
  holiday schedule, another group, or the city listed something not
  scheduled: "women's only swim", "pickleball" in a group-fitness group).
- Typos ("Baddminton doubles - adult").
- Amenities ("hot tub + steam room").

Token-based matching (all tokens of the phrase appear among an activity's
tokens, ignoring stopwords/ages/punctuation, plus a small synonym table:
skate/skating, aqua/aquafit, swim variants) would recover most variants
without inviting false positives; anything matching multiple activities
stays ambiguous.

"All X" scope phrases must be applied, not skipped:

- On a group's CHANGES, "All drop-in skating and ice sports, cancelled",
  "All drop-ins cancelled", "all programs cancelled" scope to the whole
  group (the city already splits per group; the merged phrasing appears in
  SPECIAL).
- Class phrases can also select within/across groups by normalized activity
  name ("all skating" -> activities whose name contains a skate token;
  "All pickleball - adult drop-ins" -> pickleball adult).
- On SPECIAL (no group), "all skating and ice sports" maps to groups by
  title/name tokens.

### The ice classes, and the hard-coded fallback

Two wrinkles, both found on the 2026-09-04 dataset.

**`skatings`.** Tom Brown Arena writes `All drop-in skatings, cancelled` in one
item of a December list whose other items all write `skating`. `skatings` was
not in the stemMap, so the segment token stayed `skatings`, matched no group
title and no activity, and the notice resolved to `class-unmatched` — nothing
cancelled. It is now stemmed to `skate` alongside `skating`/`skates`. Over the
full corpus this is 18 objects at that one facility, every one of them then
scoping to the whole skating group and picking up its slot.

**`iceClassVocab`.** The fallback for the other direction: a facility naming a
class that no group of its titles and no activity of its own spells. The
vocabulary is measured, not guessed. Over all 416 dataset versions
(2025-10-06 to 2026-09-04), every activity ever published in an `ice sports`
group reduces to hockey (including pick-up, child and youth hockey), ringette,
stick and puck, figure skating and speed skating; every activity ever published
in a `skating` group is a skate/skating variant except pick-up hockey.

So the taxonomy is:

| class segment | matches an activity whose tokens hold |
| --- | --- |
| `{skate}` | `skate` |
| `{ice, sports}` | `hockey`, `ringette`, `puck` or `shinny`; or `skate` with `figure` or `speed` |

**Plain public, family and adult skating are deliberately not ice sports.** The
city files them under skating, and matching them from an "all ice sports"
notice would cancel sessions the notice does not name — the no-false-positive
contract, applied to a taxonomy rather than to a parse.

### The city writes the block, not the row

A third shape, and the one that changes output. Canterbury publishes
`Public Skating, 11 am to 1 pm, cancelled` for Wednesday September 9, and Brian
Kilrea Arena runs `Adult skating` 11 to noon and `Public skating` noon to 1.
Matching the phrase literally cancels the second hour and leaves the first
running, so half the window the city gave is ignored.

`skateSiblings` widens a matched skate activity to the group's other skate
activities, and `touchedBy` then keeps the widening only where a sibling's slot
actually falls inside the notice's clock window. Two rules bound it:

- **Only with a clock.** A bare `Public skating, cancelled` must not take the
  whole skating class with it; the window is what authorizes the reach.
- **Only when it changes the slots.** Most skating groups have a sibling and
  most notices do not reach it. Marking those would put a confidence marker on
  thousands of objects the widening never touched: over the corpus the
  unguarded version marked 2,549 objects to change 12.

Over all 444 versions it changes **12 objects in 2 cases**, and marks exactly
those 12 with `skating-widened-to-window`:

| facility | notice | before | after |
| --- | --- | --- | --- |
| Canterbury | `Public Skating, 11 am to 1 pm, cancelled` | the noon slot only | both the 11 am and noon slots |
| Metcalfe | `Public skating, 4 to 4:50 pm, cancelled` | nothing, `no-slot-overlap` | the Thursday 4 pm slot |

The Metcalfe case is the one to watch, and it is why the marker exists. There is
no Public skating at 4 pm that day; `Family skating` is the only session at that
clock, so the widening cancels an activity the notice does not name. With an
exact clock and a single candidate that is very likely what the city meant, and
the previous behaviour marked nothing at all, but it is a judgement the marker
hands to the consumer rather than hides.

It runs only after `coversGroup`, `matchClass` and the sibling-group fallback
have all found nothing, and always marks `class-matched-by-vocabulary`, because
it asserts a classification the page never stated. **Over all 444 versions it
fires zero times**, so it is an untested guard rather than a measured rule; the
18-object corpus change above is entirely the stem. What keeps it honest as the
city's vocabulary drifts is `claude-qc`'s `city-ice-class-vocabulary`, which
warns when a skating or ice sports table gains an activity this table does not
cover, or one that lands in the wrong half of it.

## Time slot matching

For items with an exact activity match, a parseable single-date head, and a
clock range in the text: 604 candidates, 425 match a slot of that activity on
that weekday *exactly* (start and end equal, after resolving missing
meridiems). The 179 misses are mostly semantic, not noise:

- "added" items: correctly absent from the schedule (they are new times);
  an added time that *does* match an existing slot is suspicious.
- Sub-interval closures: "Sauna, 4 to 7:30 pm, closed" against a 6:15 am to
  6 pm slot; "Public swim, 2:30 to 4 pm, cancelled" against 1 to 4 pm.
- Multi-slot spans: "Badminton, 3 to 10 pm, cancelled" covering 3-4, 4-5,
  5-6 pm slots.
- Occasional off-by-a-bit times that overlap but do not equal a slot.

So: use **overlap** semantics to find affected slots for
cancelled/closed/changed; record whether the match was exact, contained, or
spanning, and keep exact equality as a confidence signal. For "added", emit
the new time without expecting a slot.

Missing meridiem resolution ("8:30 to 10:30"): try both interpretations;
if exactly one overlaps the activity's slots that day, take it with a
`meridiem-inferred` marker; otherwise ambiguous.

## Date resolution

Follow the existing deterministic pattern in
`website/pkg/ottrecidx/refutil.go` (`ComputeEffectiveDateRange`,
`SingleDayDate`): anchor yearless dates to the facility `SourceDate` (falling
back to the dataset `Updated` time), with conservative pivot rules for
year-wrapping ranges, in `ottrecidx.TZ`.

Change items add a validator those helpers don't have: most heads include
the **weekday**, so a candidate year is only accepted if the weekday agrees
(e.g. "Friday, July 3" must land on a Friday). Check the scrape year and
year+1 (and year-1 for stale pages); if none agrees, or more than one
plausible year agrees, mark the date ambiguous and keep the raw head.
Additional cross-check: the resolved date should fall inside (or near) the
group's schedule effective date range.

Open-ended forms ("until further notice", "will resume in the fall") resolve
to an open range anchored at the version date, flagged open-ended.

## Deduplication (SPECIAL vs CHANGES)

Prefer group CHANGES as the authoritative scoped copy. For SPECIAL items,
after normalizing text (whitespace, punctuation, merged class phrases like
"skating and ice sports" vs "skating"), drop or link items whose
(date, normalized item) already appear in one of the facility's group
CHANGES. Comparison must be on extracted text, not HTML (the copies differ
in markup and typos).

## Versioning

Blocks persist across versions (39,695 instances -> 1,652 unique). Enrichment
results should be cached by a hash of (block HTML + relevant context:
group activities/times, source date bucket) so a daily run only processes
the handful of new blocks. Note the same block HTML can resolve differently
under a different schedule (activities change season to season), hence
context in the key. Yearless dates also make cached absolute dates
version-dependent: same block + same schedule scraped in a different year
resolves differently, so the source-date year belongs in the key too.
