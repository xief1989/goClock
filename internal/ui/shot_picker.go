package ui

import (
	"context"
	"fmt"
	"image"
	"math"
	"strings"
	"syscall"
	"unicode"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"

	"goclock/internal/capture"
)

const (
	pickerClass = "GoClock.TextPicker"
	// pickerBarHeight 是底部栏的高度（96 DPI 下的像素），放得下两行提示。
	pickerBarHeight = 52
	// pickerWheelStep 是滚轮每格滚动的距离（96 DPI 下的像素）。
	pickerWheelStep = 100
	// pickerScrollTimerID、pickerScrollMs：拖选到窗口上下边缘外时按这个间隔自动滚动，长图可以一路选下去。
	pickerScrollTimerID = 1
	pickerScrollMs      = 30
	// pickerTextAlpha 是识别出的文字区域的底色浓度（提示哪里能选），pickerSelAlpha 是选中高亮的浓度。
	pickerTextAlpha = 28
	pickerSelAlpha  = 96
)

// 底部按钮。
const (
	pickerBtnCopy = iota
	pickerBtnTranslate
	pickerBtnFull
	pickerBtnClose
	pickerBtnCount
)

// 右键菜单项。
const (
	pickerMenuCopy uint32 = iota + 1
	pickerMenuTranslate
	pickerMenuSelectAll
	pickerMenuFull
)

// pickerActions 是提取文字窗口需要 App 提供的操作。
type pickerActions struct {
	// openText 把文字放进文字窗口，translate 为 true 时立即翻译。
	openText func(text string, translate bool)
	closed   func(p *pickerWindow)
}

// pickerWindow 是「提取文字」窗口：显示截图，识别完成后直接在图上拖选文字，复制或翻译。
//
// 由截图工具栏的「文」、钉图与长图预览的「识别文字」打开（见 App.openTextPicker），识别在后台进行，
// 进度与结果经 setStatus、showResult 送回。窗口结构照长图预览窗：原生窗口、双缓冲自绘、底部按钮栏。
type pickerWindow struct {
	hwnd     win.HWND
	img      *image.RGBA
	canvas   *memCanvas
	scale    float64
	scroll   int
	font     win.HFONT
	hoverBtn int
	actions  pickerActions

	// res 是识别结果，识别完成前为 nil；pick 是据此建立的选字模型。
	res  *ocrOutcome
	pick *textPicker
	// status 是底栏第一行的提示；warn 不为空时第二行用红字显示，过长截断，悬停看全文。
	status   string
	warn     string
	hoverTip bool

	// mouse 是最近一次鼠标位置（客户区坐标）；dragging 表示正在拖选。
	mouse    image.Point
	dragging bool
	// cancel 取消还在进行的识别，关窗口时调用。
	cancel context.CancelFunc
}

// newPickerWindow 在 near（屏幕坐标）所在的显示器上打开提取文字窗口。
func newPickerWindow(img *image.RGBA, near image.Point, actions pickerActions) (*pickerWindow, error) {
	canvas, err := newCanvasFromRGBA(img)
	if err != nil {
		return nil, err
	}
	mon := capture.MonitorAt(near)
	p := &pickerWindow{img: img, canvas: canvas, scale: float64(mon.DPI) / 96, hoverBtn: -1,
		actions: actions, status: "正在识别文字……"}
	p.font = newFont(dip(13, p.scale), false)

	// 窗口尽量按图片原始大小显示，放不下就占显示器可用区域的大部分。
	work := mon.Work
	w := max(min(img.Rect.Dx()+dip(40, p.scale), work.Dx()*8/10), dip(640, p.scale))
	h := max(min(img.Rect.Dy()+dip(pickerBarHeight+80, p.scale), work.Dy()*85/100), dip(360, p.scale))
	x := work.Min.X + (work.Dx()-w)/2
	y := work.Min.Y + (work.Dy()-h)/2

	hwnd, err := createRawWindow(p, rawWindowSpec{
		class:      pickerClass,
		classStyle: win.CS_DBLCLKS,
		title:      "提取文字 - " + appName,
		style:      win.WS_OVERLAPPEDWINDOW | win.WS_VSCROLL,
		bounds:     image.Rect(x, y, x+w, y+h),
	})
	if err != nil {
		canvas.Dispose()
		win.DeleteObject(win.HGDIOBJ(p.font))
		return nil, err
	}
	p.hwnd = hwnd
	disableIME(hwnd)
	p.updateScrollBar()
	win.ShowWindow(hwnd, win.SW_SHOW)
	activateWindow(hwnd)
	return p, nil
}

