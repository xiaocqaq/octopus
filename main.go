package main

import "github.com/bestruirui/octopus/cmd"

// Version v0.13.8
// 同步上游预设时保留本分支图片协议的完整路径。
// NOTE: 发布工作流 (.github/workflows/release.yaml) 只在 main.go 变动时触发,
// 并从上面那行读取版本号打 tag 发 Release。改版本号请只改上面那一行。

func main() {
	cmd.Execute()
}
