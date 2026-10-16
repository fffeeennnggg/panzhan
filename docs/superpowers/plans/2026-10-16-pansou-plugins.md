# PanSou Plugins Implementation Plan

> **For agentic workers:** Execute inline in this task and keep each step independently verifiable.

**Goal:** Compile the 67 search plugins named by PanSou issue #4 into Panzhan's own process and expose their Quark results through the existing progressive search flow.

**Architecture:** Port only PanSou's selected plugin implementations and the shared packages they require into an internal compatibility package; do not include or launch its HTTP server/API. Adapt each plugin result into Panzhan's source contract, preserving Quark password/title metadata and per-source failures.

**Tech Stack:** Go, existing Panzhan HTTP API and embedded ES module UI, PanSou MIT source.

---

### Task 1: Define source plugin contract and result conversion

**Files:**
- Modify: `internal/search/search.go`
- Create: `internal/search/plugins.go`
- Test: `internal/search/plugins_test.go`

- [ ] Add a `SourcePlugin` contract with `ID`, `Name`, `Priority`, and context-aware `Search` methods; adapt the existing source switch behind the contract without changing API behavior.
- [ ] Add tests first for converting PanSou results: accept valid Quark links, attach `Password`, use `WorkTitle` when present, reject non-Quark and unsafe URLs, merge duplicate shares.
- [ ] Run `go test ./internal/search -run 'TestPanSouResultConversion|TestSourcePlugin' -count=1` and confirm the new tests fail before implementation.
- [ ] Implement conversion and a stable priority sort; run the focused search tests.

### Task 2: Migrate the 67 selected plugin implementations

**Files:**
- Create: `internal/search/panplugin/` with only the required shared PanSou packages and 67 plugin packages.
- Modify: `go.mod`, `go.sum`, `THIRD_PARTY_NOTICES.md`
- Test: `internal/search/panplugin_registry_test.go`

- [ ] Copy the exact issue #4 plugin directories and required shared `plugin`, `model`, `config`, and `util` packages from the inspected PanSou source snapshot; preserve upstream file headers and `LICENSE`.
- [ ] Mechanically rewrite local imports from `pansou/...` to `panzhan/internal/search/panplugin/...`; exclude PanSou `main`, `api`, and `service` packages unless compile diagnostics prove one shared helper is required.
- [ ] Add explicit blank imports for exactly the 67 issue IDs and a registry test that compares the registered plugin names to a sorted fixed manifest.
- [ ] Run `go test ./internal/search/panplugin/... -run '^$'` to compile every migrated adapter without running live source probes; fix only compile/dependency issues exposed by that command.

### Task 3: Connect adapters to Panzhan search and settings

**Files:**
- Modify: `internal/search/search.go`, `internal/app/state.go`, `internal/app/server.go`, `internal/app/search_api_test.go`
- Test: `internal/search/search_test.go`

- [ ] Register each migrated adapter through the source contract and expose ID, display name, priority, and configuration requirement in `/api/sources`.
- [ ] Execute each selected plugin with a request-scoped context, normalize its output, isolate errors, and retain the current per-source cache, backoff, status, and resource-catalog behavior.
- [ ] Preserve existing user settings; use issue #4's 67 IDs as defaults for fresh installs and the existing single-`pansearch` upgrade migration.
- [ ] Add tests for one plugin returning Quark plus Baidu links, a failing plugin beside a successful plugin, enabled-source filtering, and priority ordering; run `go test ./internal/search ./internal/app`.

### Task 4: UI source status and whole-project verification

**Files:**
- Modify: `internal/web/assets/app.js`, `internal/web/assets/search-results.mjs`, `README.md`, `THIRD_PARTY_NOTICES.md`
- Test: `internal/web/assets/search-results.test.mjs`

- [ ] Display the expanded source catalog with priority and setup-required status while retaining current enable toggles and diagnostic errors.
- [ ] Apply source-priority score and title relevance in deterministic result order; do not change the Quark-only download affordance.
- [ ] Run frontend unit tests, `go test ./...`, `go vet ./...`, `GOOS=linux GOARCH=amd64 go build`, and the FPK staging validation script.
- [ ] Inspect the final diff and state clearly which third-party sources require credentials or could not be live-probed from this environment.
