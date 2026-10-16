# 盘栈 0.1.0 首版验证记录

日期：2026-10-16。目标：fnOS x86_64，独立原生应用。

## 产物

- `dist/panzhan.fpk`：6,019,309 字节，使用官方 fnpack 1.2.3 Linux amd64 在 Ubuntu WSL 构建。
- FPK SHA-256：`373e1483e69b64d39764d3223f9c24ca75c1dab347ad0da3b271853e37ac6c1d`。
- `dist/panzhan-linux-amd64`：CGO_ENABLED=0 的 Linux amd64 程序。
- Linux 程序 SHA-256：`c03e93db04eb9e28c8680eaf26c0a82ce2c89dd057a26b792438fabb621500d2`。
- `dist/panzhan-0.1.0-source.zip`：对应源码、构建脚本、许可与文档；排除运行数据、凭据、缓存、构建目录和 Git 元数据。

## 实际完成的验证

1. `go test ./...`、`go vet ./...` 通过。
2. 下载与任务测试使用本机 HTTP 协议服务器，完整执行转存响应、文件 ID 解析、原文件下载及真实文件落盘。覆盖 Range、忽略 Range、短响应、取消续传、发布后 checkpoint 前恢复、不覆盖文件、路径越界和恢复记录文件名冲突。
3. 状态测试覆盖缺少密钥、缺少状态时停止启动，旧账号回调隔离，以及暂停中的不确定转存恢复。
4. 搜索 fixture 覆盖部分来源失败、分享去重、相邻提取码、双重 HTML 编码、跨行标题和提取码、真实页面截断 HTML。
5. `node scripts/browser-smoke.cjs` 通过：首次配置、账号保存、目录选择、部分搜索失败、预览、创建任务、详情、Escape、CSP 下进度、详情轮询、关闭详情后列表刷新并保留焦点。五页在 1024、720、360、320 px 无横向溢出，无页面脚本异常。浏览器搜索与分享使用离线响应，不代表实网转存验证。
6. 最终 FPK 经 `python -X utf8 scripts/check_package.py --fpk dist/panzhan.fpk` 验证 checksum、ELF、Linux 执行位及必要资源。Ubuntu WSL 执行六项打包和生命周期测试通过；Windows 自测通过，但三个 Linux 专属测试会跳过。
7. 独立只读复核发现的问题已修复并回归；最终复核范围内没有剩余 P1/P2。

## 搜索实网

Go 引擎经开发机系统代理，在 2026-10-16 09:21（Asia/Shanghai）请求 PanSearch，关键词 `Blender` 返回 45 条夸克链接，按分享去重后 31 条。探测命令：设置 `PANZHAN_LIVE_SEARCH=1` 后运行 `go test ./internal/search -run TestLiveSources -count=1 -v`。

当前开发网络直连会被重置连接，实测通过标准 `HTTPS_PROXY` 环境变量使用已有系统代理。Go 不自动读取 Windows 系统代理。此结果只证明该时刻该网络下搜索能够返回，不保证 NAS 网络可达、链接有效、可以转存或能下载。

蜡笔请求超时；至臻未识别出正常结果结构。两者默认关闭，PanSearch 默认启用。当前只取搜索站首个结果页，不提供“所有网盘资源”的完整索引。

## 尚未验证和首版边界

- 没有使用用户真实夸克凭据，未进行夸克实网转存和下载验收。
- 没有实际 fnOS NAS，未验证手动安装、共享目录 ACL、桌面入口、升级及卸载行为。具体步骤见 `docs/fnos-install.md`。
- 没有实测速度，不能声称达到 AList 同等速度；账号、限速、CDN 和 NAS 网络均会影响下载。
- 首版下载整个分享或链接指定子目录，未提供任意文件多选；任务顺序执行，单文件可多路分片。
- FPK 仅写系统提供的 `panzhan/downloads` 共享目录及其子目录，不提供任意全盘路径写入。
- 不包含 AList、PanSou、QAS、Docker 或 Node 运行时，不自动删除用户云端文件。

完整代码按 LICENSE 与 THIRD_PARTY_NOTICES.md 分发；构建与验证方式已写入 README。

## 直接打开源码页面的反馈

用户通过 file:// 打开 index.html，未启动后端且模块脚本被浏览器限制，故停在初始文字。已在本机启动 HTTP 服务，并通过浏览器确认首次配置页正常。源码 HTML 增加静态说明和本机 HTTP 链接，样式资源改为相对路径；新增 file:// 回归先失败后通过，原有五页与窄屏测试继续通过。重新生成并校验 FPK。
