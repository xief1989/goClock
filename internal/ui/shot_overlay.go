package ui

import (
	"fmt"
	"image"
	"strings"
	"unicode/utf16"

	"github.com/lxn/walk"
	"github.com/lxn/win"

	"goclock/internal/capture"
)

// shotAction 表示截图结束时要做的事。
type shotAction int

const (
	shotCancel shotAction = iota
	shotCopy
	shotSave
	shotPin
	shotLong
	shotColor
	shotOCR
	shotTranslate
	shotRecord
)

// shotResult 是截图遮罩交还给调用方的结果。
type shotResult struct {
	action shotAction
	// img 是带标注的选区图片，复制、保存、钉图时有值。
	img *image.RGBA
	// rect 是选区的屏幕坐标（物理像素）。
	rect image.Rectangle
	// text 是取色时要复制的颜色值。
	text string
	// err 不为空表示生成图片失败。
	err error
}

// dragKind 表示鼠标按住拖动时在做什么。
type dragKind int

const (
	dragNone dragKind = iota
	dragNewSel
	dragMove
	dragResize
	dragDraw
)

const (
	shotOverlayClass = "GoClock.ShotOverlay"
	caretTimerID     = 1
	caretIntervalMs  = 530
	// clickSlop 是按下到松开移动不超过这么多像素时视为单击（选中悬停的窗口）。
	clickSlop = 3
	// 放大镜的取样格数（奇数，中心格就是光标所在像素）与每格大小。
	magCols = 17
	magRows = 11
	magCell = 8
)

// 遮罩配色。
var (
	colorMagBg   = walk.RGB(0x1F, 0x29, 0x37)
	colorMagText = walk.RGB(0xF9, 0xFA, 0xFB)
	colorMagHint = walk.RGB(0x9C, 0xA3, 0xAF)
	colorWhite   = walk.RGB(0xFF, 0xFF, 0xFF)
)

// shotOverlay 是覆盖整个虚拟桌面的截图遮罩：显示冻结的画面，让用户框选、标注，然后输出结果。
// 所有方法都在界面线程上执行。
type shotOverlay struct {
	hwnd win.HWND
	// origin 是虚拟桌面左上角的屏幕坐标；遮罩客户区坐标加上它就是屏幕坐标。
	origin image.Point
	size   image.Point
	scale  float64
	onDone func(shotResult)

	// screen 是冻结的原始画面，dim 是压暗后的画面，mosaic、blur 是马赛克化、模糊化的画面（用到时才生成），
	// back 是双缓冲用的后台画布。它们的尺寸都与虚拟桌面相同。
	screen, dim, mosaic, blur, back *memCanvas
	targets                         []capture.Target

	cursor   image.Point
	hover    image.Rectangle
	hasHover bool
	sel      image.Rectangle
	hasSel   bool

	drag      dragKind
	dragStart image.Point
	dragSel   image.Rectangle
	dragEdge  int
	dragMoved bool

	tool  shotTool
	color int
	level int
	// mosaicShape：0 画笔涂抹、1 框选区域；mosaicStyle：0 马赛克、1 模糊。
	mosaicShape int
	mosaicStyle int
	annots      []*annotation
	// cur 是正在拖动绘制的标注，text 是正在输入的文字标注。
	cur         *annotation
	text        *annotation
	caretOn     bool
	pendingHigh uint16

	layout   toolbarLayout
	hoverBtn int
	hoverPal int
	// painted 是上一次绘制的装饰范围，与本次范围合并后局部重绘。
	painted image.Rectangle

	font win.HFONT
	// imc 是被摘下的输入法上下文：平时关掉输入法保证快捷键可用，输入文字标注时再挂回去。
	imc  uintptr
	done bool
}

// newShotOverlay 冻结屏幕并显示截图遮罩。用户完成或取消后，onDone 会在遮罩销毁之后被调用。
func newShotOverlay(onDone func(shotResult)) (*shotOverlay, error) {
	vs := capture.VirtualScreen()
	shot, err := capture.GrabBitmap(vs)
	if err != nil {
		return nil, fmt.Errorf("抓取屏幕失败：%w", err)
	}

	o := &shotOverlay{origin: vs.Min, size: vs.Size(), onDone: onDone, hoverBtn: -1, hoverPal: -1, level: 1}
	ok := false
	defer func() {
		if !ok {
			o.release()
		}
	}()

	if o.screen, err = newMemCanvas(shot); err != nil {
		shot.Dispose()
		return nil, err
	}
	d, err := dimmed(shot)
	if err != nil {
		return nil, err
	}
	if o.dim, err = newMemCanvas(d); err != nil {
		d.Dispose()
		return nil, err
	}
	b, err := capture.NewBitmap(o.size.X, o.size.Y)
	if err != nil {
		return nil, err
	}
	if o.back, err = newMemCanvas(b); err != nil {
		b.Dispose()
		return nil, err
	}

	// 窗口列表必须在遮罩显示之前取，否则遮罩自己就是最上层的窗口。
	o.targets = capture.EnumTargets(vs)
	cursor := capture.CursorPos()
	o.scale = float64(capture.MonitorAt(cursor).DPI) / 96
	o.font = newFont(dip(12, o.scale), false)
	o.cursor = cursor.Sub(o.origin)
	o.updateHover()

	hwnd, err := createRawWindow(o, rawWindowSpec{
		class:      shotOverlayClass,
		classStyle: win.CS_DBLCLKS,
		title:      "GoClock 截图",
		exStyle:    win.WS_EX_TOPMOST | win.WS_EX_TOOLWINDOW,
		style:      win.WS_POPUP,
		bounds:     vs,
	})
	if err != nil {
		return nil, err
	}
	o.hwnd = hwnd
	o.imc = disableIME(hwnd)
	ok = true

	win.ShowWindow(hwnd, win.SW_SHOW)
	activateWindow(hwnd)
	win.UpdateWindow(hwnd)
	return o, nil
}

