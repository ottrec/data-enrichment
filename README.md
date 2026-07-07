# data-enrichment

Best-effort parsing of schedule changes, facility notifications, and facility special hours.

Everything in this repo (except for this README) is written and maintained by Claude, since it's not worth doing by hand.

This differs from the core dataset stuff (scraper, schema, indexing logic, simplified dataset, data storage and api), which is fully hand-coded and untouched by Claude since I do NOT trust it for that due to the amount of subtle logic required (and since I've seen other people's attempts at doing that result in severe data quality issues).

Originally, I wasn't planning to even attempt to parse this information since it's unstructured and frequently contains typos or ambiguous information. I had considered using a LLM to parse it, but realized that would be extremely buggy since I've already seen LLMs fabricate dates just looking at the regular schedule.

When I added the `/today` page to the website, I realized that the number of sessions with warnings causes a lot of clutter, and may cause people to start skipping them. After thinking about it for a bit, I noticed that if I could determine which parts of the schedule changes were specific to a specific date, I could remove most of the warnings without risking it becoming misleading.

On a whim, I decided to see how far Claude Fable could get at writing a parser for it, focusing on completeness and not making assumptions about ambiguous data. Compared to using a LLM directly, this has significant benefits including determinism, testability, cost, and performance.

It turns out that even though it's hand-written, there's only a few cases we care about, with a relatively consistent structure.

Furthermore, there's few enough unique freeform blocks across all data from the last year that I could manually verify the accuracy of the parsing.

However, there's still enough stuff that I don't want to maintain the actual parsing logic by hand, so I decided to fully vibe-code it (while still guiding the design and constraints and manually verifying the logic and output).

While it parses more than just cancellations and time changes, the rest is mostly just parsed so we can ignore the rest of the text without the risk of accidentally ignoring something important.

For the website, I currently only plan to use this data for the `/today` page, and only to:

- Mark cancelled sessions (while still keeping them visible for clarity and so users can verify it).
- Reduce the prominence of the schedule changes warning when it definitely does not apply to a specific session, or when it's a more general facility notification (to reduce the warning fatigue).
- Adding temporarily added sessions (while clearly marking them as such).
- Applying changes to the session time (keeping the old one there but struck through).
- More definitively referencing holiday schedules when explicitly stated.

All changes as a result of the enriched data have a warning box stating that.

I will also use this to detect incorrect information to report the city after verifying it (e.g., impossible dates, cancellations of time slots which don't exist in the schedule) like I do for the schedules themselves.

Changes to the this code should be made by Claude Fable (or Opus for simpler changes), and the notes need to be kept in sync. The full output for all versions of the dataset so far should be diffed and re-verified manually after each change.

The code is kinda garbage, but it works correctly and the logic is sound.
