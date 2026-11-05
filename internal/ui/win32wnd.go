package ui

import (
	"errors"
	"fmt"
	"image"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"goclock/internal/capture"
	"goclock/internal/storage"
)

// 截图相关的窗口（全屏遮罩、钉图、长截图面板、长图预览）不走 walk 控件，而是原生 Win32 窗口：
// 它们需要全屏双缓冲自绘、高频鼠标、鼠标穿透的分层样式，以及按显示器的 DPI 感知，walk 控件都给不了。
// walk 的消息循环用 GetMessage(msg, 0, 0, 0) 分发本线程的全部消息，原生窗口可以直接挂在界面线程上。

var (
	libUser32 = windows.NewLazySystemDLL("user32.dll")
	libGdi32  = windows.NewLazySystemDLL("gdi32.dll")
	libImm32  = windows.NewLazySystemDLL("imm32.dll")

	procSetWindowDisplayAffinity   = libUser32.NewProc("SetWindowDisplayAffinity")
	procGetAsyncKeyState           = libUser32.NewProc("GetAsyncKeyState")
	procSetWindowRgn               = libUser32.NewProc("SetWindowRgn")
	procSetLayeredWindowAttributes = libUser32.NewProc("SetLayeredWindowAttributes")
	procDrawTextW                  = libUser32.NewProc("DrawTextW")
	procFillRect                   = libUser32.NewProc("FillRect")
	procAppendMenuW                = libUser32.NewProc("AppendMenuW")

	procCreateSolidBrush = libGdi32.NewProc("CreateSolidBrush")
	procCreateFontW      = libGdi32.NewProc("CreateFontW")
	procBeginPath        = libGdi32.NewProc("BeginPath")
	procEndPath          = libGdi32.NewProc("EndPath")
	procWidenPath        = libGdi32.NewProc("WidenPath")
	procSelectClipPath   = libGdi32.NewProc("SelectClipPath")
	procSetBrushOrgEx    = libGdi32.NewProc("SetBrushOrgEx")

	procImmGetContext           = libImm32.NewProc("ImmGetContext")
	procImmReleaseContext       = libImm32.NewProc("ImmReleaseContext")
	procImmSetCompositionWindow = libImm32.NewProc("ImmSetCompositionWindow")
	procImmAssociateContext     = libImm32.NewProc("ImmAssociateContext")
)

// 本文件用到、但 lxn/win 没有定义的常量。
const (
	wdaExcludeFromCapture = 0x11
	lwaAlpha              = 0x2
	rgnAnd                = 1
	rgnDiff               = 4
	maNoActivate          = 3
	mfString              = 0x0000
	mfSeparator           = 0x0800
	idcHand               = 32649
	idcSizeNESW           = 32643
	idcSizeWE             = 32644
	idcSizeNS             = 32645
	idcArrow              = 32512
	cfsPoint              = 0x0002
	antialiasedQuality    = 4
	fwNormal              = 400
	fwBold                = 700
	defaultCharset        = 1
	dtCalcRect            = 0x0400
	dtNoPrefix            = 0x0800
	dtWordBreak           = 0x0010
	dtSingleLine          = 0x0020
	dtVCenter             = 0x0004
	dtCenter              = 0x0001
	dtRight               = 0x0002
	dtEndEllipsis         = 0x8000
	iconSmall             = 0
	iconBig               = 1
	vkA                   = 'A'
	vkC                   = 'C'
	vkS                   = 'S'
	vkV                   = 'V'
	vkZ                   = 'Z'
)

// rawHandler 处理原生窗口的消息；handled 为 false 时交给 DefWindowProc。
type rawHandler interface {
	handleMessage(hwnd win.HWND, msg uint32, wParam, lParam uintptr) (result uintptr, handled bool)
}

var (
	// rawHandlers 按句柄找到窗口对应的处理者，只在界面线程上访问。
	rawHandlers = map[win.HWND]rawHandler{}
	// rawCreating 是正在创建中的窗口的处理者：CreateWindowEx 返回之前就会收到消息，那时还拿不到句柄。
	rawCreating rawHandler
	// rawWndProcPtr 是所有原生窗口共用的窗口过程。回调地址数量有限，只创建一次。
	rawWndProcPtr = syscall.NewCallback(rawWndProc)
	rawClasses    = map[string]bool{}
)

