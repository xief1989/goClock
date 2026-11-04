package ui

import (
	"fmt"
	"image"
	"math"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"

	"goclock/internal/capture"
)

// 长截图会话。
//
// 选好区域后，遮罩消失，区域外留一圈边框，旁边是预览面板。程序每 80ms 抓一次这块区域，
// 画面停稳（连续两次一样）才交给拼接器，避开平滑滚动的中间帧。
// 滚动有两种方式：用户自己在框里滚滚轮（与微信一致），或者点「自动滚动」由程序模拟滚轮。

const (
	longFrameClass = "GoClock.LongFrame"
	longPanelClass = "GoClock.LongPanel"
	longTimerID    = 1
	longPollMs     = 80
	// longPanelWidth 是预览面板的宽度（96 DPI 下的像素）。
	longPanelWidth = 230
	// autoTargetRatio：自动滚动时每次争取滚过区域高度的这么多比例，留足重叠，拼接才可靠。
	autoTargetRatio = 0.45
	// autoIdleTicks：滚动之后这么多次轮询画面都没变，就认为这一下没滚动。
	autoIdleTicks = 12
	// autoMaxNotches 是一次最多模拟的滚轮格数。
	autoMaxNotches = 10
	// wheelDeltaUnit 对应 WHEEL_DELTA，一格滚轮的滚动量。
	wheelDeltaUnit = 120
)

// 面板上的按钮。
const (
	longBtnAuto = iota
	longBtnCancel
	longBtnDone
)

// 长截图的提示文字。
const (
	longHintIdle   = "在框内滚动鼠标滚轮，画面会自动拼接；也可以点「自动滚动」。"
	longHintAuto   = "正在自动滚动……按 Esc 或移动鼠标即可停止。"
	longHintBottom = "已经滚到底了，点「完成」生成长图。"
)

var (
	colorPanelBorder = walk.RGB(0xCB, 0xD5, 0xE1)
	colorThumbBg     = walk.RGB(0xF3, 0xF4, 0xF6)
	colorBtnBg       = walk.RGB(0xF3, 0xF4, 0xF6)
	colorBtnHover    = walk.RGB(0xE5, 0xE7, 0xEB)
	colorWarn        = walk.RGB(0xDC, 0x26, 0x26)
)

// longShot 是一次长截图会话。所有方法都在界面线程上执行。
type longShot struct {
	// rect 是截图区域的屏幕坐标（物理像素）。
	rect   image.Rectangle
	scale  float64
	frame  win.HWND
	panel  win.HWND
	grab   *capture.Grabber
	st     *capture.Stitcher
	onDone func(img *image.RGBA)

	// processed 表示当前这一帧稳定的画面已经交给拼接器了，画面再变之前不重复处理。
	processed bool
	thumb     *memCanvas
	hint      string
	hintWarn  bool

	auto        bool
	autoCursor  image.Point
	notches     int
	lastNotches int
	zeroRuns    int
	idleTicks   int
	failRuns    int
	escDown     bool

	hoverBtn  int
	titleFont win.HFONT
	font      win.HFONT
	done      bool
}

// newLongShot 在区域 rect（屏幕坐标）上开始长截图。结束后 onDone 收到长图，取消时收到 nil。
func newLongShot(rect image.Rectangle, onDone func(img *image.RGBA)) (*longShot, error) {
	grab, err := capture.NewGrabber(rect)
	if err != nil {
		return nil, err
	}
	mon := capture.MonitorAt(rect.Min.Add(image.Pt(rect.Dx()/2, rect.Dy()/2)))
	l := &longShot{
		rect:     rect,
		scale:    float64(mon.DPI) / 96,
		grab:     grab,
		st:       capture.NewStitcher(0),
		onDone:   onDone,
		hint:     longHintIdle,
		notches:  2,
		hoverBtn: -1,
	}
	l.titleFont = newFont(dip(15, l.scale), true)
	l.font = newFont(dip(12, l.scale), false)

	if err := l.createFrame(); err != nil {
		l.release()
		return nil, err
	}
	panel, err := createRawWindow(l, rawWindowSpec{
		class:   longPanelClass,
		title:   "GoClock 长截图",
		exStyle: win.WS_EX_TOPMOST | win.WS_EX_TOOLWINDOW | win.WS_EX_NOACTIVATE,
		style:   win.WS_POPUP,
		bounds:  l.panelBounds(mon),
	})
	if err != nil {
		win.DestroyWindow(l.frame)
		l.release()
		return nil, err
	}
	l.panel = panel
	excludeFromCapture(panel)
	// 不抢焦点：用户要滚动的是原来那个窗口，键盘滚动也得留给它。
	win.ShowWindow(panel, win.SW_SHOWNOACTIVATE)
	win.SetTimer(panel, longTimerID, longPollMs, 0)
	return l, nil
}

