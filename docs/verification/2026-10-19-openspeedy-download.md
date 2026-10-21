# OpenSpeedy 接入盘栈下载引擎实测

日期：2026-10-19。环境：本机 Windows amd64，Go 1.26.4，OpenSpeedy 3.3.17。

## 结论

已在独立的盘栈实验入口中加载 OpenSpeedy DLL，实际调用生产使用的 `internal/download.Engine.Fetch` 下载夸克原文件。首次仅测 5 倍，覆盖不完整。复核新增新版 3.3.17、本机既有旧版 1.7.7，以及 10/15/20/100 倍和下载中切换，共 32 个有效下载测量窗口，仍未建立可重复的吞吐增益。

但复核发现首次遗漏的关键限制：本机默认 IPv4 出口“以太网 2”协商速率只有 **100 Mbps**，理论上限约 **11.921 MiB/s**；未变速的盘栈已多次达到 11.1–11.2 MiB/s。高速样本受链路上限约束，不能据此排除在其他网络条件下的增益。这不是补丁倍率没有设置成功，亦不足以断言 OpenSpeedy 对盘栈在所有环境下无用。详细复核和教程来源见后文。

不能据此认定 OpenSpeedy 对所有网盘无效，也不能把短时波动解释为普遍减速。本次仅验证本机网络、当前账号、一个文件、当前盘栈引擎与该版本 DLL；未在 fnOS 上运行 Windows DLL，未部署 NAS 新版本。

## 实际接入方式

