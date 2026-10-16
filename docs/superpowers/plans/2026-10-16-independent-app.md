# 盘栈独立应用实施计划

> 执行方式：本会话按任务顺序实施并验证。用户已授权开始；不执行已撤回的服务打包方案。

目标：构建可运行的 Go 单进程应用，内嵌“搜索优先”前端，具备夸克分享解析、转存、持久化任务、可恢复下载与 x86_64 FPK 构建流程。

架构：`internal/quark` 为直接 HTTP 协议客户端；`internal/search` 为来源解析与聚合；`internal/download` 为 Range 与校验引擎；`internal/app` 为状态存储、认证、任务执行与 API；`internal/web` 为内嵌页面。没有 AList、PanSou、QAS 运行时。先用本机持久化 JSON 快照，写入采用临时文件、fsync 和原子替换，低并发 NAS 首版不引入数据库服务。

技术：Go 1.26 标准库、原生 ES modules、CSS、Python 标准库打包；HTML 解析使用 `golang.org/x/net/html`（若依赖获取成功）。参考 AGPL 能力的移植代码与整体按 AGPL-3.0-or-later 分发，保留第三方来源。PanHub 不复制代码。

工作区：当前为本任务专用分支 `codex/fnos-cloud-download`，仓库无初始提交，仅有本任务调研文档，直接沿用当前目录，避免创建无有效基线的 worktree。

- [x] 1. `go.mod`、`internal/quark/{share,client}.go` 与测试：覆盖分享链接/提取码/子目录、令牌、分页、转存结果、下载 Cookie 快照与错误响应。运行 `go test ./internal/quark`。
- [x] 2. `internal/download` 与测试：HTTP 测试服务器验证 Range、忽略 Range、短响应、取消续传、校验和不覆盖已有文件；路径限定通过 `os.Root` 实现。运行 `go test ./internal/download`。
- [x] 3. `internal/search` 与测试：提取 PanSou 来源选择器，限超时、限详情数量、去重、保留单源错误。用 HTML fixture 验证，不把本地 fixture 当成线上源实测。
- [x] 4. `internal/app` 与测试：加密账号、PBKDF2 管理密码、会话与同源写入、目录授权、状态快照、任务暂停/继续/取消/重试。转存请求意图先落盘；不确定状态禁止自动重放，提供人工核对。每批 task_id 与转存目录记录落盘。
- [x] 5. `internal/web`：搜索优先导航、首次配置、登录、真实 API 搜索与导入、资源预览、新建任务、任务详情、账号、来源与设置。默认空状态，无假数据。浏览器验证正常/错误状态与窄屏。
- [x] 6. `cmd/panzhan`、`packaging`、`scripts`：启动与优雅退出、Linux amd64 交叉编译、依照官方格式生成 FPK；保留开发启动方式和安装说明。
- [x] 7. `README.md`、`THIRD_PARTY_NOTICES.md`、测试与复核：运行 `go test ./...`、`go vet ./...`、Linux 构建、打包结构校验、浏览器 smoke。记录真实账号、来源网络与 fnOS 设备尚未验证的边界。

验收：本地协议 fixture 端到端任务能保存真实文件、暂停恢复不损坏文件、重启不误重放转存；未认证不读取账号或任务，不允许越授权根目录写入。真实夸克和飞牛设备验收另列操作清单，不把离线测试声称为线上通过。

实际结果：七项开发任务完成；最终产物与验证证据见 [首版验证记录](../../verification/2026-10-16-first-build.md)。真实账号与 fnOS 真机验收仍未进行，未纳入本地通过结论。
