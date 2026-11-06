package crashlog

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

// childEnv 不为空时，本测试进程是 TestCrashOutput 起的子进程：接管崩溃输出后在协程里故意 panic。
const childEnv = "GOCLOCK_CRASH_CHILD"

// setupTemp 在临时目录里调用 Setup，测试结束时撤销对崩溃输出的接管（否则运行时持有的句柄会让临时目录删不掉）。
func setupTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() {
		_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		mu.Lock()
		logPath = ""
		mu.Unlock()
	})
	path, err := Setup(dir)
	if err != nil {
		t.Fatalf("Setup 失败：%v", err)
	}
	return path
}

// readLog 读出日志全文。
func readLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取崩溃日志失败：%v", err)
	}
	return string(b)
}

func TestCrashOutput(t *testing.T) {
	if dir := os.Getenv(childEnv); dir != "" {
		if _, err := Setup(dir); err != nil {
			os.Exit(3)
		}
		go func() {
			panic("crashlog test")
		}()
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}

	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashOutput$")
	cmd.Env = append(os.Environ(), childEnv+"="+dir)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Fatalf("子进程应因 panic 退出（退出码 2），实际：%v\n%s", err, out)
	}
	log := readLog(t, filepath.Join(dir, fileName))
	for _, want := range []string{"启动，PID", "panic: crashlog test", "goroutine ", "crashlog.TestCrashOutput.func"} {
		if !strings.Contains(log, want) {
			t.Errorf("崩溃日志里缺少 %q：\n%s", want, log)
		}
	}
}

func TestSetupAppendsAndTruncates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)
	if err := os.WriteFile(path, []byte("上一次的记录\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reset := func() {
		_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		mu.Lock()
		logPath = ""
		mu.Unlock()
	}
	t.Cleanup(reset)

	if _, err := Setup(dir); err != nil {
		t.Fatalf("Setup 失败：%v", err)
	}
	if log := readLog(t, path); !strings.HasPrefix(log, "上一次的记录\n==== ") {
		t.Errorf("没超过上限时应追加在原内容后面：\n%s", log)
	}

	reset()
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxSize+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Setup(dir); err != nil {
		t.Fatalf("Setup 失败：%v", err)
	}
	if log := readLog(t, path); !strings.HasPrefix(log, "==== ") || strings.Count(log, "\n") != 1 {
		t.Errorf("超过上限时应清空后只剩启动行，实际 %d 字节", len(log))
	}
}

func TestGuard(t *testing.T) {
	path := setupTemp(t)

	if err := Guard("测试", func() error { return nil }); err != nil {
		t.Errorf("正常返回时不应有错误：%v", err)
	}
	plain := errors.New("普通错误")
	if err := Guard("测试", func() error { return plain }); err != plain {
		t.Errorf("普通错误应原样返回，实际：%v", err)
	}

	err := Guard("测试", func() error {
		var m map[string]int
		m["x"] = 1
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "程序内部出错") || !strings.Contains(err.Error(), path) {
		t.Fatalf("panic 应转成带日志路径的错误，实际：%v", err)
	}
	log := readLog(t, path)
	for _, want := range []string{"测试出错（已接住，程序继续运行）", "assignment to entry in nil map", "crashlog.TestGuard"} {
		if !strings.Contains(log, want) {
			t.Errorf("崩溃日志里缺少 %q：\n%s", want, log)
		}
	}
}

func TestGuardWithoutSetup(t *testing.T) {
	err := Guard("测试", func() error { panic("没有日志") })
	if err == nil || err.Error() != "程序内部出错：没有日志" {
		t.Errorf("没接管日志时只报错不带路径，实际：%v", err)
	}
}