// Close 关闭窗口。
func (p *pickerWindow) Close() {
	if p.hwnd != 0 {
		win.DestroyWindow(p.hwnd)
	}
}

// setStatus 更新底栏第一行的提示。
func (p *pickerWindow) setStatus(text string) {
	p.status = text
	invalidateRect(p.hwnd, p.barRect())
}

// showResult 显示识别结果：识别出文字就可以开始选了；失败或改用系统 OCR 时在底栏说明。
func (p *pickerWindow) showResult(out ocrOutcome, err error) {
	if out.fallback != "" {
		p.warn = "高精度识别不可用，已改用系统 OCR：" + out.fallback
	}
	switch {
	case err != nil:
		p.status = "识别失败：" + err.Error()
	case strings.TrimSpace(out.res.Text) == "":
		p.status = fmt.Sprintf("没有识别到文字（%s）", out.engine)
	default:
		p.res = &out
		p.pick = newTextPicker(out.res.Lines)
		p.status = fmt.Sprintf("识别完成（%s）：%d 行，用时 %.1f 秒。拖动选择文字，双击选中一段",
			out.engine, len(out.res.Lines), out.cost.Seconds())
	}
	win.InvalidateRect(p.hwnd, nil, false)
}

// ---------- 布局与坐标换算 ----------

// viewArea 返回显示图片的区域（客户区去掉底部栏）。
func (p *pickerWindow) viewArea() image.Rectangle {
	c := clientRect(p.hwnd)
	c.Max.Y -= dip(pickerBarHeight, p.scale)
	return c
}

// barRect 返回底部栏的区域。
func (p *pickerWindow) barRect() image.Rectangle {
	c := clientRect(p.hwnd)
	return image.Rect(c.Min.X, c.Max.Y-dip(pickerBarHeight, p.scale), c.Max.X, c.Max.Y)
}

// zoom 返回图片的显示比例：宽度放得下就按原始大小，放不下就缩到窗口宽度。
func (p *pickerWindow) zoom() float64 {
	area := p.viewArea()
	if area.Dx() <= 0 || p.img.Rect.Dx() <= area.Dx() {
		return 1
	}
	return float64(area.Dx()) / float64(p.img.Rect.Dx())
}

// contentHeight 返回按当前比例显示时整张图的高度。
func (p *pickerWindow) contentHeight() int {
	return int(float64(p.img.Rect.Dy()) * p.zoom())
}

// origin 返回图片左上角在客户区中的位置：水平居中；比显示区矮时垂直居中，否则随滚动上移。
func (p *pickerWindow) origin() image.Point {
	area := p.viewArea()
	dispW := int(float64(p.img.Rect.Dx()) * p.zoom())
	x := area.Min.X + (area.Dx()-dispW)/2
	y := area.Min.Y - p.scroll
	if ch := p.contentHeight(); ch < area.Dy() {
		y = area.Min.Y + (area.Dy()-ch)/2
	}
	return image.Pt(x, y)
}

// toClient 把原图坐标的矩形换算成客户区坐标。
func (p *pickerWindow) toClient(r image.Rectangle) image.Rectangle {
	o, z := p.origin(), p.zoom()
	return image.Rect(
		o.X+int(float64(r.Min.X)*z), o.Y+int(float64(r.Min.Y)*z),
		o.X+int(math.Ceil(float64(r.Max.X)*z)), o.Y+int(math.Ceil(float64(r.Max.Y)*z)))
}

// toImage 把客户区坐标换算成原图坐标。
func (p *pickerWindow) toImage(pt image.Point) image.Point {
	o, z := p.origin(), p.zoom()
	return image.Pt(int(float64(pt.X-o.X)/z), int(float64(pt.Y-o.Y)/z))
}

