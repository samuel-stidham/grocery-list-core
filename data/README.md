# data/

Everything in this directory except this file, `ATTRIBUTION.md`, and
`fetch.sh` is generated and ignored by git.

`fetch.sh` downloads the pinned USDA archive, checks its SHA-256, and extracts
the thirteen files the importer reads into `FoodData_Central_csv_2026-04-30/`.
Expect about 460 MB zipped and about 3.3 GB extracted.

`make import` then writes `fdc.sqlite` from that directory. `make import-dev`
writes a small database with SR Legacy and Foundation foods only, which is the
fast path for development. The importer validates every header before it
writes anything. A different release therefore fails at the first file instead
of importing into the wrong columns.