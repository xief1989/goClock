// Package autostart 通过当前用户的注册表 Run 项管理开机自动启动。
// 写入 HKEY_CURRENT_USER 不需要管理员权限。
package autostart

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// runKeyPath 是 Windows 登录后自动执行的注册表位置。
const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// valueName 是本程序在 Run 项下使用的值名称。
const valueName = "GoClock"

// exePath 返回当前可执行文件的绝对路径。
func exePath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("无法获取程序路径：%w", err)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p, nil
	}
	return abs, nil
}

// command 返回写入注册表的启动命令，路径含空格时需要带引号。
func command(exe string) string {
	return `"` + exe + `"`
}

// Enabled 判断开机自启是否已开启。
// 若注册表中记录的路径与当前程序不一致（例如程序被移动过），同样视为未开启。
func Enabled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()

	value, _, err := key.GetStringValue(valueName)
	if err != nil {
		return false
	}
	exe, err := exePath()
	if err != nil {
		return value != ""
	}
	current := strings.Trim(strings.TrimSpace(value), `"`)
	return strings.EqualFold(current, exe)
}

// Set 开启或关闭开机自启。
func Set(enable bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("无法打开注册表启动项：%w", err)
	}
	defer key.Close()

	if !enable {
		// 值本来就不存在时不算失败。
		if err := key.DeleteValue(valueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return fmt.Errorf("关闭开机自启失败：%w", err)
		}
		return nil
	}

	exe, err := exePath()
	if err != nil {
		return err
	}
	if err := key.SetStringValue(valueName, command(exe)); err != nil {
		return fmt.Errorf("开启开机自启失败：%w", err)
	}
	return nil
}
