# What the data looks like

Measured over all 315 dataset versions in `/tmp/ottrec-data.db` (roughly daily,
~10.5 months): 39,695 non-empty block instances, only **1,652 unique blocks**
(918 SPECIAL, 706 CHANGES, 28 NOTIF). After the initial version, a median of
3 (mean ~5, max 68) new unique blocks appear per version. Whatever the
approach, the working set is small and the daily delta is tiny.

## Sources

Three freeform HTML fields (see scraper `main.go`):

- `Facility.SpecialHoursHTML` (SPECIAL): the page-level "hours-details" field.
- `Facility.NotificationsHTML` (NOTIF): the page-level "notification-details"
  field.
- `ScheduleGroup.ScheduleChangesHTML` (CHANGES): "Schedule change(s)" sections
  found *inside* the group's collapse section; heading removed, parts joined
  with `\n`.

## NOTIF: basically one notice

27 of 28 unique NOTIF blocks are variants of "Pickleball/badminton/table
tennis can now be found in the gymnasium sports schedule" (plus one
"Gymnasium sports will resume in the fall"). One block is a dated event link
(Barrhaven New Year's Eve party). No date/activity association to extract
here beyond passing the text through; not worth special handling.

## CHANGES: highly regular

704/706 unique blocks are a single `<ul>` (one is `ul,ul`, one has a stray
`p,h4` prefix). The canonical shape:

```html
<ul>
  <li><strong>Friday, July 3</strong>
    <ul>
      <li>All drop-in skating, cancelled</li>
      <li>Aquafit, 8:05 to 9 am, cancelled</li>
    </ul>
  </li>
  ...
</ul>
```

Top-level `<li>` heads across unique blocks: 1,175 date-ish with a nested
`<ul>`, 82 date-ish without one (mostly "see X schedule" one-liners and
holiday hour entries), 4 weekday-only ("Fridays, Saturdays, and Sundays"),
7 other.

Leaf item tails (unique blocks, counted per item): cancelled 1,071; added 99;
"only" (pool/course restrictions like "25m pool only") 79; closed 56;
"moved to ..." ~25; "changed to ..." ~5; "schedule change" ~12; the rest are
freeform sentences ("The baby pool will be closed from 1 to 5 pm.",
"Public swim will end at 6 pm.").

Freeform/whole-scope items break down as (unique group+block pairs, per item):
"See <holiday> schedule" 415; "All drop-in <class>, cancelled" 187; "The
facility is closed and all programs cancelled" 181; amenity closures ("The
pool/hot tub/sauna/... is closed ...") 34; "All drop-ins cancelled" 8;
long tail ~16 (e.g. "All drop-in 18+ Pick-up hockey programs are cancelled",
"All group fitness drop-in activities, cancelled", and an activity that
legitimately starts with "The": "The Groove Method®, 10:15 to 11:15 am,
cancelled").

## SPECIAL: several distinct content classes mixed together

Top-level shapes: 509/918 a bare `<ul>` (same item grammar as CHANGES); the
rest mix `<p>` and `<h3>`/`<h4>` sections. Section headings seen: Closure(s),
Modified hours, Schedule change(s), Pool closure, Winter Break hours, Family
Day (weekend) hours, Easter weekend hours, Note/Notice ("Notice: December 5"),
Summer 2026, Regular season, Holiday skates, facility-area names ("Pool",
"Community Centre"), one event ("Vintage Village of Lights").

Content classes:

1. **Schedule-change lists**: identical grammar to CHANGES, often literally
   duplicating the union of the facility's group CHANGES (see below).
2. **Holiday/modified hours**: date-headed items whose payload is *facility
   open hours*, not an activity ("**December 27**, 8:30 am to 9 pm";
   "Wednesday, July 1, 11:30 am to 4 pm"; sometimes weekday patterns
   "Monday to Friday, 1:45 to 7 pm" under a season heading).
3. **Seasonal ranges**: "**Regular season**, June 29 to August 30" (outdoor
   pools etc.), "Closed for the season.", "Pre-season, June 8 to 28".
4. **Long-term closures**: "The pool is closed for maintenance until further
   notice.", "Facility is closed until further notice.", with or without
   a date-range head ("Monday, March 30 to Tuesday, September 1 > Roger
   Sénécal Arena is closed for annual maintenance.").
5. **Amenity notices**: hot tub / sauna / steam room / whirlpool / diving
   board / slide / rock wall / elevator / changerooms / squash courts closed;
   "Lap pool heater broken...". Amenities usually are not schedule
   activities, but sometimes are ("Hot tub and sauna" is an activity at
   Kanata Leisure Centre).
6. **Boilerplate**: "Please contact the facility by phone/email for
   information on opening hours and washroom availability." (the most common
   blocks in the dataset), "Public skating is not available at this
   location.", construction/parking notes, "The park is open year-round."
7. **One-off events/notices**: cooling centre hours, event links.

## SPECIAL duplicates group CHANGES

The city posts a merged facility-level list and per-group filtered lists.
E.g. Sandy Hill Arena SPECIAL says "All drop-in skating and ice sports,
cancelled" while the skating group's CHANGES says "All drop-in skating,
cancelled" and the ice sports group's says "All drop-in ice sports,
cancelled". The split is done by the city, not the scraper. Any enrichment
must dedupe these or /today would show doubled notices. The two copies can
also differ in quality: Glen Cairn Pool had "Monday, July 6 to Friday, July
10" clean in SPECIAL but garbled as "Monday, July 6 to 10 Friday, July 10"
in the group CHANGES.

## Quirk catalog (all real examples)

Markup:

- Tags nested arbitrarily deep: `<li><span><span><strong><span>Thursday,
  December 11</span></strong></span></span>`.
- Bold split mid-word: `<strong>T</strong><strong>hursday, June 11</strong>`,
  `J<strong>anuary 2</strong>`. Text must be extracted before any pattern
  matching; never rely on element boundaries.
- Zero-width spaces (`​`), `&nbsp;`, doubled spaces, trailing spaces
  inside `<strong>`.
- `<br/>` inside `<li>` splitting head from items without a nested list;
  a date head followed by `<br/>`-separated lines instead of `<ul>`
  (`<li><span><strong>Thursday, April 2</strong></span><br/><span>Aquafit
  ... cancelled</span><br/>...</li>`).
- Dates as `<h3>`/`<h4>`/`<p><strong>` instead of list heads ("<h3>Wednesday,
  July 1</h3>", "<p><strong>Friday, February 6</strong></p>" followed by
  sibling paragraphs).

Dates (usually no year):

- "Friday, July 3" (weekday + month + day: the weekday is a free validation
  bit for year inference).
- Ranges: "Friday, April 3 to Monday, April 6", "May 31 to June 28",
  "December 20 to January 2" (wraps year), "December 13 and 14".
- Enumerations: "Thursday, March 12 and Saturday, March 14".
- Explicit years occasionally: "October 31, 2025 to March 13, 2026".
- Open-ended: "November 25 until further notice".
- Garbled: "Monday, July 6 to 10 Friday, July 10".
- Prose-embedded: "The facility will close at 4:30 pm, Thursday, June 11,
  and reopen at noon, Friday, June 12.", "Facility is closed between
  Thursday, May 21 at 5 pm and Friday, May 22 at 5:30 pm."

Times:

- "8:05 to 9 am", "Noon to 5 pm", "4:15 - 5:05 pm", "9:30 to 11:30 am",
  "7:30 pm to 8:30 pm", and missing meridiems: "8:30 to 10:30",
  "12:30 to 1 pm".
- Partial-slot effects: "Public swim will end at 6 pm", "The pool is closed
  until noon", "closed at 7:30 pm", "closed from 1 to 5 pm".

Text:

- Typos: "Baddminton", "therapuetic", "Stick and puck (child 6 to 2 years)".
- Same change written twice with tiny differences (trailing space, period,
  "-" vs "to"), so raw-string dedup mostly works but not always.
