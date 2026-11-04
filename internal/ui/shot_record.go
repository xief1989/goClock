package ui

import (
	"fmt"
	"image"
	"time"

	"github.com/lxn/walk"
	"github.com/lxn/win"

	"goclock/internal/capture"
	"goclock/internal/record"
)

// 录屏会话：区域外一圈红框，旁边一条深色控制条（计时、暂停、完成、放弃）。
// 红框与控制条都设置了「不进截图」，不会被录进视频里。

const (
	recordPanelClass = "GoClock.RecordPanel"
	recordTimerID    = 1
	recordTickMs     = 500
	// 控制条尺寸（96 DPI 下的像素）。
	recordPanelWidth  = 300
	recordPanelHeight = 40
)

// 控制条上的按钮。
const (
	recBtnPause = iota
	recBtnDone
	recBtnCancel
)

var (
	colorRecordBg    = walk.RGB(0x1F, 0x29, 0x37)
	colorRecordBtn   = walk.RGB(0x37, 0x41, 0x51)
	colorRecordHover = walk.RGB(0x4B, 0x55, 0x63)
	colorRecordDot   = walk.RGB(0xEF, 0x44, 0x44)
)

// recordSession 是一次进行中的录屏。所有方法都在界面线程上执行。
type recordSession struct {
	rec      *record.Recorder
	frame    win.HWND
	panel    win.HWND
	scale    float64
	font     win.HFONT
	boldFont win.HFONT
	hoverBtn int
	blink    bool
	ending   bool
	// onEnd 在录制结束、文件写完之后（界面线程上）调用；saved 为 false 表示用户放弃了。
	onEnd func(path string, saved bool, err error)
	// host 用来把后台收尾的结果投递回界面线程。
	host walk.Form
}

// newRecordSession 开始录制 opts 描述的区域，并显示红框与控制条。
func newRecordSession(opts record.Options, host walk.Form, onEnd func(path string, saved bool, err error)) (*recordSession, error) {
	rec, err := record.Start(opts)
	if err != nil {
		return nil, err
	}
	r := rec.Rect()
	mon := capture.MonitorAt(r.Min.Add(image.Pt(r.Dx()/2, r.Dy()/2)))
	s := &recordSession{rec: rec, scale: float64(mon.DPI) / 96, hoverBtn: -1, onEnd: onEnd, host: host}
	s.font = newFont(dip(12, s.scale), false)
	s.boldFont = newFont(dip(14, s.scale), true)

	if s.frame, err = createRegionFrame(r, s.scale, colorRecordDot); err != nil {
		rec.Cancel()
		s.releaseFonts()
		return nil, err
	}
	panel, err := createRawWindow(s, rawWindowSpec{
		class:   recordPanelClass,
		title:   "GoClock 录屏",
		exStyle: win.WS_EX_TOPMOST | win.WS_EX_TOOLWINDOW | win.WS_EX_NOACTIVATE,
		style:   win.WS_POPUP,
		bounds:  s.panelBounds(mon),
	})
	if err != nil {
		win.DestroyWindow(s.frame)
		rec.Cancel()
		s.releaseFonts()
		return nil, err
	}
	s.panel = panel
	excludeFromCapture(panel)
	win.ShowWindow(panel, win.SW_SHOWNOACTIVATE)
	win.SetTimer(panel, recordTimerID, recordTickMs, 0)
	return s, nil
}

// panelBounds 计算控制条位置：贴在录制区域右下方外侧，放不下就放上方，再不行就放进区域里面。
func (s *recordSession) panelBounds(mon capture.Monitor) image.Rectangle {
	r := s.rec.Rect()
	w, h := dip(recordPanelWidth, s.scale), dip(recordPanelHeight, s.scale)
	gap := dip(8, s.scale)
	work := mon.Work
	x := max(work.Min.X, min(r.Max.X-w, work.Max.X-w))
	y := r.Max.Y + gap
	switch {
	case y+h <= work.Max.Y:
	case r.Min.Y-gap-h >= work.Min.Y:
		y = r.Min.Y - gap - h
	default:
		y = r.Max.Y - gap - h
	}
	return image.Rect(x, y, x+w, y+h)
}

// end 结束录制：save 为 true 时保存视频，否则放弃并删除文件。
// 收尾（编码器把剩余数据写完）放到后台协程里做，界面不卡顿。
func (s *recordSession) end(save bool) {
	if s.ending {
		return
	}
	s.ending = true
	s.closeWindows()
	go func() {
		var err error
		if save {
			err = s.rec.Stop()
		} else {
			s.rec.Cancel()
		}
		safeSynchronize(s.host, func() { s.onEnd(s.rec.Path(), save, err) })
	}()
}

// stopNow 立即结束并等待视频写完，退出程序前调用（程序马上退出，来不及等后台协程）。
func (s *recordSession) stopNow() {
	if s.ending {
		return
	}
	s.ending = true
	s.closeWindows()
	_ = s.rec.Stop()
}

// closeWindows 关闭红框与控制条。
func (s *recordSession) closeWindows() {
	if s.panel != 0 {
		win.KillTimer(s.panel, recordTimerID)
		win.DestroyWindow(s.panel)
	}
	if s.frame != 0 {
		win.DestroyWindow(s.frame)
		s.frame = 0
	}
}

