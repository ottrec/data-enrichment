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

### A subject naming more than one activity

`Figure skating and hockey 35+, cancelled` at Bernard Grandmaître cancelled only
the hockey. The whole phrase's tokens `{figure, skate, hockey, 35+}` are a
superset of `Hockey 35+`'s, so `match` returned it alone and confidently, while
`Figure Skating (6+)` was excluded by its age qualifier. One of the two named
activities was left running, with **no ambiguity marker**, so nothing downstream
could tell.

`matchActivityParts` splits the subject on the boundaries `classSegments` uses
and matches each part on its own. Three rules keep it from over-reaching, and
the middle one is the important one:

- **At least two parts**, and every part must resolve to exactly one activity.
  A part that is ambiguous or unmatched refuses the whole split rather than
  cancelling the half that did match.
- **A phrase that exactly names an activity is not a list.** Plenty of labels
  carry "and": `Cardio and strength`, `Weight and cardio room`, `Step and
  strength`, `Stick and Puck`. An earlier version omitted this and split
  `Cardio and strength, 9 to 10 am, cancelled` into `Cardio` + `strength`,
  which at a facility running a separate `Strength` row cancelled a session the
  notice never named. An exact or equal-token-set match on the whole phrase now
  settles that it names one thing. This was caught by the corpus diff, not by a
  test, which is what the corpus diff is for.
- **It must say something the whole phrase did not**: either more activities, or
  the whole phrase was `multiple` and the parts resolve it. Nothing is ever
  picked from among candidates, so the never-guess invariant is untouched.

At facility level the split runs **within each group in turn**, not across all
of them. A part naming an activity two groups publish (`figure skating`, in
skating and in ice sports) is ambiguous facility-wide and unambiguous inside a
group, so without this the facility-level copy of a notice split differently
from its group-scoped twin and the two stopped collapsing — 70 duplicated
notices over the corpus, then 34, now none.

Over all 444 versions it changes **319 objects across 4 distinct phrases**, and
**no object loses a slot or gains an ambiguity**:

| facility | phrase | effect |
| --- | --- | --- |
| Sandy Hill | `Youth hockey and speed skating` | 204x, no slots and `date-outside-schedule` to a real slot and no marker |
| Bernard Grandmaître | `Figure skating and hockey 35+` | 72x, one slot to both |
| Jim Durrell | `Family skating and public skating` | 36x, same slots, `multiple` to `exact` |
| Bob MacQuarrie | `Public skating and figure skating` | 7x, same slots, `multiple` to `exact` |

`amb/activity-multiple-candidates` 319 to 276, `amb/date-outside-schedule` 628
to 424. The Sandy Hill drop is the interesting one: those were the
`date-outside-schedule` warns the Dates section calls a false-positive shape,
and they were the enrichment matching a poorly resolved phrase against the wrong
table rather than anything about the dates.

### The dog swim, and the head that only looked unparsed

The city runs an end-of-season dog swim at outdoor pools and announces it in
two forms, neither of which was reaching anything.

