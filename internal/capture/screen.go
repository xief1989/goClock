// Package capture 提供与界面无关的截图能力：抓屏、窗口识别、长图拼接、剪贴板与保存。
//
// 涉及屏幕坐标的函数都按物理像素工作，内部会临时切到「按显示器感知 V2」的
// DPI 上下文，因此必须在锁定了系统线程的界面线程上调用。
package capture

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	shcore = windows.NewLazySystemDLL("shcore.dll")

	procSetThreadDpiAwarenessContext = user32.NewProc("SetThreadDpiAwarenessContext")
	procMonitorFromPoint             = user32.NewProc("MonitorFromPoint")
	procGetDpiForMonitor             = shcore.NewProc("GetDpiForMonitor")
)

// dpiAwarenessPerMonitorV2 对应 DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2，即 (HANDLE)-4。
const dpiAwarenessPerMonitorV2 = ^uintptr(3)

// monitorDefaultToNearest 对应 MONITOR_DEFAULTTONEAREST。
const monitorDefaultToNearest = 2

// 虚拟桌面相关的 GetSystemMetrics 索引。
const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79
)

// WithPerMonitorDPI 在「按显示器感知 V2」的线程 DPI 上下文中执行 fn，返回前恢复原上下文。
//
// 程序清单声明的是系统级 DPI 感知，在缩放比例不同的副屏上，坐标与抓图都会被系统换算而失真；
// 截图相关的操作统一包在这里面，拿到的才是真实的物理像素。系统不支持该接口时直接执行 fn。
func WithPerMonitorDPI(fn func()) {
	if procSetThreadDpiAwarenessContext.Find() != nil {
		fn()
		return
	}
	old, _, _ := procSetThreadDpiAwarenessContext.Call(dpiAwarenessPerMonitorV2)
	defer func() {
		if old != 0 {
			procSetThreadDpiAwarenessContext.Call(old)
		}
	}()
	fn()
}

// VirtualScreen 返回整个虚拟桌面（全部显示器拼起来）的矩形，屏幕坐标、物理像素。
func VirtualScreen() image.Rectangle {
	var r image.Rectangle
	WithPerMonitorDPI(func() {
		x := int(win.GetSystemMetrics(smXVirtualScreen))
		y := int(win.GetSystemMetrics(smYVirtualScreen))
		w := int(win.GetSystemMetrics(smCXVirtualScreen))
		h := int(win.GetSystemMetrics(smCYVirtualScreen))
		r = image.Rect(x, y, x+w, y+h)
	})
	return r
}

// CursorPos 返回鼠标指针的屏幕坐标（物理像素）。
func CursorPos() image.Point {
	var pt win.POINT
	WithPerMonitorDPI(func() {
		win.GetCursorPos(&pt)
	})
	return image.Pt(int(pt.X), int(pt.Y))
}

// SetCursorPos 把鼠标指针移到屏幕坐标 p（物理像素）。
func SetCursorPos(p image.Point) {
	WithPerMonitorDPI(func() {
		win.SetCursorPos(int32(p.X), int32(p.Y))
	})
}

// Monitor 描述一台显示器的位置与缩放，坐标均为屏幕坐标、物理像素。
type Monitor struct {
	// Bounds 是整块屏幕的范围。
	Bounds image.Rectangle
	// Work 是去掉任务栏之后的可用范围。
	Work image.Rectangle
	// DPI 是该显示器的有效 DPI，96 表示 100% 缩放。
	DPI int
}

// MonitorAt 返回离点 p 最近的那台显示器。
func MonitorAt(p image.Point) Monitor {
	m := Monitor{DPI: 96}
	WithPerMonitorDPI(func() {
		// POINT 按值传递，两个 int32 正好拼进一个 64 位寄存器。
		packed := uintptr(uint32(int32(p.X))) | uintptr(uint32(int32(p.Y)))<<32
		hmon, _, _ := procMonitorFromPoint.Call(packed, monitorDefaultToNearest)
		if hmon == 0 {
			return
		}
		var info win.MONITORINFO
		info.CbSize = uint32(unsafe.Sizeof(info))
		if win.GetMonitorInfo(win.HMONITOR(hmon), &info) {
			m.Bounds = rectFromWin(info.RcMonitor)
			m.Work = rectFromWin(info.RcWork)
		}
		if procGetDpiForMonitor.Find() == nil {
			var dpiX, dpiY uint32
			// 0 表示 MDT_EFFECTIVE_DPI。
			if ret, _, _ := procGetDpiForMonitor.Call(hmon, 0,
				uintptr(unsafe.Pointer(&dpiX)), uintptr(unsafe.Pointer(&dpiY))); ret == 0 && dpiX > 0 {
				m.DPI = int(dpiX)
			}
		}
	})
	if m.Bounds.Empty() {
		vs := VirtualScreen()
		m.Bounds, m.Work = vs, vs
	}
	return m
}

