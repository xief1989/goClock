package ui

import (
	"fmt"
	"image"
	"unsafe"

	"github.com/lxn/win"

	"goclock/internal/capture"
)

const (
	viewerClass = "GoClock.LongViewer"
	// viewerBarHeight 是底部按钮栏的高度（96 DPI 下的像素）。
	viewerBarHeight = 48
	// viewerWheelStep 是滚轮每格滚动的距离（96 DPI 下的像素）。
	viewerWheelStep = 100
)

// 预览窗底部按钮。
const (
	viewerBtnCopy = iota
	viewerBtnSave
	viewerBtnOCR
	viewerBtnPin
	viewerBtnClose
	viewerBtnCount
)

// viewerActions 是长图预览窗需要 App 提供的操作。
type viewerActions struct {
	copy      func(img *image.RGBA)
	save      func(img *image.RGBA)
	recognize func(img *image.RGBA)
	pin       func(img *image.RGBA, at image.Point)
	closed    func(v *longViewer)
}

// longViewer 是长截图完成后的预览窗口：按窗口宽度缩放显示，可上下滚动，底部有复制、保存、钉图。
type longViewer struct {
	hwnd     win.HWND
	img      *image.RGBA
	canvas   *memCanvas
	scale    float64
	scroll   int
	hoverBtn int
	font     win.HFONT
	actions  viewerActions
}

// newLongViewer 在 near（屏幕坐标）所在的显示器上打开长图预览窗。
func newLongViewer(img *image.RGBA, near image.Point, actions viewerActions) (*longViewer, error) {
	canvas, err := newCanvasFromRGBA(img)
	if err != nil {
		return nil, err
	}
	mon := capture.MonitorAt(near)
	v := &longViewer{img: img, canvas: canvas, scale: float64(mon.DPI) / 96, hoverBtn: -1, actions: actions}
	v.font = newFont(dip(13, v.scale), false)

	// 窗口宽度尽量容下整张图的原始宽度，高度占显示器可用高度的大部分。
	work := mon.Work
	frameW := dip(40, v.scale)
	w := min(img.Rect.Dx()+frameW, work.Dx()*8/10)
	w = max(w, dip(640, v.scale))
	h := work.Dy() * 85 / 100
	x := work.Min.X + (work.Dx()-w)/2
	y := work.Min.Y + (work.Dy()-h)/2

	hwnd, err := createRawWindow(v, rawWindowSpec{
		class:  viewerClass,
		title:  fmt.Sprintf("长截图预览 · %d × %d", img.Rect.Dx(), img.Rect.Dy()),
		style:  win.WS_OVERLAPPEDWINDOW | win.WS_VSCROLL,
		bounds: image.Rect(x, y, x+w, y+h),
	})
	if err != nil {
		canvas.Dispose()
		win.DeleteObject(win.HGDIOBJ(v.font))
		return nil, err
	}
	v.hwnd = hwnd
	disableIME(hwnd)
	v.updateScrollBar()
	win.ShowWindow(hwnd, win.SW_SHOW)
	activateWindow(hwnd)
	return v, nil
}

// Close 关闭预览窗。
func (v *longViewer) Close() {
	if v.hwnd != 0 {
		win.DestroyWindow(v.hwnd)
	}
}

// viewArea 返回显示图片的区域（客户区去掉底部按钮栏）。
func (v *longViewer) viewArea() image.Rectangle {
	c := clientRect(v.hwnd)
	c.Max.Y -= dip(viewerBarHeight, v.scale)
	return c
}

// zoom 返回图片的显示比例：宽度放得下就按原始大小，放不下就缩到窗口宽度。
func (v *longViewer) zoom() float64 {
	area := v.viewArea()
	if area.Dx() <= 0 || v.img.Rect.Dx() <= area.Dx() {
		return 1
	}
	return float64(area.Dx()) / float64(v.img.Rect.Dx())
}

// contentHeight 返回按当前比例显示时整张图的高度。
func (v *longViewer) contentHeight() int {
	return int(float64(v.img.Rect.Dy()) * v.zoom())
}

// updateScrollBar 按内容高度与可视高度更新滚动条，并把滚动位置限制在有效范围内。
func (v *longViewer) updateScrollBar() {
	page := max(v.viewArea().Dy(), 1)
	total := v.contentHeight()
	v.scroll = max(0, min(v.scroll, total-page))
	si := win.SCROLLINFO{
		FMask: win.SIF_RANGE | win.SIF_PAGE | win.SIF_POS,
		NMin:  0,
		NMax:  int32(max(total-1, 0)),
		NPage: uint32(page),
		NPos:  int32(v.scroll),
	}
	si.CbSize = uint32(unsafe.Sizeof(si))
	win.SetScrollInfo(v.hwnd, win.SB_VERT, &si, true)
}

