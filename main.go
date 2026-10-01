package main

import "github.com/bestruirui/octopus/cmd"

// Version v0.14.0
// 分组合并上游更新 + 修复监控页「多久以前」不刷新的 bug。
// 间隔按凭据数均摊(每条凭据每「间隔」测一次, 仍一次只打一条上游), 行序可拖拽且即为轮转顺序,
// 测活结论不再按时效消失、只被下一次结论覆盖。
// NOTE: 发布工作流 (.github/workflows/release.yaml) 只在 main.go 变动时触发,
// 并从上面那行读取版本号打 tag 发 Release。改版本号请只改上面那一行。

func main() {
	cmd.Execute()
}
