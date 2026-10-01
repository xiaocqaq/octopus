package main

import "github.com/bestruirui/octopus/cmd"

// Version v0.13.9.6
// 合并上游更新 (v0.13.7 ~ v0.13.9): 分组编辑置顶置底、日志展示渠道 Key 与思考等级、
// 渠道模板预设扩充至 17 个、子路径反代、创建渠道失败弹窗提示。
// 修复: 监控页「多久以前」不再冻结(React Compiler 缓存住了 Date.now() 读数),
// 分组页延迟在事件流之外补 30s 定时兜底。
// 「模型监控」页: 一条任务挂多个渠道+模型目标, 进程内轮转测活, 失败即冷却, 结论复用分组徽标展示。
// 间隔按凭据数均摊(每条凭据每「间隔」测一次, 仍一次只打一条上游), 行序可拖拽且即为轮转顺序,
// 测活结论不再按时效消失、只被下一次结论覆盖。
// NOTE: 发布工作流 (.github/workflows/release.yaml) 只在 main.go 变动时触发,
// 并从上面那行读取版本号打 tag 发 Release。改版本号请只改上面那一行。

func main() {
	cmd.Execute()
}
