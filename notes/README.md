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
- [implementation.md](implementation.md): status and corpus results of the
  deterministic parser (approach A), implemented in `enrich/` + `cmd/enrich`.
- [hacking.md](hacking.md): continuation notes — code map, the
  no-false-positive invariants, dev workflow, and next steps.

Tooling (from the module root, needs `/tmp/ottrec-data.db`):

- `go run ./cmd/enrich`: the enrichment itself; latest version as JSON to
  stdout, `-versions 0 -o dir` for one file per version, `-o ""` for stats
  only. Stats always go to stderr.
- `go run ./cmd/check-coverage`: verifies every word of source text is
  accounted for by an output object (the total-accounting guarantee).
- `go run ./cmd/dump-residue > notes/residue.txt`: the unique items the
  parser couldn't fully resolve (the LLM/manual second-pass candidate set),
  deduplicated and grouped by failure category. [residue.txt](residue.txt)
  is a checked-in snapshot over all 315 versions.
- `go run ./cmd/dump-special` (existing): raw line dump for `sort | uniq -c`.
- `go run ./cmd/dump-context [-versions n] [-blocks]`: whole blocks
  (newline-escaped) with facility/group/schedule/activity/time context;
  `-blocks` emits `KIND\tHTML` lines for dedup counting.
- `notes/scripts/*.py`: the analysis scripts that produced the numbers in
  these notes. Run them in a directory containing the dump outputs they name
  (see each script's docstring); they parse the escaped `dump-context` output.

A full-corpus run over all versions holds every index in turn; run it under a
memory cap (`systemd-run --user --scope -p MemoryMax=8G env GOMEMLIMIT=6GiB
...`) to be safe.
