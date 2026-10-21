# Hermes 连接盘栈 Skill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship one installable Hermes Skill package that configures PanZhan MCP and supports ongoing search, preview, confirmed download, and task lookup.

**Architecture:** Add a single package under `integrations/agents/panzhan/` with Hermes-compatible `SKILL.md` frontmatter and concise install/use instructions. The skill will use Hermes `config set` for secrets and MCP configuration, existing PanZhan MCP tools for operations, and explicit safety rules for token handling and download confirmation.

**Tech Stack:** Markdown, Hermes Skills format, Hermes CLI, existing PanZhan MCP server.

---

### Task 1: Define the installable package

**Files:**
- Modify: `docs/superpowers/specs/2026-10-19-panzhan-hermes-skill-design.md`
- Create: `docs/superpowers/plans/2026-10-19-panzhan-hermes-skill.md`

- [x] Update the approved design to include one distributable Skill package and its installation instructions.
- [x] Confirm the official Hermes Skill format, GitHub installation route, secret configuration command, and MCP reload/test commands from current Hermes documentation.
- [x] Self-review scope, links, and internal consistency against the approved requirements.

### Task 2: Write the combined Hermes Skill

**Files:**
- Create: `integrations/agents/panzhan/SKILL.md`
- Reference: `integrations/agents/AGENT-INSTRUCTIONS.md`
- Reference: `integrations/agents/hermes-mcp.example.yaml`

- [x] Add valid Hermes frontmatter (`name`, `description`, `version`, Hermes tags/category).
- [x] Write the connection procedure: confirm active Hermes profile and host, identify loopback versus Docker-reachable NAS URL, obtain the user-provided token without repeating it, preserve existing MCP settings, set the secret with `hermes config set`, test with `hermes mcp test panzhan`, then reload MCP.
- [x] Write the daily operation procedure: search, present candidates and unknown facts accurately, preview the selected result, require clear selection and authorization before download, use a stable request ID for retries, and query tasks by ID/context.
- [x] Include failure handling, authorization boundaries, untrusted-resource handling, token-retention limitations, and the rule that failed validation must not be reported as a successful connection.

### Task 3: Package installation and validation instructions

**Files:**
- Create: `integrations/agents/panzhan/README.md`

- [x] Document local installation by copying the package folder into the active profile's skills directory.
- [x] Document GitHub installation for this repository using Hermes' supported repository/path install syntax, including the private-repository `GITHUB_TOKEN` prerequisite, then start a fresh session or reset the current one.
- [x] Document the `/panzhan` command and natural-language trigger examples for connection and routine use.
- [x] Validate frontmatter fields, tool names, config keys, repository links, and absence of live credentials with repository searches and a focused review.
- [x] Confirm the working tree contains only the design update, implementation plan, and Skill package files.

## Self-Review

- Design coverage: same NAS native/Docker URL selection, profile ownership, secret storage, safe MCP merge, validation/reload, search, preview, explicit download confirmation, task lookup, and installability are assigned above.
- Secret rule: the skill never prints the token in its response or diagnostics; because the token arrives in chat, Hermes/WeChat/tool transcript retention cannot be promised away.
- Scope: the package is one skill with one install guide. Background completion notification relay, OpenClaw, SSH terminal sandboxes, and automatic authorization creation/revocation remain excluded.
- Runtime limitation: Hermes is not installed on the development machine, so live Hermes installation and end-to-end chat testing must be performed on the NAS.
