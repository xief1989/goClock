// GoClock 是一个常驻托盘的桌面提醒工具。
//
// 程序启动后最小化到系统托盘，到点时在屏幕右下角弹出提醒卡片，
// 卡片 15 秒后自动关闭，全程不发出任何声音。
package main

import (
	"os"
	"runtime"

	"github.com/lxn/walk"

	"goclock/internal/single"
	"goclock/internal/ui"
)

func main() {
	// walk 要求所有界面操作固定在同一个系统线程上。
	runtime.LockOSThread()

	// 已经有实例在运行时，唤醒它的管理窗口并退出，避免出现多个托盘图标。
	inst, ok := single.Acquire()
	if !ok {
		single.Signal()
		return
	}
	defer inst.Release()

	app, err := ui.NewApp()
	if err != nil {
		walk.MsgBox(nil, "GoClock 助手", err.Error(), walk.MsgBoxIconError|walk.MsgBoxOK)
		os.Exit(1)
	}

	// 重复启动的进程会发来唤醒信号，把管理窗口显示出来。
	inst.OnActivate(app.Activate)

	if err := app.Run(); err != nil {
		walk.MsgBox(nil, "GoClock 助手", err.Error(), walk.MsgBoxIconError|walk.MsgBoxOK)
		os.Exit(1)
	}
}
