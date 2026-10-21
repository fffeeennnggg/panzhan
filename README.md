# 盘栈

盘栈是面向 NAS 的夸克网盘资源搜索与下载工具，提供网页界面、搜索聚合、夸克分享预览、转存和下载任务管理。它以 Go 单程序运行，前端静态资源嵌入程序中。

**当前版本：0.2.2。** 主要面向飞牛 fnOS x86_64；仓库也提供绿联 UGOS Pro ARM64 的 Docker 部署文件。项目采用 AGPL-3.0，第三方代码与资源的来源及许可见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

## 功能

- 聚合网页来源、Telegram 公开频道和 PanSou 插件搜索夸克分享，并检查分享状态。
- 预览分享目录、文件类型和大小，再转存到自己的夸克账号。
- 将文件下载到 NAS 授权目录，支持并发分片、断点恢复、完整性校验和临时网络故障自动重试。
- 管理下载任务、搜索来源和账号；提供适配桌面与手机宽度的网页界面。
- 提供范围受限的 MCP 与 HTTP API，供 OpenClaw、Hermes 等 Agent 搜索、预览和创建下载任务。

搜索结果仅保留夸克分享。部分搜索来源需要额外配置或受网络环境影响，可在“搜索来源”页面查看状态；标题中的清晰度等标签不代表已检验文件实际质量。

## 平台支持

| 方式 | 目标平台 | 说明 |
| --- | --- | --- |
| 原生 FPK | 飞牛 fnOS x86_64 | 安装向导设置密码，下载保存到应用获授权的共享目录 |
| Docker / Compose | 绿联 UGOS Pro ARM64 | 从源码构建镜像，独立挂载配置与下载目录 |
| 源码运行 | 安装 Go 的开发环境 | 用于本地开发、测试与调试 |

程序内置搜索、转存和下载功能，无需另行运行 AList、PanSou 或 QAS 服务。

## 本地运行

需要 Git 和 Go 1.26 或更新版本：

```sh
git clone https://github.com/fffeeennnggg/fn.git
cd fn
go run ./cmd/panzhan -listen 127.0.0.1:9867 -data ./data -root ./downloads
```

首次运行会在 `data/setup-token.txt` 生成一次性设置令牌。打开 <http://127.0.0.1:9867>，使用令牌设置至少 8 个字符的管理密码。登录后，在“网盘账号”页面配置自己的夸克 Cookie，并选择授权下载目录。

默认监听地址仅限本机。NAS 部署时请按对应部署文档设置网络访问，并避免将未配置 HTTPS 的管理端口直接暴露到公网。

前端由后端提供服务，请通过上述 HTTP 地址访问；直接打开 `internal/web/assets/index.html` 无法使用应用。

## 构建与部署

飞牛 fnOS 原生安装包为 Linux x86_64 `.fpk`。安装 Go、Python 3 和官方 `fnpack` 后运行：

```sh
python3 scripts/build.py --fnpack /path/to/fnpack
```

默认输出为 `dist/panzhan.fpk`，可通过飞牛应用中心手动安装。也可以指定版本目录保存产物：

```sh
python3 scripts/build.py --fnpack /path/to/fnpack --output-dir dist/release-0.2.2-final
python3 scripts/check_package.py --fpk dist/release-0.2.2-final/panzhan.fpk
```

Windows 开发环境的 WSL 构建方式、手动安装、升级及权限说明见 [飞牛安装指南](docs/fnos-install.md)。Agent 配置见 [Agent 接入说明](docs/agent-integration.md)。

绿联 UGOS Pro ARM64 的 Docker 部署文件位于 [`deploy/ugreen-docker`](deploy/ugreen-docker/README.md)。该方式使用 Docker 和 Compose，不生成或安装 UPK。

## 开发与测试

```sh
go test ./...
go vet ./...
python3 -X utf8 scripts/check_package.py --self-test
```

前端模块测试使用 Node.js：

```sh
node --test scripts/*.test.mjs
```

本仓库保留应用源码、必要资源、测试、部署配置与文档，不附带预编译程序、安装包或宣传视频。构建脚本会在 `dist/` 写入本机生成物；这些构建产物不纳入源码仓库。发布包应与对应版本的完整源码、构建说明和许可文件一起分发。

## 项目结构

