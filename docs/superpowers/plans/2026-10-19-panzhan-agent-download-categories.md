# PanZhan Agent Download Categories Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make new Agent grants inherit the configured NAS download location and route explicitly confirmed downloads into user-selected, pre-existing category directories chosen by the Agent from preview data.

**Architecture:** Persist administrator-managed category-to-directory mappings in `Settings`, validate all mappings beneath the configured default download directory, and snapshot them into each new `AgentGrant`. `panzhan_preview` advertises only available category IDs; `panzhan_download` accepts a category ID and the server resolves its path from the grant snapshot. Legacy grants with no category snapshot retain their existing fixed directory and old client behavior.

**Tech Stack:** Go 1.26, Gin-backed app APIs, embedded vanilla JavaScript UI, Node built-in test runner, Hermes SKILL.md bundle, official fnpack.

---

## File Map

- Modify `internal/app/state.go`: persist global category mappings and keep them in the existing settings JSON.
- Modify `internal/app/agent_auth.go`: define category IDs, validate configured paths, snapshot mappings, and expose only safe public grant details.
- Modify `internal/app/server.go`: validate category mappings when saving settings; keep the existing settings endpoint and response shape.
- Modify `internal/app/server.go`, `README.md`, and `packaging/fnos/manifest`: release this behavior as version `0.2.2`.
- Modify `internal/app/agent_mcp.go`: publish per-grant category enums in the preview/download tool schemas.
- Modify `internal/app/agent_tools.go`: return configured categories from preview and route confirmed downloads by category ID.
- Modify `internal/app/agent_test.go` and `internal/app/tasks_test.go`: exercise authorization snapshots, path confinement, category routing, legacy grants, and idempotency.
- Modify `internal/web/assets/agent-access.mjs`: render fixed categories and directory selectors, remove the duplicate authorization-subdirectory field, and show configured grant scope.
- Modify `internal/web/assets/app.js`: manage category selection with the existing directory browser and persist mappings through `/api/settings`.
- Modify `scripts/agent-access.test.mjs`: cover rendered categories, browser controls, and removal of the old field.
- Modify `integrations/agents/panzhan/SKILL.md` and `README.md`: teach Hermes to choose a configured category using the preview and to report the resolved destination.
- Modify `docs/agent-integration.md` and `docs/fnos-install.md`: document configuration, fixed grant snapshots, categories, fallback, and old-grant behavior.
- Regenerate `internal/web/assets/agent-kit.zip` with `scripts/build_agent_kit.py` and validate the archive with `scripts/check_package.py`.

## Task 1: Category Model And Path Validation

**Files:**
- Modify: `internal/app/state.go`
- Modify: `internal/app/agent_auth.go`
- Test: `internal/app/agent_test.go`

- [ ] **Step 1: Add failing tests for category IDs and directory confinement**

Add table-driven tests for the fixed IDs `movie`, `series`, `anime`, `music`, `learning`, `software`, and the implicit `other` fallback. Configure the fixture root with `Library`, `Library/Movies`, and an outside sibling `Other`. Assert valid child directories normalize to slash-separated root-relative paths; reject unknown IDs, `other` as an explicit mapping, absolute paths, `..` traversal, mapping the base itself, missing directories, and directories outside `Settings.DefaultDir`.

Run: `go test ./internal/app -run 'TestAgentCategoryDirectoryValidation' -count=1`
Expected: FAIL because category settings and validation do not exist yet.

- [ ] **Step 2: Add settings and grant snapshot fields**

Add `AgentCategoryDirs map[string]string `json:"agent_category_dirs,omitempty"`` to `Settings` and `CategoryDirs map[string]string `json:"category_dirs,omitempty"`` to `AgentGrant`. Define the fixed category IDs and display labels in `agent_auth.go`:

```go
const (
	categoryMovie    = "movie"
	categorySeries   = "series"
	categoryAnime    = "anime"
	categoryMusic    = "music"
	categoryLearning = "learning"
	categorySoftware = "software"
	categoryOther    = "other"
)
```

Keep `other` implicit: it resolves to `AgentGrant.Directory`, so the administrator does not need to select a fallback directory.

- [ ] **Step 3: Implement mapping normalization and confinement**

