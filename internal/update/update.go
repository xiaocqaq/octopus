package update

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/rhttp"
	"github.com/charmbracelet/log"
)

// 更新源取自 conf.Repo, 由构建时注入 (build.sh 从 git origin 取), 换 fork 只需重新构建而不必改代码。
// 要求该仓库是 GitHub 或与其 releases API 同形的托管: /repos/{owner}/{repo}/releases/latest
// 与 /releases/latest/download/{asset} 两种路径都要讲得通。
var (
	updateRepo   = repoSlug(conf.Repo)
	updateApiUrl = "https://api.github.com/repos/" + updateRepo + "/releases/latest"
	updateUrl    = "https://github.com/" + updateRepo + "/releases/latest/download"
)

// 元数据查询与归档下载的超时必须分开。
// 发布归档解压前就有二十余兆, 而慢速链路(实测国内直连约 40~70 KB/s)下传完需要数分钟:
// 沿用元数据的 30 秒只够下一两兆, 更新会永远停在 context deadline exceeded, 表现就是
// "点了更新等半天, 版本号还是旧的"。
const (
	metadataTimeout     = 30 * time.Second // 元数据查询: 回 JSON, 30 秒足够。
	downloadTimeout     = 15 * time.Minute // 归档下载: 按最慢链路留足余量。
	downloadIdleTimeout = 45 * time.Second // 下载流连续无新数据超过此时立即失败, 避免更新接口长期挂起。
	latestCacheTTL      = 5 * time.Minute  // 最新版本查询缓存: 5 分钟内复用结果, 避免设置页频繁请求 GitHub。
)

// repoSlug 从仓库地址里取出 owner/repo 两段, 供拼接接口与下载地址。
// 兼容 https://host/owner/repo、SSH 的 git@host:owner/repo、以及结尾带 .git 或斜杠的写法;
// 地址不含主机名时按已给的 owner/repo 处理。
func repoSlug(repo string) string {
	trimmed := strings.TrimSuffix(strings.TrimSpace(repo), "/")
	trimmed = strings.TrimSuffix(trimmed, ".git")

	// SSH 写法 git@host:owner/repo 不是合法 URL, url.Parse 会把它当成没有主机名,
	// 于是整个字符串被当 slug 返回, 拼出的接口地址就错了。
	if strings.HasPrefix(trimmed, "git@") || strings.Contains(trimmed, "@") && strings.Contains(trimmed, ":") && !strings.Contains(trimmed, "://") {
		if idx := strings.Index(trimmed, ":"); idx >= 0 {
			return strings.Trim(trimmed[idx+1:], "/")
		}
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return strings.Trim(trimmed, "/")
	}
	return strings.Trim(parsed.Path, "/")
}

type LatestInfo struct {
	TagName     string `json:"tag_name"`
	PublishedAt string `json:"published_at"`
	Body        string `json:"body"`
	Message     string `json:"message"`
}

var github_pat = os.Getenv(strings.ToUpper(conf.APP_NAME) + "_GITHUB_PAT")

var (
	latestCacheMu sync.Mutex
	latestCache   *LatestInfo
	latestCacheAt time.Time
)

// dialTimeout 是 TCP 拨号超时。
// 默认 Transport 给 30 秒, 而黑洞地址会在握手上耗满这个时间, 让"此路不通→换下一条"白等半分钟;
// 12 秒足够连上可用链路, 又足以让不通的那条尽快让位。
const dialTimeout = 12 * time.Second

// candidate 描述一条可能成功的出网链路。
type candidate struct {
	name      string       // 链路名, 记进日志便于分清是直连还是某个代理不通。
	client    *http.Client // 该链路使用的客户端。
	closeIdle bool         // 自建客户端没有共享连接池, 用完需要归还空闲连接。
}

// candidates 按"最可能成功"的顺序列出可用链路。
//
// 顺序不是直觉上的"先直连": 直连 github.com 在国内常被整段黑洞 —— 实测 TCP 握手要耗满 21 秒
// 才报 connectex 超时, 而 api.github.com 却通, 于是表现成"能看到有新版本, 一点更新就失败"。
// 用户既然配了代理, 就说明本机不是直连环境, 让代理先试能省掉这次必然失败的等待。
//
// 系统代理这一档是必需的: rhttp.Direct() 会显式清掉 Transport.Proxy, 于是 Clash / v2ray 这类
// 只改了系统代理(不设环境变量、也不在应用里填 proxy_url)的机器, 直连必然被黑洞。
// 更新器一直没有这一档, 只要直连不通就永远更新不了 —— 与版本无关, 换哪个版本都一样。
func proxyCandidates() []candidate {
	out := make([]candidate, 0, 2)

	// 应用内设置的代理地址(proxy_url)。
	if hc, err := rhttp.Proxy(); err == nil {
		out = append(out, candidate{name: "app proxy", client: hc})
	} else {
		log.Debugf("app proxy unavailable: %v", err)
	}

	// 操作系统代理。rhttp.New 不缓存连接池, 用完必须归还。
	if addr, err := systemProxyURL(); err == nil {
		if hc, err := rhttp.New(addr); err == nil {
			out = append(out, candidate{name: "system proxy", client: tightenHTTPProxy(hc), closeIdle: true})
		} else {
			log.Debugf("system proxy client failed: %v", err)
		}
	} else {
		log.Debugf("system proxy unavailable: %v", err)
	}
	return out
}

