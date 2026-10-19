# Hermes 连接盘栈 Skill 设计

日期：2026-10-19

## 目标

让部署在同一台 NAS 上的 Hermes 能通过一个专用 Skill 配置并使用盘栈。用户创建盘栈 Agent 授权后，可以在 Hermes 的微信会话中触发“连接盘栈”，提供授权令牌，由 Hermes 合并自身 MCP 配置、测试连接并刷新工具列表。

首版面向 Hermes 与盘栈同机部署。Hermes 可原生运行或运行在 NAS 的 Docker 容器中；独立的远程终端沙箱不在首版支持范围内。

## 用户流程

1. 用户在盘栈「设置 → Agent 授权」创建授权，限定下载目录、有效期和能力，并复制一次性显示的令牌。
2. 用户在 Hermes 微信会话中要求“连接盘栈”，并发送令牌。令牌属于聊天内容，可能保留在微信会话、Hermes 会话历史或运行日志中；用户接受通过微信输入。Skill 不得主动在回复、后续提示或诊断输出中复述令牌，也不得把它拼进可见命令行或提交到仓库。
3. Skill 确认运行环境能访问当前 Hermes profile 的配置和命令。若无法确认配置归属，停止并说明需要从 NAS 上正确的 Hermes 实例执行。
4. Skill 确认盘栈 MCP 地址。若 Hermes 与盘栈共享主机网络命名空间，可使用 `http://127.0.0.1:9867/mcp`；Hermes 在独立容器时，使用该容器可达的 NAS 宿主机地址。地址不可达时向用户询问可访问的 NAS 地址，不扫描局域网。
5. Skill 将令牌保存为 Hermes profile 的 `PANZHAN_AGENT_TOKEN` 环境变量；MCP 配置中的 Authorization 只引用 `${PANZHAN_AGENT_TOKEN}`，不写入令牌字面值。
6. Skill 在当前 profile 的 `mcp_servers.panzhan` 下添加 Streamable HTTP 服务配置，保留其他 profile 设置和 MCP 服务。若该名称已配置，比较现有值并先询问是否更新，不静默覆盖。
7. Skill 运行 `hermes mcp test panzhan`，确认连接成功并发现预期工具，然后执行 `/reload-mcp`。只在这些验证成功后报告完成。
8. 同一 Skill 提供日常搜索、预览、明确确认后下载和任务查询流程，沿用现有 [Agent 操作说明](../../../integrations/agents/AGENT-INSTRUCTIONS.md)。

## 配置约定

MCP 服务配置使用当前仓库的 Hermes 示例作为基础：URL 为 `/mcp`，请求头为 `Authorization: Bearer ${PANZHAN_AGENT_TOKEN}`，传输为 Streamable HTTP，工具超时为 120 秒。环境变量值由 Hermes 的配置机制保存到活动 profile 的 `.env`；Skill 不将令牌放到 `config.yaml`、技能文件或仓库。

配置更新必须是结构化合并，不能重写整个 `config.yaml`。执行前保存可恢复备份；失败时保留原配置并报告错误。写入令牌时避免把令牌放入命令参数、shell 历史或可见诊断输出。由于令牌由聊天消息提供，Skill 无法保证 Hermes、微信平台或模型工具调用记录不保存原始消息或令牌；因此建议为接入创建短有效期授权，连接测试完成后若不再需要则在盘栈撤销。令牌授权仍由盘栈控制，Skill 不请求管理 Cookie、管理密码或其他盘栈凭据。

## 部署与运行边界

- Hermes 原生部署在 NAS 时，MCP 地址通常为 loopback 地址。
- Hermes 与盘栈处于不同 Docker 网络命名空间时，容器内的 `127.0.0.1` 不指向 NAS 宿主机。Skill 必须要求一个从 Hermes 容器可达的 NAS 地址，并通过 MCP 测试验证。
- Hermes 网关消息会话可以接收用户显式发送的令牌。此路径不使用 Hermes 本机 CLI 的安全输入提示；令牌会出现在微信历史中，且其他副本是否保留取决于 Hermes、微信平台及其日志配置。Skill 不回显令牌，保存后只报告已保存。
- 首次启用 Skill 的分发/安装流程不属于本设计；首版假定该 Skill 已存在于 Hermes 的技能目录中。
- 下载完成通知转发、OpenClaw 配置、局域网地址自动发现、远程 SSH 终端沙箱、自动新建或撤销盘栈授权均不在首版范围内。

## 失败处理

- 无法识别当前 profile、Hermes 主机或配置路径：不写文件，说明需要在 NAS 上当前 Hermes 实例中运行。
- 盘栈地址连接失败：不保存成功状态；提示检查 NAS 地址、端口 9867 和容器网络。
- 令牌被拒绝：不继续工具调用，提示用户在盘栈撤销旧授权并创建新令牌。
- 配置同名冲突：展示服务名和非敏感 URL，不展示 Authorization 值；等待用户决定是否替换。
- 测试失败或工具发现不完整：保留可恢复状态，报告失败步骤，不能声称接入完成。

## 验证

- 对配置更新器做结构化 YAML 合并测试：保留无关 MCP 项，覆盖新增、同名冲突、备份和恢复。
- 测试 Skill 生成的回复和诊断路径不会主动回显令牌；不把聊天平台或 Hermes 上游日志的留存行为描述为已受控。
- 在与 Hermes 同机的测试 NAS 上验证原生部署和 Docker 部署；Docker 测试必须覆盖不同网络命名空间下的可达地址。
- 使用独立测试授权验证 `hermes mcp test panzhan` 的发现结果与 `/reload-mcp` 后的工具可用性。
- 通过 Hermes 微信网关实际输入测试令牌，确认 Skill 不回显令牌，并确认盘栈侧可撤销该授权。

## 依据

Hermes 支持 MCP HTTP 服务、环境变量引用和 MCP 连接测试；修改 MCP 配置后可用 `/reload-mcp` 刷新。盘栈当前提供 `/mcp`、Agent 独立令牌、OpenClaw/Hermes 配置样例及操作说明。本机没有 Hermes 运行时，因此本设计尚未完成 Hermes 端到端验证。