// releaseFonts 释放字体。
func (s *recordSession) releaseFonts() {
	for _, f := range []*win.HFONT{&s.font, &s.boldFont} {
		if *f != 0 {
			win.DeleteObject(win.HGDIOBJ(*f))
			*f = 0
		}
	}
}

// handleMessage 实现 rawHandler（控制条的消息）。
func (s *recordSession) handleMessage(hwnd win.HWND, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	switch msg {
	case win.WM_PAINT:
		s.paint(hwnd)
		return 0, true
	case win.WM_ERASEBKGND:
		return 1, true
	case win.WM_MOUSEACTIVATE:
		return maNoActivate, true
	case win.WM_NCHITTEST:
		// 按钮以外的地方可以拖着控制条走，免得它挡住要录的内容。
		var pt win.POINT
		pt.X, pt.Y = int32(int16(lParam&0xFFFF)), int32(int16((lParam>>16)&0xFFFF))
		capture.WithPerMonitorDPI(func() { win.ScreenToClient(hwnd, &pt) })
		if s.buttonAt(image.Pt(int(pt.X), int(pt.Y))) < 0 {
			return win.HTCAPTION, true
		}
		return win.HTCLIENT, true
	case win.WM_TIMER:
		if wParam == recordTimerID {
			s.blink = !s.blink
			win.InvalidateRect(hwnd, nil, false)
		}
		return 0, true
	case win.WM_MOUSEMOVE:
		if b := s.buttonAt(pointFromLParam(lParam)); b != s.hoverBtn {
			s.hoverBtn = b
			win.InvalidateRect(hwnd, nil, false)
		}
		return 0, true
	case win.WM_SETCURSOR:
		if s.hoverBtn >= 0 {
			win.SetCursor(loadCursor(idcHand))
			return 1, true
		}
	case win.WM_LBUTTONUP:
		s.click(s.buttonAt(pointFromLParam(lParam)))
		return 0, true
	case win.WM_DESTROY:
		s.panel = 0
		s.releaseFonts()
		return 0, true
	}
	return 0, false
}

// buttons 返回控制条上三个按钮的位置。
func (s *recordSession) buttons() [3]image.Rectangle {
	c := clientRect(s.panel)
	bw, bh := dip(56, s.scale), dip(26, s.scale)
	gap := dip(6, s.scale)
	y := c.Min.Y + (c.Dy()-bh)/2
	x := c.Max.X - dip(8, s.scale) - bw*3 - gap*2
	var rs [3]image.Rectangle
	for i := range rs {
		rs[i] = image.Rect(x, y, x+bw, y+bh)
		x += bw + gap
	}
	return rs
}

// buttonAt 返回点 p 下的按钮，没有命中时返回 -1。
func (s *recordSession) buttonAt(p image.Point) int {
	for i, r := range s.buttons() {
		if p.In(r) {
			return i
		}
	}
	return -1
}

// click 处理按钮点击。
func (s *recordSession) click(btn int) {
	switch btn {
	case recBtnPause:
		s.rec.SetPaused(!s.rec.Paused())
		win.InvalidateRect(s.panel, nil, false)
	case recBtnDone:
		s.end(true)
	case recBtnCancel:
		s.end(false)
	}
}

// paint 绘制控制条：红点（录制中闪烁、暂停时变灰）、计时与三个按钮。
func (s *recordSession) paint(hwnd win.HWND) {
	var ps win.PAINTSTRUCT
	hdc := win.BeginPaint(hwnd, &ps)
	defer win.EndPaint(hwnd, &ps)

	client := clientRect(hwnd)
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

	fillRect(dc, client, colorRecordBg)
	paused := s.rec.Paused()
	pad := dip(12, s.scale)
	d := dip(10, s.scale)
	cy := client.Dy() / 2
	dot := image.Rect(pad, cy-d/2, pad+d, cy-d/2+d)
	if g := newGraphics(dc); g != nil {
		switch {
		case paused:
			g.fillEllipse(colorMuted, dot)
		case s.blink:
			g.fillEllipse(colorRecordDot, dot)
		default:
			g.fillEllipseAlpha(colorRecordDot, 110, dot)
		}
		g.Close()
	}

	el := s.rec.Elapsed()
	clock := fmt.Sprintf("%02d:%02d", int(el.Minutes()), int(el.Seconds())%60)
	if el >= time.Hour {
		clock = fmt.Sprintf("%d:%02d:%02d", int(el.Hours()), int(el.Minutes())%60, int(el.Seconds())%60)
	}
	tx := dot.Max.X + dip(8, s.scale)
	drawText(dc, clock, image.Rect(tx, 0, tx+dip(70, s.scale), client.Dy()), s.boldFont, colorWhite, dtVCenter|dtSingleLine)
	if paused {
		drawText(dc, "已暂停", image.Rect(tx+dip(52, s.scale), 0, tx+dip(110, s.scale), client.Dy()),
			s.font, colorMagHint, dtVCenter|dtSingleLine)
	}

	labels := [3]string{"暂停", "完成", "放弃"}
	if paused {
		labels[recBtnPause] = "继续"
	}
	for i, r := range s.buttons() {
		bg := colorRecordBtn
		if i == s.hoverBtn {
			bg = colorRecordHover
		}
		if i == recBtnDone {
			bg = colorAccent
		}
		fillRect(dc, r, bg)
		drawText(dc, labels[i], r, s.font, colorWhite, dtCenter|dtVCenter|dtSingleLine)
	}

	blit(hdc, client, dc, image.Point{})
}
