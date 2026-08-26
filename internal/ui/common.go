// Package ui 实现托盘、闹钟管理窗口以及右下角提醒弹窗。
// 除非特别说明，本包中的函数都必须在界面线程上调用。
package ui

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/lxn/walk"
	dcl "github.com/lxn/walk/declarative"

	"goclock/internal/icon"
	"goclock/internal/storage"
)

// uiFontFamily 是界面统一使用的字体。
const uiFontFamily = "Microsoft YaHei UI"

// appName 是显示在窗口标题与托盘提示中的程序名。
const appName = "GoClock 闹钟"

var (
	appIconOnce sync.Once
	appIconVal  *walk.Icon
)

// appIcon 返回程序图标。图标由代码绘制并缓存到用户目录，加载失败时返回 nil。
func appIcon() *walk.Icon {
	appIconOnce.Do(func() {
		dir, err := storage.DataDir()
		if err != nil {
			return
		}
		path := filepath.Join(dir, "app.ico")
		if _, err := os.Stat(path); err != nil {
			if err := os.WriteFile(path, icon.EncodeICO(), 0o644); err != nil {
				return
			}
		}
		if ic, err := walk.NewIconFromFile(path); err == nil {
			appIconVal = ic
		}
	})
	return appIconVal
}

// iconProperty 返回可安全用于 declarative 的图标属性，图标不可用时返回 nil。
func iconProperty() dcl.Property {
	if ic := appIcon(); ic != nil {
		return ic
	}
	return nil
}

// safeSynchronize 把任务投递到界面线程执行。
// 窗口已经销毁时静默跳过，避免退出瞬间的竞态导致程序崩溃。
func safeSynchronize(form walk.Form, f func()) {
	defer func() {
		_ = recover()
	}()
	if form == nil || form.Handle() == 0 {
		return
	}
	form.Synchronize(f)
}

// errorBox 弹出一个错误提示框。
func errorBox(owner walk.Form, message string) {
	walk.MsgBox(owner, appName, message, walk.MsgBoxIconError|walk.MsgBoxOK)
}

// confirmBox 弹出一个确认框，用户点击「是」时返回 true。
func confirmBox(owner walk.Form, message string) bool {
	return walk.MsgBox(owner, appName, message, walk.MsgBoxIconQuestion|walk.MsgBoxYesNo) == walk.DlgCmdYes
}