Add `validateAgentCategoryDirs(root, base string, configured map[string]string) (map[string]string, error)`. For every entry: require a known non-`other` ID; normalize using `filepath.Clean` and `filepath.ToSlash`; reject absolute paths and `..`; use `filepath.Rel(filepath.FromSlash(base), filepath.FromSlash(dir))` to ensure the selected directory remains strictly below the default directory; and call `directoryAt(root, dir)` to require that it exists and is accessible. Return a fresh normalized map so settings and grants do not alias caller-owned maps. The path relation check must reject both `rel == ".."` and any `strings.HasPrefix(rel, ".."+string(filepath.Separator))`.

Use this path check for settings and grant creation. Do not create or move NAS directories.

- [ ] **Step 4: Verify model tests pass**

Run: `go test ./internal/app -run 'TestAgentCategoryDirectoryValidation' -count=1`
Expected: PASS for valid nested categories and every invalid-path case.

## Task 2: Settings And Grant Snapshots

**Files:**
- Modify: `internal/app/server.go`
- Modify: `internal/app/agent_auth.go`
- Modify: `internal/app/agent_test.go`

- [ ] **Step 1: Add failing settings and grant snapshot tests**

Add tests that save category mappings through `POST /api/settings`, verify a path outside `DownloadRoot/DefaultDir` is rejected, and create a new grant whose `Root`, `Directory`, and `CategoryDirs` equal the validated settings snapshot. Change settings after grant creation and assert the existing grant is unchanged. Verify read-only grants can still be created if download settings are unavailable.

Run: `go test ./internal/app -run 'TestAgentCategorySettings|TestAgentCategoryGrantSnapshot' -count=1`
Expected: FAIL because settings do not yet validate or snapshot categories.

- [ ] **Step 2: Validate settings before persistence**

In the existing `POST /api/settings` branch, after validating `DownloadRoot` and `DefaultDir`, call `validateAgentCategoryDirs(downloadRoot, in.DefaultDir, in.AgentCategoryDirs)`. On failure return an actionable 400 error; on success persist only the normalized map. Empty/omitted category configuration remains an empty map for existing installations.

Use the same pattern already used for `DefaultDir`:

```go
categoryDirs, err := a.validateAgentCategoryDirs(downloadRoot, in.DefaultDir, in.AgentCategoryDirs)
if err != nil {
	fail(err)
	return
}
in.AgentCategoryDirs = categoryDirs
```

- [ ] **Step 3: Make new grants inherit configured paths**

Change `createAgent` to `createAgent(name string, scopes []string, days int)` and remove `Directory` from the admin request body. Read current settings once, snapshot `DownloadRoot` and `DefaultDir`, and copy validated category mappings into the grant. If the grant includes `downloads:create`, require an accessible configured default directory; preserve read-only grant creation without a writable download location. Keep old serialized grants untouched: an empty `CategoryDirs` map means the legacy fixed `Directory` behavior. Update all `createAgent` callers in `agent_test.go` and `grantForTest`.

- [ ] **Step 4: Verify settings and snapshot tests pass**

Run: `go test ./internal/app -run 'TestAgentCategorySettings|TestAgentCategoryGrantSnapshot|TestReadOnly' -count=1`
Expected: PASS; updates to global settings do not mutate existing grants.

## Task 3: Administrator Category UI

**Files:**
- Modify: `internal/web/assets/agent-access.mjs`
- Modify: `internal/web/assets/app.js`
- Test: `scripts/agent-access.test.mjs`

- [ ] **Step 1: Add failing UI tests**

Update `scripts/agent-access.test.mjs` to assert that the authorization form has no `agent_directory` field; the page shows all fixed category labels and mapped/unmapped states; each optional category has a `data-agent-category-browse` control; and existing grants show their base directory plus category snapshot without rendering secrets.

Run: `node --test scripts/agent-access.test.mjs`
Expected: FAIL because the current UI has one authorization-subdirectory input and no category map controls.

- [ ] **Step 2: Render mapping controls and grant snapshots**

Change `agentAccessHTML(agents, fresh, root, defaultDir, origin, categoryDirs)` to render the fixed categories with the currently saved relative paths and existing directory-browser buttons. Remove the per-grant directory input. Render the new grant base from root plus default directory and show its configured category paths in the grant list; escape every label/path as today. Use the existing stable button pattern, e.g. `<button type="button" data-agent-category-browse="movie">选择目录</button>`, and render each category with an escaped path or an explicit unconfigured label.

- [ ] **Step 3: Wire category selection to existing APIs**