// rawWndProc 把消息转给窗口对应的处理者。
func rawWndProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) (result uintptr) {
	h := rawHandlers[hwnd]
	if h == nil && rawCreating != nil {
		h = rawCreating
		rawHandlers[hwnd] = h
	}
	if msg == win.WM_NCDESTROY {
		delete(rawHandlers, hwnd)
	}
	if h != nil {
		// 截图窗口里的意外不能把整个托盘程序带崩：吞掉 panic，按默认方式处理这条消息。
		ret, handled := func() (r uintptr, ok bool) {
			defer func() {
				if recover() != nil {
					r, ok = 0, false
				}
			}()
			return h.handleMessage(hwnd, msg, wParam, lParam)
		}()
		if handled {
			return ret
		}
	}
	return win.DefWindowProc(hwnd, msg, wParam, lParam)
}

// registerRawClass 注册一个原生窗口类，同名的类只注册一次。
func registerRawClass(name string, style uint32) error {
	if rawClasses[name] {
		return nil
	}
	wc := win.WNDCLASSEX{
		Style:         style,
		LpfnWndProc:   rawWndProcPtr,
		HInstance:     win.GetModuleHandle(nil),
		HCursor:       win.LoadCursor(0, win.MAKEINTRESOURCE(idcArrow)),
		LpszClassName: syscall.StringToUTF16Ptr(name),
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	if win.RegisterClassEx(&wc) == 0 {
		return fmt.Errorf("注册窗口类 %s 失败：%w", name, windows.GetLastError())
	}
	rawClasses[name] = true
	return nil
}

// rawWindowSpec 描述要创建的原生窗口。
type rawWindowSpec struct {
	class      string
	classStyle uint32
	title      string
	exStyle    uint32
	style      uint32
	// bounds 是窗口的屏幕坐标范围（物理像素）。
	bounds image.Rectangle
}

// createRawWindow 在「按显示器感知 V2」的 DPI 上下文中创建原生窗口，
// 这样窗口的坐标、尺寸与抓屏得到的像素一一对应，不会被系统缩放。
func createRawWindow(h rawHandler, spec rawWindowSpec) (win.HWND, error) {
	if err := registerRawClass(spec.class, spec.classStyle); err != nil {
		return 0, err
	}
	var hwnd win.HWND
	capture.WithPerMonitorDPI(func() {
		rawCreating = h
		defer func() { rawCreating = nil }()
		hwnd = win.CreateWindowEx(spec.exStyle,
			syscall.StringToUTF16Ptr(spec.class), syscall.StringToUTF16Ptr(spec.title),
			spec.style,
			int32(spec.bounds.Min.X), int32(spec.bounds.Min.Y),
			int32(spec.bounds.Dx()), int32(spec.bounds.Dy()),
			0, 0, win.GetModuleHandle(nil), nil)
	})
	if hwnd == 0 {
		return 0, fmt.Errorf("创建窗口失败：%w", windows.GetLastError())
	}
	rawHandlers[hwnd] = h
	if small := appHICON(16); small != 0 {
		win.SendMessage(hwnd, win.WM_SETICON, iconSmall, uintptr(small))
	}
	if big := appHICON(32); big != 0 {
		win.SendMessage(hwnd, win.WM_SETICON, iconBig, uintptr(big))
	}
	return hwnd, nil
}

// appHICONs 缓存按尺寸加载的程序图标句柄，程序退出前一直有效。
var appHICONs = map[int]win.HICON{}

// appHICON 返回指定尺寸的程序图标句柄，供原生窗口的标题栏与任务栏使用。
// walk 的 Icon 不公开句柄，所以直接读 appIcon 缓存到用户目录的图标文件。
func appHICON(size int) win.HICON {
	if h, ok := appHICONs[size]; ok {
		return h
	}
	var h win.HICON
	if appIcon() != nil {
		if dir, err := storage.DataDir(); err == nil {
			path := syscall.StringToUTF16Ptr(filepath.Join(dir, "app.ico"))
			h = win.HICON(win.LoadImage(0, path, win.IMAGE_ICON, int32(size), int32(size), win.LR_LOADFROMFILE))
		}
	}
	appHICONs[size] = h
	return h
}

// activateWindow 把窗口切到前台并给它键盘焦点。
//
// 由热键触发时，系统的前台锁常常让 SetForegroundWindow 失效：窗口显示出来了，
// 键盘输入却还留在原来的程序里，截图时按 Esc、C 都没反应。
// 先把本线程的输入状态临时挂到当前前台窗口的线程上，再切前台，这是绕过前台锁的常规做法。
func activateWindow(hwnd win.HWND) {
	fg := win.GetForegroundWindow()
	if fg == hwnd {
		win.SetFocus(hwnd)
		return
	}
	cur := win.GetCurrentThreadId()
	fgThread := win.GetWindowThreadProcessId(fg, nil)
	attached := fg != 0 && fgThread != 0 && fgThread != cur &&
		win.AttachThreadInput(int32(cur), int32(fgThread), true)
	win.BringWindowToTop(hwnd)
	win.SetForegroundWindow(hwnd)
	win.SetFocus(hwnd)
	if attached {
		win.AttachThreadInput(int32(cur), int32(fgThread), false)
	}
}

// excludeFromCapture 让窗口不出现在任何截图里（Win10 2004 起支持），失败时静默忽略。
func excludeFromCapture(hwnd win.HWND) {
	if procSetWindowDisplayAffinity.Find() == nil {
		procSetWindowDisplayAffinity.Call(uintptr(hwnd), wdaExcludeFromCapture)
	}
}

// pointFromLParam 从鼠标消息的 lParam 中取出客户区坐标（带符号，多屏时可能为负）。
func pointFromLParam(lParam uintptr) image.Point {
	return image.Pt(int(int16(lParam&0xFFFF)), int(int16((lParam>>16)&0xFFFF)))
}

// wheelDelta 从 WM_MOUSEWHEEL 的 wParam 中取出滚动量，向上为正。
func wheelDelta(wParam uintptr) int {
	return int(int16((wParam >> 16) & 0xFFFF))
}

// keyDown 判断某个键当前是否处于按下状态（随消息队列同步）。
func keyDown(vk int32) bool {
	return win.GetKeyState(vk) < 0
}

// asyncKeyDown 判断某个键此刻是否被按住，不依赖窗口是否有焦点。
func asyncKeyDown(vk int32) bool {
	ret, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return ret&0x8000 != 0
}

// winRect 把 image.Rectangle 转换成 Win32 的 RECT。
func winRect(r image.Rectangle) win.RECT {
	return win.RECT{Left: int32(r.Min.X), Top: int32(r.Min.Y), Right: int32(r.Max.X), Bottom: int32(r.Max.Y)}
}

// imageRect 把 Win32 的 RECT 转换成 image.Rectangle。
func imageRect(r win.RECT) image.Rectangle {
	return image.Rect(int(r.Left), int(r.Top), int(r.Right), int(r.Bottom))
}

// invalidateRect 让窗口的一块区域重绘，区域为空时什么也不做。
func invalidateRect(hwnd win.HWND, r image.Rectangle) {
	if r.Empty() {
		return
	}
	rc := winRect(r)
	win.InvalidateRect(hwnd, &rc, false)
}

// clientRect 返回窗口客户区的范围。
func clientRect(hwnd win.HWND) image.Rectangle {
	var rc win.RECT
	win.GetClientRect(hwnd, &rc)
	return imageRect(rc)
}

// loadCursor 加载系统光标。
func loadCursor(id uintptr) win.HCURSOR {
	return win.LoadCursor(0, win.MAKEINTRESOURCE(id))
}

// dip 把 96 DPI 下的长度按缩放比例换算成物理像素，至少为 1。
func dip(v int, scale float64) int {
	px := int(float64(v)*scale + 0.5)
	return max(px, 1)
}

// fillRect 用纯色填充矩形。
func fillRect(hdc win.HDC, r image.Rectangle, c walk.Color) {
	if r.Empty() {
		return
	}
	brush, _, _ := procCreateSolidBrush.Call(uintptr(c))
	if brush == 0 {
		return
	}
	rc := winRect(r)
	procFillRect.Call(uintptr(hdc), uintptr(unsafe.Pointer(&rc)), brush)
	win.DeleteObject(win.HGDIOBJ(brush))
}

// frameRect 沿矩形内侧画一圈宽 width 的边框。
func frameRect(hdc win.HDC, r image.Rectangle, width int, c walk.Color) {
	fillRect(hdc, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+width), c)
	fillRect(hdc, image.Rect(r.Min.X, r.Max.Y-width, r.Max.X, r.Max.Y), c)
	fillRect(hdc, image.Rect(r.Min.X, r.Min.Y+width, r.Min.X+width, r.Max.Y-width), c)
	fillRect(hdc, image.Rect(r.Max.X-width, r.Min.Y+width, r.Max.X, r.Max.Y-width), c)
}