// scrollTo 滚动到指定位置。
func (v *longViewer) scrollTo(pos int) {
	v.scroll = pos
	v.updateScrollBar()
	win.InvalidateRect(v.hwnd, nil, false)
}

// buttons 返回底部按钮的位置与文字。
func (v *longViewer) buttons() ([viewerBtnCount]image.Rectangle, [viewerBtnCount]string) {
	c := clientRect(v.hwnd)
	barH := dip(viewerBarHeight, v.scale)
	bw, bh := dip(92, v.scale), dip(30, v.scale)
	gap := dip(8, v.scale)
	y := c.Max.Y - barH + (barH-bh)/2
	x := c.Max.X - dip(12, v.scale) - bw*viewerBtnCount - gap*(viewerBtnCount-1)
	var rs [viewerBtnCount]image.Rectangle
	for i := range rs {
		rs[i] = image.Rect(x, y, x+bw, y+bh)
		x += bw + gap
	}
	return rs, [viewerBtnCount]string{"复制 Ctrl+C", "保存 Ctrl+S", "识别文字", "钉到桌面", "关闭 Esc"}
}

// buttonAt 返回点 p 下的按钮，没有命中时返回 -1。
func (v *longViewer) buttonAt(p image.Point) int {
	rs, _ := v.buttons()
	for i, r := range rs {
		if p.In(r) {
			return i
		}
	}
	return -1
}

// click 执行按钮动作。
func (v *longViewer) click(btn int) {
	switch btn {
	case viewerBtnCopy:
		v.actions.copy(v.img)
	case viewerBtnSave:
		v.actions.save(v.img)
	case viewerBtnOCR:
		v.actions.recognize(v.img)
	case viewerBtnPin:
		var rc win.RECT
		capture.WithPerMonitorDPI(func() {
			win.GetWindowRect(v.hwnd, &rc)
		})
		v.actions.pin(v.img, image.Pt(int(rc.Left), int(rc.Top)))
		v.Close()
	case viewerBtnClose:
		v.Close()
	}
}

// handleMessage 实现 rawHandler。
func (v *longViewer) handleMessage(hwnd win.HWND, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	switch msg {
	case win.WM_PAINT:
		v.paint()
		return 0, true
	case win.WM_ERASEBKGND:
		return 1, true
	case win.WM_SIZE:
		if v.hwnd != 0 {
			v.updateScrollBar()
			win.InvalidateRect(hwnd, nil, false)
		}
		return 0, true
	case win.WM_DPICHANGED:
		// 拖到缩放比例不同的显示器上：窗口保持像素尺寸不变（图片本来就按物理像素显示），
		// 只把按钮栏的字体、间距换成新显示器的缩放比例。
		v.scale = float64(wParam&0xFFFF) / 96
		win.DeleteObject(win.HGDIOBJ(v.font))
		v.font = newFont(dip(13, v.scale), false)
		v.updateScrollBar()
		win.InvalidateRect(hwnd, nil, false)
		return 0, true
	case win.WM_MOUSEWHEEL:
		v.scrollTo(v.scroll - wheelDelta(wParam)*dip(viewerWheelStep, v.scale)/wheelDeltaUnit)
		return 0, true
	case win.WM_VSCROLL:
		v.onVScroll(uint32(wParam & 0xFFFF))
		return 0, true
	case win.WM_MOUSEMOVE:
		if b := v.buttonAt(pointFromLParam(lParam)); b != v.hoverBtn {
			v.hoverBtn = b
			win.InvalidateRect(hwnd, nil, false)
		}
		return 0, true
	case win.WM_LBUTTONUP:
		v.click(v.buttonAt(pointFromLParam(lParam)))
		return 0, true
	case win.WM_KEYDOWN:
		v.onKeyDown(uint32(wParam))
		return 0, true
	case win.WM_DESTROY:
		v.canvas.Dispose()
		win.DeleteObject(win.HGDIOBJ(v.font))
		v.hwnd = 0
		if v.actions.closed != nil {
			v.actions.closed(v)
		}
		return 0, true
	}
	return 0, false
}

