# Changelog

All notable changes to this project's fork are documented here.
This fork tracks upstream `bestruirui/octopus` and carries fork-specific v0.14.x features.

## [v0.14.0] — 2026-10-01

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
- 生产环境：https://xiao.xlingo.fun/