// blit 把 src 中以 srcPt 为左上角的一块复制到 dst 的矩形 r。
func blit(dst win.HDC, r image.Rectangle, src win.HDC, srcPt image.Point) {
	if r.Empty() {
		return
	}
	win.BitBlt(dst, int32(r.Min.X), int32(r.Min.Y), int32(r.Dx()), int32(r.Dy()),
		src, int32(srcPt.X), int32(srcPt.Y), win.SRCCOPY)
}

// newFont 按像素高度创建界面字体，调用方负责 DeleteObject。
func newFont(px int, bold bool) win.HFONT {
	weight := fwNormal
	if bold {
		weight = fwBold
	}
	face := syscall.StringToUTF16Ptr(uiFontFamily)
	ret, _, _ := procCreateFontW.Call(
		uintptr(int32(-px)), 0, 0, 0, uintptr(weight),
		0, 0, 0, defaultCharset, 0, 0, antialiasedQuality, 0,
		uintptr(unsafe.Pointer(face)))
	return win.HFONT(ret)
}

// drawText 在矩形 r 内绘制文字，format 为 DrawText 的格式标志。
func drawText(hdc win.HDC, text string, r image.Rectangle, font win.HFONT, c walk.Color, format uint32) {
	if text == "" {
		return
	}
	old := win.SelectObject(hdc, win.HGDIOBJ(font))
	win.SetBkMode(hdc, win.TRANSPARENT)
	win.SetTextColor(hdc, win.COLORREF(c))
	u, _ := syscall.UTF16FromString(text)
	rc := winRect(r)
	procDrawTextW.Call(uintptr(hdc), uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1),
		uintptr(unsafe.Pointer(&rc)), uintptr(format|dtNoPrefix))
	win.SelectObject(hdc, old)
}