// onVScroll 响应滚动条操作。
func (v *longViewer) onVScroll(code uint32) {
	page := v.viewArea().Dy()
	line := dip(viewerWheelStep, v.scale) / 2
	switch code {
	case win.SB_LINEUP:
		v.scrollTo(v.scroll - line)
	case win.SB_LINEDOWN:
		v.scrollTo(v.scroll + line)
	case win.SB_PAGEUP:
		v.scrollTo(v.scroll - page)
	case win.SB_PAGEDOWN:
		v.scrollTo(v.scroll + page)
	case win.SB_TOP:
		v.scrollTo(0)
	case win.SB_BOTTOM:
		v.scrollTo(v.contentHeight())
	case win.SB_THUMBTRACK, win.SB_THUMBPOSITION:
		si := win.SCROLLINFO{FMask: win.SIF_TRACKPOS}
		si.CbSize = uint32(unsafe.Sizeof(si))
		if win.GetScrollInfo(v.hwnd, win.SB_VERT, &si) {
			v.scrollTo(int(si.NTrackPos))
		}
	}
}

// onKeyDown 处理快捷键与键盘滚动。
func (v *longViewer) onKeyDown(vk uint32) {
	ctrl := keyDown(win.VK_CONTROL)
	page := v.viewArea().Dy()
	switch {
	case vk == win.VK_ESCAPE:
		v.Close()
	case ctrl && vk == vkC:
		v.click(viewerBtnCopy)
	case ctrl && vk == vkS:
		v.click(viewerBtnSave)
	case vk == win.VK_UP:
		v.scrollTo(v.scroll - dip(viewerWheelStep, v.scale)/2)
	case vk == win.VK_DOWN:
		v.scrollTo(v.scroll + dip(viewerWheelStep, v.scale)/2)
	case vk == win.VK_PRIOR:
		v.scrollTo(v.scroll - page)
	case vk == win.VK_NEXT, vk == win.VK_SPACE:
		v.scrollTo(v.scroll + page)
	case vk == win.VK_HOME:
		v.scrollTo(0)
	case vk == win.VK_END:
		v.scrollTo(v.contentHeight())
	}
}

// paint 绘制可视范围内的图片与底部按钮栏。
func (v *longViewer) paint() {
	var ps win.PAINTSTRUCT
	hdc := win.BeginPaint(v.hwnd, &ps)
	defer win.EndPaint(v.hwnd, &ps)

	client := clientRect(v.hwnd)
	bmp, err := capture.NewBitmap(max(client.Dx(), 1), max(client.Dy(), 1))
	if err != nil {
		return
	}
	c, err := newMemCanvas(bmp)
	if err != nil {
		bmp.Dispose()
		return
	}
	defer c.Dispose()
	dc := c.dc

	area := v.viewArea()
	fillRect(dc, area, colorThumbBg)
	z := v.zoom()
	dispW := int(float64(v.img.Rect.Dx()) * z)
	x := area.Min.X + (area.Dx()-dispW)/2
	visibleH := min(area.Dy(), v.contentHeight()-v.scroll)
	if visibleH > 0 {
		dst := image.Rect(x, area.Min.Y, x+dispW, area.Min.Y+visibleH)
		srcY0 := int(float64(v.scroll) / z)
		srcY1 := int(float64(v.scroll+visibleH) / z)
		src := image.Rect(0, srcY0, v.img.Rect.Dx(), max(srcY1, srcY0+1))
		if z == 1 {
			blit(dc, dst, v.canvas.dc, src.Min)
		} else {
			stretchHalftone(dc, dst, v.canvas.dc, src)
		}
	}

	bar := image.Rect(client.Min.X, area.Max.Y, client.Max.X, client.Max.Y)
	fillRect(dc, bar, colorPanel)
	fillRect(dc, image.Rect(bar.Min.X, bar.Min.Y, bar.Max.X, bar.Min.Y+1), colorPanelBorder)
	info := fmt.Sprintf("%d × %d px", v.img.Rect.Dx(), v.img.Rect.Dy())
	if z < 1 {
		info += fmt.Sprintf("（按 %.0f%% 显示）", z*100)
	}
	drawText(dc, info, image.Rect(bar.Min.X+dip(12, v.scale), bar.Min.Y, bar.Max.X, bar.Max.Y),
		v.font, colorMuted, dtVCenter|dtSingleLine)

	rs, labels := v.buttons()
	for i, r := range rs {
		bg, fg := colorBtnBg, colorTitle
		if i == v.hoverBtn {
			bg = colorBtnHover
		}
		if i == viewerBtnCopy {
			bg, fg = colorAccent, colorWhite
		}
		fillRect(dc, r, bg)
		drawText(dc, labels[i], r, v.font, fg, dtCenter|dtVCenter|dtSingleLine)
	}

	blit(hdc, client, dc, image.Point{})
}
