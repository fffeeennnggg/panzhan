# PanZhan Hermes Skill

This package contains one Hermes Skill for both initial PanZhan MCP setup and everyday PanZhan use. It supports a Hermes profile on the same NAS, including native and Docker deployments where the Hermes process can access its own profile configuration.

## Install

Install into the profile that runs the Hermes WeChat gateway.

From a local checkout on the NAS, copy the package directory into the profile's skills directory:

```sh
mkdir -p ~/.hermes/skills
test ! -e ~/.hermes/skills/panzhan || { echo 'Skill already exists; back it up before replacing.'; exit 1; }
cp -R integrations/agents/panzhan ~/.hermes/skills/
```

For a named profile, in the same shell set `profile` to its name and confirm its home with `hermes -p "$profile" config path`; copy into that profile home's `skills/panzhan/` directory. Do not copy `.env` or any credentials into the skill directory. If `panzhan/` already exists, back it up before replacing it.

If the repository is available to Hermes through GitHub, install the repository path with Hermes' Skills command:

```sh
hermes skills install fffeeennnggg/fn/integrations/agents/panzhan
```

For a private repository, Hermes needs GitHub access through its own `GITHUB_TOKEN`; do not send that token to the PanZhan Skill. If Hermes cannot access the repository, use the local copy method. After installation, start a new chat session or run `/reset` so the skill appears in the current session.

## Use

Invoke `/panzhan` in the gateway chat, or ask naturally:

- “连接盘栈” and provide the PanZhan Agent token when prompted.
- “搜索某某资源” to find candidates.
- “盘栈有哪些搜索来源” to list enabled sources.
- “预览第 2 个” to inspect a selected share.
- “下载第 2 个” is clear intent: the skill previews that candidate and proceeds if the actual files match. It chooses only a category ID returned by that preview, using `other` when unsure. “随便下个好的” is ambiguous and requires the user to choose.
- “查一下任务 <task-id>” to check download progress.

For connection, the skill uses `hermes config set` to save `PANZHAN_AGENT_TOKEN` in the active profile's `.env`, merges the `mcp_servers.panzhan` keys, tests with `hermes mcp test panzhan`, and refreshes the session with `/reload-mcp`. It never stores the token in the skill or `config.yaml`.

If `PANZHAN_AGENT_TOKEN` is already set in that profile, the skill stops rather than silently replacing it. To rotate the token, remove the old variable from the same Hermes profile with `hermes -p "$profile" config unset PANZHAN_AGENT_TOKEN`, then start the connection flow again with the new token.

The token is sent as a chat message and may remain in WeChat, Hermes session history, tool invocations, or logs. The skill does not repeat it; use a scoped, short-lived authorization and revoke it when it is no longer needed. Hermes's `config set` takes the value as a command argument, so do not use shell tracing and understand that the command invocation may be retained by the terminal/tool platform.

The automatic completion event relay is a separate optional component and is not installed by this skill.
