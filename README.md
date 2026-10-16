# 盘栈

飞牛 fnOS 的独立网盘资源工具。搜索或导入夸克分享，预览目录，转存到自己的夸克账号，再把真实文件下载到 NAS 授权目录。

**当前版本：0.1.0 首个可验证版本，目标 x86_64。** 使用自有 Go 后端、内嵌前端、任务与下载引擎，没有 AList、PanSou、QAS 或 Docker 运行时。许可为 AGPL-3.0，来源见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

## 已实现

- 搜索优先界面：资源搜索、下载任务、网盘账号、搜索来源、设置；支持手机宽度。
- 多来源聚合：现有网页来源、Telegram 公开频道与 PanSou issue #4 清单中的 67 个搜索插件在盘栈进程内注册；搜索结果只接收经过 HTTPS 主机校验的夸克分享，保留提取码和作品标题。网页插件与频道任务沿用独立并发、逐批显示、暂停/继续、退避重试和 LRU 搜索缓存。需要账号或专属配置的来源会在来源页标记。
- 新安装默认启用聚合来源；旧安装原先仅启用 PanSearch 时会升级到新来源集合，自定义来源配置保持原样。蜡笔已因失效移除；至臻不是可靠来源，也不再列出。BT 搜索结果不能进入夸克转存链路，因此不接入 Nyaa、The Pirate Bay。所有结果仍校验并只保留夸克分享链接。
- 直接导入夸克分享、提取码和链接指定子目录；预览当前分享目录。
- 必要夸克协议移植：分享 token、分页、每批最多 100 项转存、转存后文件 ID、原文件直链和会话匹配请求头。
- 持久化任务与转存意图、已知 task_id 恢复查询；不确定转存不自动重放，可在详情核对或确认空批次后重新提交。
- Range 并发下载、已完成分片的 SHA-256 日志与续传、短响应拒绝、服务器忽略 Range 时改为从头完整下载。
- 文件大小、本地 SHA-256，以及存在上游 MD5 时的 MD5 检查；已存在文件不会覆盖。
- 管理密码、一次性安装令牌、同源写入保护、加密 Cookie、本机授权目录约束。
- Linux amd64 单程序构建与官方 fnpack 原生 FPK；可进行 Linux 生命周期和归档校验。

## 本地运行

安装 Go 1.26 或更新版本：

```sh
go run ./cmd/panzhan -listen 127.0.0.1:9867 -data ./data -root ./downloads
```

打开 http://127.0.0.1:9867 。从 `data/setup-token.txt` 读取首次安装令牌并设置至少 12 字节的密码。进入“网盘账号”保存自己夸克网页端的 Cookie 并验证，再选择授权目录。页面不会回显完整 Cookie。

不能双击 `internal/web/assets/index.html` 预览应用：`file://` 没有后端 API，浏览器也会限制模块脚本加载。请先启动上述程序，再通过 HTTP 地址打开。Windows 已构建时也可运行 `dist/panzhan.exe -listen 127.0.0.1:9867 -data data -root downloads`。

监听地址默认仅本机；NAS 包通过局域网端口 9867 打开，并使用应用自己的密码认证。下载根目录由启动参数指定，页面仅浏览其内部目录。每个任务创建“资源名-任务编号”子目录。

## FPK

安装包输出：`dist/panzhan.fpk`。原生程序：`dist/panzhan-linux-amd64`。

```sh
python scripts/build.py --fnpack /path/to/official/fnpack
```

Windows 需使用已配置的 Ubuntu WSL 和官方 Linux fnpack，以保留归档中的 Linux 执行位：

```powershell
python -X utf8 scripts/build.py --wsl --fnpack .cache/tools/fnpack-linux-amd64
```

完整安装、共享目录、首次令牌及升级说明见 [docs/fnos-install.md](docs/fnos-install.md)。安装包内默认使用系统创建的 `panzhan/downloads` 共享目录，不提供全盘写入权限。

## 验证与边界

```sh
go test ./...
go vet ./...
python -X utf8 scripts/check_package.py --self-test
python -X utf8 scripts/check_package.py --fpk dist/panzhan.fpk
```

浏览器测试：先构建 `go build -o dist/panzhan.exe ./cmd/panzhan`，再运行 `node scripts/browser-smoke.cjs`。测试需本地 Playwright/Chrome；可通过 `PANZHAN_PLAYWRIGHT`（包路径）、`PANZHAN_CHROME`（浏览器路径）覆盖默认位置。浏览器测试会创建独立 `.cache` 数据目录；搜索、分享响应使用离线 fixture，不会调用真实网盘。

真实来源只读探测（不需要夸克凭据）：设置 `PANZHAN_LIVE_SEARCH=1` 后执行 `go test ./internal/search -run TestLiveSources -count=1 -v`。该测试只报告网络和来源响应，不把零结果或错误当成可用性证明；来源请求格式与解析器有本地 fixture 覆盖。

2026-10-16 在当前开发网络经系统代理验证 PanSearch：关键词 Blender 返回 45 条夸克链接，去重后 31 条；仅验证搜索返回，未验证分享有效或可转存。本机直连被重置连接，Go 不读取 Windows 系统代理；开发运行如需代理可设置标准 `HTTPS_PROXY` / `HTTP_PROXY` 环境变量（不要将本机代理地址照搬到 NAS）。蜡笔曾超时、至臻页面解析失败，已从来源目录中移除。

当前已建立本地 HTTP 协议与文件落盘的端到端测试。尚未在用户的真实夸克账号和飞牛设备完成验收，不能把这些测试等同于夸克实网可用、达到 AList 同等速度或 fnOS 真机兼容。

首版顺序执行任务，单文件可并发；未实现任意文件多选、自动删云端、其他盘下载、订阅追更、限速和媒体整理。空目录不单独下载。使用 FAT/exFAT 等不支持硬链接的保存卷时，原子发布会失败并保留临时文件；当前以 fnOS 常用的支持硬链接文件系统为目标。

## 数据恢复

`state.json`、`master.key` 必须一同备份；状态与密钥任一缺失时会停止启动，不会静默重置已有安装。任务状态在写入成功后才确认，断电/重启会核对已完成分片。下载目录内的 `.pz.part`、`.pz.json`、`.pz.json.tmp`、`.pz.receipt` 是恢复记录，不要在任务尚未完成时删除；分享中占用这些后缀的文件会被拒绝，需先在网盘更名。取消任务只停止处理，不删除云端或本地文件。

遇到不确定转存，先在夸克核对 `/盘栈转存/任务编号/batch-编号`，再使用任务详情中的核对操作。程序不会替用户删除云端内容。

调研与方案：[能力提取](docs/research/2026-10-15-capability-extraction.md)、[可行性与页面设计](docs/research/2026-10-15-product-feasibility-and-pages.md)、[当前实施计划](docs/superpowers/plans/2026-10-16-independent-app.md)。旧的服务打包方案已经撤回。
