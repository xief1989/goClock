// Package hotkey 注册系统级全局热键，并在按下时调用对应的回调。
//
// 热键消息由一个仅消息窗口接收。这个窗口建在界面线程上，
// walk 的消息循环会把 WM_HOTKEY 分发给它，所以回调同样在界面线程上执行。
package hotkey

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

// 修饰键标志，对应 RegisterHotKey 的 fsModifiers。
const (
	modAlt      = 0x0001
	modControl  = 0x0002
	modShift    = 0x0004
	modWin      = 0x0008
	modNoRepeat = 0x4000
)

// errHotkeyAlreadyRegistered 对应 ERROR_HOTKEY_ALREADY_REGISTERED。
const errHotkeyAlreadyRegistered = syscall.Errno(1409)

// className 是接收热键消息的窗口类名。
const className = "GoClock.Hotkey"

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procRegisterHotKey   = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey = user32.NewProc("UnregisterHotKey")
)

// Combo 是解析好的热键组合。
type Combo struct {
	// Mods 是修饰键标志的组合。
	Mods uint32
	// Key 是虚拟键码。
	Key uint32
}

// keyNames 是可用作热键主键的按键名称（小写）到虚拟键码的映射，字母、数字与 F 键另行处理。
var keyNames = map[string]uint32{
	"printscreen": win.VK_SNAPSHOT,
	"prtsc":       win.VK_SNAPSHOT,
	"space":       win.VK_SPACE,
	"insert":      win.VK_INSERT,
	"delete":      win.VK_DELETE,
	"home":        win.VK_HOME,
	"end":         win.VK_END,
	"pageup":      win.VK_PRIOR,
	"pagedown":    win.VK_NEXT,
	"pause":       win.VK_PAUSE,
}

// displayNames 是格式化热键时使用的规范按键名。
var displayNames = map[uint32]string{
	win.VK_SNAPSHOT: "PrintScreen",
	win.VK_SPACE:    "Space",
	win.VK_INSERT:   "Insert",
	win.VK_DELETE:   "Delete",
	win.VK_HOME:     "Home",
	win.VK_END:      "End",
	win.VK_PRIOR:    "PageUp",
	win.VK_NEXT:     "PageDown",
	win.VK_PAUSE:    "Pause",
}

// Parse 把 "Ctrl+Alt+A" 这样的文字解析成热键组合，不区分大小写，允许多余空格。
//
// 字母、数字与 Space 等普通按键必须带修饰键，否则会把日常打字也拦截掉；
// F1~F24、PrintScreen、Pause 可以单独使用。
func Parse(s string) (Combo, error) {
	parts := strings.Split(s, "+")
	var c Combo
	for i, raw := range parts {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			return Combo{}, fmt.Errorf("热键「%s」格式不对，应形如 Ctrl+Alt+A", s)
		}
		last := i == len(parts)-1
		switch name {
		case "ctrl", "control":
			c.Mods |= modControl
		case "alt":
			c.Mods |= modAlt
		case "shift":
			c.Mods |= modShift
		case "win", "windows":
			c.Mods |= modWin
		default:
			if !last {
				return Combo{}, fmt.Errorf("热键「%s」里的「%s」只能放在最后", s, strings.TrimSpace(raw))
			}
			vk, ok := keyCode(name)
			if !ok {
				return Combo{}, fmt.Errorf("不认识的按键「%s」", strings.TrimSpace(raw))
			}
			c.Key = vk
		}
	}
	if c.Key == 0 {
		return Combo{}, fmt.Errorf("热键「%s」缺少主键，例如 Ctrl+Alt+A 里的 A", s)
	}
	if c.Mods == 0 && !standalone(c.Key) {
		return Combo{}, fmt.Errorf("热键「%s」需要搭配 Ctrl、Alt、Shift 或 Win 使用", s)
	}
	return c, nil
}

// keyCode 把小写按键名转换成虚拟键码。
func keyCode(name string) (uint32, bool) {
	if len(name) == 1 {
		ch := name[0]
		switch {
		case ch >= 'a' && ch <= 'z':
			return uint32(ch - 'a' + 'A'), true
		case ch >= '0' && ch <= '9':
			return uint32(ch), true
		}
	}
	if len(name) >= 2 && name[0] == 'f' {
		var n int
		if _, err := fmt.Sscanf(name[1:], "%d", &n); err == nil && n >= 1 && n <= 24 && fmt.Sprint(n) == name[1:] {
			return uint32(win.VK_F1 + n - 1), true
		}
	}
	vk, ok := keyNames[name]
	return vk, ok
}

// standalone 判断按键能否不带修饰键单独作为热键。
func standalone(vk uint32) bool {
	return (vk >= win.VK_F1 && vk <= win.VK_F24) || vk == win.VK_SNAPSHOT || vk == win.VK_PAUSE
}