// bounds 返回遮罩客户区的范围。
func (o *shotOverlay) bounds() image.Rectangle {
	return image.Rectangle{Max: o.size}
}

// handleMessage 实现 rawHandler。
func (o *shotOverlay) handleMessage(hwnd win.HWND, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	switch msg {
	case win.WM_PAINT:
		o.paint()
		return 0, true
	case win.WM_ERASEBKGND:
		return 1, true
	case win.WM_SETCURSOR:
		if lParam&0xFFFF == win.HTCLIENT {
			win.SetCursor(o.cursorFor(o.cursor))
			return 1, true
		}
	case win.WM_MOUSEMOVE:
		o.onMouseMove(pointFromLParam(lParam))
		return 0, true
	case win.WM_LBUTTONDOWN:
		o.onLButtonDown(pointFromLParam(lParam))
		return 0, true
	case win.WM_LBUTTONUP:
		o.onLButtonUp(pointFromLParam(lParam))
		return 0, true
	case win.WM_LBUTTONDBLCLK:
		o.onDoubleClick(pointFromLParam(lParam))
		return 0, true
	case win.WM_RBUTTONUP:
		o.onRightClick()
		return 0, true
	case win.WM_CAPTURECHANGED:
		// 鼠标捕获被别的窗口抢走时，按松开处理，避免卡在拖动状态。
		if win.HWND(lParam) != hwnd && o.drag != dragNone {
			o.onLButtonUp(o.cursor)
		}
		return 0, true
	case win.WM_KEYDOWN:
		o.onKeyDown(uint32(wParam))
		return 0, true
	case win.WM_CHAR:
		o.onChar(uint16(wParam))
		return 0, true
	case win.WM_TIMER:
		if wParam == caretTimerID && o.text != nil {
			o.caretOn = !o.caretOn
			invalidateRect(o.hwnd, o.textBounds())
		}
		return 0, true
	case win.WM_DESTROY:
		o.release()
		return 0, true
	}
	return 0, false
}

// release 释放遮罩持有的全部 GDI 资源。
func (o *shotOverlay) release() {
	if o.hwnd != 0 {
		win.KillTimer(o.hwnd, caretTimerID)
	}
	o.screen.Dispose()
	o.dim.Dispose()
	o.mosaic.Dispose()
	o.blur.Dispose()
	o.back.Dispose()
	if o.font != 0 {
		win.DeleteObject(win.HGDIOBJ(o.font))
		o.font = 0
	}
}

// ---------- 鼠标与键盘 ----------

// onMouseMove 根据当前拖动状态更新选区、标注或悬停目标。
func (o *shotOverlay) onMouseMove(p image.Point) {
	o.cursor = p
	switch o.drag {
	case dragNewSel:
		if !o.dragMoved && (absInt(p.X-o.dragStart.X) >= clickSlop || absInt(p.Y-o.dragStart.Y) >= clickSlop) {
			o.dragMoved = true
		}
		if o.dragMoved {
			o.sel = spanRect(o.dragStart, p).Intersect(o.bounds())
			o.hasSel = !o.sel.Empty()
		}
	case dragMove:
		o.sel = clampInside(o.dragSel.Add(p.Sub(o.dragStart)), o.bounds())
	case dragResize:
		o.sel = resizeRect(o.dragSel, o.dragEdge, p.Sub(o.dragStart)).Intersect(o.bounds())
		if o.sel.Empty() {
			o.sel = image.Rectangle{Min: o.sel.Min, Max: o.sel.Min.Add(image.Pt(1, 1))}
		}
	case dragDraw:
		o.extendAnnotation(p)
	default:
		if !o.hasSel {
			o.updateHover()
		} else {
			o.hoverBtn = o.layout.buttonAt(p)
			o.hoverPal = o.layout.paletteAt(p)
		}
	}
	if o.drag != dragNone {
		// 鼠标被捕获期间系统不发 WM_SETCURSOR，光标要自己设。
		win.SetCursor(o.cursorFor(p))
		o.relayout()
	}
	o.refresh()
}

// onLButtonDown 处理左键按下：先看是否点在工具栏上，再决定是框选、移动、缩放还是开始画标注。
func (o *shotOverlay) onLButtonDown(p image.Point) {
	o.cursor = p
	if o.hasSel && o.drag == dragNone && o.clickToolbar(p) {
		return
	}
	if o.text != nil {
		if p.In(o.textBounds()) {
			return
		}
		o.commitText()
	}

	start := func(kind dragKind) {
		o.drag = kind
		o.dragStart = p
		o.dragSel = o.sel
		o.dragMoved = false
		win.SetCapture(o.hwnd)
	}

	switch {
	case !o.hasSel:
		o.updateHover()
		start(dragNewSel)
	case o.tool == toolText && p.In(o.sel):
		o.startText(p)
	case o.tool != toolNone && p.In(o.sel):
		o.cur = &annotation{tool: o.tool, color: shotColors[o.color], level: o.level, pts: []image.Point{p},
			area: o.tool == toolMosaic && o.mosaicShape == 1, blur: o.tool == toolMosaic && o.mosaicStyle == 1}
		start(dragDraw)
	case o.tool == toolNone:
		if e := o.handleAt(p); e >= 0 {
			o.dragEdge = e
			start(dragResize)
		} else if p.In(o.sel) {
			start(dragMove)
		} else if len(o.annots) == 0 {
			// 还没画标注时，在选区外拖动等于重新框选。
			o.hasSel = false
			o.updateHover()
			start(dragNewSel)
		}
	}
	o.refresh()
}