// createFrame 创建区域外的那圈边框。
func (l *longShot) createFrame() error {
	hwnd, err := createRegionFrame(l.rect, l.scale, colorAccent)
	if err != nil {
		return err
	}
	l.frame = hwnd
	return nil
}

// createRegionFrame 在屏幕区域 rect 外围画一圈 color 色的边框窗口：只有边框本身可见，
// 鼠标可以穿透，也不会出现在截图与录屏里。长截图与录屏共用。
func createRegionFrame(rect image.Rectangle, scale float64, color walk.Color) (win.HWND, error) {
	bw := dip(2, scale)
	outer := rect.Inset(-bw)
	hwnd, err := createRawWindow(regionFrame{color: color}, rawWindowSpec{
		class:   longFrameClass,
		title:   "GoClock 区域边框",
		exStyle: win.WS_EX_TOPMOST | win.WS_EX_TOOLWINDOW | win.WS_EX_NOACTIVATE | win.WS_EX_LAYERED | win.WS_EX_TRANSPARENT,
		style:   win.WS_POPUP,
		bounds:  outer,
	})
	if err != nil {
		return 0, err
	}
	procSetLayeredWindowAttributes.Call(uintptr(hwnd), 0, 255, lwaAlpha)
	// 窗口形状 = 外框减去内框，中间是空的。区域交给系统后由系统负责释放。
	rgn := win.CreateRectRgn(0, 0, int32(outer.Dx()), int32(outer.Dy()))
	inner := win.CreateRectRgn(int32(bw), int32(bw), int32(outer.Dx()-bw), int32(outer.Dy()-bw))
	win.CombineRgn(rgn, rgn, inner, rgnDiff)
	win.DeleteObject(win.HGDIOBJ(inner))
	procSetWindowRgn.Call(uintptr(hwnd), uintptr(rgn), 1)
	excludeFromCapture(hwnd)
	win.ShowWindow(hwnd, win.SW_SHOWNOACTIVATE)
	return hwnd, nil
}

// panelBounds 计算预览面板的位置：优先放在区域右侧，放不下放左侧，再不行就压在区域右边缘内。
func (l *longShot) panelBounds(mon capture.Monitor) image.Rectangle {
	w := dip(longPanelWidth, l.scale)
	gap := dip(10, l.scale)
	work := mon.Work
	h := max(dip(340, l.scale), min(l.rect.Dy(), work.Dy()))
	h = min(h, work.Dy())
	var x int
	switch {
	case l.rect.Max.X+gap+w <= work.Max.X:
		x = l.rect.Max.X + gap
	case l.rect.Min.X-gap-w >= work.Min.X:
		x = l.rect.Min.X - gap - w
	default:
		x = work.Max.X - w
	}
	y := max(work.Min.Y, min(l.rect.Min.Y, work.Max.Y-h))
	return image.Rect(x, y, x+w, y+h)
}

// release 释放会话持有的资源（不含窗口本身）。
func (l *longShot) release() {
	l.grab.Close()
	l.thumb.Dispose()
	l.thumb = nil
	for _, f := range []*win.HFONT{&l.titleFont, &l.font} {
		if *f != 0 {
			win.DeleteObject(win.HGDIOBJ(*f))
			*f = 0
		}
	}
}

// Cancel 放弃长截图，退出程序时调用。
func (l *longShot) Cancel() {
	l.finish(false)
}

