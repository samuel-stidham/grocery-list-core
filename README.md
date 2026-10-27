# grocery-list-core

The nutrient backend of a private meal planner. It imports the USDA FoodData
Central CSV download into one SQLite file and serves nutrient lookups over a
local, read-only HTTP API. One Go binary, `fdc`, no C toolchain, no network
access at run time.

The frontend lives in the sibling repository `grocery-list-frontend`. It is
the only client.

## Data

FoodData Central is public domain data from the U.S. Department of
Agriculture, Agricultural Research Service. See
[data/ATTRIBUTION.md](data/ATTRIBUTION.md) for the citation and the exact
release. The release is pinned by URL and SHA-256 in `data/fetch.sh` and
`internal/meta`. All four food types import by default: SR Legacy,
Foundation, Branded, and Survey (FNDDS). That is about two million foods and
twenty seven million nutrient rows.

## Build and import

```sh
make help         # every target with a one-line description
make fetch        # download the pinned archive and extract the needed files
make import       # dist/fdc import ... --out data/fdc.sqlite, all four types
make import-dev   # SR Legacy and Foundation only, seconds, for development
make run          # serve data/fdc.sqlite on 127.0.0.1:8474 (the test instance)
make test         # go test with CGO_ENABLED=0
make lint         # gofumpt, gci, go vet, golangci-lint --fix (gosec, staticcheck, revive inside)
make lint-check   # the same linters without fixing anything, fails on any finding
make check        # lint-check, test, vuln: what CI would run, modifies nothing
make build-all    # linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 into dist/
```

The import validates every CSV header before writing anything. It streams
each file once, writes to a temporary file, verifies it, and renames it into
place. A failed import leaves the previous database untouched. A running
`fdc serve` keeps serving the release it opened. Restart it after an import
to pick up the new file. The report at
the end prints row counts per table and the result of a smoke test. Iron for
"Spinach, raw" (fdc_id 168462) must be 2.71 mg per 100 g under nutrient id
1089, nutrient number 303.

## Lint

`make lint` runs the formatters and `golangci-lint` with the same linter set
as safetybox, gosec included. Findings get fixed, not suppressed. The few
exclusions live in `.golangci.yml` and each one is justified in
[docs/linting.md](docs/linting.md). `make gosec` and `make vuln` run the
security scanner and the vulnerability check on their own.

## Serve

```sh
dist/fdc serve --db data/fdc.sqlite --listen 127.0.0.1:8474
curl -s http://127.0.0.1:8474/v1/foods?q=spinach
```

Without `--log-file` the daemon logs to stderr, one record per request plus
startup and shutdown. With it, the daemon appends to that file and rotates it
by size in process. `--log-max-mb` (default 10) and `--log-keep` (default 5)
cap a runaway log at 60 MB. `--log-json` switches the records to JSON and
`--verbose` enables debug level. A 500 response never carries the error text.
The cause is in the log. Requests must name the server as `127.0.0.1`,
`localhost`, `::1`, or the `--listen` host. Any other `Host` is a 403.

[docs/API.md](docs/API.md) documents every endpoint with examples.

## Install

```sh
make install
```

`make install` builds, copies the binary to `~/.local/bin/fdc`, and runs
`fdc install`. On Linux that writes the systemd user unit
`~/.config/systemd/user/fdc.service`, enables it, and restarts it. On macOS
it writes `~/Library/LaunchAgents/me.samuelstidham.fdc.plist` and bootstraps
it. The service file carries every serve flag written out, `--log-file`
included, so it states exactly how the daemon runs. A second `make install`
after a rebuild is the upgrade: the file is rewritten and the service
restarted on the new binary.

`fdc install` takes the serve flags, so `--db`, `--listen`, and the log
settings end up in the service file. `make install
INSTALL_FLAGS='--listen 127.0.0.1:9000'` forwards them. `--bin` points the
file at another binary, which must be an absolute path to an executable
file. `--print` shows the file without touching anything. After the
restart, install asks `/v1/about` on the listen address. Then it asks the
service manager once more, so a stray process on the port cannot pass for
the unit. On macOS that reads the state line of `launchctl print`, because
a loaded job whose process exited still prints. Install fails with the
status hint when the daemon does not answer within five seconds, because
`systemctl restart` returns before the port is bound. Ctrl-C anywhere after
the file is written ends install early. The report then names the status
command, since the daemon may still come up, and says to rerun install. The
database is built separately.
`fdc import --out ~/.local/share/fdc/fdc.sqlite` writes the path the unit
serves, and `systemctl --user restart fdc` picks the new file up.

The unit is wanted by `default.target`, so it starts at login and stops at
logout. For a machine that must serve after a reboot with nobody logged in,
enable lingering once with `loginctl enable-linger $USER`. `PrivateTmp=yes`
in the unit needs unprivileged user namespaces, which mainstream kernels
allow. A hardened kernel that refuses them fails the unit at start, and the
journal names the key.

`make uninstall` stops the service, removes its file and the binary, and
leaves the database and the log in place. It runs the fresh build from
`dist/`, so it works after the installed binary is gone.

## Layout

```
cmd/fdc/              the binary: import, serve, install, uninstall, version
internal/importer/    CSV streaming, filtering, batching, verification
internal/store/       schema, read-only open, every query
internal/api/         the /v1 handlers, request logging, error codes
internal/datatypes/   short type names and their USDA values
internal/meta/        attribution text and the pinned release
internal/logging/     slog setup and the size-rotating log file writer
internal/paths/       platform paths for the database, log, binary, and unit
internal/daemon/      systemd unit and launchd agent writers behind an injected runner, the wait for the first answer
data/                 fetch.sh, ATTRIBUTION.md, and the ignored downloads
docs/                 API.md and linting.md
```

## Status

Core is complete: `import`, `serve`, `install`, and `uninstall`. The
frontend repo builds on the `/v1` API and copies `internal/daemon` and
`internal/logging` for its own daemon.