// onLButtonUp 结束拖动。
func (o *shotOverlay) onLButtonUp(p image.Point) {
	if o.drag == dragNone {
		return
	}
	// 先清掉拖动状态再释放捕获：ReleaseCapture 会同步发来 WM_CAPTURECHANGED，避免重入。
	kind := o.drag
	o.drag = dragNone
	win.ReleaseCapture()

	switch kind {
	case dragNewSel:
		if !o.dragMoved {
			// 单击：直接选中悬停的窗口或控件。
			o.sel, o.hasSel = o.hover, o.hasHover
		}
	case dragDraw:
		o.extendAnnotation(p)
		if o.cur.meaningful() {
			o.annots = append(o.annots, o.cur)
		}
		o.cur = nil
	}
	o.relayout()
	o.refresh()
}

// onDoubleClick 双击选区完成截图；双击工具栏按钮按单击处理。
func (o *shotOverlay) onDoubleClick(p image.Point) {
	if o.hasSel && o.clickToolbar(p) {
		return
	}
	if o.hasSel && p.In(o.sel) && o.text == nil && o.tool != toolText {
		o.finish(shotResult{action: shotCopy})
	}
}

// onRightClick 逐级后退：先取消正在输入的文字，再取消选区，最后退出截图。
func (o *shotOverlay) onRightClick() {
	switch {
	case o.text != nil:
		o.cancelText()
	case o.drag != dragNone:
	case o.hasSel:
		o.hasSel = false
		o.annots = nil
		o.tool = toolNone
		o.hoverBtn = -1
		o.updateHover()
	default:
		o.finish(shotResult{action: shotCancel})
		return
	}
	o.refresh()
}

// onKeyDown 处理快捷键。输入文字时只响应 Esc（取消）与 Ctrl+Enter（确定）。
func (o *shotOverlay) onKeyDown(vk uint32) {
	ctrl := keyDown(win.VK_CONTROL)
	if o.text != nil {
		switch {
		case vk == win.VK_ESCAPE:
			o.cancelText()
		case vk == win.VK_RETURN && ctrl:
			o.commitText()
		}
		o.refresh()
		return
	}
	step := 1
	if keyDown(win.VK_SHIFT) {
		step = 10
	}
	switch {
	case vk == win.VK_ESCAPE:
		o.finish(shotResult{action: shotCancel})
	case vk == win.VK_RETURN && o.hasSel:
		o.finish(shotResult{action: shotCopy})
	case ctrl && vk == vkC && o.hasSel:
		o.finish(shotResult{action: shotCopy})
	case ctrl && vk == vkS && o.hasSel:
		o.finish(shotResult{action: shotSave})
	case ctrl && vk == vkZ:
		o.undo()
	case !ctrl && vk == vkC && o.magnifierVisible():
		o.finish(shotResult{action: shotColor})
	case vk == win.VK_LEFT:
		o.nudge(image.Pt(-step, 0))
	case vk == win.VK_RIGHT:
		o.nudge(image.Pt(step, 0))
	case vk == win.VK_UP:
		o.nudge(image.Pt(0, -step))
	case vk == win.VK_DOWN:
		o.nudge(image.Pt(0, step))
	}
}

// nudge 用方向键微调：没有选区时挪动光标，方便精确对准像素；有选区时平移选区。
func (o *shotOverlay) nudge(d image.Point) {
	if o.drag != dragNone {
		return
	}
	if !o.hasSel {
		capture.SetCursorPos(o.cursor.Add(d).Add(o.origin))
		return
	}
	if o.tool == toolNone {
		o.sel = clampInside(o.sel.Add(d), o.bounds())
		o.relayout()
		o.refresh()
	}
}

// onChar 把输入的字符追加到正在编辑的文字标注上。
func (o *shotOverlay) onChar(ch uint16) {
	if o.text == nil {
		return
	}
	switch {
	case ch == 0x08: // 退格
		r := []rune(o.text.text)
		if len(r) > 0 {
			o.text.text = string(r[:len(r)-1])
		}
	case ch == 0x0D: // 回车换行，Ctrl+Enter 才是确定
		if !keyDown(win.VK_CONTROL) {
			o.text.text += "\n"
		}
	case ch == 0x16: // Ctrl+V 粘贴
		if s, err := walk.Clipboard().Text(); err == nil {
			o.text.text += strings.ReplaceAll(s, "\r\n", "\n")
		}
	case ch < 0x20:
	case utf16.IsSurrogate(rune(ch)):
		// 表情等补充平面字符分两次送来，先记下高位代理，等低位到了再合成。
		if ch < 0xDC00 {
			o.pendingHigh = ch
		} else if o.pendingHigh != 0 {
			o.text.text += string(utf16.DecodeRune(rune(o.pendingHigh), rune(ch)))
			o.pendingHigh = 0
		}
	default:
		o.text.text += string(rune(ch))
	}
	o.caretOn = true
	moveIMEWindow(o.hwnd, o.caretPos())
	o.refresh()
}