// rectFromWin 把 Win32 的 RECT 转换成 image.Rectangle。
func rectFromWin(r win.RECT) image.Rectangle {
	return image.Rect(int(r.Left), int(r.Top), int(r.Right), int(r.Bottom))
}

// Bitmap 是一块 32 位、自顶向下的 DIB 区：GDI 可以直接拿它绘制，Go 代码也能直接读写像素。
type Bitmap struct {
	// Handle 是 DIB 区的位图句柄，可以选入内存 DC 参与 GDI 绘制。
	Handle win.HBITMAP
	// Width、Height 是位图尺寸（像素）。
	Width, Height int
	// Pix 指向 DIB 内存，按 BGRA 排列，每行 Width*4 字节；Dispose 之后不可再访问。
	// GDI 画完之后、读像素之前要先调用 win.GdiFlush。
	Pix []byte
}

// NewBitmap 创建指定尺寸的空白位图。
func NewBitmap(w, h int) (*Bitmap, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("位图尺寸无效：%dx%d", w, h)
	}
	bi := win.BITMAPINFOHEADER{
		BiWidth:       int32(w),
		BiHeight:      -int32(h), // 负数表示自顶向下，行顺序与 image.RGBA 一致
		BiPlanes:      1,
		BiBitCount:    32,
		BiCompression: win.BI_RGB,
	}
	bi.BiSize = uint32(unsafe.Sizeof(bi))
	var bits unsafe.Pointer
	hbmp := win.CreateDIBSection(0, &bi, win.DIB_RGB_COLORS, &bits, 0, 0)
	if hbmp == 0 || bits == nil {
		return nil, fmt.Errorf("创建 %dx%d 位图失败", w, h)
	}
	return &Bitmap{
		Handle: hbmp,
		Width:  w,
		Height: h,
		Pix:    unsafe.Slice((*byte)(bits), w*h*4),
	}, nil
}

// Dispose 释放位图。重复调用是安全的。
func (b *Bitmap) Dispose() {
	if b == nil || b.Handle == 0 {
		return
	}
	win.DeleteObject(win.HGDIOBJ(b.Handle))
	b.Handle = 0
	b.Pix = nil
}

// PixelAt 返回 (x, y) 处的颜色，越界时返回黑色。
func (b *Bitmap) PixelAt(x, y int) (r, g, bl uint8) {
	if x < 0 || y < 0 || x >= b.Width || y >= b.Height {
		return 0, 0, 0
	}
	i := (y*b.Width + x) * 4
	return b.Pix[i+2], b.Pix[i+1], b.Pix[i]
}

// RGBA 把位图中的区域 r 复制成 image.RGBA（交换红蓝通道，Alpha 置为不透明）。
func (b *Bitmap) RGBA(r image.Rectangle) *image.RGBA {
	r = r.Intersect(image.Rect(0, 0, b.Width, b.Height))
	img := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := 0; y < r.Dy(); y++ {
		src := b.Pix[((r.Min.Y+y)*b.Width+r.Min.X)*4:]
		dst := img.Pix[y*img.Stride:]
		bgraToRGBA(dst[:r.Dx()*4], src[:r.Dx()*4])
	}
	return img
}

// BitmapFromRGBA 把 image.RGBA 复制成一块新的 Bitmap。
func BitmapFromRGBA(img *image.RGBA) (*Bitmap, error) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	b, err := NewBitmap(w, h)
	if err != nil {
		return nil, err
	}
	for y := 0; y < h; y++ {
		src := img.Pix[y*img.Stride:]
		dst := b.Pix[y*w*4:]
		// 红蓝互换是对称的，同一个函数反过来用即可。
		bgraToRGBA(dst[:w*4], src[:w*4])
	}
	return b, nil
}

