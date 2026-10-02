# Changelog

All notable changes to this project's fork are documented here.
This fork tracks upstream `bestruirui/octopus` and carries fork-specific v0.13.9.x features.

## [v0.13.9.7] — 2026-10-02

**糖果(智商)测试接入模型监控** — 用一道固定的糖果题测模型"有没有变笨", 与心跳测活共用同一套结论灯。

### Added
- **07a6dde** 监控任务内置智商题 — 定时任务在轮转测活时发出糖果题, 判分只看回答里的数字; 结论分 正常 / 降智 两态, 请求失败单独一态(不混进"降智")。
- **9f3b737** 监控卡片加糖果测试按钮 — 卡片上可手动对某条凭据/模型发题, 结论只显示 正常/降智。
- **03e554c** 行内结论灯 — 模型监控页与分组页的每一行都换成「心跳 + 糖果」两颗灯, 去掉原来的闪电图标; 只用颜色表示通过/未通过, 详情鼠标悬停才显示。
- **544e0f3** + **bf2a3a0** 监控目标改为 `渠道/凭据/模型` 三段(斜杠分隔, 与卡片行同面孔)。
- **3abb2f8** 糖果题的题面与标准答案可在设置弹窗里改(默认内置糖果题, 答案 `21`); 判分改为"回答里出现答案即算通过"(整数按数值比较, 文本按大小写不敏感包含), 题干再啰嗦也能判对。
- **cc1b811** 每条监控任务一个糖果开关 — 开着时每一拍直接发糖果题(糖果题本身就包含心跳, 故不再额外发), 关掉只发 `hi` 心跳。

### Fixed
- **300044a** 糖果判分兼容话多的回答 — 原来只认简短回答, 长回答会被误判降智; 现在按"整段回答取末尾数字 / 自报标记 / 末行数字"三层抽取, 并认全 OpenAI Chat、Anthropic Messages、OpenAI Responses 三种信封(推理与 thinking 文本不参与判分)。
- **710166b** 糖果题题面改成"只要答案、不要思考", 判分只认简短回答。
- **c62a792** 编辑监控任务时不再改动监控范围 — 原样打开、原样保存曾把该目标在测的凭据收窄成一条, 凭据候选还没到时会写进排除项、甚至清空整条目标。
- **92aa193** 日志页「总耗时」恢复端到端口径, 不再只显示响应阶段。

### Notes
- 糖果测试默认开启(新建与存量任务都是), 每一拍都是一道完整题目: 实测一次约 10s, 上游流量约为原来(每 6 拍一道)的 6 倍。不想付这个代价就在任务卡片上把糖果关掉, 那颗灯会回到快的心跳。
- 判分语义: 整数答案按数值相等比较(`210` 不会撞上 `21`, `021` 算命中), 文本答案按大小写不敏感包含; 答案只从"可见正文"里读, 合法 JSON 但读不出回答时不会拿 `usage` 里的 token 数当答案。

## [v0.13.9.6] — 2026-10-01

**合并上游更新** (upstream `v0.13.7` ~ `v0.13.9`)。

### Merged
- **e088b8b** Merge remote-tracking branch `upstream/master` (12 upstream commits since `33942bf`):
  - `0538c3e` 分组编辑新增置顶置底 — 在分组编辑器的成员列表里新增 ↑/↓ 按钮对成员排序。
  - `8e93fa6` 日志展示使用的渠道的 key — 在日志列表与详情里显示 `channel · key` 复合标签。
  - `ecb4923` 增加思考等级 — 在日志里显示 `reasoning_effort`（high/medium/low）徽标。
  - `ecb4923` 优化日志布局 — 日志详情弹窗新增轮次列表，支持多轮对比。
  - `357787d` + `aa07f77` 增加更多模板预设 — 渠道创建新增 4 个预设（volcengine-coding-plan, qwen-token-plan, kimi-code, glm-coding-plan），每含中/英文描述。
  - `bdc9948` 子路径用于反向代理场景 — API 请求与 SW 注册改用 `document.baseURI` 相对路径。
  - `276ec89` 创建渠道错误时弹窗提醒 — 弹窗显示保存失败的 toast。
  - 版本提交 (`0e1c3fc` v0.13.7, `758b582`, `9357a32` v0.13.8, `9f91bad`, `a94a740` v0.13.9)。

### Fixed
- **ffe93d3** 监控页「多久以前」不再冻结 — React Compiler 会缓存住 `describeProbedAt` 里的 `Date.now()` 读数，导致"刚刚"永远不更新。修复为页面级 `useProbeClock()` 每 10s 把 `now` 放进 React state，传给 `describeProbedAt(probed_at, now, t)`。
- **ffe93d3** 分组页延迟定时兜底 — `useGroupList` 新增 `refetchInterval`（30s），作为事件流（SSE）之外的兜底轮询。

### Notes
- 删除 fork 旧机制: `RetryErrors` 后端机制 + `state_reasoning_test.go` / `state_retry_test.go` / `log.test.ts` 测试文件。
