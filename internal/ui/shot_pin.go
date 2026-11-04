package ui

import (
	"image"

	"github.com/lxn/win"

	"goclock/internal/capture"
)

const (
	pinWindowClass = "GoClock.Pin"
	// 钉图缩放的上下限与每格滚轮的步长。
	pinMinZoom  = 0.1
	pinMaxZoom  = 5.0
	pinZoomStep = 1.1
)

// 钉图右键菜单项。
const (
	pinMenuCopy uint32 = iota + 1
	pinMenuSave
	pinMenuOCR
	pinMenuTranslate
	pinMenuActual
	pinMenuClose
)

// pinActions 是钉图窗口需要 App 提供的操作。
type pinActions struct {
	copy func(img *image.RGBA)
	save func(img *image.RGBA)
	// recognize 识别图片里的文字，translate 为 true 时接着翻译。
	recognize func(img *image.RGBA, translate bool)
	closed    func(p *pinWindow)
}

// pinWindow 是把截图钉在桌面上的置顶小窗：可拖动、滚轮缩放、双击关闭。
type pinWindow struct {
	hwnd    win.HWND
	img     *image.RGBA
	canvas  *memCanvas
	zoom    float64
	actions pinActions
}

// newPinWindow 在屏幕坐标 at 处钉一张图，zoom 是初始缩放比例。
func newPinWindow(img *image.RGBA, at image.Point, zoom float64, actions pinActions) (*pinWindow, error) {
	canvas, err := newCanvasFromRGBA(img)
	if err != nil {
		return nil, err
	}
	p := &pinWindow{img: img, canvas: canvas, zoom: zoom, actions: actions}
	size := p.scaledSize()
	hwnd, err := createRawWindow(p, rawWindowSpec{
		class: pinWindowClass,
		// CS_DROPSHADOW 让钉图带一圈系统阴影，和背景区分开。
		classStyle: win.CS_DROPSHADOW,
		title:      "GoClock 钉图",
		exStyle:    win.WS_EX_TOPMOST | win.WS_EX_TOOLWINDOW,
		style:      win.WS_POPUP,
		bounds:     image.Rectangle{Min: at, Max: at.Add(size)},
	})
	if err != nil {
		canvas.Dispose()
		return nil, err
	}
	p.hwnd = hwnd
	disableIME(hwnd)
	win.ShowWindow(hwnd, win.SW_SHOW)
	activateWindow(hwnd)
	return p, nil
}

// scaledSize 返回按当前缩放比例显示时的窗口尺寸。
func (p *pinWindow) scaledSize() image.Point {
	w := max(int(float64(p.img.Rect.Dx())*p.zoom+0.5), 8)
	h := max(int(float64(p.img.Rect.Dy())*p.zoom+0.5), 8)
	return image.Pt(w, h)
}

// Close 关闭钉图。
func (p *pinWindow) Close() {
	if p.hwnd != 0 {
		win.DestroyWindow(p.hwnd)
	}
}

// handleMessage 实现 rawHandler。
func (p *pinWindow) handleMessage(hwnd win.HWND, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	switch msg {
	case win.WM_PAINT:
		p.paint()
		return 0, true
	case win.WM_ERASEBKGND:
		return 1, true
	case win.WM_NCHITTEST:
		// 整张图都当作标题栏，按住任意位置都能拖动。
		return win.HTCAPTION, true
	case win.WM_NCLBUTTONDBLCLK:
		p.Close()
		return 0, true
	case win.WM_CONTEXTMENU:
		p.showMenu()
		return 0, true
	case win.WM_MOUSEWHEEL:
		p.zoomBy(wheelDelta(wParam))
		return 0, true
	case win.WM_KEYDOWN:
		ctrl := keyDown(win.VK_CONTROL)
		switch {
		case wParam == win.VK_ESCAPE:
			p.Close()
		case ctrl && wParam == vkC:
			p.actions.copy(p.img)
		case ctrl && wParam == vkS:
			p.actions.save(p.img)
		}
		return 0, true
	case win.WM_DESTROY:
		p.canvas.Dispose()
		p.hwnd = 0
		if p.actions.closed != nil {
			p.actions.closed(p)
		}
		return 0, true
	}
	return 0, false
}

// paint 绘制图片与一圈细边框。
func (p *pinWindow) paint() {
	var ps win.PAINTSTRUCT
	hdc := win.BeginPaint(p.hwnd, &ps)
	defer win.EndPaint(p.hwnd, &ps)
	client := clientRect(p.hwnd)
	src := image.Rect(0, 0, p.img.Rect.Dx(), p.img.Rect.Dy())
	if client.Size() == src.Size() {
		blit(hdc, client, p.canvas.dc, image.Point{})
	} else {
		stretchHalftone(hdc, client, p.canvas.dc, src)
	}
	frameRect(hdc, client, 1, colorAccent)
}

// zoomBy 按滚轮方向缩放，并保持光标下的那一点不动。
func (p *pinWindow) zoomBy(delta int) {
	zoom := p.zoom
	if delta > 0 {
		zoom *= pinZoomStep
	} else {
		zoom /= pinZoomStep
	}
	p.setZoom(max(pinMinZoom, min(zoom, pinMaxZoom)), capture.CursorPos())
}

// setZoom 把缩放比例设为 zoom，anchor 是保持不动的屏幕坐标点。
func (p *pinWindow) setZoom(zoom float64, anchor image.Point) {
	var rc win.RECT
	capture.WithPerMonitorDPI(func() {
		win.GetWindowRect(p.hwnd, &rc)
	})
	old := imageRect(rc)
	ratio := zoom / p.zoom
	p.zoom = zoom
	size := p.scaledSize()
	x := anchor.X - int(float64(anchor.X-old.Min.X)*ratio)
	y := anchor.Y - int(float64(anchor.Y-old.Min.Y)*ratio)
	capture.WithPerMonitorDPI(func() {
		win.SetWindowPos(p.hwnd, 0, int32(x), int32(y), int32(size.X), int32(size.Y),
			win.SWP_NOZORDER|win.SWP_NOACTIVATE)
	})
	win.InvalidateRect(p.hwnd, nil, false)
}

// showMenu 弹出右键菜单。
func (p *pinWindow) showMenu() {
	cmd := showPopupMenu(p.hwnd, capture.CursorPos(), []popupMenuItem{
		{pinMenuCopy, "复制\tCtrl+C"},
		{pinMenuSave, "另存为…\tCtrl+S"},
		{0, ""},
		{pinMenuOCR, "识别文字"},
		{pinMenuTranslate, "识别并翻译"},
		{0, ""},
		{pinMenuActual, "原始大小"},
		{0, ""},
		{pinMenuClose, "关闭\tEsc / 双击"},
	})
	switch cmd {
	case pinMenuCopy:
		p.actions.copy(p.img)
	case pinMenuSave:
		p.actions.save(p.img)
	case pinMenuOCR:
		p.actions.recognize(p.img, false)
	case pinMenuTranslate:
		p.actions.recognize(p.img, true)
	case pinMenuActual:
		var rc win.RECT
		capture.WithPerMonitorDPI(func() {
			win.GetWindowRect(p.hwnd, &rc)
		})
		p.setZoom(1, image.Pt(int(rc.Left), int(rc.Top)))
	case pinMenuClose:
		p.Close()
	}
}