func directCandidate() (candidate, bool) {
	hc, err := rhttp.Direct()
	if err != nil {
		log.Debugf("direct client failed: %v", err)
		return candidate{}, false
	}
	return candidate{name: "direct", client: tightenDirect(hc)}, true
}

// metadataCandidates 查最新版本走 api.github.com: 国内这条常常能直连。
// 代理出口 IP 是机房地址, 未认证限额 60 次/小时按 IP 共享, 很多用户挤在同一出口就会 403。
// 因此元数据先直连, 直连失败再代理。
func metadataCandidates() []candidate {
	out := make([]candidate, 0, 3)
	if cand, ok := directCandidate(); ok {
		out = append(out, cand)
	}
	return append(out, proxyCandidates()...)
}

// downloadCandidates 下发行包走 github.com: 国内直连常被黑洞, 用户配了代理就先走代理。
func downloadCandidates() []candidate {
	out := proxyCandidates()
	if cand, ok := directCandidate(); ok {
		out = append(out, cand)
	}
	return out
}

// cloneWithDialTimeout 复制 Transport 并把拨号超时收紧到 dialTimeout。
func cloneWithDialTimeout(tr *http.Transport) *http.Client {
	cloned := tr.Clone()
	cloned.DialContext = (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext
	return &http.Client{Transport: cloned}
}

// tightenDirect 收紧直连客户端的拨号超时。
func tightenDirect(hc *http.Client) *http.Client {
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		return hc
	}
	return cloneWithDialTimeout(tr)
}

// tightenHTTPProxy 收紧走 HTTP 代理的客户端拨号超时。
// 只处理 Transport.Proxy 这条路径: SOCKS 用的是自定义拨号器(自带超时且语义不同), 覆盖会破坏它。
func tightenHTTPProxy(hc *http.Client) *http.Client {
	tr, ok := hc.Transport.(*http.Transport)
	if !ok || tr.Proxy == nil {
		return hc
	}
	return cloneWithDialTimeout(tr)
}

// newGetRequest 构造带超时与可选鉴权头的 GET 请求。
func newGetRequest(ctx context.Context, rawURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if github_pat != "" {
		req.Header.Set("Authorization", "Bearer "+github_pat)
	}
	return req, nil
}

// doGet 发起 GET 并校验状态码, 成功时返回未读取的响应, 由调用方负责关闭。
func doGet(ctx context.Context, client *http.Client, rawURL string) (*http.Response, error) {
	req, err := newGetRequest(ctx, rawURL)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	// 状态码要先判: 403/404 的正文往往是一段 HTML, 交给上层解析只会得到"响应不是 JSON/zip"
	// 这类与真实原因无关的报错, 把限流或路径错误掩盖掉。
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return resp, nil
}

// metadataCandidateBuilder / downloadCandidateBuilder 允许测试注入候选链。
var (
	metadataCandidateBuilder = metadataCandidates
	downloadCandidateBuilder = downloadCandidates
)

// doRequestWithFallback 依次尝试各条链路取回一份小体积正文(元数据)。
func doRequestWithFallback(rawURL string) ([]byte, error) {
	return requestWithCandidates(metadataCandidateBuilder(), rawURL)
}

// requestWithCandidates 按给定顺序尝试链路, 返回第一个成功的正文。
func requestWithCandidates(cands []candidate, rawURL string) ([]byte, error) {
	var lastErr error
	for _, cand := range cands {
		ctx, cancel := context.WithTimeout(context.Background(), metadataTimeout)
		data, err := readMetadata(ctx, cand.client, rawURL)
		cancel()
		if err == nil {
			return data, nil
		}
		log.Warnf("metadata via %s failed: %v", cand.name, err)
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no usable network route")
	}
	return nil, lastErr
}