// clickToolbar 处理工具栏与调色板上的点击，点中了返回 true。
func (o *shotOverlay) clickToolbar(p image.Point) bool {
	if o.drag != dragNone {
		return false
	}
	if i := o.layout.buttonAt(p); i >= 0 {
		o.pressButton(barItems[i].btn)
		return true
	}
	if i := o.layout.paletteAt(p); i >= 0 {
		o.pickPalette(o.layout.items[i])
		return true
	}
	return p.In(o.layout.bar) || p.In(o.layout.palette)
}

// pickPalette 应用选项条上的一项：粗细与颜色同时作用于正在输入的文字。
func (o *shotOverlay) pickPalette(it paletteItem) {
	switch it.kind {
	case palSize:
		o.level = it.index
		if o.text != nil {
			o.text.level = it.index
		}
	case palColor:
		o.color = it.index
		if o.text != nil {
			o.text.color = shotColors[it.index]
		}
	case palShape:
		o.mosaicShape = it.index
	case palStyle:
		o.mosaicStyle = it.index
		o.ensureMosaicSource()
	}
	o.refresh()
}

// pressButton 执行工具栏按钮的动作。
func (o *shotOverlay) pressButton(b shotButton) {
	if t := b.tool(); t != toolNone {
		if o.text != nil {
			o.commitText()
		}
		if o.tool == t {
			o.tool = toolNone
		} else {
			o.tool = t
			if t == toolMosaic {
				o.ensureMosaicSource()
			}
		}
		// 选项条可能出现或消失，先按旧范围刷新一次，免得旧选项条残留在屏幕上。
		o.refresh()
		o.relayout()
		o.refresh()
		return
	}
	switch b {
	case btnUndo:
		o.undo()
	case btnOCR:
		o.finish(shotResult{action: shotOCR})
	case btnTranslate:
		o.finish(shotResult{action: shotTranslate})
	case btnRecord:
		o.finish(shotResult{action: shotRecord})
	case btnLong:
		o.finish(shotResult{action: shotLong})
	case btnPin:
		o.finish(shotResult{action: shotPin})
	case btnSave:
		o.finish(shotResult{action: shotSave})
	case btnCancel:
		o.finish(shotResult{action: shotCancel})
	case btnDone:
		o.finish(shotResult{action: shotCopy})
	}
}

// ---------- 标注 ----------

// extendAnnotation 把拖动中的标注延伸到点 p（限制在选区内）。
func (o *shotOverlay) extendAnnotation(p image.Point) {
	if o.cur == nil {
		return
	}
	p = clampPoint(p, o.sel)
	switch {
	case o.cur.tool == toolPen, o.cur.tool == toolMosaic && !o.cur.area:
		if last := o.cur.pts[len(o.cur.pts)-1]; last != p {
			o.cur.pts = append(o.cur.pts, p)
		}
	default:
		o.cur.pts = []image.Point{o.cur.pts[0], p}
	}
}

// undo 撤销最近一个标注；正在输入文字时先取消输入。
func (o *shotOverlay) undo() {
	switch {
	case o.text != nil:
		o.cancelText()
	case len(o.annots) > 0:
		o.annots = o.annots[:len(o.annots)-1]
	}
	o.refresh()
	invalidateRect(o.hwnd, o.sel)
}

// startText 在点 p 开始输入一段文字。
func (o *shotOverlay) startText(p image.Point) {
	o.text = &annotation{tool: toolText, color: shotColors[o.color], level: o.level, pts: []image.Point{p}}
	o.caretOn = true
	o.pendingHigh = 0
	win.SetTimer(o.hwnd, caretTimerID, caretIntervalMs, 0)
	restoreIME(o.hwnd, o.imc)
	moveIMEWindow(o.hwnd, o.caretPos())
	o.refresh()
}

// commitText 结束文字输入，有内容时保留为标注。
func (o *shotOverlay) commitText() {
	if o.text == nil {
		return
	}
	invalidateRect(o.hwnd, o.textBounds())
	if o.text.meaningful() {
		o.annots = append(o.annots, o.text)
	}
	o.text = nil
	win.KillTimer(o.hwnd, caretTimerID)
	disableIME(o.hwnd)
}

// cancelText 放弃正在输入的文字。
func (o *shotOverlay) cancelText() {
	if o.text == nil {
		return
	}
	invalidateRect(o.hwnd, o.textBounds())
	o.text = nil
	win.KillTimer(o.hwnd, caretTimerID)
	disableIME(o.hwnd)
}

// textFont 创建当前文字标注字号的字体，调用方负责释放。
func (o *shotOverlay) textFont() win.HFONT {
	return newFont(dip(textSizes[o.text.level], o.scale), false)
}

// textBounds 返回正在输入的文字连同虚线框占据的范围。
func (o *shotOverlay) textBounds() image.Rectangle {
	if o.text == nil {
		return image.Rectangle{}
	}
	font := o.textFont()
	defer win.DeleteObject(win.HGDIOBJ(font))
	size := measureText(o.back.dc, o.text.text, font)
	px := dip(textSizes[o.text.level], o.scale)
	size.X = max(size.X, px)
	p := o.text.pts[0]
	return image.Rectangle{Min: p, Max: p.Add(size)}.Inset(-dip(4, o.scale))
}

// caretPos 返回光标（插入点）所在位置：最后一行文字的末尾。
func (o *shotOverlay) caretPos() image.Point {
	if o.text == nil {
		return o.cursor
	}
	font := o.textFont()
	defer win.DeleteObject(win.HGDIOBJ(font))
	lines := strings.Split(o.text.text, "\n")
	last := lines[len(lines)-1]
	lineH := measureText(o.back.dc, "测", font).Y
	w := 0
	if last != "" {
		w = measureText(o.back.dc, last, font).X
	}
	return o.text.pts[0].Add(image.Pt(w, lineH*(len(lines)-1)))
}

