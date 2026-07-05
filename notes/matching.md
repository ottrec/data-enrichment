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