Crestview's schedule_changes writes `End of season dog swim, 4:30 to 5:30 pm,
added`, which resolved correctly (novel activity, added, dated, timed) and was
still marked `head-unparsed`. Its special_hours writes `Dogs swim free, 4:30 to
5:30 pm`, with no "added", which fell through to `freeform-item` with no scope
at all. `dogSwimRe` scopes both as the novel activity they name, marked
`dog-swim-session`. **The effect is left unset where the city writes no trigger
word**: inventing `added` would break invariant 1, and the marker is what tells
a consumer this is a one-off session rather than a schedule row.

**Eight other outdoor pools announce what is almost certainly the same event as
a bare date and time** (`Sunday, August 30, 5 to 6 pm`) with a `See Outdoor
Pools` link and nothing else. The page never says "dog", so they stay
`hours-context-unknown`: the only evidence is the link target and the time of
year, and that is a guess, not a parse. If they should be dog swims, the rule
has to come from somewhere other than the page.

The second half is `allSupplementary`. A `<li>` whose children are only a
cross-reference (`See Outdoor Pools for more information.`, `Details: Outdoor
pools`) is a complete item, not an unrecognized one, so its head no longer
carries `head-unparsed`. That marker drops from 239 to 46 over the corpus, and
what remains is the shapes that really are unrecognized, including the
partial-day closures below.

### Two smaller scope losses, found by scanning the residue

Both cost a whole-group cancellation and both were marked, so nothing was
silently wrong, but neither reached a session.

**A class segment naming part of the group's own title.** South Fallingbrook
posts `All drop-in sports are cancelled.` on a `gymnasium sports` group.
`coversGroup` wants the group's title tokens inside the segment, and `{sports}`
does not contain `{gymnasium, sports}`, so it failed; the reverse direction is
what matches here, and it is the same partial match the facility-level path
already made. 46 objects over the corpus, all this one notice, now group-scoped
with `class-title-partial`.

**A preposition stranded by clock removal.** `findClockRanges` lifts the range
out of the middle of the sentence, so `From 11 am to 2 pm, all drop-in programs
are cancelled` became the phrase `From all drop-in programs`, which matches
nothing. Stripping a leading `from`/`between` when a clock was actually removed
leaves `all drop-in programs`, which scopes to the group, and the 11 am to 2 pm
window then restricts which sessions it reaches. 7 objects, all Richelieu
Vanier. **Only those two words**: `beginning` and `starting` introduce dates as
well (`Fridays and Sundays beginning July 3`), and stripping those edits the
date language instead, which the corpus diff showed on 66 Fairfields objects.

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

## Closure subjects

The subject of a "<subject> is closed" sentence goes through
`resolveClosureSubject` (`subject.go`), which returns a kind and the reason
that decided it. The reason is a stats counter (`subject/closure/<reason>`),
so a change in how subjects resolve shows in the stats diff before anyone
reads the objects; it is not in the output.

| kind | reason | rule | example |
| --- | --- | --- | --- |
| facility | `facility-generic` | every token is a generic facility word | "the facility", "the pool" at Deborah Anne Kirwan Pool |
| facility | `facility-name-token` | a distinctive word of the facility's name | "Fairfields Heritage House", "the museum" at Cumberland Heritage Village Museum |
| facility | `facility-list-with-desk` | a list of the facility and its service desk | "The complex and Client Services remain closed." |
| posted-group | `cancelled-under-group` | cancelling, posted under a group | "The pool is closed and all programs cancelled." in a swim group's changes |
| class | `class-named` | cancelling, the cancellation names a class | "the weight and cardio room is closed, and all group fitness drop-ins are cancelled" |
| part | `part-groups` | a generic subject naming only some of the groups, or a cancelling closure whose part's words name some group titles (`groupsForPart`, pool read as swim) | "The pool is closed for maintenance." at a complex; "Squash and racquetball courts are closed and all drop-ins cancelled." |
| part-unmatched | `part-unmatched` | cancelling, no title names the part; marked `closed-part-unmatched` | "The arena is closed and all programs cancelled." at a complex with no arena group |
| unit | `unit-of-row` | some of the numbered units the matched row runs on (`subjectNamesUnitOfActivity`) | "Squash court 3" against "Squash courts 1, 2, 3, 5, 7 and 9" |
| activity | `activity-exact`, `-normalized`, `-fuzzy` | one activity of the schedule, unless the match is fuzzy and the subject is the activity's words plus more ending in a core amenity noun the activity lacks (`placeNamedForActivity`), which is the amenity | "Squash court 3" at Nepean Sportsplex, a row of its own; not "Elizabeth Manley Figure Skating Arena" against Figure skating |
| other-facility | `other-facility` | every distinctive token of another facility's name, at least two; the amenity is that facility's name | "Meridian Theatres @ Centrepointe will remain closed" at Ben Franklin Place |
| amenity | `amenity-core` | qualifiers up to a core amenity noun (`isAmenity`) | "Roger Sénécal Arena", "the steam room", "the pool's wheelchair ramp" |
| none | `unmatched` | nothing above; marked `activity-unmatched` | "The museum" at Billings Estate |

A generic word that names only some of the facility's groups ("the pool" at
a complex) is a part, closed or cancelling: the closure is the swim group's
and the cancellation is the part's programs, not the facility's (hacking.md
invariant 5). A closure alone carries no amenity, since an amenity closure
reads as a notice about a place and this one is the group's.

A facility closure that states an exception ("The museum is closed to
daily visitors for the winter season, except for programs and special
events.", "with the exception of special programs and events", "but
programs") is marked `facility-except-programs`, on this path and on the
"the facility is closed" one. The heritage sites write it (Cumberland,
Fairfields) and publish no drop-ins today; the day one does, the marker's
row (likely) keeps the closure from striking them. There is no museum
rule: Billings and Nepean Museum are their museum, Cumberland and
Fairfields are open for programs, Pinhey's park stays open, and a word
cannot tell them apart.

The subject is what `subjectClosedRe` leaves after "the", with the verb
and the adverb taken off ("will remain", "remains", "is currently",
"still"): "The pool's wheelchair ramp is currently unavailable." resolved
to nothing for a year because "currently" was the subject's last token
(hacking.md "Things that bit us").

The order is the closure form's: facility, the cancelling cases, unit,
activity, other facility, amenity. The clause form (a subject clause after the fold,
"Public swim, 1 to 3 pm, cancelled") resolves in another order, activity
first with the facility never considered and a multiple match kept as
candidates, and the two are kept apart on purpose: in the closure form a
multiple match is an amenity ("The steam room is closed" beside "Hot tub
and steam room" and "Sauna and steam room"), and in the clause form the
facility check would close Splash Wave Pool from "Public Swim - wave tank
and warm pool, cancelled". The structural review's d.md measured both
unifications (20 and 3 objects, all wrong).

## The word lists

Seven lists decide how a phrase reads:

| list | file | rule |
| --- | --- | --- |
| `stopTokens` | text.go | dropped from every token set (phrases, labels, titles): glue and drop-in boilerplate |
| `stemMap` | text.go | a variant the city alternates with, folded before matching |
| `genericFacility` | item.go | words facility names share: a subject of only these is the facility, and none is a distinctive token of a name (`subjectIsFacility`, `otherFacilities`) |
| `amenityCore` | item.go | a core noun: a phrase ending with one, or reaching one through qualifiers, is an amenity (`isAmenity`) |
| `amenityQualifier` | item.go | a word that may lead to a core noun, for a phrase that does not end with one ("lap pool heater broken") |
| `partGenericTokens` | item.go | words a closed part's name carries and no group title does, skipped by `groupsForPart` |
| `iceClassVocab` | match.go | the ice class taxonomy above |

An entry is there because a phrase needs it, and `vocabRows` in
`enrich/vocab_test.go` is the record: per entry, the fixture, the phrase,
and `want`, what the phrase resolves to with the entry in (the closure
subject's reason where the closure path decides, then each notice's scope
level, match quality, amenity, and activities or groups).
`TestVocabularyMotivation` requires `want`, removes the entry, and
requires the phrase's objects in the fixture or its resolution to change.
`TestVocabularyCovered` fails on an entry with no row, so a new word lands
with its phrase, and an entry whose phrase stops depending on it fails
too. The table has 75 rows: 61 corpus phrases, and 14 entries no fixture
needs, each with its reason and a probe that resolves the case it exists
for:

- `genericFacility`: `arena`, `building`, `dome`, `hall`, `park`,
  `recreation` keep a word facility names share out of the name-token
  match, so "the skate park" at Fisher Park Community Centre is not the
  facility; `facility` is the facility wherever it is posted, and the
  facility sentences (`facilityRe`) read it before the subject resolver
  does.
- `partGenericTokens`: all four. `court` and `courts` are the part rule of
  84c17cd ("squash court" names the squash group, whose title does not say
  court); `room` keeps a bare "room" from naming the weight and cardio
  room group; `rooms` is St. Laurent's "weight and cardio rooms".
- `iceClassVocab`: both, the documented guard above.
- `stopTokens:to`: "10 to 14" in a label and "10-14" in a notice read as
  one spelling (`TestTokens`).

Two corpus rows decide only the reason: `genericFacility:complex` (Tony
Graham's "The complex and Client Services remain closed." is
`facility-list-with-desk` with it, `facility-name-token` without) and
`genericFacility:rink` ("The rink is closed" at Jim Tubman Chevrolet Rink
is `facility-generic` rather than `facility-name-token`). Removing either
leaves the objects as they are and moves the `subject/closure/<reason>`
counters.

`want` is what the phrase resolves to today, and three rows pin a reading
that is not what the city meant. `genericFacility:centre` and `:community`
have one phrase, CARDELREC's "the community centre is closed for annual
maintenance. The arenas are open.", which is the facility: a changes
warning on every group, no strike. `amenityCore:entrance` has Bob
MacQuarrie's "The Main Entrance will be closed due to construction. Please
use the West Entrance.", whose second sentence is an amenity notice
("please use west entrance") with no effect. Reading the candidate phrases
turned up one more, since fixed: with the `skating` stem, Bob
MacQuarrie's "Elizabeth Manley Figure Skating Arena is closed for annual
maintenance." held every token of "Figure skating" and was a closure of
that activity. A fuzzy match on a subject that adds words ending in a core
amenity noun is now the amenity (`placeNamedForActivity`, subject.go).

68 entries were deleted in the structural review (d.md's D3) because no
phrase in the cache's history resolves differently without them: without
each alone and without all 68 together, the golden output and the stats
summed over the fixtures are unchanged, and so are the full-corpus
rendering and stats. By reason:

- plural or spelling never written: `amenityCore` `tubs`, `whirlpools`,
  `slides`, `elevators`, `gyms`, `rinks`, `tracks`, `fields`, `entrances`,
  `studios`, `ramps`, `center`; `genericFacility` `center` (no facility
  name has it); `stemMap` `swims`, `canceled`; `stopTokens` `session`,
  `program`, `activity`, `schedules` (the city writes the other number)
- a core noun later in the phrase decides: `amenityCore` `steam` ("steam
  room"), `lawn` ("the Great Lawn and the sledding hill"), `heater` ("lap
  pool heater")
- no subject the parser resolves reaches them: `amenityCore` `washroom`,
  `washrooms` (the washroom notices carry no effect word), `ice`
- a qualifier before a phrase that ends with its core noun, so the
  qualifier never decides (36): `amenityQualifier` `main`, `baby`,
  `training`, `therapeutic`, `whale`, `wave`, `leisure`, `outdoor`,
  `indoor`, `hot`, `rock`, `sledding`, `great`, `men's`, `women's`,
  `mens`, `womens`, `25m`, `50m`, `1m`, `3m`, `m`, `metre`, `meter`, `1`,
  `3`, `25`, `50`, `customer`, `service`, `cross`, `country`, `ski`,
  `pool` (a core noun, so the qualifier entry could not decide), `dance`
  and `wheelchair` (647596b: "dance studio" and "pool's wheelchair ramp"
  end with their core noun)
- glue no label and phrase differ by where it decides: `stopTokens` `a`,
  `an`, `or`, `on`, `for`, `with`, `s` (`foldText` keeps the apostrophe,
  so "'s" never splits off)

A phrase one of them would have helped now degrades to what an unmatched
subject gives (a warning), and the entry comes back with its row.

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