Extend the app state with `agentCategoryDirs`. Load it from `/api/settings`; when the user selects a category directory through the existing `/api/directories` browser, update only that category and rerender. Add a save action that posts a copy of current `state.settings` with the full category map to `/api/settings`; show server validation errors without dropping unsaved form input. Keep selected paths root-relative and do not accept paths typed by the Agent.

Store the current browser category in `state.browser.category`. Add a `data-agent-category-browse` click branch that calls `openBrowser('agent-category', defaultDir)`. On `data-use-dir`, update `state.agentCategoryDirs[state.browser.category]` with the selected root-relative path and rerender the Agent section. Save with `api('settings', {...state.settings, agent_category_dirs: state.agentCategoryDirs})`; on success replace `state.settings` with the saved response model and render again.

- [ ] **Step 4: Verify UI regression tests pass**

Run: `node --test scripts/agent-access.test.mjs`
Expected: PASS; the duplicate directory input is absent and category mappings display and update.

## Task 4: MCP Category Contract And Download Routing

**Files:**
- Modify: `internal/app/agent_mcp.go`
- Modify: `internal/app/agent_tools.go`
- Modify: `internal/app/agent_test.go`

- [ ] **Step 1: Add failing tool schema and routing tests**

Extend `agent_test.go` to check that `panzhan_preview` advertises only the new grant's mapped category IDs plus `other`, and never returns an absolute path in category metadata. Call `panzhan_download` with a mapped category and assert the created job directory is beneath that mapping. Assert unknown/unconfigured category IDs and arbitrary path-like strings are rejected without creating a task. Verify `other` uses the grant default directory, repeated request IDs return the original task/category, and a legacy grant without `CategoryDirs` continues to accept the old request shape and use its fixed directory.

Run: `go test ./internal/app -run 'TestAgentCategoryToolContract|TestAgentCategoryDownloadRouting|TestLegacyAgentDownload' -count=1`
Expected: FAIL because the MCP schema and tool handlers have no category field.

- [ ] **Step 2: Add per-grant schemas**

Make `agentToolSpecs(g)` construct the `panzhan_download.category_id` enum from `g.CategoryDirs` plus `other`. Require `category_id` only for grants with a category snapshot; keep it optional for legacy grants. Describe the preview result as the source of valid IDs. Preserve `additionalProperties: false` and all existing scope filtering.

Build the schema field using the current `toolSchema` helper: `map[string]any{"type":"string", "enum": append(sortedMappedIDs, "other"), "description":"panzhan_preview 返回的已授权类别 ID"}`. Add it to the required list only when `len(g.CategoryDirs) > 0`; do not add a free-form path field.

- [ ] **Step 3: Return categories from preview**

In `panzhan_preview`, return a stable ordered list of `{id, name}` values from the grant snapshot plus `{id: "other", name: "其他"}`. Continue to omit directory paths from category metadata. Keep the existing actual file list, `choice_id`, expiry, and explicit-confirmation contract.

- [ ] **Step 4: Resolve category server-side before task creation**

Extend the download argument struct with `CategoryID`. After validating confirmation and resolving the choice, map the ID through the authenticated grant snapshot; resolve `other` to `g.Directory`. Reject unknown IDs for categorized grants. For legacy grants with no category map and no `category_id`, use `g.Directory`; reject any supplied path or unsupported category value. Pass only the server-resolved path to `createAgentJob`. Preserve idempotency: a duplicate `request_id` returns the original job before considering a new category value.

Use this resolution order after the duplicate-request check and choice lookup:

```go
destination := g.Directory
if len(g.CategoryDirs) > 0 {
	if in.CategoryID == categoryOther {
		destination = g.Directory
	} else if mapped, ok := g.CategoryDirs[in.CategoryID]; ok {
		destination = mapped
	} else {
		return nil, errors.New("类别无效或未配置，请重新预览可用类别")
	}
} else if in.CategoryID != "" {
	return nil, errors.New("旧授权不支持类别，请重新创建授权后使用分类下载")
}
```

Before `createAgentJob`, open `destination` with `directoryAt(g.Root, destination)` and return an actionable error if the selected directory has been removed or lost access.

- [ ] **Step 5: Report the actual destination**

Include the resolved category display name and task directory in the returned task view or download result, without returning any unmapped path. Ensure retrying the same request returns the same destination.

