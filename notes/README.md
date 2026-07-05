# Data enrichment notes

Goal: derive a data file from each dataset version that associates special
hours, notifications, and schedule changes with concrete dates (and where
possible, specific activities and time slots), carrying the raw text plus
best-effort boolean flags (cancelled, added, time change, closure, ...).
Hard requirement: no false positives; ambiguity is marked, never assumed.
The output will eventually feed the website /today page (implemented
separately).

- [data-shape.md](data-shape.md): what the three HTML fields actually contain,
  measured across all 315 dataset versions, with the full quirk catalog.
- [matching.md](matching.md): association targets, measured match rates for
  activity names and time slots, date resolution, ambiguity taxonomy.
- [approaches.md](approaches.md): candidate designs, tradeoffs, recommendation,
  and a sketch of the output record format.

Tooling (from the module root, needs `/tmp/ottrec-data.db`):

- `go run ./cmd/dump-special` (existing): raw line dump for `sort | uniq -c`.
- `go run ./cmd/dump-context [-versions n] [-blocks]`: whole blocks
  (newline-escaped) with facility/group/schedule/activity/time context;
  `-blocks` emits `KIND\tHTML` lines for dedup counting.
- `notes/scripts/*.py`: the analysis scripts that produced the numbers in
  these notes. Run them in a directory containing the dump outputs they name
  (see each script's docstring); they parse the escaped `dump-context` output.