- `cmd/panzhan-speedprobe/main_windows.go`：直接复用盘栈夸克客户端和下载引擎；从现有数据目录只读解密账号，在内存验证会话、获取直链，然后选择性加载 DLL。新版使用 `SP_SetSpeed/SP_IsEnabled`，旧版使用 `ChangeSpeed/GetStatus`。加载失败、未启用或时间钩子报告失败时拒绝实验；旧版没有失败计数接口，记录为 null，另以实际时钟走速验证。
- `scripts/test_openspeedy.py`：独立控制进程，以未加载补丁的 Python 单调时钟测量实际响应体接收字节数。避免使用变速后的进程计时或页面速度作为依据。
- DLL 来自 [官方 3.3.17 发布](https://github.com/game1024/OpenSpeedy/releases/tag/3.3.17)，仅保存在忽略的 `.tmp/openspeedy-probe` 目录。官方 ZIP SHA-256 为 `405b5aede1be27767c62e19a1bea587a25253cf485d54d3d4b1e81fc4a37b658`，与 GitHub 发布资产 digest 一致；DLL Authenticode 状态 Valid，签名主体为 SignPath Foundation。
- DLL SHA-256：`20a3c52d953ec47282c0c7049e8b1b8077b29a10dcffa21812d331cce4a2a19e`。核心源码参考提交 `6afd7a320b64889e04a03c4b762aae7783dc7d10`。

普通盘栈程序、前端设置、FPK 与既有下载逻辑均未接入此实验 DLL。实验入口是 Windows 专用命令，不进入 fnOS 主程序。

## 方法与原始结果

使用同一个已完成任务中保留的转存文件，大小 2,607,095,503 字节；不新建转存、不修改既有任务。下载节点均为 `dl-pc-zb.drive.quark.cn`。每轮新建空临时目录，固定 3 路、10 MiB 分片，先预热 1 秒，再独立计时约 5 秒。每轮接收上限约 128 MiB，最多允许并发读取缓冲带来的约 96 KiB 超出。

| 顺序 | 条件 | 测量窗口接收字节 | 实测速率 MiB/s |
| ---: | --- | ---: | ---: |
| 1 | 不加载 DLL | 58,662,886 | 11.188 |
| 2 | 加载 DLL，1 倍 | 55,444,363 | 10.571 |
| 3 | 加载 DLL，5 倍 | 21,676,953 | 4.132 |
| 4 | 加载 DLL，1 倍 | 55,066,649 | 10.502 |
| 5 | 加载 DLL，5 倍 | 58,146,829 | 11.090 |
| 6 | 加载 DLL，1 倍 | 58,998,759 | 11.253 |
| 7 | 加载 DLL，5 倍 | 57,081,831 | 10.886 |

七轮均为 HTTP/2 206，无下载错误、无提前结束，均在主动停止后清理临时文件。完整结果见同目录 `2026-10-19-openspeedy-results.json`；其中保留实际外部时长、字节数、协议、时钟倍率与二进制哈希，不含 Cookie、文件 ID 或完整下载直链。

5 倍各轮的 Win32 GetTickCount64 / FILETIME 实际走速约为真实时间的 5 倍，Go 的 `time.Since` 实测仍约为 1 倍，`SP_GetHookFailures` 均为 0。因此此次对照确实启用了 OpenSpeedy 时间修改，未发现对应的 5 倍下载吞吐提升。

## 前置失败及限制

首次直接使用保存会话取链时，未加载 DLL 的基线收到 HTTP 412、接收文件字节为 0，该轮判为无效并停止。增加“只在内存验证现有会话”后，同一文件可获得有效 206。这个结果只说明验证会话后的实验路径可用，不足以锁定 412 的服务端原因；账号 Cookie 没有写回。

先行的 3 秒探测分别得到未加载 10.459、加载 1 倍 10.536、加载 5 倍 9.480 MiB/s，仅用于确认请求成功，不与后续不同采样窗口混算。

固定时间顺序、单个文件、较短窗口、上游和路由波动都限制结论。本机结果不能代表 NAS 的当前速度，亦不能代表百度官方客户端内部限速器的行为。

## 验证与复跑

```powershell
go build -o .tmp/openspeedy-probe/panzhan-speedprobe.exe ./cmd/panzhan-speedprobe
python scripts/test_openspeedy.py --clock-only --seconds 2 --warmup 0 --pairs 1 --output .tmp/openspeedy-probe/clock-results.json
python scripts/test_openspeedy.py --verify-account --pairs 3 --seconds 5 --warmup 1 --output .tmp/openspeedy-probe/live-results-final.json
go test ./internal/download ./internal/quark ./cmd/panzhan-speedprobe
go vet ./cmd/panzhan-speedprobe
python -m py_compile scripts/test_openspeedy.py
```

真实下载要求现有 `data/state.json`、`data/master.key` 和可用夸克账号，且本地没有活动下载任务。DLL 路径可通过 `--dll` 指定，数据路径可通过 `--data` 指定；不会自动安装软件或取得其他应用的凭据。

上述 Go 测试及 vet 通过，Python 语法校验通过。最初沙箱内的回环协议测试连接失败，换用允许本地网络的执行环境后通过。测试前后 `state.json` 与 `master.key` 的 SHA-256 一致，实验临时目录已清理。

## 用户质疑后的方法复核

首次七轮的原始结果继续保留，不能用新增窗口覆盖或混算首次中位数。首次测得加载后 1 倍三轮中位数 10.571 MiB/s，5 倍三轮中位数 10.886 MiB/s，差约 3.0%。其不足包括只测 5 倍、只有新版、采样短、倍率均在开始下载前设置，以及未检查本机链路上限。

重新查阅的可核对教程如下。下列速率是作者报告，未在本机复现他们的百度官方客户端环境。

| 来源 | 可核对的条件 | 与盘栈实验的区别 |
| --- | --- | --- |
| [Tbox 教程](https://www.tboxn.com/3437.html) | 百度 Windows 客户端 7.60.7.104，勾选百度相关进程，10 倍 | 目标是百度官方客户端下载组件 |
| [小羿教程](https://xiaoyi.vc/openspeedy-baidu.html) | 百度先开始下载，再设 15–20 倍，以外部网络监控观察；作者另说明其经验不适用于其他网盘 | 支持补测操作顺序，但不能外推夸克直链 |
| [LINUX DO 组件实验](https://linux.do/t/topic/2848134) | 百度 kernel.dll 3.0.20.234，在本地组件实验中加载官方时间补丁 | 是本地组件证据，作者明确未验证真实在线客户端注入的完整链路 |

网上亦能搜到标题宣称支持夸克的视频，宣传范围与实际可复核参数并不一致；不能因此认定所有案例无效，也不能将其等同于盘栈已具备相同提速条件。盘栈直接以 Go HTTP Range 引擎读取夸克直链，不启动百度或夸克官方客户端的下载子进程，故不存在漏勾这些子进程的问题。DLL 直接在执行网络下载的盘栈实验进程内加载。

### 新增对照

每个下载中切换实验保持同一文件、同一取链、同一 `Engine.Fetch` 调用；倍率命令不重启下载。每阶段预热 1 秒，独立 Python 时钟测量 6 秒；每次连续下载硬上限为 512 MiB，随后取消并清理。独立启动的新版高倍率对照则仍为每轮预热 1 秒、测量 5 秒、128 MiB 上限。所有新增下载窗口均为 HTTP/2 206，无提前结束或错误。

| DLL / 实验 | 按时间顺序的倍率 | 对应实际速率 MiB/s |
| --- | --- | --- |
| 3.3.17，下载前设置，两组 | 未加载 / 1 / 20 / 100 / 1 / 20 / 100 | 8.868 / 5.858 / 4.029 / 5.641 / 4.535 / 3.634 / 11.216 |
| 3.3.17，同次下载切换 | 1 / 20 / 1 / 100 / 1 | 11.147 / 11.203 / 11.016 / 11.217 / 9.994 |
| 1.7.7，同次下载切换 | 1 / 20 / 1 / 100 / 1 | 2.349 / 2.310 / 5.952 / 9.009 / 2.945 |
| 1.7.7，100 倍复查 A | 1 / 100 / 1 / 100 / 1 | 3.025 / 2.702 / 3.276 / 11.121 / 11.192 |
| 1.7.7，100 倍复查 B | 1 / 100 / 1 / 100 / 1 | 11.161 / 11.244 / 11.198 / 11.197 / 10.902 |
| 1.7.7，教程低倍率 | 1 / 10 / 1 / 15 / 1 | 11.043 / 11.137 / 11.194 / 11.158 / 7.925 |

旧版首次 100 倍的上升被保留为可疑信号，没有直接当作无效。在随后的复查 A 中，第一次调到 100 倍没有升速，第二次升速后切回 1 倍仍保持高速；复查 B 中各倍率接近链路上限。因此尚不能将首次的 9.009 MiB/s 归因于倍率，也不能把波动窗口当成已经证明减速。

所有补测倍率均以 Windows GetTickCount64 和 FILETIME 的实际走速核验，分别接近设定的 10/15/20/100 倍；Go 计时仍约为真实时间的 1 倍。新版钩子失败计数为 0；旧版无该接口，未伪记为 0。旧版 DLL 来自本机已有 `D:\apps\Speedy-v1.7.7\speedpatch64.dll`，SHA-256 为 `f1c9d851e3be1877d1570b9c2f9494faf965ae4bbb40cd3c5b0386aeeffe8807`，接口签名已核对[官方 v1.7.7 头文件](https://github.com/game1024/OpenSpeedy/blob/v1.7.7/speedpatch/speedpatch.h)及 DllMain 初始化流程，没有重复调用 Init。

Go 1.26.4 Windows amd64 的 `runtime.nanotime1` 从共享的 `_INTERRUPT_TIME` 读取单调时间，源码与实测的“Windows API 时钟变速、Go 单调计时不变”一致。盘栈正常传输路径未设置按时间令牌发放的客户端限速器；重试退避使用 Go timer。这个差异解释了为何不能将百度本地组件实验的增益直接套到盘栈，但不能取代真实网络实验。

### 链路上限与可得结论

复核命令 `Get-NetRoute -DestinationPrefix 0.0.0.0/0` 返回默认出口为“以太网 2”（索引 8）；`Get-NetAdapter` 返回该接口 Up、100 Mbps；`Get-NetAdapterAdvancedProperty` 的 Speed & Duplex 为 Auto Negotiation。因此目前不是已知的“手动强制百兆”设置，而是链路协商结果；尚未定位网线、交换机端口、驱动或硬件中的具体原因，没有更改网卡配置。

100 Mbps / 8 / 2^20 = 11.921 MiB/s，这是未计协议开销的理论上限；本次接收字节统计仅包含 HTTP 响应体。多次 1 倍基线约 11.2 MiB/s，已接近上限，不能期待在这条链路上观察到教程所述几十 MiB/s。低速波动窗口也没有给出可重复的倍率收益。

可得结论应限定为：**已排查本次倍率未生效、旧版接口不兼容、只在下载前调速等测试遗漏；当前本机盘栈/夸克路径未证实稳定收益，且百兆出口限制了高速样本的判别能力。不能据此断言网盘提速报告不真实，或排除千兆环境、NAS 和其他客户端下载路径的效果。** 要进一步验证提速潜力，应在链路有余量且能复现低速基线的实际环境继续对照。未在 fnOS/NAS 部署或运行该 Windows DLL。

原始补测数据见 `2026-10-19-openspeedy-audit-results.json`，含 32 个有效下载窗口、时钟验证、二进制哈希、网络限制和教程来源。各次实验账号数据哈希均不变。

### 补测复跑

```powershell
go build -o .tmp/openspeedy-probe/panzhan-speedprobe-audit.exe ./cmd/panzhan-speedprobe
python scripts/test_openspeedy.py --exe .tmp/openspeedy-probe/panzhan-speedprobe-audit.exe --clock-only --factors 1 20 100 --pairs 1 --seconds 1 --warmup 0
python scripts/test_openspeedy.py --exe .tmp/openspeedy-probe/panzhan-speedprobe-audit.exe --verify-account --factors 1 20 100 --pairs 2 --seconds 5 --warmup 1
python scripts/test_openspeedy.py --exe .tmp/openspeedy-probe/panzhan-speedprobe-audit.exe --verify-account --switch-in-place --factors 1 20 1 100 1 --pairs 1 --seconds 6 --warmup 1
python scripts/test_openspeedy.py --exe .tmp/openspeedy-probe/panzhan-speedprobe-audit.exe --dll D:/apps/Speedy-v1.7.7/speedpatch64.dll --verify-account --switch-in-place --factors 1 100 1 100 1 --pairs 2 --seconds 6 --warmup 1
python scripts/test_openspeedy.py --exe .tmp/openspeedy-probe/panzhan-speedprobe-audit.exe --dll D:/apps/Speedy-v1.7.7/speedpatch64.dll --verify-account --switch-in-place --factors 1 10 1 15 1 --pairs 1 --seconds 6 --warmup 1
```

复跑时用不同 `--output` 路径保留各次数据；持续窗口触达硬上限会被标记为无效，不能当成限速或提速结果。实验工具的默认窗口已改为 5 秒、预热 1 秒，以避免约 11 MiB/s 时默认持续过久触达 128 MiB 上限。
