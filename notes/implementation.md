# Implementation: deterministic parser (approach A)

Package `enrich/` + `cmd/enrich`. Consumes dataset versions through
`ottrecidx` (data access, `ComputeEffectiveDateRange`, `SingleDate`, TZ) and
emits one JSON `Output` per version (`enrich/record.go`).

## Output shape

A flat `objects` list plus a facility > group > activity > session reference
hierarchy. Every fragment of source text is exactly one object (kind
`notice`, `unparsed`, or `ignored` with a reason: heading, date-context,
boilerplate, service-desk, duplicate); `cmd/check-coverage` verifies this
mechanically over the whole corpus (every word of every block must appear in
some object's raw text). Objects carry blockHash + seq (block reading order)
and, when the markup allows tracking, the [start,end) byte offset into the
source block HTML.

The tree references objects by id at the most specific level the association
is guaranteed for: facility, group (including unresolved multiple-candidate
matches, with `candidates`), activity (novel:true for added activities not in
the published schedule), or concrete per-date sessions. Session refs separate
`objects` (about published schedule times) from `added` (times added by the
notice). Special-hours notices that duplicate a group's schedule-changes copy
collapse into an ignored/duplicate stub pointing at the survivor, which gets
`sources: [schedule_changes, special_hours]`. Marked-but-validated matches
(matched-other-group, activity-typo-match, time-disambiguated) descend, since
they are deterministic; anything ambiguous stays higher up.

## Pipeline

1. **Block split** (`html.go`): x/net/html fragment parse into headings,
   paragraphs, and (nested) lists. Text is extracted by direct concatenation
   (split-bold survives), `<br>` becomes a line break, zero-width/nbsp
   stripped (`text.go`).
2. **Walk** (`enrich.go`): headings set the section and can set the date
   context; date-only paragraphs/list-heads set the date context; leaf items
   (list items, `<br>` lines, paragraphs) become candidate notices. Handles
   the inverted form (statement head, date children) and garbled date heads
   (children emitted with a `date-garbled` marker, no dates). An intro line
   like "The facility is not available on the following dates:" makes the
   following bare date+time items closures instead of hours.
3. **Dates** (`date.go`): weekday/month/day/[year] grammar with ranges,
   enumerations, weekday-only sets, trailing weekday restrictions ("April 23
   to June 15, Monday to Friday"), "until further notice". Yearless dates
   anchor to the facility SourceDate (fallback dataset Updated) as in
   ottrecidx; the written weekday validates the year. Ranges resolve both
   endpoints jointly (weekday agreements scored, ties broken by anchor
   proximity) so one typo'd endpoint can't drag the range a year off.
   Garbled ranges ("Monday, July 6 to 10 Friday, July 10") are repaired from
   the first and last mentions only when both written weekdays validate in
   the same year; the date-garbled marker stays. All other shortfalls become
   markers, never guesses.
4. **Clocks** (`clock.go`): clock-range grammar; missing meridiems produce
   candidate readings (>12h readings dropped when a shorter one exists,
   shortest first), disambiguated against schedule slots where possible,
   marked `meridiem-inferred`/`meridiem-ambiguous`. Single-ended mentions
   ("closed until noon", "closed at 7:30 pm", "will end at 6 pm") synthesize
   the affected part of the day (TimeAssoc OpenStart/OpenEnd; "will end at"
   also sets TimeChange).
5. **Items** (`item.go`): multi-sentence items split at sentence boundaries
   and parse per sentence (sibling freeform sentences don't add redundant
   unparsed records). Then sentence-level patterns first (see-schedule,
   facility closures, "closed for the season", "Regular season + range",
   "subject is closed" with facility/activity/amenity subject resolution),
   then comma-clause decomposition (keyword clauses, moved/changed-to,
   trailing "only" restriction, subject phrase). Scope phrases ("all
   drop-in skating and ice sports") resolve to whole group / class-matched
   activities / groups by title. Amenity subjects (hot tub, one named arena
   of two, ...) never claim activity effects.
6. **Matching** (`match.go`): exact folded spelling, then equal token sets,
   then token subset either way (stemmed, stopworded), then edit-distance-1
   token pairing for typos ("Baddminton"), accepted only when it identifies
   exactly one activity and marked `activity-typo-match`. Multiple candidates
   are kept as candidates with `activity-multiple-candidates`, except when
   the item's exact time+weekday slot uniquely identifies one
   (`activity-time-disambiguated`). The city sometimes posts a change under
   the wrong group ("Public skating, cancelled" in ice sports, "All drop-in
   skating" in the swim group): when the posted group has no match, sibling
   groups are tried and the result marked `matched-other-group`. Class
   segments also fall back to partial group-title matches ("all gymnasium
   programming" -> Gymnasium Sports, `class-title-partial`), only after
   everything stricter failed.
7. **Ignored**: recognized boilerplate (contact-for-hours, pickleball-moved
   notices) and unambiguous customer-service-desk closures/hours (subject
   tokens purely service-desk words, closure-only effects) are dropped with
   a stats counter; nothing else is.
8. **Validation**: resolved dates checked against the group's schedule
   ranges (negative-only semantics); item clock ranges related to actual
   slots (exact/within/covers/overlaps/novel/none); cancels with no slot
   overlap marked. "Added" times are expected to be novel.
9. **Dedup**: special_hours notices repeating a group's schedule_changes
   (dates+effects+scope key, merged class phrasing matches per-group copies)
   get `duplicateOfGroups` (flagged, not dropped).

## Corpus results (315 versions, 2025-08 to 2026-07)

62,579 items → 50,106 notices + 11,845 boilerplate + 808 unparsed freeform
(1.3%). Scope: activity 11,336 / class ~800 / group 16,103 / facility 19,140 /
amenity 1,712 / none 1,018 (2.0%). Time relations on activity-scoped items:
exact 7,138 / within 1,078 / covers 309 / overlaps 193 / novel 1,317 (added) /
none 554. ~6,450 special-duplicates-changes flags; 38 timeChange (end-early),
3 activity-typo-match, 3,862 modifiedHours, 172 matched-other-group.
Every "cancelled" item in the corpus now resolves to a dated, scoped notice
(the residue file contains zero items mentioning cancellation).

Marker highlights, spot-checked:

- `weekday-mismatch` 856: city typos ("Friday, January 4", "Thursday,
  April 4 to Monday, April 6"); dates still resolve to the intended near
  year.
- `no-slot-overlap` 505: mostly holiday-Monday cancellations of times that
  only existed on unpublished holiday schedules (e.g. Richmond Memorial
  Thanksgiving); genuinely absent from the data, honestly marked.
- `meridiem-inferred` 9,170: pervasive because the city writes "9 to 10 am";
  spot-checks all resolve correctly and most are confirmed by exact slot
  matches.
- Bare "date + clock" items in special_hours/notifications resolve to
  ModifiedHours (facility hours, ignorable for schedule purposes) unless the
  range exactly equals an activity slot on those dates or is under 4h
  (`possible-activity-time` 1,254, mostly amenity slots like hot tub that
  span the whole facility day) or too short to call (`hours-context-unknown`
  144). Inside a schedule_changes block they are always flagged
  `possible-activity-time`, never dismissed as hours.
- `activity-unmatched` 111: mostly facilities with no published schedule at
  the time. Added sessions whose activity isn't in the schedule ("Women's
  only swim, added") get scope activity with matchQuality "novel" instead:
  expected for additions, no marker.
- `date-garbled` 103: all the Glen Cairn "July 6 to 10 Friday, July 10"
  family; all repaired (double weekday validation), marker retained.

## Known limitations / possible next steps

- Prose-embedded dates ("The facility will close at 4:30 pm, Thursday,
  June 11, and reopen at noon, Friday, June 12.") stay unparsed-freeform
  (dates are only parsed at the start of a line/sentence).
- "Reopen at X" is ignored; only the closing side of a closure gets a time.
- The LLM residue pass (approach C) would target exactly the
  unparsed-freeform + activity-unmatched tail, behind the same validators.
  Options to be explored before implementing.
- Caching by block hash (see [matching.md](matching.md)) is not implemented;
  a full run takes ~2 min for all 315 versions, so per-version reruns are
  cheap enough without it.
