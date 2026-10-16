# 飞牛网盘下载 Implementation Plan

> **已撤回，禁止执行。** 用户于 2026-10-15 明确纠正：需要源码能力调研与全新程序，不接受将上游应用或服务一起打包。以下是旧方案记录，不再具有执行授权。以 [能力提取调研](../../research/2026-10-15-capability-extraction.md) 为准。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Track steps with checkboxes. The user approved the design and execution; no additional plan approval is needed.

**Goal:** Build an x86_64 native FPK with one authenticated UI for PanSou search, QAS-based Quark saving, AList copying, persistent progress, and verified NAS output.

**Architecture:** A Go HTTP application owns SQLite task state and authenticated APIs. A private Python JSON worker imports pinned QAS core without its WebUI, while private AList and PanSou processes supply copy and search. A Vue 3 TypeScript frontend is served from static build output. Local development also accepts existing service endpoints.

**Tech Stack:** Go 1.26, modernc SQLite, Python 3.12 standalone with requests/natsort/treelib, Vue 3, TypeScript, Vite, official fnpack. Target Linux amd64. Use the existing empty project directory; there is no existing Git repository or worktree to isolate.

## Shared contracts

All JSON API responses use `{data: value}` or non-2xx `{error: string}`. Cookie sessions are HttpOnly/SameSite strict, authentication required except setup-status, first setup and login. Mutations enforce same-origin. Credentials and extraction codes are never included in task-list responses or logs.

```text
GET  /api/setup                         -> {data:{initialized:boolean}}
POST /api/setup {password}              -> sets session
POST /api/login {password}              -> sets session
POST /api/logout                       -> clears session
GET  /api/settings                      -> redacted SettingsView
PUT  /api/settings SettingsInput        -> redacted SettingsView
POST /api/diagnostics                   -> component status array
GET  /api/directories?path=relative     -> [{name,path}] under authorized root
GET  /api/search?q=keyword              -> {results:[{title,url,source,password}],warnings:[]}
GET  /api/tasks                         -> Task[]
POST /api/tasks {title,url,password,destination} -> Task
POST /api/tasks/{id}/cancel             -> Task
POST /api/tasks/{id}/retry              -> Task
GET  /api/tasks/{id}                    -> Task with per-file state
GET  /healthz                          -> {status:"ok"}
```

SettingsInput: `quark_cookie, alist_url, alist_token, alist_quark_path, alist_local_path, pansou_url, pansou_token, download_root, quark_save_path`. Empty secret input preserves existing secret. download_root is startup-authorized via `FN_DOWNLOAD_ROOT` or trusted local CLI config; web settings cannot expand outside this grant. SettingsView adds `quark_cookie_set, alist_token_set, pansou_token_set`; secrets are omitted. UI must use API error text without injecting HTML.

Task: `id,title,url,destination,status,error,created_at,updated_at,total_bytes,completed_bytes,files`. File: `path,size,status,progress,upstream_id`. States: `queued,inspecting,saving,waiting_visibility,downloading,verifying,completed,failed,canceled,needs_attention`. Destination is a relative NAS directory below authorized root. Unknown copy or transfer outcome must enter `needs_attention`; it must never automatically duplicate an upstream side effect.

Worker stdin: JSON `{action, cookie, share_url, password, save_path, checkpoint_dir}`. stdout: one JSON object `{files:[{path,size}],...}` or `{error:...}`; redirect QAS console output away from stdout and sanitize failures. Use a controlled worker checkpoint file to persist transfer identity and submission intent, reconciling remote manifests before retrying. Commands inspect/save must handle nested directories and original names with bounded page/depth/item/time limits.

## Task 1: Native backend and QAS worker

Files: `go.mod`, `cmd/fn-cloud/main.go`, `internal/{config,store,paths,upstream,jobs,httpapi}/*.go`, `worker/quark_worker.py`, tests in matching packages, `worker/test_worker.py`.

