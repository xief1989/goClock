package ui

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/lxn/walk"
	dcl "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

// 弹窗的尺寸与留白，单位均为 DIP（96 DPI 下即像素）。
const (
	// toastWidth 是提醒弹窗的宽度。
	toastWidth = 340
	// toastBorder 是外框线宽。
	toastBorder = 1
	// toastAccentWidth 是左侧竖向强调条的宽度。
	toastAccentWidth = 5
	// toastPadLeft 是强调条与正文之间的留白。
	toastPadLeft = 14
	// toastPadRight 是正文与右边框之间的留白。
	toastPadRight = 12
	// toastPadTop 是卡片顶部的留白。
	toastPadTop = 12
	// toastPadBottom 是卡片底部的留白。
	toastPadBottom = 10
	// toastTitleHeight 是标题行的高度。
	toastTitleHeight = 22
	// toastFooterHeight 是底部时间行的高度。
	toastFooterHeight = 16
	// toastGapTitleBody 是标题与正文之间的间距。
	toastGapTitleBody = 6
	// toastGapBodyFooter 是正文与底部之间的间距。
	toastGapBodyFooter = 8
	// toastCloseSize 是右上角关闭图标的边长。
	toastCloseSize = 16
	// toastMaxBodyLines 是正文最多显示的行数，超出部分会被裁掉。
	toastMaxBodyLines = 4

	// toastAutoCloseSeconds 是弹窗自动关闭的倒计时秒数。
	toastAutoCloseSeconds = 15
	// toastMaxVisible 是屏幕上同时存在的弹窗数量上限。
	toastMaxVisible = 4
	// toastScreenMargin 是弹窗与屏幕边缘的间距。
	toastScreenMargin = 12
	// toastGap 是多个弹窗之间的垂直间距。
	toastGap = 8

	// lineHeightFallback 是无法测量文本时使用的兜底行高。
	lineHeightFallback = 18
)

// toastContentLeft 是正文区域的左边界。
const toastContentLeft = toastBorder + toastAccentWidth + toastPadLeft

// toastContentWidth 是正文区域的可用宽度。
const toastContentWidth = toastWidth - toastContentLeft - toastPadRight - toastBorder

// spiGetWorkArea 对应 SystemParametersInfo 的 SPI_GETWORKAREA，
// 取到的是排除任务栏之后的桌面可用区域。
const spiGetWorkArea = 0x0030

var (
	colorBorder = walk.RGB(0xC9, 0xD4, 0xE3)
	colorPanel  = walk.RGB(0xFF, 0xFF, 0xFF)
	colorAccent = walk.RGB(0x25, 0x63, 0xEB)
	colorTitle  = walk.RGB(0x1F, 0x29, 0x37)
	colorBody   = walk.RGB(0x37, 0x41, 0x51)
	colorMuted  = walk.RGB(0x9C, 0xA3, 0xAF)
	colorClose  = walk.RGB(0x6B, 0x72, 0x80)
)

// toastRes 是弹窗绘制所需的字体与画刷，全部弹窗共用一份。
type toastRes struct {
	titleFont *walk.Font
	bodyFont  *walk.Font
	smallFont *walk.Font

	borderBrush *walk.SolidColorBrush
	panelBrush  *walk.SolidColorBrush
	accentBrush *walk.SolidColorBrush

	closePen walk.Pen
}

var (
	toastResOnce sync.Once
	toastResVal  *toastRes
)

// resources 返回弹窗绘制资源，任一项创建失败时返回 nil。
func resources() *toastRes {
	toastResOnce.Do(func() {
		r := &toastRes{}
		var err error
		if r.titleFont, err = walk.NewFont(uiFontFamily, 11, walk.FontBold); err != nil {
			return
		}
		if r.bodyFont, err = walk.NewFont(uiFontFamily, 9, 0); err != nil {
			return
		}
		if r.smallFont, err = walk.NewFont(uiFontFamily, 8, 0); err != nil {
			return
		}
		if r.borderBrush, err = walk.NewSolidColorBrush(colorBorder); err != nil {
			return
		}
		if r.panelBrush, err = walk.NewSolidColorBrush(colorPanel); err != nil {
			return
		}
		if r.accentBrush, err = walk.NewSolidColorBrush(colorAccent); err != nil {
			return
		}
		if r.closePen, err = walk.NewCosmeticPen(walk.PenSolid, colorClose); err != nil {
			return
		}
		toastResVal = r
	})
	return toastResVal
}