// measureText 测量文字在给定字体下的尺寸；多行文字按换行符分行，不自动折行。
func measureText(hdc win.HDC, text string, font win.HFONT) image.Point {
	if text == "" {
		text = " "
	}
	old := win.SelectObject(hdc, win.HGDIOBJ(font))
	u, _ := syscall.UTF16FromString(text)
	var rc win.RECT
	procDrawTextW.Call(uintptr(hdc), uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1),
		uintptr(unsafe.Pointer(&rc)), dtCalcRect|dtNoPrefix)
	win.SelectObject(hdc, old)
	return image.Pt(int(rc.Right-rc.Left), int(rc.Bottom-rc.Top))
}

// memCanvas 是选入了一块位图的内存 DC。
type memCanvas struct {
	dc  win.HDC
	bmp *capture.Bitmap
	old win.HGDIOBJ
}

// newMemCanvas 为位图创建内存 DC。
func newMemCanvas(bmp *capture.Bitmap) (*memCanvas, error) {
	dc := win.CreateCompatibleDC(0)
	if dc == 0 {
		return nil, errors.New("创建内存设备上下文失败")
	}
	return &memCanvas{dc: dc, bmp: bmp, old: win.SelectObject(dc, win.HGDIOBJ(bmp.Handle))}, nil
}

// Dispose 释放内存 DC 以及它持有的位图。重复调用是安全的。
func (c *memCanvas) Dispose() {
	if c == nil || c.dc == 0 {
		return
	}
	win.SelectObject(c.dc, c.old)
	win.DeleteDC(c.dc)
	c.dc = 0
	c.bmp.Dispose()
}

