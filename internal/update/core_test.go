package update

import (
	"os"
	"path/filepath"
	"testing"
)

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