// toastManager 管理屏幕右下角所有正在显示的提醒弹窗，负责堆叠排列与倒计时。
// 其所有方法都必须在界面线程上调用。
type toastManager struct {
	// host 是生命周期贯穿整个程序的主窗口。
	// 倒计时协程只向它投递任务，因此不会碰到已经销毁的弹窗窗口。
	host  *walk.MainWindow
	items []*toast
	// stop 用于结束当前的倒计时协程，无弹窗时为 nil。
	stop chan struct{}
}

// toast 是一个提醒弹窗。整张卡片由一个自绘控件承担，
// 因此点击卡片上的任意位置都能把它关掉。
type toast struct {
	mgr    *toastManager
	form   *walk.MainWindow
	widget *walk.CustomWidget

	title   string
	message string
	firedAt time.Time

	remain int
	// paused 表示倒计时因鼠标停留而暂停。
	paused bool

	bodyHeight int
	cardHeight int
	widthPx    int
	heightPx   int
	closed     bool
}

// Show 弹出一个提醒。title 为标题，message 为正文，firedAt 为触发时刻。
func (m *toastManager) Show(title, message string, firedAt time.Time) {
	// 超过上限时先收掉最早的一个，避免铺满整个屏幕。
	for len(m.items) >= toastMaxVisible {
		m.items[0].close()
	}

	t := &toast{
		mgr:     m,
		title:   strings.TrimSpace(title),
		message: strings.TrimSpace(message),
		firedAt: firedAt,
		remain:  toastAutoCloseSeconds,
	}
	if err := t.build(); err != nil {
		return
	}

	m.items = append(m.items, t)
	m.arrange()
	m.startTicker()
}

// startTicker 启动倒计时协程，已在运行时直接返回。
func (m *toastManager) startTicker() {
	if m.stop != nil || m.host == nil {
		return
	}
	stop := make(chan struct{})
	m.stop = stop
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				safeSynchronize(m.host, m.tickAll)
			}
		}
	}()
}

// stopTicker 结束倒计时协程。
func (m *toastManager) stopTicker() {
	if m.stop == nil {
		return
	}
	close(m.stop)
	m.stop = nil
}

// tickAll 在界面线程上推进所有弹窗的倒计时。
func (m *toastManager) tickAll() {
	// 倒计时过程中弹窗可能自行关闭，因此先复制一份再遍历。
	for _, t := range append([]*toast(nil), m.items...) {
		t.countdownStep()
	}
	if len(m.items) == 0 {
		m.stopTicker()
	}
}

// CloseAll 关闭全部弹窗并停止倒计时，退出程序前调用。
func (m *toastManager) CloseAll() {
	for len(m.items) > 0 {
		m.items[0].close()
	}
	m.stopTicker()
}

// remove 把弹窗移出管理列表。
func (m *toastManager) remove(t *toast) {
	for i, item := range m.items {
		if item == t {
			m.items = append(m.items[:i], m.items[i+1:]...)
			break
		}
	}
}

// arrange 从工作区右下角起自下而上排列弹窗，最新的一个贴近托盘。
func (m *toastManager) arrange() {
	if len(m.items) == 0 {
		return
	}
	work := workArea()
	dpi := m.items[len(m.items)-1].form.DPI()
	margin := walk.IntFrom96DPI(toastScreenMargin, dpi)
	gap := walk.IntFrom96DPI(toastGap, dpi)

	y := int(work.Bottom) - margin
	for i := len(m.items) - 1; i >= 0; i-- {
		t := m.items[i]
		y -= t.heightPx
		x := int(work.Right) - margin - t.widthPx
		win.SetWindowPos(
			t.form.Handle(),
			win.HWND_TOPMOST,
			int32(x), int32(y),
			int32(t.widthPx), int32(t.heightPx),
			win.SWP_NOACTIVATE|win.SWP_SHOWWINDOW,
		)
		y -= gap
	}
}

// workArea 返回主显示器排除任务栏后的可用区域（像素）。
func workArea() win.RECT {
	var rc win.RECT
	if win.SystemParametersInfo(spiGetWorkArea, 0, unsafe.Pointer(&rc), 0) {
		return rc
	}
	return win.RECT{
		Right:  win.GetSystemMetrics(win.SM_CXSCREEN),
		Bottom: win.GetSystemMetrics(win.SM_CYSCREEN),
	}
}

