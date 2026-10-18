# Telegram 频道清单同步

来源：https://github.com/fish2018/pansou/issues/4 。issue 更新时间 2026-10-09T06:16:25Z，读取日期 2026-10-17。

问题：盘栈搜索引擎独立使用 `panHubChannels()` 中的旧版 69 个频道；虽然之前移植了 PanSou 的 67 个搜索插件，其 Telegram 清单没有同步。PanSou 内部 `config` 包的 `CHANNELS` 环境变量不是这份盘栈搜索引擎清单的配置入口。

issue 标题写“TG频道 110个”，但 `export CHANNELS=` 的值实际是 111 个用户名，大小写不敏感去重后仍是 111 个。与旧清单重合 27 个，因此本次更新增加 84 个、移出 42 个；没有直接合并两个版本，也不将被移出的频道标记为已证实失效。

改动：将完整 `CHANNELS` 值按原始顺序保存为 `internal/search/telegram_channels.txt`，由 Go 内嵌；两个旧清单调用点统一读取此快照。优先频道调整为当前清单中的 tgsearchers7、Quark_Movies、Aliyun_4K_Movies、shareAliyun、leoziyuan，共 5 个，其余 106 个沿用普通频道调度。来源启停和并发设置保留。来源页聚合组与 Telegram 选项显示实际频道数。

增加回归检查：清单数量、用户名格式、大小写去重、新清单代表频道、旧清单替换、优先频道均存在且不重复；通过本机 HTTP fixture 验证新增 Panzi88com 可被实际调度并提取夸克分享。该测试不访问真实 Telegram，不代表 111 个频道全部在线或都提供夸克资源。

验证结果：`go test ./internal/search ./internal/app`、`go vet ./internal/search ./internal/app` 通过；17 项前端测试通过；Windows 和 Linux amd64 构建完成；`git diff --check` 通过。本地 9867 服务已更新并成功解锁，来源页实际显示“TG 111 个频道”，展开 Telegram 项显示“111 个频道 · 5 个优先”，原有 75 个来源仍启用。截图 `.cache/redesign-shots/telegram-111-main.png`。
