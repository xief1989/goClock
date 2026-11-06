// Package crashlog 把进程崩溃与后台协程出错时的调用栈记进 %APPDATA%\GoClock\crash.log。
//
// GoClock 编译成无控制台的窗口程序，Go 运行时崩溃时打印到标准错误的调用栈会直接丢失，
// 系统日志与错误报告里也不留痕迹；接管崩溃输出后，下次再崩就能按日志定位。
package crashlog

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"
)

// fileName 是崩溃日志的文件名。
const fileName = "crash.log"

// maxSize：启动时日志超过这么大就清空重写，免得常年累积。
const maxSize = 1 << 20

var (
	mu sync.Mutex
	// logPath 是 Setup 成功后的日志路径；为空表示没有接管（Setup 未调用或失败）。
	logPath string
)

// Setup 在 dir 下打开崩溃日志，写一行启动标记，并让运行时把崩溃输出也写进这个文件。返回日志的完整路径。
//
// 由 main 在确认是唯一实例后调用一次。之后任何协程里没接住的 panic、运行时致命错误（并发写 map、内存耗尽、
// 动态库里的访问越界等）都会连同所有协程的调用栈追加到日志里。
func Setup(dir string) (string, error) {
	path := filepath.Join(dir, fileName)
	flag := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if info, err := os.Stat(path); err == nil && info.Size() > maxSize {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flag, 0o644)
	if err != nil {
		return "", fmt.Errorf("无法打开崩溃日志：%w", err)
	}
	// 运行时会复制一份文件句柄自己持有，这里的句柄用完即可关闭。
	defer f.Close()
	fmt.Fprintf(f, "==== %s 启动，PID %d ====\n", now(), os.Getpid())
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		return "", fmt.Errorf("无法接管崩溃输出：%w", err)
	}
	// 默认只打印出事的那个协程；托盘程序的协程不多，全打出来才看得出它当时在等谁。
	debug.SetTraceback("all")

	mu.Lock()
	logPath = path
	mu.Unlock()
	return path, nil
}

// Guard 运行 f；f 里出现 panic 时把调用栈记进崩溃日志，转成错误返回，进程照常运行。
//
// 用在处理任意截图、任意文字的后台协程里（识别、翻译）：输入千奇百怪，这类协程里没接住的 panic 会让整个托盘程序退出。
// 只适合 f 出错后其余状态仍然可用的场合，不要拿它兜住所有协程，否则真正的状态错乱会被藏起来。
func Guard(what string, f func() error) (err error) {
	defer func() {
		v := recover()
		if v == nil {
			return
		}
		if path := record(what, v, debug.Stack()); path != "" {
			err = fmt.Errorf("程序内部出错：%v（详情已记入 %s）", v, path)
		} else {
			err = fmt.Errorf("程序内部出错：%v", v)
		}
	}()
	return f()
}

// record 把接住的 panic 与调用栈追加到日志，返回日志路径；没有接管或写不进去时返回空串。
func record(what string, v any, stack []byte) string {
	mu.Lock()
	defer mu.Unlock()
	if logPath == "" {
		return ""
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "==== %s %s出错（已接住，程序继续运行）：%v ====\n%s\n", now(), what, v, stack); err != nil {
		return ""
	}
	return logPath
}

// now 返回写进日志的本地时间。
func now() string {
	return time.Now().Format("2006-01-02 15:04:05")
}