// finish 结束会话，ok 为 true 时把拼好的长图交给 onDone。只执行一次。
func (l *longShot) finish(ok bool) {
	if l.done {
		return
	}
	l.done = true
	var img *image.RGBA
	if ok {
		img = l.st.Image()
	}
	win.KillTimer(l.panel, longTimerID)
	win.DestroyWindow(l.frame)
	win.DestroyWindow(l.panel)
	l.onDone(img)
}

// handleMessage 实现 rawHandler（预览面板的消息）。
func (l *longShot) handleMessage(hwnd win.HWND, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	switch msg {
	case win.WM_PAINT:
		l.paint(hwnd)
		return 0, true
	case win.WM_ERASEBKGND:
		return 1, true
	case win.WM_MOUSEACTIVATE:
		return maNoActivate, true
	case win.WM_TIMER:
		if wParam == longTimerID {
			l.tick()
		}
		return 0, true
	case win.WM_MOUSEMOVE:
		if b := l.buttonAt(pointFromLParam(lParam)); b != l.hoverBtn {
			l.hoverBtn = b
			win.InvalidateRect(hwnd, nil, false)
		}
		return 0, true
	case win.WM_SETCURSOR:
		if l.hoverBtn >= 0 {
			win.SetCursor(loadCursor(idcHand))
			return 1, true
		}
	case win.WM_LBUTTONUP:
		l.click(l.buttonAt(pointFromLParam(lParam)))
		return 0, true
	case win.WM_DESTROY:
		l.release()
		return 0, true
	}
	return 0, false
}

// tick 每次轮询执行一次：处理 Esc、抓帧、在画面停稳时拼接，以及推进自动滚动。
func (l *longShot) tick() {
	if l.done {
		return
	}
	esc := asyncKeyDown(win.VK_ESCAPE)
	if esc && !l.escDown {
		l.escDown = true
		if !l.auto {
			l.finish(false)
			return
		}
		l.stopAuto("已停止自动滚动。可以继续手动滚动，或点「完成」。", false)
	}
	l.escDown = esc
	if l.auto && l.mouseMoved() {
		l.stopAuto("检测到鼠标移动，已停止自动滚动。", false)
	}

	changed, err := l.grab.Poll()
	if err != nil {
		return
	}
	if changed {
		l.processed = false
		l.idleTicks = 0
		return
	}
	if l.processed {
		l.autoIdle()
		return
	}
	l.processed = true
	status, added := l.st.Add(l.grab.Frame())
	l.onAdded(status, added)
}

// onAdded 根据拼接结果更新提示、缩略图，并决定自动滚动的下一步。
func (l *longShot) onAdded(status capture.AddStatus, added int) {
	switch status {
	case capture.Appended:
		l.failRuns = 0
		l.zeroRuns = 0
		l.updateThumb()
		if l.auto {
			l.adaptNotches(added)
			l.setHint(longHintAuto, false)
			l.wheel(l.notches)
		} else if l.hintWarn {
			l.setHint(longHintIdle, false)
		}
	case capture.Unchanged:
		if l.auto {
			l.zeroRuns++
			if l.zeroRuns >= 4 {
				l.stopAuto(longHintBottom, false)
			} else {
				l.wheel(l.notches)
			}
		}
	case capture.NoMatch:
		l.failRuns++
		l.setHint("滚得太快，没接上。请往回滚一点，再慢一些滚动。", true)
		if l.auto {
			if l.failRuns >= 3 {
				l.stopAuto("这块区域的内容无法自动对齐，请改为手动滚动。", true)
				return
			}
			// 退回刚才那一下，再用更小的步子重新滚。
			back := l.lastNotches
			l.notches = max(1, l.notches/2)
			l.wheel(-back)
		}
	case capture.Full:
		l.updateThumb()
		l.stopAuto(fmt.Sprintf("已达到最大高度 %d px，点「完成」生成长图。", capture.DefaultMaxHeight), false)
	}
	win.InvalidateRect(l.panel, nil, false)
}

// autoIdle 在画面停住且已处理过时被调用：自动滚动发出滚轮后迟迟没有动静，说明已经到底了。
func (l *longShot) autoIdle() {
	if !l.auto {
		return
	}
	l.idleTicks++
	if l.idleTicks < autoIdleTicks {
		return
	}
	l.idleTicks = 0
	l.zeroRuns++
	if l.zeroRuns >= 2 {
		l.stopAuto(longHintBottom, false)
		return
	}
	l.wheel(l.notches)
}