| 目录 | 内容 |
| --- | --- |
| `cmd/panzhan/` | 主程序入口 |
| `internal/app/` | 管理接口、授权、任务和持久化 |
| `internal/download/` | 下载、分片、校验与恢复 |
| `internal/quark/` | 夸克分享与账号接口 |
| `internal/search/` | 搜索来源、调度与插件 |
| `internal/web/` | 嵌入式网页资源 |
| `packaging/fnos/` | 飞牛安装包模板和生命周期脚本 |
| `deploy/` | Docker 部署配置 |
| `integrations/agents/` | Agent 配置示例和 Hermes Skill |
| `scripts/` | 构建、验证和辅助工具 |
| `docs/` | 安装、接入及技术记录 |

## 数据与安全

运行数据保存在启动参数指定的 `data` 与 `downloads` 目录中，包含管理状态、加密后的网盘会话、任务记录及下载文件。备份时请将 `state.json` 与 `master.key` 一并保存；不要提交运行数据、账号 Cookie、管理密码或 Agent Token。

Agent 令牌只返回一次，可单独撤销。下载 API 使用预览确认和授权目录约束；不要把管理密码、Cookie 或 Agent Token 放进 issue、日志、截图或公开配置文件。

下载中的 `.pz.part`、`.pz.json`、`.pz.json.tmp` 和 `.pz.receipt` 用于断点恢复，任务未完成时请保留。暂停或取消任务只停止处理，不删除本地文件或云端内容。转存结果不确定时，请先核对夸克中的 `/盘栈转存/任务编号/batch-编号`，再通过任务详情核对，避免重复提交。

## 当前限制

- 下载与转存主要支持夸克；不提供其他网盘下载、订阅追更、自动媒体整理或自动删除云端文件。
- 任务顺序执行，单文件可并发；空目录不单独下载。
- 文件发布依赖保存卷支持硬链接，FAT/exFAT 等文件系统不适用。
- 搜索来源、夸克接口和网络状况会影响可用性与下载速度，不承诺固定带宽。
- 最新版本的完整 NAS 生命周期及不同设备兼容性仍需在目标设备验证；本地测试与打包检查不能替代真机验收。

反馈问题时，请注明盘栈版本、NAS 型号、系统版本、部署方式和复现步骤，并提供脱敏后的错误信息。

## 开源协议

盘栈整体以 **GNU Affero General Public License v3.0（AGPL-3.0）** 发布，完整条款见 [LICENSE](LICENSE)。本项目新增文件如标注 `AGPL-3.0-or-later`，适用文件中的标注；复用的第三方代码和资源保留其原版权及许可。

- 可以依照协议使用、研究、修改和再分发，也允许商业使用。
- 再分发源码或二进制时，应保留版权、许可和修改声明，并按协议提供相应的完整源码及构建所需内容；基于本项目形成的受协议约束的修改版本也应遵守 AGPL。
- 如果修改本程序并通过网络向用户提供交互服务，AGPL 第 13 条要求向这些用户显著提供获取该修改版本对应源码的机会。
- 本软件按协议“按原样”提供，不附带担保。以上是便于理解的摘要，具体权利和义务以许可原文为准。

第三方来源、复用范围、上游版本和许可文件详见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。致谢不替代各项目的许可要求。

## 致谢

感谢以下项目及其维护者的工作：

| 项目 | 盘栈中的使用或参考 | 上游许可 |
| --- | --- | --- |
| [AList](https://github.com/AlistGo/alist) | 夸克下载协议、会话与请求头处理，以及并发 Range 下载思路 | AGPL-3.0 |
| [quark-auto-save](https://github.com/Cp0204/quark-auto-save) | 夸克分享、目录、批量转存与任务查询协议 | AGPL-3.0 |
| [PanSou](https://github.com/fish2018/pansou) | 搜索插件及其共用代码；公开 Telegram 频道清单来自其 issue #4 | MIT，见 [许可文本](licenses/PanSou-MIT.txt) |
| [golang.org/x/net](https://go.googlesource.com/net) | HTML 解析能力 | BSD-3-Clause，见 [许可文本](licenses/Go-x-net-BSD.txt) |
| [Work Sans](https://github.com/weiweihuanghuang/Work-Sans) | 网页界面使用的本地字体 | SIL OFL 1.1，见 [字体许可](internal/web/assets/fonts/OFL.txt) |
| [PanHub](https://github.com/wu529778790/panhub.shenzjd.com) | 调研与交互参考，未复制其代码、素材或外部后端接口调用 | PolyForm Noncommercial |

也感谢 Go 生态中的依赖项目与贡献者；直接及间接依赖版本记录在 [go.mod](go.mod) 和 [go.sum](go.sum) 中，使用与分发时仍需遵守各依赖自身的许可。
