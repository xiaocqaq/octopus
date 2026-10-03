package update

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpdateCoreRejectsConcurrentRun(t *testing.T) {
	updateMu.Lock()
	defer updateMu.Unlock()

	if err := UpdateCore(); err == nil || !strings.Contains(err.Error(), "update already in progress") {
		t.Fatalf("expected concurrent update rejection, got %v", err)
	}
}

func TestCopyWithIdleTimeoutStopsStalledDownload(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	go func() {
		_, _ = writer.Write([]byte("partial"))
		time.Sleep(100 * time.Millisecond)
		_ = writer.Close()
	}()

	var dst bytes.Buffer
	_, err := copyWithIdleTimeout(&dst, reader, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "download idle timeout") {
		t.Fatalf("expected idle timeout, got %v", err)
	}
	if dst.String() != "partial" {
		t.Fatalf("expected partial data to be preserved for diagnostics, got %q", dst.String())
	}
}

// 回归测试: 只要有数据持续流动, 空闲计时器就必须被不断推后。
// 早期实现只启动一次性计时器且从不重置, 于是 idle 变成整包下载的硬上限,
// 45 秒内传不完包的慢速链路(国内直连约 40~70 KB/s, 二十余兆需要约 8 分钟)必然失败。
func TestCopyWithIdleTimeoutAllowsSlowButSteadyDownload(t *testing.T) {
	const chunks = 20
	const idle = 30 * time.Millisecond

	reader, writer := io.Pipe()
	defer reader.Close()
	go func() {
		for i := 0; i < chunks; i++ {
			if _, err := writer.Write([]byte("x")); err != nil {
				return
			}
			// 每次间隔远小于 idle, 但总体耗时(chunks*10ms=200ms)远超 idle。
			time.Sleep(10 * time.Millisecond)
		}
		_ = writer.Close()
	}()

	var dst bytes.Buffer
	written, err := copyWithIdleTimeout(&dst, reader, idle)
	if err != nil {
		t.Fatalf("slow but steady download must not time out, got %v", err)
	}
	if written != chunks || dst.Len() != chunks {
		t.Fatalf("expected %d bytes copied, got %d/%d", chunks, written, dst.Len())
	}
}

// 计时器到期的同一刻数据也读完时, 不能把成功的下载误报成超时。
// 用一个"一次读完就 EOF"的源, 配合极短 idle, 让 resultCh 与 timer 同时就绪,
// select 会在两者间随机选择; 无论选到哪支都必须返回成功。
type oneShotReader struct {
	data []byte
	done bool
}

func (r *oneShotReader) Read(b []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(b, r.data), io.EOF
}

func (r *oneShotReader) Close() error { return nil }

func TestCopyWithIdleTimeoutDoesNotMisreportCompletion(t *testing.T) {
	for i := 0; i < 200; i++ {
		var dst bytes.Buffer
		written, err := copyWithIdleTimeout(&dst, &oneShotReader{data: []byte("done")}, time.Nanosecond)
		if err != nil {
			t.Fatalf("iteration %d: completed download must not fail, got %v", i, err)
		}
		if written != 4 || dst.String() != "done" {
			t.Fatalf("iteration %d: expected 4 bytes %q, got %d %q", i, "done", written, dst.String())
		}
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestReplaceExecutableWithExistingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "octopus")
	src := filepath.Join(dir, "octopus.new")

	writeExecutable(t, target, "old-binary")
	writeExecutable(t, src, "new-binary")

	if err := replaceExecutable(src, target); err != nil {
		t.Fatalf("replace failed: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-binary" {
		t.Fatalf("target not replaced: %q", got)
	}
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Fatalf(".old should be cleaned up, stat err=%v", err)
	}
}

// TestReplaceExecutableMissingTarget 覆盖线上服务器那种场景:
// 进程还活着, 但路径上的二进制已经不在了(上次 rename 后没写成新文件)。
// 旧代码在这里直接 os.Rename 报 "no such file or directory", 更新永远失败。
func TestReplaceExecutableMissingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "octopus")
	src := filepath.Join(dir, "octopus.new")

	writeExecutable(t, src, "new-binary")

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("precondition: target should not exist")
	}

	if err := replaceExecutable(src, target); err != nil {
		t.Fatalf("replace should succeed even when target is missing, got %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-binary" {
		t.Fatalf("target not written: %q", got)
	}
}

func TestReplaceExecutableMissingSource(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "octopus")
	src := filepath.Join(dir, "does-not-exist")

	writeExecutable(t, target, "old-binary")

	if err := replaceExecutable(src, target); err == nil {
		t.Fatal("expected error when new binary is missing")
	}

	// 失败后旧文件必须还在, 不能被弄丢。
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("old target should be restored, got %v", err)
	}
	if string(got) != "old-binary" {
		t.Fatalf("old target corrupted: %q", got)
	}
}

func TestCurrentExecutablePathStripsDeletedSuffix(t *testing.T) {
	// 无法真的制造 " (deleted)" 后缀, 只验证正常路径不被改动。
	path, err := currentExecutablePath()
	if err != nil {
		t.Fatal(err)
	}
	if path == "" || filepath.Base(path) == "" {
		t.Fatalf("unexpected executable path: %q", path)
	}
}
