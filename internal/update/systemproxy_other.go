//go:build !windows

package update

import (
	"fmt"
	"os"
	"strings"
)

// systemProxyURL 在非 Windows 平台上取环境变量里的代理地址。
//
// 非 Windows 没有统一可读的系统代理设置(macOS 要解析 `scutil --proxy`, Linux 各家桌面环境不一),
// 而这类环境下的代理通常就是靠环境变量告知进程的, 因此这里只认环境变量。
func systemProxyURL() (string, error) {
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value, nil
		}
	}
	return "", fmt.Errorf("no system proxy configured in environment")
}
