# grocery-list-core: build, lint, test, import, and serve the fdc binary.
# Same shape as the Makefile in safetybox so the targets feel the same across
# repos. Every shipped build is CGO_ENABLED=0, so the four cross-compiled
# binaries need no C toolchain. The race detector needs cgo, so the -race
# targets override that for the test run only.

BIN            := fdc
PKG            := github.com/samuel-stidham/grocery-list-core
LOCAL_VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO_VERSION     := $(shell go list -m -f '{{.GoVersion}}')
GOBIN          := $(shell go env GOPATH)/bin
GOVULNCHECK_VERSION := v1.6.0
LDFLAGS        := -s -w -X main.version=$(LOCAL_VERSION)
GOFLAGS        := -trimpath
PLATFORMS      := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64
BIN_DIR        := $(HOME)/.local/bin
# Extra flags for fdc install, for example INSTALL_FLAGS='--listen 127.0.0.1:9000'.
INSTALL_FLAGS  ?=

# The data pipeline. fetch.sh pins the release, import reads the extracted
# directory, and the test instance serves the database in the checkout.
DATA_DIR       := data
RELEASE        := FoodData_Central_csv_2026-04-30
SRC            := $(DATA_DIR)/$(RELEASE)
DB             := $(DATA_DIR)/fdc.sqlite
TEST_LISTEN    := 127.0.0.1:8474

.DEFAULT_GOAL := help

.PHONY: help
help: ## Display all available targets.
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: test-vars
test-vars: ## Print resolved variables.
	@echo $(BIN)
	@echo $(PKG)
	@echo $(LOCAL_VERSION)
	@echo $(GO_VERSION)
	@echo $(SRC)
	@echo $(DB)

.PHONY: build
build: ## Build the fdc binary for this machine into dist/.
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/$(BIN) ./cmd/$(BIN)

.PHONY: dev
dev: ## Build a dev binary into dist/ (version tagged -dev).
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)-dev" -o dist/$(BIN) ./cmd/$(BIN)

.PHONY: build-all
build-all: ## Cross-compile linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 into dist/.
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "building dist/$(BIN)-$$os-$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/$(BIN)-$$os-$$arch ./cmd/$(BIN) || exit 1; \
	done

.PHONY: install
install: build ## Copy the binary to ~/.local/bin and install the user daemon (systemd or launchd).
	install -d -m 0755 $(BIN_DIR)
	install -m 0755 dist/$(BIN) $(BIN_DIR)/$(BIN)
	$(BIN_DIR)/$(BIN) install $(INSTALL_FLAGS)

.PHONY: uninstall
uninstall: build ## Stop and remove the user daemon and the binary, using a fresh build. The database and the log stay.
	dist/$(BIN) uninstall
	rm -f $(BIN_DIR)/$(BIN)

artifacts: ## Create the artifacts folder.
	mkdir -p ./artifacts

.PHONY: clean
clean: ## Remove build output and artifacts. Leaves data/ alone.
	rm -rf dist/
	rm -rf artifacts/

.PHONY: test
test: ## Run all Go tests. Fast, for the local loop.
	CGO_ENABLED=0 go test -count=1 ./...

.PHONY: test-race
test-race: ## Run all Go tests with the race detector. Needs cgo.
	CGO_ENABLED=1 go test -race -count=1 ./...

.PHONY: cover
cover: artifacts ## Run tests and write the coverage profile.
	CGO_ENABLED=1 go test -race -coverprofile=artifacts/coverage.out -covermode=atomic ./...

.PHONY: cover-func
cover-func: cover ## Per-function coverage summary in the terminal.
	go tool cover -func=artifacts/coverage.out

.PHONY: cover-html
cover-html: cover ## Render the coverage report (stdlib HTML).
	go tool cover -html=artifacts/coverage.out -o artifacts/coverage.html
	@echo "wrote artifacts/coverage.html"

.PHONY: dep-tidy
dep-tidy: ## Remove unused dependencies and add missing dependencies.
	go mod tidy -go=$(GO_VERSION)

.PHONY: update-dep
update-dep: ## Update one dependency. Usage: make update-dep DEP=modernc.org/sqlite
	go get -u $(DEP)

.PHONY: gci
gci: ## Rewrite import grouping (stdlib / this module / third-party).
	gci write --custom-order \
		-s standard \
		-s "prefix($(PKG))" \
		-s default \
		cmd internal

.PHONY: gofumpt
gofumpt: ## Run the gofumpt formatter.
	gofumpt -l -w .

.PHONY: vet
vet: ## Run go vet.
	go vet ./...

.PHONY: lint-go
lint-go: ## Run golangci-lint with the repo config (includes gosec, staticcheck, revive).
	golangci-lint run --fix

.PHONY: gosec
gosec: ## Run gosec on its own, with the full report.
	gosec -quiet ./...

.PHONY: lint
lint: gofumpt gci vet lint-go ## Run the formatters and every linter, fixing what they can.

.PHONY: lint-check
lint-check: vet ## Fail on any formatting or lint finding without fixing anything. What CI runs.
	golangci-lint run

.PHONY: vuln
vuln: ## Scan dependencies for known vulnerabilities.
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

.PHONY: check
check: lint-check test vuln ## Everything CI would run: lint-check, test, vuln. Nothing is modified.

.PHONY: fetch
fetch: ## Download the pinned USDA archive, verify it, and extract the needed files.
	$(DATA_DIR)/fetch.sh

.PHONY: import
import: build ## Import all four food types into data/fdc.sqlite (about two minutes).
	dist/$(BIN) import --src $(SRC) --out $(DB)

.PHONY: import-dev
import-dev: build ## Import SR Legacy and Foundation only (seconds), for development.
	dist/$(BIN) import --src $(SRC) --out $(DB) --types sr_legacy,foundation

.PHONY: verify
verify: build ## Print the verification report for data/fdc.sqlite.
	dist/$(BIN) import --verify-only --out $(DB)

.PHONY: run
run: build ## Serve data/fdc.sqlite on 127.0.0.1:8474, the test instance.
	dist/$(BIN) serve --db $(DB) --listen $(TEST_LISTEN)
