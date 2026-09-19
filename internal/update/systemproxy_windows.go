//go:build windows

package update

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// systemProxyURL 读取 Windows 的 Internet 设置(WinINET)里的代理地址。
//
// 国内常见的 Clash / v2ray 默认只写这里, 既不设 HTTP_PROXY 环境变量, 也不改 WinHTTP;
// 而 rhttp.Direct() 会显式清掉 Transport.Proxy, 于是这类机器上更新器只能直连 github.com,
// 结果被黑洞 —— 表现就是"能看到有新版本, 一点更新就失败, 版本永远不动"。
func systemProxyURL() (string, error) {
	key, err := registry.OpenKey(
		registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		registry.QUERY_VALUE,
	)
	if err != nil {
		return "", err
	}
	defer key.Close()

	enabled, _, err := key.GetIntegerValue("ProxyEnable")
	if err != nil {
		return "", err
	}
	if enabled == 0 {
		return "", fmt.Errorf("system proxy is disabled")
	}

	server, _, err := key.GetStringValue("ProxyServer")
	if err != nil {
		return "", err
	}

	addr := normalizeWindowsProxyServer(server)
	if addr == "" {
		return "", fmt.Errorf("system proxy address is empty")
	}
	return addr, nil
}

// normalizeWindowsProxyServer 把 ProxyServer 的取值转成 Go 能用的代理地址。
// 取值可能是 "host:port", 也可能是 "http=host:port;https=host:port" 这类按协议分开的列表;
// 后者取 http/https 那一项 —— 更新走的是 HTTPS, 代理本身通常是同一个端口。
func normalizeWindowsProxyServer(server string) string {
	server = strings.TrimSpace(server)
	if server == "" {
		return ""
	}

	if !strings.Contains(server, "=") {
		return withScheme(server)
	}

	for _, part := range strings.Split(server, ";") {
		pair := strings.SplitN(part, "=", 2)
		if len(pair) != 2 {
			continue
		}
		scheme := strings.ToLower(strings.TrimSpace(pair[0]))
		if scheme == "http" || scheme == "https" {
			return withScheme(strings.TrimSpace(pair[1]))
		}
	}
	return ""
}

// withScheme 给缺协议的地址补上 http://, url.Parse 要求有协议才能识别出主机。
func withScheme(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if strings.Contains(addr, "://") {
		return addr
	}
	return "http://" + addr
}