- [ ] Write behavior tests first: unauthorized access, setup race, secret redaction, path traversal and symlink escape, duplicate active task creation, transfer accepted but incomplete, AList HTTP 200 business failure, nested files, missing copy task on recovery, conflicting existing file, cancellation and restart persistence. Run `go test ./...` and `python -m unittest discover -s worker -p 'test_*.py'` to observe the missing implementation failures.
- [ ] Implement SQLite repository with atomic state updates and unique active source/destination guard. Persist file manifest and each copy submission intent before the side effect. Transactions protect concurrent API requests and scheduler updates.
- [ ] Implement QAS JSON worker by importing the unmodified pinned upstream file. No upstream WebUI or auto-download plugins run. Require an exact expected manifest before copy, bounded listing/polling and explicit ambiguity recovery. Set network timeouts even when upstream omits them. Cookie enters through stdin, never command arguments.
- [ ] Implement AList client against pinned v3.65.0: business envelope validation, paginated refreshed listing, create directories, one copy per leaf file and task-info/cancel calls. Persist returned IDs; validate task response state using actual upstream definitions. Missing task is reconciled with owned completed output only; it is not silently marked successful.
- [ ] Implement PanSou `POST /api/search` with `kw`, `cloud_types:["quark"]`, response normalization and link dedupe. Validate Quark hosts and schemes; preserve extraction codes through task creation.
- [ ] Implement scheduler and auth/settings/directory/task APIs, controlled concurrency, graceful shutdown. No insecure default password or public internal admin API. Configure trusted endpoints locally; do not expose cookies via any response. Preserve partial/canceled output; do not delete cloud files.
- [ ] Run Go tests, vet and Python tests; exercise an end-to-end fake upstream HTTP service with real SQLite and real local filesystem verification. Record that mocks do not establish actual Quark or fnOS compatibility.

## Task 2: Unified Vue UI

Files: `web/package.json`, lockfile, `web/index.html`, `web/vite.config.ts`, `web/tsconfig.json`, `web/src/{main.ts,App.vue,api.ts,types.ts,style.css,components/*.vue}`.

- [ ] Build a Chinese product UI for a NAS utility: fixed slim sidebar, green accent, clear search results and task rows, readable forms. Avoid marketing layout. Responsive mobile navigation and reduced-motion support.
- [ ] Implement first-admin setup and login, persistent session handling, authenticated search, explicit share-paste action, create-task dialog with real directory browsing, task status/progress, retry/cancel and configuration/diagnostics.
- [ ] Provide genuine empty, loading, error and unconfigured states. No example results or fake downloading states. Disable unsupported actions and preserve useful error messages. Never store credentials in localStorage.
- [ ] Run `npm --prefix web run build`; verify browser at desktop and mobile widths and test setup/login/settings/task creation against the local backend. Include component tests only where interaction warrants them.

## Task 3: Versioned native packaging

Files: `packaging/fnos/{manifest,config/*,cmd/*,app/ui/*,wizard/*}`, `scripts/{prepare-runtime.ps1,build.ps1}`, `third_party/versions.json`, license notices and source provenance.

- [ ] Read official fnOS docs and actual fnpack scaffold before choosing manifest/privilege/resource fields. Download Windows fnpack from the official distribution; record version and checksum.
- [ ] Download locked AList Linux amd64 release, pinned PanSou source, Python standalone Linux x86_64 runtime, QAS core and Python dependency wheels. Record SHA256 values; preserve each license. Build PanSou from pinned source and Go backend with `CGO_ENABLED=0 GOOS=linux GOARCH=amd64`.
- [ ] Stage all runtime components and static UI in `dist/staging`. Configure loopback ports and AList local/Quark mounts using app-private state. Reuse trusted external endpoints in development only when configured.
- [ ] Provide start/stop/status scripts with PID identity checking, graceful stop, health checks and failure cleanup. Runtime paths derive from fnOS environment rather than fixed volume numbers. Keep user downloads on uninstall and configuration on upgrade.
- [ ] Package with official `fnpack build`, inspect resulting archive, check ELF architecture, required files, permissions, manifest checksum and source/license availability. If any external download fails, retain runnable development artifacts and explicitly record which packaging component blocks completion.

## Task 4: Review, documentation and handoff

Files: `README.md`, `docs/{architecture.md,testing.md,fnos-install.md}`, `LICENSE`, `THIRD_PARTY_NOTICES.md`.

- [ ] Review spec compliance independently, then code quality; resolve reported issues and rerun affected checks. Inspect diff and ensure credentials/generated runtime data are ignored.
- [ ] Document local startup, configuration, FPK install, writable directory grant, existing AList connection, first real resource test and speed comparison. Explain copy progress and completion semantics precisely.
- [ ] Run final `go test ./...`, `go vet ./...`, Python worker tests and frontend build, plus packaging validation. Distinguish local verification from real NAS/Quark validation.
- [ ] Deliver concrete artifact paths and report remaining system-version/account/device validation requirements without claiming unavailable real-world tests passed.