// updateScrollBar 按内容高度与可视高度更新滚动条，并把滚动位置限制在有效范围内。
func (p *pickerWindow) updateScrollBar() {
	page := max(p.viewArea().Dy(), 1)
	total := p.contentHeight()
	p.scroll = max(0, min(p.scroll, total-page))
	si := win.SCROLLINFO{
		FMask: win.SIF_RANGE | win.SIF_PAGE | win.SIF_POS,
		NMin:  0,
		NMax:  int32(max(total-1, 0)),
		NPage: uint32(page),
		NPos:  int32(p.scroll),
	}
	si.CbSize = uint32(unsafe.Sizeof(si))
	win.SetScrollInfo(p.hwnd, win.SB_VERT, &si, true)
}

// scrollTo 滚动到指定位置；正在拖选时选区跟着鼠标所在处延伸。
func (p *pickerWindow) scrollTo(pos int) {
	p.scroll = pos
	p.updateScrollBar()
	if p.dragging {
		p.extendTo(p.mouse)
	}
	win.InvalidateRect(p.hwnd, nil, false)
}

// buttons 返回底部按钮的位置与文字。
func (p *pickerWindow) buttons() ([pickerBtnCount]image.Rectangle, [pickerBtnCount]string) {
	bar := p.barRect()
	bw, bh := dip(92, p.scale), dip(30, p.scale)
	gap := dip(8, p.scale)
	y := bar.Min.Y + (bar.Dy()-bh)/2
	x := bar.Max.X - dip(12, p.scale) - bw*pickerBtnCount - gap*(pickerBtnCount-1)
	var rs [pickerBtnCount]image.Rectangle
	for i := range rs {
		rs[i] = image.Rect(x, y, x+bw, y+bh)
		x += bw + gap
	}
	return rs, [pickerBtnCount]string{"复制 Ctrl+C", "翻译", "全文", "关闭 Esc"}
}

// buttonAt 返回点 pt 下的按钮，没有命中时返回 -1。
func (p *pickerWindow) buttonAt(pt image.Point) int {
	rs, _ := p.buttons()
	for i, r := range rs {
		if pt.In(r) {
			return i
		}
	}
	return -1
}

// infoRects 返回底栏左侧两行提示的位置：第一行是状态，第二行是改用系统 OCR 的原因。
func (p *pickerWindow) infoRects() (image.Rectangle, image.Rectangle) {
	bar := p.barRect()
	rs, _ := p.buttons()
	info := image.Rect(bar.Min.X+dip(12, p.scale), bar.Min.Y, rs[0].Min.X-dip(12, p.scale), bar.Max.Y)
	if p.warn == "" {
		return info, image.Rectangle{}
	}
	mid := info.Min.Y + info.Dy()/2
	return image.Rect(info.Min.X, info.Min.Y+dip(4, p.scale), info.Max.X, mid),
		image.Rect(info.Min.X, mid, info.Max.X, info.Max.Y-dip(4, p.scale))
}

// ---------- 操作 ----------

// currentText 返回选中的文字，没有选区时返回全部文字。
func (p *pickerWindow) currentText() string {
	switch {
	case p.res == nil:
		return ""
	case p.pick.hasSelection():
		return p.pick.selectedText()
	}
	return p.res.res.Text
}