// ensureMosaicSource 按当前打码样式，第一次用到时生成整屏马赛克或模糊画面。
// 模糊整屏要零点几秒，期间显示等待光标。
func (o *shotOverlay) ensureMosaicSource() {
	if o.mosaicStyle == 1 {
		if o.blur == nil {
			old := win.SetCursor(loadCursor(win.IDC_WAIT))
			if b, err := blurred(o.screen.bmp, dip(blurRadius, o.scale)); err == nil {
				if o.blur, err = newMemCanvas(b); err != nil {
					b.Dispose()
				}
			}
			win.SetCursor(old)
		}
		return
	}
	if o.mosaic == nil {
		if m, err := pixelate(o.screen.bmp, dip(mosaicBlock, o.scale)); err == nil {
			if o.mosaic, err = newMemCanvas(m); err != nil {
				m.Dispose()
			}
		}
	}
}

// ---------- 结束 ----------

// finish 结束截图：按动作生成结果、销毁遮罩，然后回调 onDone。只会执行一次。
func (o *shotOverlay) finish(res shotResult) {
	if o.done {
		return
	}
	o.done = true
	o.commitText()
	if o.hasSel {
		res.rect = o.sel.Add(o.origin)
	}
	switch res.action {
	case shotCopy, shotSave, shotPin:
		if !o.hasSel {
			res.action = shotCancel
			break
		}
		res.img, res.err = o.renderResult()
	case shotOCR, shotTranslate:
		// 识别文字用不带标注的原图，画上去的框、箭头只会干扰识别。
		if !o.hasSel {
			res.action = shotCancel
			break
		}
		res.img = o.screen.bmp.RGBA(o.sel)
	case shotLong, shotRecord:
		if !o.hasSel {
			res.action = shotCancel
		}
	case shotColor:
		r, g, b := o.screen.bmp.PixelAt(o.cursor.X, o.cursor.Y)
		res.text = fmt.Sprintf("#%02X%02X%02X", r, g, b)
	}
	win.ReleaseCapture()
	win.DestroyWindow(o.hwnd)
	o.onDone(res)
}

// renderResult 把选区连同标注渲染成一张图片，走的是与屏幕显示相同的绘制函数。
func (o *shotOverlay) renderResult() (*image.RGBA, error) {
	w, h := o.sel.Dx(), o.sel.Dy()
	bmp, err := capture.NewBitmap(w, h)
	if err != nil {
		return nil, err
	}
	c, err := newMemCanvas(bmp)
	if err != nil {
		bmp.Dispose()
		return nil, err
	}
	defer c.Dispose()

	full := image.Rect(0, 0, w, h)
	blit(c.dc, full, o.screen.dc, o.sel.Min)
	r := o.renderer(c.dc, o.sel.Min)
	r.draw(o.annots)
	win.GdiFlush()
	return bmp.RGBA(full), nil
}

// renderer 返回绘制标注用的渲染器，off 是目标画布原点在遮罩坐标中的位置。
func (o *shotOverlay) renderer(hdc win.HDC, off image.Point) annotRenderer {
	r := annotRenderer{hdc: hdc, scale: o.scale, off: off}
	if o.mosaic != nil {
		r.mosaic = o.mosaic.dc
	}
	if o.blur != nil {
		r.blur = o.blur.dc
	}
	return r
}

// ---------- 绘制 ----------

// paint 把需要重绘的区域渲染到后台画布，再一次性贴到窗口上，避免闪烁。
func (o *shotOverlay) paint() {
	var ps win.PAINTSTRUCT
	hdc := win.BeginPaint(o.hwnd, &ps)
	clip := imageRect(ps.RcPaint)
	o.render(clip)
	blit(hdc, clip, o.back.dc, clip.Min)
	win.EndPaint(o.hwnd, &ps)
}

// render 在后台画布上绘制 clip 范围内的全部内容。
func (o *shotOverlay) render(clip image.Rectangle) {
	dc := o.back.dc
	save := win.SaveDC(dc)
	defer win.RestoreDC(dc, save)
	win.IntersectClipRect(dc, int32(clip.Min.X), int32(clip.Min.Y), int32(clip.Max.X), int32(clip.Max.Y))

	blit(dc, clip, o.dim.dc, clip.Min)

	switch {
	case o.hasSel:
		blit(dc, o.sel, o.screen.dc, o.sel.Min)
		o.drawAnnotationsOn(dc)
		o.drawSelectionFrame(dc)
		o.drawSizeLabel(dc)
		if o.toolbarVisible() {
			drawToolbar(dc, &o.layout, toolbarState{
				tool: o.tool, color: o.color, level: o.level,
				mosaicShape: o.mosaicShape, mosaicStyle: o.mosaicStyle,
				hoverBtn: o.hoverBtn, hoverPal: o.hoverPal,
				canUndo: len(o.annots) > 0 || o.text != nil,
			}, o.scale)
			if r, text := o.tooltip(); !r.Empty() {
				drawTooltip(dc, r, text, o.font)
			}
		}
	case o.hasHover:
		blit(dc, o.hover, o.screen.dc, o.hover.Min)
		frameRect(dc, o.hover, dip(2, o.scale), colorAccent)
	}

	if o.magnifierVisible() {
		o.drawMagnifier(dc)
	}
}

