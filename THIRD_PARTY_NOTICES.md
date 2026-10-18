# 第三方来源与许可

盘栈为独立单进程程序。以下项目是代码移植或协议、解析实现的参考来源；安装包不包含或启动这些项目的服务。整体源码以 GNU Affero General Public License v3.0（AGPL-3.0）提供，完整条款见 LICENSE。标注 or-later 的本项目新增文件适用该标注；上游复用部分保留其原许可条件。

## AList

- 项目：https://github.com/AlistGo/alist
- 快照：v3.65.0，ea628f9a4fb2dcd1f400a4a3f91ccb59b3c1da69。
- 许可：AGPL-3.0，原作者为 AList contributors。
- 使用：`drivers/quark_uc/util.go`、`meta.go` 中的夸克原文件下载地址、User-Agent、Cookie 快照和下载头协议；`internal/net/request.go`、`internal/stream/util.go` 的并发 Range 思路。
- 本项目对应：`internal/quark/client.go`、`internal/download/download.go`。移除了原项目驱动、流与存储框架依赖；分片落盘、哈希日志、发布凭据与恢复逻辑由本项目实现。

## quark-auto-save

- 项目：https://github.com/Cp0204/quark-auto-save
- 快照：1c35e969332e03980f60c34fc15b2467370bafb2。
- 许可：AGPL-3.0，原作者 Cp0204 与 contributors。上游 README 另有用途声明，参照原仓库；本项目未删除或改写该仓库内容。
- 使用：`quark_auto_save.py` 的分享 token/detail、目录、save/task 协议，转存结果 `save_as_top_fids` 与每批最多 100 项。
- 本项目对应：`internal/quark`、`internal/app/tasks.go`。以 Go 移植必要协议；无 Python 服务依赖，不移植清理回收站、自动删除云文件等操作。

## PanSou

- 项目：https://github.com/fish2018/pansou
- 快照：221665fb2c9279f22af4684ea2d8c0c331eac1cc。
- 许可：MIT，完整上游版权声明与许可见 `licenses/PanSou-MIT.txt`。
- 使用：issue #4 中列出的 67 个 `plugin/` 搜索适配器，以及它们所需的 `model`、`plugin`、`config` 和 `util` 共用代码。
- Telegram 频道清单：来自 https://github.com/fish2018/pansou/issues/4 ，issue 更新于 2026-10-09，获取于 2026-10-17；采用 `CHANNELS` 中的 111 个不同用户名（标题写作 110），保存在 `internal/search/telegram_channels.txt`。
- 本项目对应：`internal/search/panplugin/`。适配器在盘栈进程内通过盘栈的搜索入口运行；未迁入 PanSou `main`、API/service HTTP 服务、认证、缓存服务或独立网页。插件所需的第三方管理路由不会由盘栈挂载。结果转换仅保留通过 HTTPS 主机校验的夸克分享，并传递提取码和作品标题。
- 修改：内部模块导入路径改为 `panzhan/internal/search/panplugin/...`；在统一结果转换层适配盘栈模型；注册清单以 issue #4 为准。迁入的上游 Go 文件标注 `SPDX-License-Identifier: MIT`，完整许可仍随包分发。

## golang.org/x/net

- 项目：https://go.googlesource.com/net
- 版本：v0.46.0。
- 使用：HTML parser；许可为 BSD-3-Clause，完整条款见 `licenses/Go-x-net-BSD.txt`。

## Work Sans

- 项目：https://github.com/weiweihuanghuang/Work-Sans。
- 字体经 Google Fonts 分发，使用 SIL Open Font License 1.1。
- 本地字体与完整许可证：`internal/web/assets/fonts/work-sans-latin.woff2`、`internal/web/assets/fonts/OFL.txt`。浏览器直接从本机应用加载字体，不请求外部字体服务。

## PanHub

https://github.com/wu529778790/panhub.shenzjd.com 仅作为调研和交互参考。其仓库采用 PolyForm Noncommercial；盘栈未复制其源码、素材或外部后端接口调用。页面由本项目重新实现。

分发应用时应一并提供本项目对应版本的完整源码、构建说明和以上许可文件。当前源码就是仓库中实际构建版本；FPK 构建流程会附带许可与来源说明。
