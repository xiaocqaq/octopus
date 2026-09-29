package main

import "github.com/bestruirui/octopus/cmd"

// Version v0.13.9
// 新增「模型监控」页: 一条任务挂多个渠道+模型目标, 进程内轮转测活, 失败即冷却, 结论复用分组徽标展示。
// NOTE: 发布工作流 (.github/workflows/release.yaml) 只在 main.go 变动时触发,
// 并从上面那行读取版本号打 tag 发 Release。改版本号请只改上面那一行。

func main() {
	cmd.Execute()
}