// drawAnnotationsOn 在选区内绘制全部标注，以及正在拖动、正在输入的那一个。
func (o *shotOverlay) drawAnnotationsOn(dc win.HDC) {
	save := win.SaveDC(dc)
	defer win.RestoreDC(dc, save)
	win.IntersectClipRect(dc, int32(o.sel.Min.X), int32(o.sel.Min.Y), int32(o.sel.Max.X), int32(o.sel.Max.Y))

	list := o.annots
	if o.cur != nil || o.text != nil {
		list = append([]*annotation(nil), o.annots...)
		if o.cur != nil {
			list = append(list, o.cur)
		}
		if o.text != nil {
			list = append(list, o.text)
		}
	}
	r := o.renderer(dc, image.Point{})
	r.draw(list)

	if o.text != nil {
		// 输入中的文字外面画一圈虚线框，并显示闪烁的插入点。
		b := o.textBounds()
		dashFrame(dc, b, dip(1, o.scale), o.text.color)
		if o.caretOn {
			font := o.textFont()
			lineH := measureText(dc, "测", font).Y
			win.DeleteObject(win.HGDIOBJ(font))
			c := o.caretPos()
			fillRect(dc, image.Rect(c.X, c.Y, c.X+dip(2, o.scale), c.Y+lineH), o.text.color)
		}
	}
}

// drawSelectionFrame 绘制选区边框与调整大小用的八个手柄。
func (o *shotOverlay) drawSelectionFrame(dc win.HDC) {
	frameRect(dc, o.sel, dip(1, o.scale), colorAccent)
	if o.tool != toolNone || o.drag == dragNewSel {
		return
	}
	hs := dip(5, o.scale)
	for _, h := range handlePoints(o.sel) {
		r := image.Rect(h.X-hs/2, h.Y-hs/2, h.X-hs/2+hs, h.Y-hs/2+hs)
		fillRect(dc, r.Inset(-dip(1, o.scale)), colorWhite)
		fillRect(dc, r, colorAccent)
	}
}

// sizeLabelRect 返回选区尺寸标签的位置与文字：优先放在选区左上角外侧，放不下就放进选区里。
func (o *shotOverlay) sizeLabelRect() (image.Rectangle, string) {
	text := fmt.Sprintf("%d × %d", o.sel.Dx(), o.sel.Dy())
	size := measureText(o.back.dc, text, o.font)
	padX, padY := dip(6, o.scale), dip(3, o.scale)
	w, h := size.X+padX*2, size.Y+padY*2
	y := o.sel.Min.Y - h - dip(4, o.scale)
	if y < o.monitorRect(o.sel.Min).Min.Y {
		y = o.sel.Min.Y + dip(4, o.scale)
	}
	return image.Rect(o.sel.Min.X, y, o.sel.Min.X+w, y+h), text
}

// drawSizeLabel 绘制选区尺寸标签。
func (o *shotOverlay) drawSizeLabel(dc win.HDC) {
	r, text := o.sizeLabelRect()
	fillRect(dc, r, colorTipBg)
	drawText(dc, text, r, o.font, colorTipText, dtCenter|dtVCenter|dtSingleLine)
}

// tooltip 返回当前悬停的按钮或选项的提示条位置与文字，没有悬停时返回空矩形。
func (o *shotOverlay) tooltip() (image.Rectangle, string) {
	if o.drag != dragNone {
		return image.Rectangle{}, ""
	}
	var anchor image.Rectangle
	var text string
	switch {
	case o.hoverBtn >= 0 && o.hoverBtn < len(barItems):
		anchor, text = o.layout.buttons[o.hoverBtn], barItems[o.hoverBtn].tip
	case o.hoverPal >= 0 && o.hoverPal < len(o.layout.items):
		anchor, text = o.layout.items[o.hoverPal].rect, o.layout.items[o.hoverPal].tip
	}
	if text == "" {
		return image.Rectangle{}, ""
	}
	size := measureText(o.back.dc, text, o.font)
	return o.layout.tipRect(anchor, size, o.monitorRect(o.layout.bar.Min), o.scale), text
}

// toolbarVisible 判断此刻是否显示工具栏：框选、移动、缩放过程中先隐藏。
func (o *shotOverlay) toolbarVisible() bool {
	return o.hasSel && (o.drag == dragNone || o.drag == dragDraw)
}

// magnifierVisible 判断此刻是否显示放大镜：还没选区、正在框选或调整大小时显示，便于对准像素。
func (o *shotOverlay) magnifierVisible() bool {
	return !o.hasSel || o.drag == dragNewSel || o.drag == dragResize
}

// magnifierRect 返回放大镜（含下方信息栏）的位置，默认在光标右下方，靠近屏幕边缘时翻到另一侧。
func (o *shotOverlay) magnifierRect() image.Rectangle {
	cell := dip(magCell, o.scale)
	w := magCols * cell
	h := magRows*cell + o.magInfoHeight()
	off := dip(20, o.scale)
	mon := o.monitorRect(o.cursor)
	x, y := o.cursor.X+off, o.cursor.Y+off
	if x+w > mon.Max.X {
		x = o.cursor.X - off - w
	}
	if y+h > mon.Max.Y {
		y = o.cursor.Y - off - h
	}
	return image.Rect(x, y, x+w, y+h)
}

// magInfoHeight 返回放大镜下方信息栏的高度（四行文字加上下留白）。
func (o *shotOverlay) magInfoHeight() int {
	return dip(18, o.scale)*4 + dip(8, o.scale)
}