// newCanvasFromRGBA 把图片复制进一块新位图并为它创建内存 DC。
func newCanvasFromRGBA(img *image.RGBA) (*memCanvas, error) {
	bmp, err := capture.BitmapFromRGBA(img)
	if err != nil {
		return nil, err
	}
	c, err := newMemCanvas(bmp)
	if err != nil {
		bmp.Dispose()
		return nil, err
	}
	return c, nil
}

// stretchHalftone 用高质量的 HALFTONE 模式把 src 的一块缩放绘制到 dst。
func stretchHalftone(dst win.HDC, d image.Rectangle, src win.HDC, s image.Rectangle) {
	win.SetStretchBltMode(dst, win.HALFTONE)
	// 切换到 HALFTONE 之后必须重设画刷原点，否则会出现错位的花纹。
	procSetBrushOrgEx.Call(uintptr(dst), 0, 0, 0)
	win.StretchBlt(dst, int32(d.Min.X), int32(d.Min.Y), int32(d.Dx()), int32(d.Dy()),
		src, int32(s.Min.X), int32(s.Min.Y), int32(s.Dx()), int32(s.Dy()), win.SRCCOPY)
}

// popupMenuItem 是右键菜单中的一项；id 为 0 表示分隔线。
type popupMenuItem struct {
	id   uint32
	text string
}

// showPopupMenu 在屏幕坐标 at 弹出菜单，返回用户选中项的 id，取消时返回 0。
func showPopupMenu(owner win.HWND, at image.Point, items []popupMenuItem) uint32 {
	menu := win.CreatePopupMenu()
	if menu == 0 {
		return 0
	}
	defer win.DestroyMenu(menu)
	for _, it := range items {
		if it.id == 0 {
			procAppendMenuW.Call(uintptr(menu), mfSeparator, 0, 0)
			continue
		}
		procAppendMenuW.Call(uintptr(menu), mfString, uintptr(it.id),
			uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(it.text))))
	}
	// 弹出菜单前把属主窗口设为前台，否则点菜单外面时菜单不会消失。
	win.SetForegroundWindow(owner)
	var cmd uint32
	capture.WithPerMonitorDPI(func() {
		cmd = uint32(win.TrackPopupMenuEx(menu, win.TPM_RETURNCMD|win.TPM_RIGHTBUTTON,
			int32(at.X), int32(at.Y), owner, nil))
	})
	return cmd
}

// disableIME 把输入法从窗口上摘掉，返回原来的输入法上下文，供 restoreIME 挂回去。
//
// 中文输入法开着时，Esc、C、方向键这些按键会被输入法截走（窗口只收到 VK_PROCESSKEY），
// 截图遮罩、钉图、预览窗的快捷键就全部失灵。这些窗口平时不需要打字，一律关掉输入法。
func disableIME(hwnd win.HWND) uintptr {
	if procImmAssociateContext.Find() != nil {
		return 0
	}
	prev, _, _ := procImmAssociateContext.Call(uintptr(hwnd), 0)
	return prev
}

// restoreIME 把 disableIME 摘下的输入法上下文挂回窗口，用于输入文字标注时打中文。
func restoreIME(hwnd win.HWND, himc uintptr) {
	if himc == 0 || procImmAssociateContext.Find() != nil {
		return
	}
	procImmAssociateContext.Call(uintptr(hwnd), himc)
}

// compositionForm 对应 IMM 的 COMPOSITIONFORM。
type compositionForm struct {
	style uint32
	pos   win.POINT
	area  win.RECT
}

// moveIMEWindow 把输入法的候选/组字窗口挪到客户区坐标 p 附近，打中文时候选框跟着文字走。
func moveIMEWindow(hwnd win.HWND, p image.Point) {
	if procImmGetContext.Find() != nil {
		return
	}
	himc, _, _ := procImmGetContext.Call(uintptr(hwnd))
	if himc == 0 {
		return
	}
	form := compositionForm{style: cfsPoint, pos: win.POINT{X: int32(p.X), Y: int32(p.Y)}}
	procImmSetCompositionWindow.Call(himc, uintptr(unsafe.Pointer(&form)))
	procImmReleaseContext.Call(uintptr(hwnd), himc)
}