// bgraToRGBA 把一行 BGRA 像素转换成 RGBA 写入 dst，Alpha 统一置为 255。
// 屏幕抓图的 Alpha 通道没有意义（多为 0），保留下来会让 PNG 变成全透明。
func bgraToRGBA(dst, src []byte) {
	for i := 0; i+3 < len(src) && i+3 < len(dst); i += 4 {
		dst[i], dst[i+1], dst[i+2], dst[i+3] = src[i+2], src[i+1], src[i], 255
	}
}

// GrabBitmap 把屏幕上的矩形区域 r（屏幕坐标、物理像素）抓到一块新的 Bitmap。
func GrabBitmap(r image.Rectangle) (*Bitmap, error) {
	if r.Empty() {
		return nil, errors.New("抓屏区域为空")
	}
	b, err := NewBitmap(r.Dx(), r.Dy())
	if err != nil {
		return nil, err
	}
	if err := blitScreen(b, r.Min); err != nil {
		b.Dispose()
		return nil, err
	}
	return b, nil
}

// blitScreen 把屏幕上从 origin 开始、与 b 同样大小的区域复制进 b。
func blitScreen(b *Bitmap, origin image.Point) error {
	return blitScreenWith(b, origin, false)
}

// blitScreenWith 同 blitScreen，withCursor 为 true 时把鼠标指针也画进去（截图不需要，录屏需要）。
func blitScreenWith(b *Bitmap, origin image.Point, withCursor bool) error {
	var err error
	WithPerMonitorDPI(func() {
		screenDC := win.GetDC(0)
		if screenDC == 0 {
			err = errors.New("无法获取屏幕设备上下文")
			return
		}
		defer win.ReleaseDC(0, screenDC)

		memDC := win.CreateCompatibleDC(screenDC)
		if memDC == 0 {
			err = errors.New("无法创建内存设备上下文")
			return
		}
		defer win.DeleteDC(memDC)

		old := win.SelectObject(memDC, win.HGDIOBJ(b.Handle))
		// CAPTUREBLT 让分层窗口（部分菜单、提示条）也能被抓进来。
		ok := win.BitBlt(memDC, 0, 0, int32(b.Width), int32(b.Height),
			screenDC, int32(origin.X), int32(origin.Y), win.SRCCOPY|win.CAPTUREBLT)
		if ok && withCursor {
			drawCursor(memDC, origin)
		}
		win.SelectObject(memDC, old)
		win.GdiFlush()
		if !ok {
			err = errors.New("抓屏失败")
		}
	})
	return err
}

// Grabber 反复抓取同一块屏幕区域，供长截图高频轮询使用。
// 内部位图与比较缓冲都是复用的，轮询本身不产生新的内存分配。
type Grabber struct {
	rect image.Rectangle
	bmp  *Bitmap
	// last 是上一次抓到的原始像素（BGRA），用于判断画面是否变化。
	last []byte
	has  bool
}

// NewGrabber 创建针对区域 r（屏幕坐标、物理像素）的抓取器。
func NewGrabber(r image.Rectangle) (*Grabber, error) {
	b, err := NewBitmap(r.Dx(), r.Dy())
	if err != nil {
		return nil, err
	}
	return &Grabber{rect: r, bmp: b, last: make([]byte, len(b.Pix))}, nil
}

// Poll 抓取一帧，返回它与上一次抓到的画面相比是否有变化；第一次抓取总是返回 true。
func (g *Grabber) Poll() (changed bool, err error) {
	if err := blitScreen(g.bmp, g.rect.Min); err != nil {
		return false, err
	}
	changed = !g.has || !bytes.Equal(g.bmp.Pix, g.last)
	if changed {
		copy(g.last, g.bmp.Pix)
		g.has = true
	}
	return changed, nil
}

// Snap 抓取一帧并直接返回内部位图的像素（BGRA、自上而下），下次抓取时会被覆盖。
// 供录屏这类马上就会把数据拷走的场合使用，省掉一次转换与分配。
func (g *Grabber) Snap(withCursor bool) ([]byte, error) {
	if err := blitScreenWith(g.bmp, g.rect.Min, withCursor); err != nil {
		return nil, err
	}
	return g.bmp.Pix, nil
}

// Frame 把最近一次抓到的画面复制成一份独立的 image.RGBA。
func (g *Grabber) Frame() *image.RGBA {
	return g.bmp.RGBA(image.Rect(0, 0, g.bmp.Width, g.bmp.Height))
}

// Close 释放内部位图。
func (g *Grabber) Close() {
	g.bmp.Dispose()
}