// drawMagnifier 绘制放大镜：放大光标周围的像素，下面显示坐标、颜色值和取色提示。
func (o *shotOverlay) drawMagnifier(dc win.HDC) {
	r := o.magnifierRect()
	cell := dip(magCell, o.scale)
	zoom := image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+magRows*cell)
	fillRect(dc, r, colorMagBg)

	src := image.Rect(o.cursor.X-magCols/2, o.cursor.Y-magRows/2, o.cursor.X+magCols/2+1, o.cursor.Y+magRows/2+1)
	win.SetStretchBltMode(dc, win.COLORONCOLOR)
	win.StretchBlt(dc, int32(zoom.Min.X), int32(zoom.Min.Y), int32(zoom.Dx()), int32(zoom.Dy()),
		o.screen.dc, int32(src.Min.X), int32(src.Min.Y), int32(src.Dx()), int32(src.Dy()), win.SRCCOPY)

	// 十字准线：穿过中心格的一行一列半透明高亮，中心格加白框。
	// 注意不能用 image.Rect 先填零再改 Max，它会把坐标规整成左上到右下，中心格就跑到原点去了。
	cmin := image.Pt(zoom.Min.X+magCols/2*cell, zoom.Min.Y+magRows/2*cell)
	center := image.Rectangle{Min: cmin, Max: cmin.Add(image.Pt(cell, cell))}
	if g := newGraphics(dc); g != nil {
		g.fillRectAlpha(colorAccent, 70, image.Rect(zoom.Min.X, center.Min.Y, zoom.Max.X, center.Max.Y))
		g.fillRectAlpha(colorAccent, 70, image.Rect(center.Min.X, zoom.Min.Y, center.Max.X, zoom.Max.Y))
		g.Close()
	}
	frameRect(dc, center, dip(1, o.scale), colorWhite)
	frameRect(dc, zoom, dip(1, o.scale), colorWhite)

	red, green, blue := o.screen.bmp.PixelAt(o.cursor.X, o.cursor.Y)
	screenPt := o.cursor.Add(o.origin)
	lineH := dip(18, o.scale)
	pad := dip(8, o.scale)
	y := zoom.Max.Y + dip(4, o.scale)
	lines := []struct {
		text  string
		color walk.Color
	}{
		{fmt.Sprintf("坐标 (%d, %d)", screenPt.X, screenPt.Y), colorMagText},
		{fmt.Sprintf("#%02X%02X%02X", red, green, blue), colorMagText},
		{fmt.Sprintf("RGB(%d, %d, %d)", red, green, blue), colorMagText},
		{"按 C 复制颜色值", colorMagHint},
	}
	for _, l := range lines {
		drawText(dc, l.text, image.Rect(r.Min.X+pad, y, r.Max.X-pad, y+lineH), o.font, l.color, dtVCenter|dtSingleLine)
		y += lineH
	}
}

// ---------- 布局与刷新 ----------

// monitorRect 返回点 p（遮罩坐标）所在显示器的范围（遮罩坐标）。
func (o *shotOverlay) monitorRect(p image.Point) image.Rectangle {
	return capture.MonitorAt(p.Add(o.origin)).Bounds.Sub(o.origin)
}

// relayout 按当前选区与工具重新计算工具栏位置。
func (o *shotOverlay) relayout() {
	if !o.hasSel {
		o.layout = toolbarLayout{}
		o.hoverBtn, o.hoverPal = -1, -1
		return
	}
	mode := paletteNone
	switch o.tool {
	case toolNone:
	case toolMosaic:
		mode = paletteMosaic
	default:
		mode = paletteDraw
	}
	corner := image.Pt(o.sel.Max.X-1, o.sel.Max.Y-1)
	o.layout = layoutToolbar(o.sel, o.monitorRect(corner), o.scale, mode)
	if o.hoverPal >= len(o.layout.items) {
		o.hoverPal = -1
	}
}

// visualBounds 返回此刻除底图之外所有装饰（选区、工具栏、标签、放大镜等）占据的范围。
func (o *shotOverlay) visualBounds() image.Rectangle {
	var r image.Rectangle
	switch {
	case o.hasSel:
		r = o.sel.Inset(-dip(6, o.scale))
		label, _ := o.sizeLabelRect()
		r = r.Union(label)
		if o.toolbarVisible() {
			r = r.Union(o.layout.bounds())
			tip, _ := o.tooltip()
			r = r.Union(tip)
		}
	case o.hasHover:
		r = o.hover
	}
	if o.magnifierVisible() {
		r = r.Union(o.magnifierRect())
	}
	return r
}

// refresh 把上一次与这一次的装饰范围一起标记为待重绘。只重绘变化的部分，4K 屏上拖动也不卡。
func (o *shotOverlay) refresh() {
	if o.hwnd == 0 {
		return
	}
	cur := o.visualBounds()
	invalidateRect(o.hwnd, cur.Union(o.painted))
	o.painted = cur
}

// updateHover 按光标位置更新悬停选中的窗口或控件。
func (o *shotOverlay) updateHover() {
	r, ok := capture.Pick(o.targets, o.cursor.Add(o.origin))
	o.hover = r.Sub(o.origin).Intersect(o.bounds())
	o.hasHover = ok && !o.hover.Empty()
}

