package capture

import (
	"image"
	"syscall"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

var (
	dwmapi = windows.NewLazySystemDLL("dwmapi.dll")

	procEnumWindows           = user32.NewProc("EnumWindows")
	procDwmGetWindowAttribute = dwmapi.NewProc("DwmGetWindowAttribute")
)

// DWM 窗口属性编号。
const (
	dwmwaExtendedFrameBounds = 9
	dwmwaCloaked             = 14
)

// maxChildrenPerWindow 限制每个顶层窗口最多收集多少个子窗口，避免控件极多的程序拖慢截图启动。
const maxChildrenPerWindow = 600

// minTargetSize 是可被悬停选中的最小边长，再小的控件选中了也没有意义。
const minTargetSize = 6

// Target 是悬停识别用的一个顶层窗口及其子窗口的范围，屏幕坐标、物理像素。
type Target struct {
	// Rect 是顶层窗口的可见范围（不含 Win10 起看不见的缩放边框）。
	Rect image.Rectangle
	// Children 是其中可见子窗口的范围，已裁剪在 Rect 之内。
	Children []image.Rectangle
}

var (
	enumWindowsCallback = syscall.NewCallback(collectHWND)
	enumChildCallback   = syscall.NewCallback(collectHWND)
	// collected 是枚举回调写入的临时结果，只在界面线程上使用。
	collected []win.HWND
)

// collectHWND 是 EnumWindows / EnumChildWindows 的回调，把句柄依次记下来。
func collectHWND(hwnd win.HWND, _ uintptr) uintptr {
	collected = append(collected, hwnd)
	return 1
}

// EnumTargets 按 Z 序自上而下列出可见的顶层窗口及其子窗口，结果裁剪在 bounds 之内。
// 必须在截图遮罩显示之前调用，否则遮罩自己会成为最上层的窗口。
func EnumTargets(bounds image.Rectangle) []Target {
	var targets []Target
	WithPerMonitorDPI(func() {
		collected = collected[:0]
		procEnumWindows.Call(enumWindowsCallback, 0)
		tops := append([]win.HWND(nil), collected...)

		for _, hwnd := range tops {
			r, ok := topLevelRect(hwnd)
			if !ok {
				continue
			}
			r = r.Intersect(bounds)
			if r.Dx() < minTargetSize || r.Dy() < minTargetSize {
				continue
			}
			targets = append(targets, Target{Rect: r, Children: childRects(hwnd, r)})
		}
	})
	return targets
}

// topLevelRect 返回顶层窗口在屏幕上实际可见的范围；不可见、最小化或被隐藏的窗口返回 false。
func topLevelRect(hwnd win.HWND) (image.Rectangle, bool) {
	if !win.IsWindowVisible(hwnd) || win.IsIconic(hwnd) {
		return image.Rectangle{}, false
	}
	// 鼠标穿透的窗口（各种透明浮层）点不到，也不该被选中。
	if uint32(win.GetWindowLong(hwnd, win.GWL_EXSTYLE))&win.WS_EX_TRANSPARENT != 0 {
		return image.Rectangle{}, false
	}
	// 被 DWM 隐藏（cloaked）的窗口虽然算可见，屏幕上却看不到，例如其他虚拟桌面上的窗口。
	var cloaked uint32
	if procDwmGetWindowAttribute.Find() == nil {
		if ret, _, _ := procDwmGetWindowAttribute.Call(uintptr(hwnd), dwmwaCloaked,
			uintptr(unsafe.Pointer(&cloaked)), unsafe.Sizeof(cloaked)); ret == 0 && cloaked != 0 {
			return image.Rectangle{}, false
		}
	}

	// GetWindowRect 会把 Win10 起看不见的缩放边框也算进去，DWM 给出的才是肉眼看到的范围。
	var rc win.RECT
	if procDwmGetWindowAttribute.Find() == nil {
		if ret, _, _ := procDwmGetWindowAttribute.Call(uintptr(hwnd), dwmwaExtendedFrameBounds,
			uintptr(unsafe.Pointer(&rc)), unsafe.Sizeof(rc)); ret == 0 {
			return rectFromWin(rc), true
		}
	}
	if !win.GetWindowRect(hwnd, &rc) {
		return image.Rectangle{}, false
	}
	return rectFromWin(rc), true
}

// childRects 收集顶层窗口下所有可见子窗口的范围。
func childRects(hwnd win.HWND, parent image.Rectangle) []image.Rectangle {
	collected = collected[:0]
	win.EnumChildWindows(hwnd, enumChildCallback, 0)
	var list []image.Rectangle
	for _, child := range collected {
		if len(list) >= maxChildrenPerWindow {
			break
		}
		if !win.IsWindowVisible(child) {
			continue
		}
		var rc win.RECT
		if !win.GetWindowRect(child, &rc) {
			continue
		}
		r := rectFromWin(rc).Intersect(parent)
		if r.Dx() < minTargetSize || r.Dy() < minTargetSize || r == parent {
			continue
		}
		list = append(list, r)
	}
	return list
}

// Pick 返回点 p 处最内层的窗口范围：先找最上层包含 p 的顶层窗口，
// 再在它的子窗口里找包含 p 且面积最小的一个；没有子窗口命中时返回顶层窗口本身。
func Pick(targets []Target, p image.Point) (image.Rectangle, bool) {
	for _, t := range targets {
		if !p.In(t.Rect) {
			continue
		}
		best := t.Rect
		bestArea := area(best)
		for _, c := range t.Children {
			if p.In(c) && area(c) < bestArea {
				best, bestArea = c, area(c)
			}
		}
		return best, true
	}
	return image.Rectangle{}, false
}

// area 返回矩形面积。
func area(r image.Rectangle) int {
	return r.Dx() * r.Dy()
}