// copyText 把选中的文字（没选就是全部）复制到剪贴板。
func (p *pickerWindow) copyText() {
	text := p.currentText()
	if text == "" {
		return
	}
	if err := walk.Clipboard().SetText(strings.ReplaceAll(text, "\n", "\r\n")); err != nil {
		p.setStatus("复制失败：" + err.Error())
		return
	}
	n := 0
	for _, r := range text {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	what := "全部"
	if p.pick.hasSelection() {
		what = "选中的"
	}
	p.setStatus(fmt.Sprintf("已复制%s文字（%d 个字）", what, n))
}

// click 执行按钮或菜单动作；识别完成前只有「关闭」可用。
func (p *pickerWindow) click(btn int) {
	if btn == pickerBtnClose {
		p.Close()
		return
	}
	if p.res == nil {
		return
	}
	switch btn {
	case pickerBtnCopy:
		p.copyText()
	case pickerBtnTranslate:
		p.actions.openText(p.currentText(), true)
	case pickerBtnFull:
		p.actions.openText(p.res.res.Text, false)
	}
}

// selectAll 全选。
func (p *pickerWindow) selectAll() {
	if p.pick != nil {
		p.pick.selectAll()
		p.invalidateView()
	}
}

// invalidateView 重绘图片区域。
func (p *pickerWindow) invalidateView() {
	invalidateRect(p.hwnd, p.viewArea())
}

// ---------- 消息处理 ----------

// handleMessage 实现 rawHandler。
func (p *pickerWindow) handleMessage(hwnd win.HWND, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	switch msg {
	case win.WM_PAINT:
		p.paint()
		return 0, true
	case win.WM_ERASEBKGND:
		return 1, true
	case win.WM_SIZE:
		if p.hwnd != 0 {
			p.updateScrollBar()
			win.InvalidateRect(hwnd, nil, false)
		}
		return 0, true
	case win.WM_DPICHANGED:
		// 拖到缩放比例不同的显示器上：图片按物理像素显示不变，只换底栏的字体与间距。
		p.scale = float64(wParam&0xFFFF) / 96
		win.DeleteObject(win.HGDIOBJ(p.font))
		p.font = newFont(dip(13, p.scale), false)
		p.updateScrollBar()
		win.InvalidateRect(hwnd, nil, false)
		return 0, true
	case win.WM_SETCURSOR:
		if lParam&0xFFFF == win.HTCLIENT {
			win.SetCursor(p.cursorFor(p.mouse))
			return 1, true
		}
	case win.WM_MOUSEWHEEL:
		p.scrollTo(p.scroll - wheelDelta(wParam)*dip(pickerWheelStep, p.scale)/wheelDeltaUnit)
		return 0, true
	case win.WM_VSCROLL:
		p.onVScroll(uint32(wParam & 0xFFFF))
		return 0, true
	case win.WM_MOUSEMOVE:
		p.onMouseMove(pointFromLParam(lParam))
		return 0, true
	case win.WM_MOUSELEAVE:
		if p.hoverBtn != -1 || p.hoverTip {
			p.hoverBtn, p.hoverTip = -1, false
			win.InvalidateRect(hwnd, nil, false)
		}
		return 0, true
	case win.WM_LBUTTONDOWN:
		p.onLButtonDown(pointFromLParam(lParam))
		return 0, true
	case win.WM_LBUTTONUP:
		p.onLButtonUp(pointFromLParam(lParam))
		return 0, true
	case win.WM_LBUTTONDBLCLK:
		if pt := pointFromLParam(lParam); p.pick != nil && !p.pick.empty() && pt.In(p.viewArea()) {
			p.pick.selectSpan(p.pick.hit(p.toImage(pt)))
			p.invalidateView()
		}
		return 0, true
	case win.WM_CAPTURECHANGED:
		// 鼠标捕获被别的窗口抢走时结束拖选，避免卡在拖动状态。
		if win.HWND(lParam) != hwnd && p.dragging {
			p.stopDrag(false)
		}
		return 0, true
	case win.WM_RBUTTONUP:
		p.onRightClick()
		return 0, true
	case win.WM_TIMER:
		if wParam == pickerScrollTimerID {
			p.autoScroll()
		}
		return 0, true
	case win.WM_KEYDOWN:
		p.onKeyDown(uint32(wParam))
		return 0, true
	case win.WM_DESTROY:
		if p.cancel != nil {
			p.cancel()
		}
		win.KillTimer(hwnd, pickerScrollTimerID)
		p.canvas.Dispose()
		win.DeleteObject(win.HGDIOBJ(p.font))
		p.hwnd = 0
		if p.actions.closed != nil {
			p.actions.closed(p)
		}
		return 0, true
	}
	return 0, false
}

// cursorFor 返回点 pt 处的光标：文字上是文本光标，其余是箭头。
func (p *pickerWindow) cursorFor(pt image.Point) win.HCURSOR {
	if p.dragging || (p.pick != nil && pt.In(p.viewArea()) && p.pick.onText(p.toImage(pt))) {
		return loadCursor(win.IDC_IBEAM)
	}
	return loadCursor(idcArrow)
}

// onMouseMove 拖选时延伸选区，否则更新按钮与提示的悬停状态。
func (p *pickerWindow) onMouseMove(pt image.Point) {
	p.mouse = pt
	if p.dragging {
		// 鼠标被捕获期间系统不发 WM_SETCURSOR，光标要自己设。
		win.SetCursor(p.cursorFor(pt))
		p.extendTo(pt)
		return
	}
	_, warnRect := p.infoRects()
	btn, tip := p.buttonAt(pt), pt.In(warnRect)
	if btn != p.hoverBtn || tip != p.hoverTip {
		p.hoverBtn, p.hoverTip = btn, tip
		// 鼠标离开窗口时要收到 WM_MOUSELEAVE，把悬停效果与提示条撤掉。
		tme := win.TRACKMOUSEEVENT{DwFlags: win.TME_LEAVE, HwndTrack: p.hwnd}
		tme.CbSize = uint32(unsafe.Sizeof(tme))
		win.TrackMouseEvent(&tme)
		win.InvalidateRect(p.hwnd, nil, false)
	}
}

// onLButtonDown 在图片上按下左键开始拖选，按住 Shift 时从原来的起点延伸。
func (p *pickerWindow) onLButtonDown(pt image.Point) {
	p.mouse = pt
	if p.pick == nil || p.pick.empty() || !pt.In(p.viewArea()) {
		return
	}
	h := p.pick.hit(p.toImage(pt))
	if keyDown(win.VK_SHIFT) {
		p.pick.extend(h)
	} else {
		p.pick.begin(h)
	}
	p.dragging = true
	win.SetCapture(p.hwnd)
	win.SetTimer(p.hwnd, pickerScrollTimerID, pickerScrollMs, 0)
	p.invalidateView()
}

// onLButtonUp 结束拖选；不是拖选时按底部按钮处理。
func (p *pickerWindow) onLButtonUp(pt image.Point) {
	if p.dragging {
		p.stopDrag(true)
		return
	}
	p.click(p.buttonAt(pt))
}

// stopDrag 结束拖选。release 为 false 表示捕获已被别的窗口抢走，不必再释放。
func (p *pickerWindow) stopDrag(release bool) {
	// 先清掉拖动状态再释放捕获：ReleaseCapture 会同步发来 WM_CAPTURECHANGED，避免重入。
	p.dragging = false
	win.KillTimer(p.hwnd, pickerScrollTimerID)
	if release {
		win.ReleaseCapture()
	}
}

// extendTo 把选区延伸到客户区坐标 pt；鼠标在图片区外时按最近的边缘算。
func (p *pickerWindow) extendTo(pt image.Point) {
	area := p.viewArea()
	if p.pick == nil || p.pick.empty() || area.Empty() {
		return
	}
	q := image.Pt(min(max(pt.X, area.Min.X), area.Max.X-1), min(max(pt.Y, area.Min.Y), area.Max.Y-1))
	p.pick.extend(p.pick.hit(p.toImage(q)))
	p.invalidateView()
}

// autoScroll 在拖选到图片区上下边缘之外时滚动，离边缘越远滚得越快。
func (p *pickerWindow) autoScroll() {
	if !p.dragging {
		return
	}
	area := p.viewArea()
	d := 0
	switch {
	case p.mouse.Y < area.Min.Y:
		d = p.mouse.Y - area.Min.Y
	case p.mouse.Y >= area.Max.Y:
		d = p.mouse.Y - area.Max.Y + 1
	}
	if d == 0 {
		return
	}
	limit := dip(60, p.scale)
	step := min(max(d/2, -limit), limit)
	if step == 0 {
		step = d
	}
	p.scrollTo(p.scroll + step)
}

// onRightClick 弹出右键菜单；识别完成前没有可做的事，不弹。
func (p *pickerWindow) onRightClick() {
	if p.res == nil {
		return
	}
	switch showPopupMenu(p.hwnd, capture.CursorPos(), []popupMenuItem{
		{pickerMenuCopy, "复制\tCtrl+C"},
		{pickerMenuTranslate, "翻译"},
		{pickerMenuSelectAll, "全选\tCtrl+A"},
		{0, ""},
		{pickerMenuFull, "查看全文"},
	}) {
	case pickerMenuCopy:
		p.click(pickerBtnCopy)
	case pickerMenuTranslate:
		p.click(pickerBtnTranslate)
	case pickerMenuSelectAll:
		p.selectAll()
	case pickerMenuFull:
		p.click(pickerBtnFull)
	}
}

// onVScroll 响应滚动条操作。
func (p *pickerWindow) onVScroll(code uint32) {
	page := p.viewArea().Dy()
	line := dip(pickerWheelStep, p.scale) / 2
	switch code {
	case win.SB_LINEUP:
		p.scrollTo(p.scroll - line)
	case win.SB_LINEDOWN:
		p.scrollTo(p.scroll + line)
	case win.SB_PAGEUP:
		p.scrollTo(p.scroll - page)
	case win.SB_PAGEDOWN:
		p.scrollTo(p.scroll + page)
	case win.SB_TOP:
		p.scrollTo(0)
	case win.SB_BOTTOM:
		p.scrollTo(p.contentHeight())
	case win.SB_THUMBTRACK, win.SB_THUMBPOSITION:
		si := win.SCROLLINFO{FMask: win.SIF_TRACKPOS}
		si.CbSize = uint32(unsafe.Sizeof(si))
		if win.GetScrollInfo(p.hwnd, win.SB_VERT, &si) {
			p.scrollTo(int(si.NTrackPos))
		}
	}
}

// onKeyDown 处理快捷键与键盘滚动。
func (p *pickerWindow) onKeyDown(vk uint32) {
	ctrl := keyDown(win.VK_CONTROL)
	page := p.viewArea().Dy()
	switch {
	case vk == win.VK_ESCAPE:
		p.Close()
	case ctrl && vk == vkC:
		p.click(pickerBtnCopy)
	case ctrl && vk == vkA:
		p.selectAll()
	case vk == win.VK_UP:
		p.scrollTo(p.scroll - dip(pickerWheelStep, p.scale)/2)
	case vk == win.VK_DOWN:
		p.scrollTo(p.scroll + dip(pickerWheelStep, p.scale)/2)
	case vk == win.VK_PRIOR:
		p.scrollTo(p.scroll - page)
	case vk == win.VK_NEXT, vk == win.VK_SPACE:
		p.scrollTo(p.scroll + page)
	case vk == win.VK_HOME:
		p.scrollTo(0)
	case vk == win.VK_END:
		p.scrollTo(p.contentHeight())
	}
}

// ---------- 绘制 ----------

// paint 绘制图片、文字底色与选中高亮、底部栏，悬停在红字上时再画一条完整原因的提示。
func (p *pickerWindow) paint() {
	var ps win.PAINTSTRUCT
	hdc := win.BeginPaint(p.hwnd, &ps)
	defer win.EndPaint(p.hwnd, &ps)

	client := clientRect(p.hwnd)
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

	area := p.viewArea()
	fillRect(dc, area, colorThumbBg)
	o, z := p.origin(), p.zoom()
	shown := image.Rect(o.X, o.Y, o.X+int(float64(p.img.Rect.Dx())*z), o.Y+p.contentHeight())
	if vis := shown.Intersect(area); !vis.Empty() {
		if z == 1 {
			blit(dc, vis, p.canvas.dc, vis.Min.Sub(o))
		} else {
			srcY0 := int(float64(vis.Min.Y-o.Y) / z)
			srcY1 := int(float64(vis.Max.Y-o.Y) / z)
			stretchHalftone(dc, vis, p.canvas.dc, image.Rect(0, srcY0, p.img.Rect.Dx(), max(srcY1, srcY0+1)))
		}
	}
	p.paintHighlights(dc, area)
	p.paintBar(dc)
	blit(hdc, client, dc, image.Point{})
}

// paintHighlights 给识别出的每段文字铺一层淡底色，再画选中部分的高亮；只画落在可见区域里的。
func (p *pickerWindow) paintHighlights(dc win.HDC, area image.Rectangle) {
	if p.pick == nil || p.pick.empty() {
		return
	}
	g := newGraphics(dc)
	if g == nil {
		return
	}
	defer g.Close()
	for _, s := range p.pick.spans {
		if r := p.toClient(s.rect); r.Overlaps(area) {
			g.fillRectAlpha(colorAccent, pickerTextAlpha, r)
		}
	}
	for _, h := range p.pick.highlights() {
		if r := p.toClient(h); r.Overlaps(area) {
			g.fillRectAlpha(colorAccent, pickerSelAlpha, r)
		}
	}
}

// paintBar 绘制底部栏：左侧状态与改用原因，右侧按钮。
func (p *pickerWindow) paintBar(dc win.HDC) {
	bar := p.barRect()
	fillRect(dc, bar, colorPanel)
	fillRect(dc, image.Rect(bar.Min.X, bar.Min.Y, bar.Max.X, bar.Min.Y+1), colorPanelBorder)

	statusRect, warnRect := p.infoRects()
	fmtLine := uint32(dtVCenter | dtSingleLine | dtEndEllipsis)
	drawText(dc, p.status, statusRect, p.font, colorBody, fmtLine)
	if p.warn != "" {
		drawText(dc, p.warn, warnRect, p.font, colorWarn, fmtLine)
	}

	rs, labels := p.buttons()
	for i, r := range rs {
		bg, fg := colorBtnBg, colorTitle
		switch {
		case i != pickerBtnClose && p.res == nil:
			fg = colorMuted
		case i == p.hoverBtn:
			bg = colorBtnHover
		}
		if i == pickerBtnCopy && p.res != nil {
			bg, fg = colorAccent, colorWhite
		}
		fillRect(dc, r, bg)
		drawText(dc, labels[i], r, p.font, fg, dtCenter|dtVCenter|dtSingleLine)
	}

	if p.hoverTip && p.warn != "" {
		p.paintWarnTip(dc, warnRect)
	}
}

// paintWarnTip 在红字上方画一条深色提示，显示改用系统 OCR 的完整原因，太长就折行。
func (p *pickerWindow) paintWarnTip(dc win.HDC, anchor image.Rectangle) {
	client := clientRect(p.hwnd)
	pad := dip(8, p.scale)
	maxW := client.Dx() - 2*dip(12, p.scale)
	textW := min(measureText(dc, p.warn, p.font).X, maxW-2*pad)
	textH := wrappedHeight(dc, p.warn, p.font, textW)
	r := image.Rect(anchor.Min.X, anchor.Min.Y-dip(6, p.scale)-textH-2*pad, anchor.Min.X+textW+2*pad, anchor.Min.Y-dip(6, p.scale))
	if r.Max.X > client.Max.X-dip(12, p.scale) {
		r = r.Sub(image.Pt(r.Max.X-client.Max.X+dip(12, p.scale), 0))
	}
	fillRect(dc, r, colorTipBg)
	drawText(dc, p.warn, r.Inset(pad), p.font, colorTipText, dtWordBreak)
}

// wrappedHeight 测量文字在给定宽度内自动折行后的高度。
func wrappedHeight(hdc win.HDC, text string, font win.HFONT, width int) int {
	old := win.SelectObject(hdc, win.HGDIOBJ(font))
	defer win.SelectObject(hdc, old)
	u, _ := syscall.UTF16FromString(text)
	rc := win.RECT{Right: int32(width)}
	procDrawTextW.Call(uintptr(hdc), uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1),
		uintptr(unsafe.Pointer(&rc)), dtCalcRect|dtWordBreak|dtNoPrefix)
	return int(rc.Bottom - rc.Top)
}

// openTextPicker 打开「提取文字」窗口并在后台识别，near 是用来挑显示器的屏幕坐标。
func (a *App) openTextPicker(img *image.RGBA, near image.Point) {
	p, err := newPickerWindow(img, near, pickerActions{
		openText: func(text string, translate bool) {
			a.openTextWindow(textJob{text: text, translate: translate})
		},
		closed: func(p *pickerWindow) { delete(a.pickers, p) },
	})
	if err != nil {
		errorBox(a.mw, "无法打开提取文字窗口："+err.Error())
		return
	}
	a.pickers[p] = struct{}{}

	engine, proxy := a.settings.OCREngine, a.settings.Proxy
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	report := func(text string) {
		safeSynchronize(a.mw, func() {
			if ctx.Err() == nil {
				p.setStatus(text)
			}
		})
	}
	go func() {
		out, err := a.ocr.recognize(ctx, img, engine, proxy, report)
		// 与识别窗口相同：ctx 只由关窗口取消，在界面线程上判断窗口是否已关，再释放 ctx。
		safeSynchronize(a.mw, func() {
			closed := ctx.Err() != nil
			cancel()
			if !closed {
				p.showResult(out, err)
			}
		})
	}()
}
