# 盘栈：fnOS 原生安装

本项目提供 Linux x86_64 原生 `.fpk`。包内包含 Go 单文件程序、桌面入口和生命周期脚本；前端由 Go 嵌入，无需 Node.js 运行时，也不需要 Docker。应用内建搜索来源、夸克转存和 NAS 下载功能，无需另行部署搜索或网盘中转服务。包内不包含独立的 AList、PanSou 或 QAS 服务。

## 构建

在项目根目录安装 Go（版本见 `go.mod`）、Python 3，以及官方 [fnpack](https://developer.fnnas.com/docs/cli/fnpack/)。本次使用官方 fnpack 1.2.3 Linux amd64 版。可从官方页面选择开发机对应版本，打包工具本身不会放入 NAS 应用包。

Windows 版 fnpack 1.2.3 会将文件归档为 `0666`，丢失 Linux 程序所需的执行位，因此本项目的 Windows 构建使用已有的 Ubuntu WSL 和官方 Linux fnpack：

```powershell
New-Item -ItemType Directory -Force .cache/tools
Invoke-WebRequest https://static2.fnnas.com/fnpack/fnpack-1.2.3-linux-amd64 -OutFile .cache/tools/fnpack-linux-amd64
python -X utf8 scripts/check_package.py --self-test
wsl --distribution Ubuntu --exec python3 /mnt/e/diancai/fn/scripts/check_package.py --self-test
python -X utf8 scripts/build.py --wsl --fnpack .cache/tools/fnpack-linux-amd64
```

WSL 测试命令中的 `/mnt/e/diancai/fn` 需要替换为实际项目路径。Windows 构建会把暂存目录复制到 WSL 的临时 Linux 文件系统，设置脚本和程序为 `0755` 后调用官方工具，不依赖 Windows 挂载盘的权限模拟。需要事先安装并初始化 Ubuntu WSL；脚本不会自动安装发行版。

已有其他 WSL 发行版时，可通过 `--wsl-distribution` 选择。打包只需要 POSIX shell 和基本文件工具；精简发行版系统盘空间不足时，可用 `--wsl-temp-dir /dev/shm` 指定空间充足的 Linux 临时目录。例如：

```powershell
python -X utf8 scripts/build.py --wsl --wsl-distribution docker-desktop --wsl-temp-dir /dev/shm --fnpack .cache/tools/fnpack-linux-amd64
```

构建过程会规范化启动脚本的 LF 换行，并将 fnpack 的临时文件限制在本次创建、自动清理的打包目录。目标 fnOS 的启动脚本仍依赖 Bash；打包主机不要求安装 Bash。

Linux/macOS 可使用相应的官方 fnpack 可执行文件：

```sh
python3 scripts/build.py --fnpack /path/to/fnpack
```

构建脚本设置 `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`，执行：

```sh
go build -trimpath -o dist/panzhan-linux-amd64 ./cmd/panzhan
```

随后将 `packaging/fnos` 暂存到 `dist/fnos/panzhan`，加入程序、AGPL 许可证、第三方声明及 `licenses` 中的许可证文本，检查 ELF 架构、JSON 与脚本换行，并调用官方 `fnpack build --directory`。最终文件为 `dist/panzhan.fpk`。构建后会再次验证 FPK 内的原生 ELF、Unix 执行位与官方 payload checksum。`--stage-only` 只执行交叉编译与暂存，可用于检查打包输入。

## 安装和首次访问

1. 在 x86_64 飞牛 NAS 的应用中心选择手动安装，上传 `dist` 中生成的 `.fpk`。
2. 选择安装存储空间。系统创建 `panzhan/downloads` 共享目录，并向专用应用用户 `panzhan` 授予访问权限。
3. 启动应用，从桌面“盘栈”入口打开，或在可信局域网访问 `http://NAS地址:9867/`。端口需要可从浏览器所在设备访问；本包没有配置反向代理或统一网关。
4. 安装向导中设置并确认管理密码，安装后从应用入口直接登录。无需 SSH 或读取初始化令牌。
5. 在“网盘账号”页保存夸克网页 Cookie 并验证，在“搜索来源”页启用内建来源。

从旧版升级后仍无法登录，或忘记管理密码时，可在飞牛应用中心打开盘栈的“设置”，重新填写并确认管理密码。保存后应用会重启，使用新密码登录；账号、下载任务和文件保留。仅覆盖升级或普通重启不会改变原密码。

端口入口采用应用自己的登录认证，与 fnOS 登录状态分开。桌面入口默认仅管理员可见。HTTP 端口适合可信局域网；跨网络访问应通过用户自行配置的 HTTPS 反向代理或 VPN。

## 选择其他 NAS 下载位置

首次安装默认使用 `panzhan/downloads`。要写入其他共享文件夹，先在飞牛应用中心的盘栈“设置/权限”中，为盘栈授予该共享目录的读写权限；然后打开盘栈“设置”页，在“NAS 共享目录路径”输入该共享的绝对路径并浏览，选中目标文件夹后保存。

例如路径可能是 `/vol1/video`，实际路径以 NAS 中该共享文件夹的路径为准。授权前，盘栈无法列出该路径；没有读写授权时无法保存。应用不提升自身权限，也不创建任意全盘写入权。更改默认保存位置只影响新任务；已创建任务继续保存到原来的位置。每次安装、更新或重启后，请在“设置/权限”确认其他共享目录授权仍有效。

## Agent 接入（0.2.0）

覆盖升级后，在盘栈「设置 → Agent 授权」创建独立令牌并下载 Agent 接入包。可为同机或其他机器上的 OpenClaw/Hermes 配置 MCP；完成通知的监听器运行在 Agent 主机，微信登录和会话权限由对应网关配置。盘栈安装包不会自动安装 Agent、登录微信或开放公网端口。完整步骤见 [Agent 接入](agent-integration.md)。

## 数据与权限

| 内容 | 位置 |
| --- | --- |
| 应用程序、桌面入口 | `TRIM_APPDEST` |
| 配置、账户信息和任务状态 | `TRIM_PKGVAR` |
| 下载文件 | `TRIM_DATA_SHARE_PATHS` 提供的 `panzhan/downloads` 共享目录 |
| 进程日志 | `TRIM_PKGVAR/panzhan.log` |
| 进程标识和生命周期锁 | `TRIM_PKGVAR/panzhan.pid`、`lifecycle.lock` |

安装后通常可通过 `/var/apps/panzhan/target` 和 `/var/apps/panzhan/var` 的系统链接定位程序与状态目录；脚本实际使用 fnOS 环境变量，未硬编码卷编号。共享目录由 fnOS 管理 ACL，脚本不修改其权限。程序以 `run-as=package` 的专用用户执行，未申请 root、附加系统组、Docker 或全盘访问权限。

下载默认使用系统提供的共享目录。仅在 fnOS 设置中授权其他目录，不会自动改变应用的下载根目录；还需在盘栈设置中选择该已授权目录。应用以专用用户权限访问所选位置，不提升系统权限。

## 启停、升级与卸载

使用应用中心的启动、停止操作。`cmd/main status` 按官方约定以 `0` 表示运行、`3` 表示停止。脚本用私有 PID 文件、`/proc` 可执行文件路径与进程启动时间核对身份，并用 `flock` 串行化操作；停止仅对核对后的进程发送 SIGTERM，等待最多 25 秒，以覆盖服务的 20 秒优雅关闭时间，不使用进程名称批量终止。

升级脚本不清空配置、任务或下载文件。卸载脚本也不主动删除用户数据；fnOS 自身的应用中心卸载选项和共享目录管理行为仍以 NAS 上的提示为准。卸载前请备份应用状态和需要保留的下载文件，重新安装后不要假定系统一定保留应用内部状态。

## 排查与验收

- 启动失败：查看应用中心提示和 `TRIM_PKGVAR/panzhan.log`，确认 9867 未被占用，系统已创建共享目录，应用用户有写入权限。
- 下载目录不可用：检查 fnOS 共享目录和应用权限，恢复 `panzhan/downloads` 的系统授权后重新启动。不要使用 root 模式绕过目录权限。
- 页面打不开：确认应用运行、NAS 地址正确、9867 可访问。公网域名、HTTPS 网关或仅转发 fnOS 管理端口的远程访问方式，不一定能访问此独立 HTTP 端口。
- 停止提示仍在运行：查看日志，等待正在收尾的任务结束后重试；脚本不会强制终止未确认身份的其他进程。

已按官方文档和官方 fnpack 的必要文件/格式检查制作包，并进行 Linux ELF 架构、归档执行权限和 checksum 校验。在 Ubuntu WSL 下的生命周期测试验证了真实 Linux `/proc` 进程核对、正常停止以及忽略指向其他存活进程的 PID 文件。本开发环境没有实际 fnOS NAS，不能把打包成功等同于真机兼容验证，也未声明未经测试的最低系统版本。发布前应在目标 NAS 上验证：手动安装、共享目录 ACL、首次初始化、桌面入口、搜索、账户连接、下载、重启恢复、停止/启动、覆盖升级以及卸载保留行为。生命周期脚本使用 fnOS 的 Bash、`/proc`、`readlink` 和 `flock`。

官方格式参考：[Manifest](https://developer.fnnas.com/docs/core-concepts/manifest/)、[环境变量](https://developer.fnnas.com/docs/core-concepts/environment-variables/)、[应用权限](https://developer.fnnas.com/docs/core-concepts/privilege/)、[共享资源](https://developer.fnnas.com/docs/core-concepts/resource/)、[桌面入口](https://developer.fnnas.com/docs/core-concepts/app-entry/)。