// adaptNotches 根据上一下实际滚过的行数调整每次的滚轮格数，让每次都滚过区域高度的 45% 左右。
func (l *longShot) adaptNotches(added int) {
	if added <= 0 || l.lastNotches <= 0 {
		return
	}
	perNotch := float64(added) / float64(l.lastNotches)
	target := autoTargetRatio * float64(l.rect.Dy())
	n := int(math.Round(target / perNotch))
	l.notches = max(1, min(n, autoMaxNotches))
}

// startAuto 开始自动滚动：把鼠标移到区域中央，然后模拟滚轮。
func (l *longShot) startAuto() {
	l.auto = true
	l.zeroRuns, l.failRuns, l.idleTicks = 0, 0, 0
	l.autoCursor = l.rect.Min.Add(image.Pt(l.rect.Dx()/2, l.rect.Dy()/2))
	capture.SetCursorPos(l.autoCursor)
	l.setHint(longHintAuto, false)
	l.wheel(l.notches)
}

// stopAuto 停止自动滚动并显示提示。
func (l *longShot) stopAuto(hint string, warn bool) {
	l.auto = false
	l.setHint(hint, warn)
}

// mouseMoved 判断自动滚动期间用户是否动了鼠标。
func (l *longShot) mouseMoved() bool {
	d := capture.CursorPos().Sub(l.autoCursor)
	tol := dip(6, l.scale)
	return absInt(d.X) > tol || absInt(d.Y) > tol
}

// wheel 模拟滚动 n 格滚轮，正数向下、负数向上。滚轮消息发给光标下面的窗口。
func (l *longShot) wheel(n int) {
	if n == 0 {
		return
	}
	l.lastNotches = n
	l.idleTicks = 0
	input := win.MOUSE_INPUT{
		Type: win.INPUT_MOUSE,
		Mi: win.MOUSEINPUT{
			MouseData: uint32(int32(-wheelDeltaUnit * n)),
			DwFlags:   win.MOUSEEVENTF_WHEEL,
		},
	}
	win.SendInput(1, unsafe.Pointer(&input), int32(unsafe.Sizeof(input)))
}

// setHint 更新面板上的提示文字，warn 为 true 时用醒目的红色。
func (l *longShot) setHint(text string, warn bool) {
	if l.hint == text && l.hintWarn == warn {
		return
	}
	l.hint, l.hintWarn = text, warn
	win.InvalidateRect(l.panel, nil, false)
}

// click 处理面板按钮的点击。
func (l *longShot) click(btn int) {
	switch btn {
	case longBtnAuto:
		if l.auto {
			l.stopAuto("已停止自动滚动。可以继续手动滚动，或点「完成」。", false)
		} else {
			l.startAuto()
		}
		win.InvalidateRect(l.panel, nil, false)
	case longBtnCancel:
		l.finish(false)
	case longBtnDone:
		l.finish(true)
	}
}

// ---------- 面板布局与绘制 ----------

// panelLayout 是面板内各元素的位置。
type panelLayout struct {
	title, hint, thumb, status image.Rectangle
	buttons                    [3]image.Rectangle
}

// layout 计算面板内各元素的位置。
func (l *longShot) layout(client image.Rectangle) panelLayout {
	pad := dip(12, l.scale)
	gap := dip(8, l.scale)
	inner := client.Inset(pad)
	var p panelLayout
	p.title = image.Rect(inner.Min.X, inner.Min.Y, inner.Max.X, inner.Min.Y+dip(22, l.scale))
	p.hint = image.Rect(inner.Min.X, p.title.Max.Y+dip(4, l.scale), inner.Max.X, p.title.Max.Y+dip(4, l.scale)+dip(54, l.scale))

	btnH := dip(30, l.scale)
	by := inner.Max.Y - btnH
	small := dip(54, l.scale)
	p.buttons[longBtnDone] = image.Rect(inner.Max.X-small, by, inner.Max.X, by+btnH)
	p.buttons[longBtnCancel] = image.Rect(inner.Max.X-small*2-gap, by, inner.Max.X-small-gap, by+btnH)
	p.buttons[longBtnAuto] = image.Rect(inner.Min.X, by, p.buttons[longBtnCancel].Min.X-gap, by+btnH)

	p.status = image.Rect(inner.Min.X, by-gap-dip(18, l.scale), inner.Max.X, by-gap)
	p.thumb = image.Rect(inner.Min.X, p.hint.Max.Y+gap, inner.Max.X, p.status.Min.Y-gap)
	return p
}