// build 创建弹窗窗口并按内容算出高度。窗口创建后保持隐藏，由 arrange 负责显示。
func (t *toast) build() error {
	if err := (dcl.MainWindow{
		AssignTo: &t.form,
		Title:    "GoClock 提醒",
		Visible:  false,
		Layout:   dcl.VBox{MarginsZero: true, SpacingZero: true},
		Children: []dcl.Widget{
			dcl.CustomWidget{
				AssignTo:  &t.widget,
				Paint:     t.paint,
				PaintMode: dcl.PaintNoErase,
			},
		},
	}).Create(); err != nil {
		return err
	}

	if ic := appIcon(); ic != nil {
		t.form.SetIcon(ic)
	}

	hwnd := t.form.Handle()

	// 去掉标题栏与可调边框，改为纯弹出式窗口。
	style := win.GetWindowLongPtr(hwnd, win.GWL_STYLE)
	style &^= win.WS_CAPTION | win.WS_THICKFRAME | win.WS_MINIMIZEBOX | win.WS_MAXIMIZEBOX | win.WS_SYSMENU
	style |= win.WS_POPUP
	win.SetWindowLongPtr(hwnd, win.GWL_STYLE, style)

	// 不进任务栏、不进 Alt+Tab，并且点击时不抢走当前窗口的焦点。
	exStyle := win.GetWindowLongPtr(hwnd, win.GWL_EXSTYLE)
	exStyle |= win.WS_EX_TOOLWINDOW | win.WS_EX_TOPMOST | win.WS_EX_NOACTIVATE
	exStyle &^= win.WS_EX_APPWINDOW
	win.SetWindowLongPtr(hwnd, win.GWL_EXSTYLE, exStyle)

	// 整张卡片都可以点击关闭，用手型光标提示这一点。
	t.widget.SetCursor(walk.CursorHand())
	t.widget.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			t.close()
		}
	})

	t.layout()

	dpi := t.form.DPI()
	t.widthPx = walk.IntFrom96DPI(toastWidth, dpi)
	t.heightPx = walk.IntFrom96DPI(t.cardHeight, dpi)

	win.SetWindowPos(hwnd, win.HWND_TOPMOST, 0, 0, int32(t.widthPx), int32(t.heightPx),
		win.SWP_NOACTIVATE|win.SWP_NOMOVE|win.SWP_FRAMECHANGED)

	return nil
}

// layout 测量正文并算出卡片总高度。
func (t *toast) layout() {
	canvas, err := t.widget.CreateCanvas()
	if err != nil {
		t.bodyHeight = 0
		if t.message != "" {
			t.bodyHeight = lineHeightFallback
		}
	} else {
		defer canvas.Dispose()
		t.bodyHeight = t.measureBodyOn(canvas)
	}
	t.applyLayout()
}

// applyLayout 根据已测得的正文高度算出卡片总高。
func (t *toast) applyLayout() {
	t.cardHeight = toastPadTop + toastTitleHeight + toastGapBodyFooter +
		toastFooterHeight + toastPadBottom
	// 没有正文时不留空行，卡片收紧一些。
	if t.bodyHeight > 0 {
		t.cardHeight += toastGapTitleBody + t.bodyHeight
	}
}

// measureBodyOn 用给定画布计算正文实际需要的高度，并限制在 toastMaxBodyLines 行以内。
// 独立成函数是为了让离屏预览与实际弹窗共用同一套测量逻辑。
func (t *toast) measureBodyOn(canvas *walk.Canvas) int {
	res := resources()
	if t.message == "" {
		return 0
	}
	if res == nil {
		return lineHeightFallback
	}

	lineHeight := lineHeightFallback
	probe := walk.Rectangle{Width: toastContentWidth, Height: 200}
	if r, _, err := canvas.MeasureText("测", res.bodyFont, probe, walk.TextSingleLine); err == nil && r.Height > 0 {
		lineHeight = r.Height
	}

	need := lineHeight
	bounds := walk.Rectangle{Width: toastContentWidth, Height: lineHeight * 20}
	if r, _, err := canvas.MeasureText(t.message, res.bodyFont, bounds, walk.TextWordbreak); err == nil && r.Height > 0 {
		need = r.Height
	}

	if limit := lineHeight * toastMaxBodyLines; need > limit {
		need = limit
	}
	return need
}

