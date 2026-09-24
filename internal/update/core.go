package update

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bestruirui/octopus/internal/utils/shutdown"
	"github.com/charmbracelet/log"
)

// restartDelay 是替换完可执行文件到真正重启之间的等待。
// 更新接口返回前进程就 shutdown 会把还没写到客户端的成功响应掐断, 前端只会看到连接中断
// 并报"更新失败", 而实际上二进制已经换好了 —— 这个延迟用来让响应先发出去。
const restartDelay = 1 * time.Second

var updateMu sync.Mutex

func UpdateCore() error {
	if !updateMu.TryLock() {
		return errors.New("update already in progress")
	}
	defer updateMu.Unlock()

	log.Infof("start update core")

	filename, err := getDownloadFilename()
	if err != nil {
		log.Warnf("update core failed: %v", err)
		return err
	}

	downloadUrl := updateUrl + "/" + filename
	log.Infof("download url: %s", downloadUrl)

	execPath, err := currentExecutablePath()
	if err != nil {
		log.Warnf("get executable path failed: %v", err)
		return err
	}
	execName := filepath.Base(execPath)

	tmpDir, err := os.MkdirTemp("", execName+"-update-*")
	if err != nil {
		log.Warnf("create temp dir failed: %v", err)
		return err
	}
	defer os.RemoveAll(tmpDir)
	log.Infof("using temp dir: %s", tmpDir)

	// 落盘再解压: 归档二十余兆, 直接读进内存没有意义, 而解压需要读到文件尾部。
	archivePath := filepath.Join(tmpDir, filename)
	if err := download(downloadUrl, archivePath); err != nil {
		log.Warnf("download failed: %v", err)
		log.Warnf("if this host cannot reach the GitHub release CDN, set a proxy in Settings (proxy_url); the download timeout is %s", downloadTimeout)
		return err
	}

	if err := unzipFile(archivePath, tmpDir); err != nil {
		log.Warnf("unzip failed: %v", err)
		return err
	}

	newExec := filepath.Join(tmpDir, execName)
	if info, err := os.Stat(newExec); err != nil || info.IsDir() {
		log.Warnf("new executable not found at %s: %v", newExec, err)
		return fmt.Errorf("new executable not found in archive root: %w", err)
	}
	log.Infof("new executable: %s", newExec)

	if err := replaceExecutable(newExec, execPath); err != nil {
		log.Warnf("replace executable failed: %v", err)
		return err
	}

	log.Infof("update core success")
	// 延迟重启: 先让调用方把成功响应写回客户端, 再关服务换进程。
	go func() {
		time.Sleep(restartDelay)
		restartExecutable(execPath)
	}()
	return nil
}

// currentExecutablePath 返回可用于写回的二进制路径。
// Linux 上若当前文件已被删(上次更新 rename 成功但没写成新文件, 或进程仍占着已删除 inode),
// os.Executable 仍可能带 " (deleted)" 或指向一个已经不存在的路径。更新不能因此直接失败。
func currentExecutablePath() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(execPath, " (deleted)"), nil
}

// replaceExecutable 把新二进制放到正在运行的路径上。
// Windows 不能覆盖正在运行的 exe, 必须先把旧文件改名; Linux 上旧路径可以已经不存在,
// 这时跳过改名, 直接写入目标路径 —— 这就是服务器上报
// "rename /root/octopus/octopus ... no such file or directory" 的情况。
func replaceExecutable(newExec, execPath string) error {
	if err := os.MkdirAll(filepath.Dir(execPath), os.ModePerm); err != nil {
		return err
	}

	mode := os.FileMode(0o755)
	oldPath := execPath + ".old"
	hadOld := false
	if info, err := os.Stat(execPath); err == nil {
		mode = info.Mode().Perm()
		if err := os.Rename(execPath, oldPath); err != nil {
			return fmt.Errorf("rename old executable: %w", err)
		}
		hadOld = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else {
		log.Warnf("current executable %s is missing; writing a new file at that path", execPath)
	}

	if err := copyFile(newExec, execPath); err != nil {
		if hadOld {
			_ = os.Rename(oldPath, execPath)
		}
		return err
	}
	_ = os.Chmod(execPath, mode)
	_ = os.RemoveAll(oldPath)
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), os.ModePerm); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// getDownloadFilename 返回当前平台对应的发布归档名称。
func getDownloadFilename() (string, error) {
	arch := runtime.GOARCH
	goos := runtime.GOOS

	switch goos {
	case "windows":
		switch arch {
		case "amd64":
			return "octopus-windows-amd64.zip", nil
		}
	case "darwin":
		switch arch {
		case "amd64":
			return "octopus-darwin-amd64.zip", nil
		case "arm64":
			return "octopus-darwin-arm64.zip", nil
		}
	case "linux":
		switch arch {
		case "386":
			return "octopus-linux-386.zip", nil
		case "amd64":
			return "octopus-linux-amd64.zip", nil
		case "arm":
			return "octopus-linux-arm.zip", nil
		case "arm64":
			return "octopus-linux-arm64.zip", nil
		}
	case "android":
		switch arch {
		case "386":
			return "octopus-android-386.zip", nil
		case "amd64":
			return "octopus-android-amd64.zip", nil
		case "arm":
			return "octopus-android-arm.zip", nil
		case "arm64":
			return "octopus-android-arm64.zip", nil
		}
	}
	return "", fmt.Errorf("unsupported platform: %s/%s", goos, arch)
}

func restartExecutable(execPath string) {
	shutdown.Shutdown()

	log.Infof("restarting: %q %q", execPath, os.Args[1:])

	if runtime.GOOS == "windows" {
		cmd := exec.Command(execPath, os.Args[1:]...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			log.Errorf("restarting failed: %v", err)
		}
		os.Exit(0)
	}

	if err := syscall.Exec(execPath, os.Args, os.Environ()); err != nil {
		log.Errorf("restarting failed: %v", err)
	}
}
