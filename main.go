package main

import "github.com/bestruirui/octopus/cmd"

// Version v0.13.9.2
// 「模型监控」页: 一条任务挂多个渠道+模型目标, 进程内轮转测活, 失败即冷却, 结论复用分组徽标展示。
// 间隔按凭据数均摊(每条凭据每「间隔」测一次, 仍一次只打一条上游), 行序可拖拽且即为轮转顺序,
// 测活结论不再按时效消失、只被下一次结论覆盖。
// NOTE: 发布工作流 (.github/workflows/release.yaml) 只在 main.go 变动时触发,
// 并从上面那行读取版本号打 tag 发 Release。改版本号请只改上面那一行。

func main() {
	cmd.Execute()
}