// String 返回热键的规范写法，如 "Ctrl+Alt+A"。
func (c Combo) String() string {
	var parts []string
	if c.Mods&modControl != 0 {
		parts = append(parts, "Ctrl")
	}
	if c.Mods&modAlt != 0 {
		parts = append(parts, "Alt")
	}
	if c.Mods&modShift != 0 {
		parts = append(parts, "Shift")
	}
	if c.Mods&modWin != 0 {
		parts = append(parts, "Win")
	}
	switch {
	case (c.Key >= 'A' && c.Key <= 'Z') || (c.Key >= '0' && c.Key <= '9'):
		parts = append(parts, string(rune(c.Key)))
	case c.Key >= win.VK_F1 && c.Key <= win.VK_F24:
		parts = append(parts, fmt.Sprintf("F%d", c.Key-win.VK_F1+1))
	default:
		if name, ok := displayNames[c.Key]; ok {
			parts = append(parts, name)
		} else {
			parts = append(parts, fmt.Sprintf("0x%02X", c.Key))
		}
	}
	return strings.Join(parts, "+")
}

// Manager 管理一组已注册的全局热键。所有方法都必须在界面线程上调用。
type Manager struct {
	hwnd     win.HWND
	handlers map[int32]func()
	nextID   int32
}

var (
	// managers 按窗口句柄找到对应的 Manager，只在界面线程上访问。
	managers = map[win.HWND]*Manager{}
	// wndProcPtr 是热键窗口的窗口过程。回调地址数量有限，只创建一次。
	wndProcPtr  = syscall.NewCallback(wndProc)
	classLoaded bool
)

// New 创建热键管理器，内部会建一个仅消息窗口来接收 WM_HOTKEY。
func New() (*Manager, error) {
	if err := registerClass(); err != nil {
		return nil, err
	}
	hwnd := win.CreateWindowEx(0, syscall.StringToUTF16Ptr(className), nil, 0,
		0, 0, 0, 0, win.HWND_MESSAGE, 0, win.GetModuleHandle(nil), nil)
	if hwnd == 0 {
		return nil, fmt.Errorf("创建热键窗口失败：%w", windows.GetLastError())
	}
	m := &Manager{hwnd: hwnd, handlers: map[int32]func(){}, nextID: 1}
	managers[hwnd] = m
	return m, nil
}

// registerClass 注册热键窗口类，只做一次。
func registerClass() error {
	if classLoaded {
		return nil
	}
	wc := win.WNDCLASSEX{
		LpfnWndProc:   wndProcPtr,
		HInstance:     win.GetModuleHandle(nil),
		LpszClassName: syscall.StringToUTF16Ptr(className),
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	if win.RegisterClassEx(&wc) == 0 {
		return fmt.Errorf("注册热键窗口类失败：%w", windows.GetLastError())
	}
	classLoaded = true
	return nil
}

// Register 注册一个热键，按下时调用 fn。返回的编号用于注销。
// 热键已被其他程序占用时返回带中文说明的错误。
func (m *Manager) Register(c Combo, fn func()) (int32, error) {
	id := m.nextID
	ret, _, err := procRegisterHotKey.Call(uintptr(m.hwnd), uintptr(id), uintptr(c.Mods|modNoRepeat), uintptr(c.Key))
	if ret == 0 {
		if errors.Is(err, errHotkeyAlreadyRegistered) {
			return 0, fmt.Errorf("热键 %s 已被其他程序占用", c)
		}
		return 0, fmt.Errorf("注册热键 %s 失败：%w", c, err)
	}
	m.nextID++
	m.handlers[id] = fn
	return id, nil
}

// Unregister 注销指定编号的热键，编号无效时什么也不做。
func (m *Manager) Unregister(id int32) {
	if _, ok := m.handlers[id]; !ok {
		return
	}
	procUnregisterHotKey.Call(uintptr(m.hwnd), uintptr(id))
	delete(m.handlers, id)
}

// Close 注销全部热键并销毁内部窗口，退出程序前调用。
func (m *Manager) Close() {
	for id := range m.handlers {
		m.Unregister(id)
	}
	if m.hwnd != 0 {
		delete(managers, m.hwnd)
		win.DestroyWindow(m.hwnd)
		m.hwnd = 0
	}
}

// wndProc 把 WM_HOTKEY 转给对应的回调。
func wndProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	if msg == win.WM_HOTKEY {
		if m := managers[hwnd]; m != nil {
			if fn := m.handlers[int32(wParam)]; fn != nil {
				fn()
			}
		}
		return 0
	}
	return win.DefWindowProc(hwnd, msg, wParam, lParam)
}
