# Linting

`make lint` must come back empty. This file explains every exclusion in
`.golangci.yml` and every inline suppression in the code. Add the paragraph
here before adding either.

## The linter set

`.golangci.yml` enables the same linters as safetybox, so every Go repo here
is held to one standard. golangci-lint runs gosec, staticcheck and revive as
part of that set. `make gosec` also runs gosec on its own, because the
standalone tool prints the offending lines in full. `make vuln` runs
govulncheck against the dependency list.

The formatters are gofmt, gofumpt, goimports and gci. `make lint` runs them
in place before the linters, and lets `golangci-lint --fix` repair what it
can. `make lint-check` runs the same checks without touching a file and fails
on any finding, which is what `make check` and CI use. A formatting finding
therefore never reaches the report.

## Settings of our own

The settings below change a linter's defaults. Everything else is safetybox's
configuration unchanged.

**tagliatelle, JSON tags are snake_case.** The `/v1` API contract uses
snake_case keys, matched to USDA's own column names. Every JSON tag in the
repo is snake_case on purpose, so the linter is told that this is the rule.

**varnamelen, short names allowed in a few declarations.** `w io.Writer`,
`r io.Reader`, `t *testing.T`, `db *sql.DB`, `tx *sql.Tx`, `i int` and
`ok bool` are idiomatic Go and read better short. The code uses longer names
almost everywhere. The list is the floor, not the habit.

**goconst, ignore tests.** Repeated literals in tests are fixture values. A
constant for each one would hide what the test checks.

**paralleltest, ignore missing.** Each test package shares one fixture
database built in `TestMain`. Marking every test parallel would add noise
without making the suite faster, because the import dominates the run time.

## Test files

Test files skip nine linters: dogsled, dupl, errorlint, funlen, goconst,
mnd, prealloc, unparam and varnamelen. Tests repeat shapes on purpose, match
errors by text, grow long because they list cases, and use literal numbers
from the fixture. Every other linter applies to tests in full. That includes
gosec, cyclop, gocognit, errcheck and bodyclose.

**gosec G304 and G703 in tests.** G304 flags a file open with a variable
path. G703 flags a write to a path that reached the function through a
parameter or a call. Test helpers build every path from `t.TempDir()` or from
the fixture directory under `testdata`, both controlled by the test.
Production code gets no such exclusion. The CSV reader cleans and joins its
path, and the only write goes to the output file the user named.

## Inline suppressions

The code carries six. There are no others, and this command lists them:

```sh
grep -rn 'nolint\|nosec' --include='*.go' .
```

**`#nosec G202` on three queries in `internal/store/queries.go`.**
`MissingFoods`, `NutrientAmounts` and `Nutrients` build an `IN (?,?,?)` list
whose length depends on the request. Only placeholder markers are
concatenated, and every value is bound as a parameter. gosec cannot tell a
placeholder list from user input, so it reports SQL string concatenation.
The mark is gosec's own `#nosec` form because both golangci-lint and the
standalone `make gosec` honor it. A `//nolint:gosec` comment would silence
only golangci-lint, and `make gosec` would keep reporting the three lines.

**`//nolint:gochecknoglobals` on two test fixtures.**
`internal/api/api_test.go` holds one `httptest.Server`, and
`internal/store/queries_test.go` holds one `*sql.DB` with its path. Both are built in
`TestMain` and shared by every test in the package. The database is
read-only, so sharing it costs no isolation. Building it per test would
multiply the import time by the test count for nothing.

**`#nosec G204` on the `go list` call in `cmd/fdc/deps_test.go`.**
The import direction test asks `go list -deps` which packages each inner
package pulls in. gosec flags any subprocess whose arguments are not all
literals. The one variable argument is a package path taken from the
constant lists in the same test. No input reaches the command.

## Handling a new finding

Fix the code first. Most findings point at a real problem: an unchecked
error, a response body left open, a context that was not passed down. When
the finding is wrong for one line, suppress that line alone. State the
reason in the same comment and add the paragraph here. Use `#nosec` for
gosec rules and `//nolint:<linter>` for everything else. Never widen an
exclusion in `.golangci.yml` to make one finding go away.

## Test layout

Tests of exported behaviour live in external `_test` packages, so they see
the package the way its callers do. Tests of unexported helpers live in
files named `*_internal_test.go` inside the package. The `testpackage`
linter enforces the split.