// cursorFor 返回光标在点 p 时应显示的形状。
func (o *shotOverlay) cursorFor(p image.Point) win.HCURSOR {
	switch {
	case o.drag == dragMove:
		return loadCursor(win.IDC_SIZEALL)
	case o.drag == dragResize:
		return loadCursor(resizeCursorID(o.dragEdge))
	case o.drag == dragNewSel || o.drag == dragDraw:
		return loadCursor(win.IDC_CROSS)
	case o.hasSel && (p.In(o.layout.bar) || p.In(o.layout.palette)):
		return loadCursor(idcHand)
	case o.text != nil && p.In(o.textBounds()):
		return loadCursor(win.IDC_IBEAM)
	case o.hasSel && o.tool == toolText && p.In(o.sel):
		return loadCursor(win.IDC_IBEAM)
	case o.hasSel && o.tool != toolNone && p.In(o.sel):
		return loadCursor(win.IDC_CROSS)
	case o.hasSel && o.tool == toolNone:
		if e := o.handleAt(p); e >= 0 {
			return loadCursor(resizeCursorID(e))
		}
		if p.In(o.sel) {
			return loadCursor(win.IDC_SIZEALL)
		}
		if len(o.annots) == 0 {
			return loadCursor(win.IDC_CROSS)
		}
		return loadCursor(idcArrow)
	}
	return loadCursor(win.IDC_CROSS)
}

// handleAt 返回点 p 处的手柄编号（0~7，从左上角顺时针），也接受沿四条边拖动；没命中时返回 -1。
func (o *shotOverlay) handleAt(p image.Point) int {
	tol := dip(6, o.scale)
	for i, h := range handlePoints(o.sel) {
		if absInt(p.X-h.X) <= tol && absInt(p.Y-h.Y) <= tol {
			return i
		}
	}
	s := o.sel
	inX := p.X >= s.Min.X && p.X < s.Max.X
	inY := p.Y >= s.Min.Y && p.Y < s.Max.Y
	switch {
	case inX && absInt(p.Y-s.Min.Y) <= tol/2:
		return 1
	case inY && absInt(p.X-s.Max.X) <= tol/2:
		return 3
	case inX && absInt(p.Y-s.Max.Y) <= tol/2:
		return 5
	case inY && absInt(p.X-s.Min.X) <= tol/2:
		return 7
	}
	return -1
}

// handlePoints 返回选区八个手柄的位置：左上、上、右上、右、右下、下、左下、左。
func handlePoints(r image.Rectangle) [8]image.Point {
	cx, cy := (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2
	return [8]image.Point{
		{r.Min.X, r.Min.Y}, {cx, r.Min.Y}, {r.Max.X, r.Min.Y}, {r.Max.X, cy},
		{r.Max.X, r.Max.Y}, {cx, r.Max.Y}, {r.Min.X, r.Max.Y}, {r.Min.X, cy},
	}
}

// resizeCursorID 返回手柄对应的调整大小光标。
func resizeCursorID(edge int) uintptr {
	switch edge {
	case 0, 4:
		return win.IDC_SIZENWSE
	case 2, 6:
		return idcSizeNESW
	case 1, 5:
		return idcSizeNS
	default:
		return idcSizeWE
	}
}

// resizeRect 按拖动的手柄与位移调整矩形；拖过对边时矩形会翻转而不是变成负尺寸。
func resizeRect(r image.Rectangle, edge int, d image.Point) image.Rectangle {
	x0, y0, x1, y1 := r.Min.X, r.Min.Y, r.Max.X, r.Max.Y
	switch edge {
	case 0:
		x0, y0 = x0+d.X, y0+d.Y
	case 1:
		y0 += d.Y
	case 2:
		x1, y0 = x1+d.X, y0+d.Y
	case 3:
		x1 += d.X
	case 4:
		x1, y1 = x1+d.X, y1+d.Y
	case 5:
		y1 += d.Y
	case 6:
		x0, y1 = x0+d.X, y1+d.Y
	case 7:
		x0 += d.X
	}
	return image.Rect(x0, y0, x1, y1)
}

// spanRect 返回以 a、b 两点为对角、并包含这两个像素的矩形。
func spanRect(a, b image.Point) image.Rectangle {
	return image.Rect(min(a.X, b.X), min(a.Y, b.Y), max(a.X, b.X)+1, max(a.Y, b.Y)+1)
}

// clampInside 平移矩形使其完整落在 bounds 内（尺寸不变）。
func clampInside(r, bounds image.Rectangle) image.Rectangle {
	d := image.Point{}
	if r.Min.X < bounds.Min.X {
		d.X = bounds.Min.X - r.Min.X
	} else if r.Max.X > bounds.Max.X {
		d.X = bounds.Max.X - r.Max.X
	}
	if r.Min.Y < bounds.Min.Y {
		d.Y = bounds.Min.Y - r.Min.Y
	} else if r.Max.Y > bounds.Max.Y {
		d.Y = bounds.Max.Y - r.Max.Y
	}
	return r.Add(d)
}

// clampPoint 把点限制在矩形内。
func clampPoint(p image.Point, r image.Rectangle) image.Point {
	return image.Pt(max(r.Min.X, min(p.X, r.Max.X-1)), max(r.Min.Y, min(p.Y, r.Max.Y-1)))
}

// absInt 返回整数的绝对值。
func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// dashFrame 沿矩形内侧画一圈虚线框。
func dashFrame(hdc win.HDC, r image.Rectangle, width int, c walk.Color) {
	dash := width * 4
	for x := r.Min.X; x < r.Max.X; x += dash * 2 {
		x1 := min(x+dash, r.Max.X)
		fillRect(hdc, image.Rect(x, r.Min.Y, x1, r.Min.Y+width), c)
		fillRect(hdc, image.Rect(x, r.Max.Y-width, x1, r.Max.Y), c)
	}
	for y := r.Min.Y; y < r.Max.Y; y += dash * 2 {
		y1 := min(y+dash, r.Max.Y)
		fillRect(hdc, image.Rect(r.Min.X, y, r.Min.X+width, y1), c)
		fillRect(hdc, image.Rect(r.Max.X-width, y, r.Max.X, y1), c)
	}
}
