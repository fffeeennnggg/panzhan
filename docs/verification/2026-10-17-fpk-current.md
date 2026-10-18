# 当前版本飞牛安装包

当前交付版本为 **0.2.0**，安装包 `dist/panzhan-0.2.0.fpk`，大小 17,012,636 字节，SHA-256 为 `f4e83d0a228f48960c9314a2aa7846f2025a747cadc823f9fd60edb6c1457992`。包含通用 Agent MCP/API、独立授权、幂等后台下载和完成通知事件，详见 [0.2.0 验证记录](2026-10-18-agent-integration.md)。已通过包体校验和原生 Linux 启停验证；尚未在用户真实 Agent/微信网关部署联调。

## 历史构建记录：0.1.5

当前验证版本：0.1.5，目标 fnOS x86_64。安装包为 `dist/panzhan-0.1.5-download-test.fpk`（独立构建目录：`dist/speed-0.1.5/panzhan.fpk`），16,929,746 字节，SHA-256 为 `abfe8a7ddd7e722b246fec0f4a2cdfd46887e70d7b3e35f0f482bd6c757627c1`。

2026-10-18 已通过 Go 全量测试、改动包静态检查、17 项前端逻辑测试和 FPK checksum/ELF/执行位检查。包结构自测共 6 项，Windows 支持的 3 项通过、依赖 Linux 的 3 项跳过。用户已在飞牛覆盖安装 0.1.5，并完成真实夸克文件的限时网络接收及临时写盘同步验证，详情见 [下载速度实验](../research/2026-10-17-download-speed-experiments.md)。包中不包含账号、管理密码或本机任务数据。

## 历史构建记录：0.1.0

日期：2026-10-17。应用版本：0.1.0。目标：fnOS x86_64。

- 安装包：`dist/panzhan.fpk`。
- 文件大小：16,892,484 字节，约 16.1 MiB。
- SHA-256：`58deab899ab30e5f4d6d98e63b7ec1cd9d7286eabe067bee23e2acfea59bbafc`。
- 构建：当前工作区源码，`GOOS=linux GOARCH=amd64 CGO_ENABLED=0`，使用 `-trimpath`；飞牛官方 fnpack 1.2.3 Linux amd64，在现有 WSL 精简发行版的独立 Linux 内存临时目录中打包。
- 内容：当前银白/灰/蓝界面、来源批量管理、111 个 TG 频道、文件信息和下载实时信息功能。

## 验证

1. 打包输入检查通过：Linux x86_64 ELF、必要文件、JSON 和脚本 LF 换行。
2. 打包自检：6 项，Windows 可运行的 3 项通过；3 项依赖 Bash 和 Linux /proc 的生命周期测试跳过。当前精简 WSL 不提供 Bash，未在该环境声称完整生命周期脚本测试通过。
3. 最终 FPK 检查通过：官方 payload checksum、ELF、可执行权限、许可、桌面入口、专用应用用户和共享目录声明。
4. 从最终 FPK 读取的程序与当前 Linux 构建逐字节一致，包文件清单不包含本机配置、账号、任务或下载文件。
5. 直接运行包内 Linux 程序，在隔离临时目录初始化，读取 `/api/status` 得到 `configured=false`、`authenticated=false`、`version=0.1.0`，随后 SIGTERM 正常停止。

首次安装需要独立初始化管理密码和夸克连接。未携带本机已有密码、Cookie 或任务记录。真实 fnOS 安装、桌面入口及共享目录权限由用户试装验证；步骤见 `docs/fnos-install.md`。