func readMetadata(ctx context.Context, client *http.Client, rawURL string) ([]byte, error) {
	resp, err := doGet(ctx, client, rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// download 把发布归档流式落到 dst: 依次尝试各条链路, 全部失败才报错。
// 与元数据查询不同, 这里用 downloadTimeout, 且不把整包读进内存 —— 归档二十余兆,
// 而更新只发生在服务端, 一次多占几十兆内存换不来任何好处。
func download(rawURL, dst string) error {
	return downloadWithCandidates(downloadCandidateBuilder(), rawURL, dst)
}

// downloadWithCandidates 按给定顺序尝试链路把归档流式落到 dst, 全部失败才报错。
func downloadWithCandidates(cands []candidate, rawURL, dst string) error {
	if len(cands) == 0 {
		return fmt.Errorf("no usable network route for download")
	}

	var lastErr error
	for _, cand := range cands {
		err := downloadVia(cand, rawURL, dst)
		if cand.closeIdle {
			cand.client.CloseIdleConnections()
		}
		if err == nil {
			log.Infof("download succeeded via %s", cand.name)
			return nil
		}
		log.Warnf("download via %s failed: %v", cand.name, err)
		lastErr = err
	}

	// 把可执行的处置办法一并带出去: 直连不通时用户唯一能做的就是填一个能用的代理。
	return fmt.Errorf("all download routes failed (last: %w); if this host cannot reach the GitHub release assets, configure a working proxy in Settings (proxy_url)", lastErr)
}

func downloadVia(cand candidate, rawURL, dst string) error {
	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()

	resp, err := doGet(ctx, cand.client, rawURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	written, err := copyWithIdleTimeout(out, resp.Body, downloadIdleTimeout)
	if err != nil {
		return err
	}
	log.Infof("downloaded %d bytes via %s", written, cand.name)
	return nil
}

// copyWithIdleTimeout 让下载同时受总超时和读空闲超时约束。
// http.Client 的总超时只能限制整个请求, 对持续保持连接但不再发送数据的 CDN/代理无效;
// 这里在空闲窗口到期时主动关闭响应体, 解除阻塞的 Read。
func copyWithIdleTimeout(dst io.Writer, src io.ReadCloser, idle time.Duration) (int64, error) {
	type result struct {
		written int64
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		written, err := io.Copy(dst, src)
		resultCh <- result{written: written, err: err}
	}()

	timer := time.NewTimer(idle)
	defer timer.Stop()
	select {
	case result := <-resultCh:
		return result.written, result.err
	case <-timer.C:
		_ = src.Close()
		result := <-resultCh
		if result.err != nil {
			return result.written, fmt.Errorf("download idle timeout after %s: %w", idle, result.err)
		}
		return result.written, fmt.Errorf("download idle timeout after %s", idle)
	}
}

func GetLatestInfo() (*LatestInfo, error) {
	latestCacheMu.Lock()
	if latestCache != nil && time.Since(latestCacheAt) < latestCacheTTL {
		cached := *latestCache
		latestCacheMu.Unlock()
		return &cached, nil
	}
	stale := latestCache
	latestCacheMu.Unlock()

	body, err := doRequestWithFallback(updateApiUrl)
	if err != nil {
		// GitHub 未认证限额按出口 IP 共享, 一个被限流的代理出口会让所有用户一起 403。
		// 这时宁可回上次成功的结果, 也不要让设置页直接报错。
		if stale != nil {
			log.Warnf("get latest info failed (%v); serving cached result from %s", err, latestCacheAt.Format(time.RFC3339))
			cached := *stale
			return &cached, nil
		}
		return nil, err
	}

	var latestInfo LatestInfo
	if err := json.Unmarshal(body, &latestInfo); err != nil {
		log.Debugf("unmarshal body failed: %v", err)
		return nil, err
	}
	if latestInfo.Message != "" {
		return nil, fmt.Errorf("failed to get latest info: %s", latestInfo.Message)
	}

	latestCacheMu.Lock()
	latestCache = &latestInfo
	latestCacheAt = time.Now()
	latestCacheMu.Unlock()
	return &latestInfo, nil
}

// unzipFile 直接从磁盘上的归档解压, 供大体积发布包使用, 避免整包读进内存。
func unzipFile(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		log.Debugf("open zip failed: %v", err)
		return err
	}
	defer r.Close()
	return extractArchive(&r.Reader, dest)
}

func extractArchive(r *zip.Reader, dest string) error {
	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)

		if !isPathInDest(fpath, dest) {
			log.Debugf("invalid file path: %s", fpath)
			return fmt.Errorf("invalid file path: %s", fpath)
		}

		info := f.FileInfo()
		if info.IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}

		if err := extractFile(f, fpath); err != nil {
			return err
		}
	}
	return nil
}

func extractFile(f *zip.File, fpath string) error {
	if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
		log.Debugf("mkdir all failed: %v", err)
		return err
	}

	outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode().Perm())
	if err != nil {
		if err = os.Remove(fpath); err != nil {
			log.Debugf("remove file failed: %v", err)
			return err
		}
		outFile, err = os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			log.Debugf("open file failed: %v", err)
			return err
		}
	}
	defer outFile.Close()

	rc, err := f.Open()
	if err != nil {
		log.Debugf("open file failed: %v", err)
		return err
	}
	defer rc.Close()

	if _, err = io.Copy(outFile, rc); err != nil {
		log.Debugf("copy failed: %v", err)
		return err
	}
	return nil
}

func isPathInDest(fpath, dest string) bool {
	rel, err := filepath.Rel(dest, fpath)
	if err != nil {
		return false
	}
	return filepath.IsLocal(rel)
}