// paint 绘制整张提醒卡片。
func (t *toast) paint(canvas *walk.Canvas, _ walk.Rectangle) error {
	res := resources()
	if res == nil {
		return nil
	}
	w := toastWidth
	h := t.cardHeight

	// 外框与白色底面。
	if err := canvas.FillRectangle(res.borderBrush, walk.Rectangle{Width: w, Height: h}); err != nil {
		return err
	}
	inner := walk.Rectangle{
		X: toastBorder, Y: toastBorder,
		Width: w - toastBorder*2, Height: h - toastBorder*2,
	}
	if err := canvas.FillRectangle(res.panelBrush, inner); err != nil {
		return err
	}
	// 左侧竖向强调条。
	accent := walk.Rectangle{
		X: toastBorder, Y: toastBorder,
		Width: toastAccentWidth, Height: h - toastBorder*2,
	}
	if err := canvas.FillRectangle(res.accentBrush, accent); err != nil {
		return err
	}

	// 标题，右侧给关闭图标让出位置。
	titleRect := walk.Rectangle{
		X: toastContentLeft, Y: toastPadTop,
		Width:  toastContentWidth - toastCloseSize - 8,
		Height: toastTitleHeight,
	}
	if err := canvas.DrawText(t.title, res.titleFont, colorTitle, titleRect,
		walk.TextSingleLine|walk.TextVCenter|walk.TextEndEllipsis); err != nil {
		return err
	}

	if err := t.paintCloseIcon(canvas, res); err != nil {
		return err
	}

	// 正文按可用宽度自动换行，超出高度的部分会被裁掉。
	if t.bodyHeight > 0 {
		bodyRect := walk.Rectangle{
			X: toastContentLeft, Y: toastPadTop + toastTitleHeight + toastGapTitleBody,
			Width: toastContentWidth, Height: t.bodyHeight,
		}
		if err := canvas.DrawText(t.message, res.bodyFont, colorBody, bodyRect,
			walk.TextWordbreak|walk.TextTop|walk.TextEndEllipsis); err != nil {
			return err
		}
	}

	// 底部：左侧触发时间，右侧倒计时提示。
	footerY := h - toastPadBottom - toastFooterHeight
	half := toastContentWidth / 2
	timeRect := walk.Rectangle{
		X: toastContentLeft, Y: footerY,
		Width: half, Height: toastFooterHeight,
	}
	if err := canvas.DrawText(t.firedAt.Format("2006-01-02 15:04"), res.smallFont, colorMuted, timeRect,
		walk.TextSingleLine|walk.TextVCenter); err != nil {
		return err
	}

	tipRect := walk.Rectangle{
		X: toastContentLeft + half, Y: footerY,
		Width: toastContentWidth - half, Height: toastFooterHeight,
	}
	return canvas.DrawText(t.tipText(), res.smallFont, colorMuted, tipRect,
		walk.TextSingleLine|walk.TextVCenter|walk.TextRight)
}

// paintCloseIcon 在右上角画出关闭用的叉号。
func (t *toast) paintCloseIcon(canvas *walk.Canvas, res *toastRes) error {
	r := t.closeRect()
	// 叉号四周各留一点空隙，看起来更轻。
	const inset = 4
	x1, y1 := r.X+inset, r.Y+inset
	x2, y2 := r.X+r.Width-inset, r.Y+r.Height-inset

	if err := canvas.DrawLine(res.closePen, walk.Point{X: x1, Y: y1}, walk.Point{X: x2, Y: y2}); err != nil {
		return err
	}
	return canvas.DrawLine(res.closePen, walk.Point{X: x2, Y: y1}, walk.Point{X: x1, Y: y2})
}

// closeRect 返回关闭图标所占的区域。
func (t *toast) closeRect() walk.Rectangle {
	return walk.Rectangle{
		X:      toastWidth - toastBorder - toastPadRight - toastCloseSize,
		Y:      toastPadTop + (toastTitleHeight-toastCloseSize)/2,
		Width:  toastCloseSize,
		Height: toastCloseSize,
	}
}

// tipText 返回底部右侧的倒计时提示文字。
func (t *toast) tipText() string {
	if t.paused {
		return "移开鼠标后继续"
	}
	return fmt.Sprintf("%d 秒后自动关闭", t.remain)
}

// countdownStep 每秒在界面线程上执行一次，负责推进倒计时并在归零时关闭弹窗。
func (t *toast) countdownStep() {
	if t.closed {
		return
	}

	// 鼠标停在卡片上时暂停倒计时；移开后从暂停处继续，而不是重新计时。
	if t.hovered() {
		if !t.paused {
			t.paused = true
			t.widget.Invalidate()
		}
		return
	}
	if t.paused {
		t.paused = false
	}

	t.remain--
	if t.remain <= 0 {
		t.close()
		return
	}
	t.widget.Invalidate()
}

// hovered 判断鼠标指针当前是否落在弹窗范围内。
func (t *toast) hovered() bool {
	var pt win.POINT
	if !win.GetCursorPos(&pt) {
		return false
	}
	var rc win.RECT
	if !win.GetWindowRect(t.form.Handle(), &rc) {
		return false
	}
	return pt.X >= rc.Left && pt.X < rc.Right && pt.Y >= rc.Top && pt.Y < rc.Bottom
}

// close 关闭并销毁弹窗，同时让剩余弹窗重新排列。
func (t *toast) close() {
	if t.closed {
		return
	}
	t.closed = true
	t.mgr.remove(t)
	t.form.Dispose()
	t.mgr.arrange()
}