// buttonAt 返回点 p 下的按钮，没有命中时返回 -1。
func (l *longShot) buttonAt(p image.Point) int {
	lay := l.layout(clientRect(l.panel))
	for i, r := range lay.buttons {
		if p.In(r) {
			return i
		}
	}
	return -1
}

// updateThumb 按面板缩略图区域的大小重新生成长图缩略图。
func (l *longShot) updateThumb() {
	area := l.layout(clientRect(l.panel)).thumb.Inset(dip(1, l.scale))
	img := l.st.Thumbnail(area.Dx(), area.Dy())
	if img == nil {
		return
	}
	c, err := newCanvasFromRGBA(img)
	if err != nil {
		return
	}
	l.thumb.Dispose()
	l.thumb = c
}

// paint 绘制预览面板，先画到内存位图再一次性贴上去，避免闪烁。
func (l *longShot) paint(hwnd win.HWND) {
	var ps win.PAINTSTRUCT
	hdc := win.BeginPaint(hwnd, &ps)
	defer win.EndPaint(hwnd, &ps)

	client := clientRect(hwnd)
	bmp, err := capture.NewBitmap(client.Dx(), client.Dy())
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

	fillRect(dc, client, colorPanelBorder)
	fillRect(dc, client.Inset(1), colorPanel)
	lay := l.layout(client)

	drawText(dc, "长截图", lay.title, l.titleFont, colorTitle, dtVCenter|dtSingleLine)
	hintColor := colorMuted
	if l.hintWarn {
		hintColor = colorWarn
	}
	drawText(dc, l.hint, lay.hint, l.font, hintColor, dtWordBreak)

	fillRect(dc, lay.thumb, colorThumbBg)
	if l.thumb != nil {
		tw, th := l.thumb.bmp.Width, l.thumb.bmp.Height
		x := lay.thumb.Min.X + (lay.thumb.Dx()-tw)/2
		dst := image.Rect(x, lay.thumb.Min.Y+1, x+tw, lay.thumb.Min.Y+1+th)
		blit(dc, dst, l.thumb.dc, image.Point{})
		frameRect(dc, dst.Inset(-1), 1, colorPanelBorder)
	}

	status := "等待画面稳定……"
	if h := l.st.Height(); h > 0 {
		status = fmt.Sprintf("已拼接 %d × %d px", l.st.Width(), h)
	}
	drawText(dc, status, lay.status, l.font, colorBody, dtVCenter|dtSingleLine)

	labels := [3]string{"自动滚动", "取消", "完成"}
	if l.auto {
		labels[longBtnAuto] = "停止滚动"
	}
	for i, r := range lay.buttons {
		bg, fg := colorBtnBg, colorTitle
		if i == l.hoverBtn {
			bg = colorBtnHover
		}
		if i == longBtnDone {
			bg, fg = colorAccent, colorWhite
		}
		fillRect(dc, r, bg)
		drawText(dc, labels[i], r, l.font, fg, dtCenter|dtVCenter|dtSingleLine)
	}

	blit(hdc, client, dc, image.Point{})
}

// regionFrame 是区域外那圈边框窗口，只负责把自己涂成指定颜色。
type regionFrame struct {
	color walk.Color
}

// handleMessage 实现 rawHandler。
func (f regionFrame) handleMessage(hwnd win.HWND, msg uint32, _, _ uintptr) (uintptr, bool) {
	switch msg {
	case win.WM_PAINT:
		var ps win.PAINTSTRUCT
		hdc := win.BeginPaint(hwnd, &ps)
		fillRect(hdc, clientRect(hwnd), f.color)
		win.EndPaint(hwnd, &ps)
		return 0, true
	case win.WM_ERASEBKGND:
		return 1, true
	}
	return 0, false
}