- [ ] **Step 6: Verify tool contract tests pass**

Run: `go test ./internal/app -run 'TestAgentCategoryToolContract|TestAgentCategoryDownloadRouting|TestLegacyAgentDownload' -count=1`
Expected: PASS for mapped, fallback, invalid, legacy, and duplicate requests.

## Task 5: Hermes Skill And Documentation

**Files:**
- Modify: `integrations/agents/panzhan/SKILL.md`
- Modify: `integrations/agents/panzhan/README.md`
- Modify: `docs/agent-integration.md`
- Modify: `docs/fnos-install.md`
- Modify: `scripts/build_agent_kit.py`
- Generated: `internal/web/assets/agent-kit.zip`
- Test: `scripts/check_package.py`

- [ ] **Step 1: Update Skill contract tests**

Extend package tests to assert that the packaged Skill describes: preview before categorization, choose one returned `category_id`, prefer explicit user destination then preview contents/title, use `other` on uncertainty, preserve explicit download confirmation and stable `request_id`, and report the resolved directory. Assert the Skill never asks for or invents a destination path.

Run: `python -X utf8 scripts/check_package.py --self-test`
Expected: FAIL until the new source text has been added to the archive file list and the archive is rebuilt.

- [ ] **Step 2: Update the Hermes workflow**

In the Skill, instruct Hermes to use the categories returned by the current preview; base classification on preview filenames, title, and any explicit user category; select `other` when uncertain; pass the returned ID to `panzhan_download`; and report the destination from the result. Never derive a filesystem path or bypass the explicit confirmation and request-ID rules.

- [ ] **Step 3: Document settings and migration**

Document the fixed categories, selecting existing directories under the current saved default location, new grant snapshots, `other` fallback, no automatic directory creation, old grants retaining their fixed directory, and the need to recreate a grant to apply changed settings. Update both Chinese-facing Agent integration and fnOS upgrade guidance.

- [ ] **Step 4: Rebuild and verify the Agent bundle**

Run: `python -X utf8 scripts/build_agent_kit.py`
Then run: `python -X utf8 scripts/check_package.py --self-test`
Expected: PASS; the zip exactly matches all source files listed in `scripts/build_agent_kit.py`.

## Task 6: Full Verification And Release Package

**Files:**
- Verify all modified Go, JavaScript, documentation, and bundled Agent files.
- Generate a fresh versioned Linux amd64 FPK using the repository's official WSL/fnpack build flow.

- [ ] **Step 1: Run focused and full tests**

Run: `go test -p 1 ./... -count=1`
Expected: every Go package passes, including Agent routing, legacy authorization, and path-confinement cases.

Run: `go vet ./...`
Expected: exit code 0.

Run: `node --test scripts/*.test.mjs`
Expected: all browser-model, Agent-access, retry, and progress tests pass.

- [ ] **Step 2: Check diffs and documentation links**

Run: `git diff --check`
Expected: exit code 0 with no whitespace errors. Review that no token, generated local state, or unrelated worktree change was added.

- [ ] **Step 3: Build and validate the release FPK**

First bump all displayed/application/package versions from `0.2.1` to `0.2.2` in `internal/app/server.go`, `README.md`, and `packaging/fnos/manifest`; update the package changelog to mention Agent category routing. Then run:

```sh
python -X utf8 scripts/build.py --wsl --fnpack .cache/tools/fnpack-linux-amd64 --output-dir dist/release-0.2.2-categories
python -X utf8 scripts/check_package.py --fpk dist/release-0.2.2-categories/panzhan.fpk
```

Expected: official fnpack reports success and both package checks pass. Record the SHA-256. Ensure the binary includes the generated current `agent-kit.zip`.

## Review Checklist

- Every requirement in `docs/superpowers/specs/2026-10-19-panzhan-agent-download-categories-design.md` maps to a task above.
- All source paths are existing repository paths except the new tests, which are created in the listed test files; the FPK output is generated under `dist/release-0.2.2-categories`.
- Settings mappings are validated beneath the effective default directory; category IDs are allowlisted; API input never accepts Agent-supplied paths.
- New grants snapshot current settings; legacy grants keep their old scope and request shape.
- Hermes receives categories only after actual preview and retains human selection/confirmation, idempotency, and task retry behavior.
- Version `0.2.2` is fixed in the plan and must be applied consistently to app status responses, README, manifest, and package changelog.
