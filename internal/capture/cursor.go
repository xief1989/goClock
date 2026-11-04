package capture

import (
	"image"
	"sync"
	"unsafe"

	"github.com/lxn/win"
)

var (
	procGetCursorInfo = user32.NewProc("GetCursorInfo")
	procGetIconInfo   = user32.NewProc("GetIconInfo")
	procDrawIconEx    = user32.NewProc("DrawIconEx")
)

const (
	cursorShowing = 0x1
	diNormal      = 0x3
)

// cursorInfo 对应 CURSORINFO。
type cursorInfo struct {
	size    uint32
	flags   uint32
	hCursor uintptr
	pos     win.POINT
}

// iconInfo 对应 ICONINFO。
type iconInfo struct {
	isIcon   int32
	hotspotX uint32
	hotspotY uint32
	hbmMask  uintptr
	hbmColor uintptr
}

var (
	hotspotMu sync.Mutex
	// hotspots 缓存各光标的热点位置。取热点要调用 GetIconInfo，它每次都会创建两张位图，
	// 录屏时每秒几十帧，缓存起来避免反复创建与释放。
	hotspots = map[uintptr]image.Point{}
)

// drawCursor 把当前鼠标指针画到 dc 上，origin 是 dc 左上角对应的屏幕坐标。指针隐藏时不画。
// 需要在按显示器感知 DPI 的上下文中调用，坐标才是物理像素。
func drawCursor(dc win.HDC, origin image.Point) {
	ci := cursorInfo{size: uint32(unsafe.Sizeof(cursorInfo{}))}
	if ret, _, _ := procGetCursorInfo.Call(uintptr(unsafe.Pointer(&ci))); ret == 0 {
		return
	}
	if ci.flags&cursorShowing == 0 || ci.hCursor == 0 {
		return
	}
	hot := cursorHotspot(ci.hCursor)
	x := int(ci.pos.X) - hot.X - origin.X
	y := int(ci.pos.Y) - hot.Y - origin.Y
	procDrawIconEx.Call(uintptr(dc), uintptr(int32(x)), uintptr(int32(y)), ci.hCursor, 0, 0, 0, 0, diNormal)
}

// cursorHotspot 返回光标的热点（点击位置相对光标图片左上角的偏移）。
func cursorHotspot(h uintptr) image.Point {
	hotspotMu.Lock()
	defer hotspotMu.Unlock()
	if p, ok := hotspots[h]; ok {
		return p
	}
	var ii iconInfo
	var p image.Point
	if ret, _, _ := procGetIconInfo.Call(h, uintptr(unsafe.Pointer(&ii))); ret != 0 {
		p = image.Pt(int(ii.hotspotX), int(ii.hotspotY))
		if ii.hbmMask != 0 {
			win.DeleteObject(win.HGDIOBJ(ii.hbmMask))
		}
		if ii.hbmColor != 0 {
			win.DeleteObject(win.HGDIOBJ(ii.hbmColor))
		}
	}
	hotspots[h] = p
	return p
}
