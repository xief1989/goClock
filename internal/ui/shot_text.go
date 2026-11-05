package ui

import (
	"context"
	"fmt"
	"image"
	"strings"
	"time"

	"github.com/lxn/walk"
	dcl "github.com/lxn/walk/declarative"

	"goclock/internal/httpx"
	"goclock/internal/storage"
	"goclock/internal/translate"
)

// translateTimeout 是一次翻译请求的超时时间。
const translateTimeout = 30 * time.Second

// textJob 描述打开识别 / 翻译窗口时要做的事。
type textJob struct {
	// img 不为空时先识别图片里的文字。
	img *image.RGBA
	// text 是预先填入原文框的文字（img 为空时使用）。
	text string
	// translate 表示原文就绪后立即翻译。
	translate bool
}

// textWindow 是识别文字与翻译的结果窗口：上面是可编辑的原文，下面是译文。
type textWindow struct {
	app *App
	mw  *walk.MainWindow

	src       *walk.TextEdit
	dst       *walk.TextEdit
	status    *walk.Label
	note      *walk.Label
	lang      *walk.ComboBox
	btnTrans  *walk.PushButton
	cancelReq context.CancelFunc
	cancelOCR context.CancelFunc
}

// noteMaxRunes 是底部提示文字的最大长度，更长的截断，完整内容放在鼠标提示里。
const noteMaxRunes = 48

// openTextWindow 打开一个识别 / 翻译窗口并按 job 开始工作。
func (a *App) openTextWindow(job textJob) {
	w := &textWindow{app: a}
	title := "文本翻译"
	if job.img != nil {
		title = "识别文字"
	}
	langNames := make([]string, len(translate.Languages))
	langIndex := 0
	for i, l := range translate.Languages {
		langNames[i] = l.Name
		if l.Code == a.settings.TranslateTarget {
			langIndex = i
		}
	}

	err := (dcl.MainWindow{
		AssignTo: &w.mw,
		Title:    title + " - " + appName,
		Icon:     iconProperty(),
		Font:     dcl.Font{Family: uiFontFamily, PointSize: 9},
		// walk 窗口的客户区背景默认为空，标签的 TextColor 会被系统默认色盖掉；显式给个与默认一致的底色，灰字和警告色才生效。
		Background: dcl.SystemColorBrush{Color: walk.SysColorBtnFace},
		MinSize:    dcl.Size{Width: 480, Height: 420},
		Size:       dcl.Size{Width: 680, Height: 580},
		Layout:     dcl.VBox{Margins: dcl.Margins{Left: 12, Top: 10, Right: 12, Bottom: 10}, Spacing: 6},
		Children: []dcl.Widget{
			dcl.Composite{
				Layout: dcl.HBox{MarginsZero: true, Spacing: 8},
				Children: []dcl.Widget{
					dcl.Label{Text: "原文（可以修改）"},
					dcl.HSpacer{},
					dcl.Label{AssignTo: &w.status, TextColor: colorMuted},
				},
			},
			dcl.TextEdit{
				AssignTo: &w.src,
				VScroll:  true,
				Font:     dcl.Font{Family: uiFontFamily, PointSize: 10},
				MinSize:  dcl.Size{Height: 120},
			},
			dcl.Composite{
				Layout: dcl.HBox{MarginsZero: true, Spacing: 8},
				Children: []dcl.Widget{
					dcl.PushButton{Text: "复制原文", MinSize: dcl.Size{Width: 84}, OnClicked: func() { w.copyText(w.src) }},
					dcl.HSpacer{},
					dcl.Label{Text: "译成"},
					dcl.ComboBox{AssignTo: &w.lang, Model: langNames, CurrentIndex: langIndex, MinSize: dcl.Size{Width: 220}},
					dcl.PushButton{AssignTo: &w.btnTrans, Text: "翻译", MinSize: dcl.Size{Width: 84}, OnClicked: w.translate},
				},
			},
			dcl.TextEdit{
				AssignTo: &w.dst,
				ReadOnly: true,
				VScroll:  true,
				Font:     dcl.Font{Family: uiFontFamily, PointSize: 10},
				MinSize:  dcl.Size{Height: 120},
			},
			dcl.Composite{
				Layout: dcl.HBox{MarginsZero: true, Spacing: 8},
				Children: []dcl.Widget{
					dcl.Label{AssignTo: &w.note, Text: "识别在本机完成；翻译会把原文发给 Google。", TextColor: colorMuted},
					dcl.HSpacer{},
					dcl.PushButton{Text: "复制译文", MinSize: dcl.Size{Width: 84}, OnClicked: func() { w.copyText(w.dst) }},
					dcl.PushButton{Text: "关闭", MinSize: dcl.Size{Width: 84}, OnClicked: func() { w.mw.Close() }},
				},
			},
		},
	}).Create()
	if err != nil {
		errorBox(a.mw, "无法打开窗口："+err.Error())
		return
	}

	w.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if w.cancelReq != nil {
			w.cancelReq()
		}
		if w.cancelOCR != nil {
			w.cancelOCR()
		}
		delete(a.textWindows, w)
	})
	w.mw.Disposing().Attach(func() { delete(a.textWindows, w) })
	a.textWindows[w] = struct{}{}

	w.mw.SetVisible(true)
	activateWindow(w.mw.Handle())

	switch {
	case job.img != nil:
		w.recognize(job.img, job.translate)
	default:
		setEditText(w.src, job.text)
		if job.translate && strings.TrimSpace(job.text) != "" {
			w.translate()
		}
	}
}

// recognize 在后台识别图片文字，完成后填进原文框；then 为 true 时接着翻译。
// 首次用高精度识别时要先下载模型，进度显示在状态栏；关掉窗口会取消下载。
func (w *textWindow) recognize(img *image.RGBA, then bool) {
	engine, proxy := w.app.settings.OCREngine, w.app.settings.Proxy
	ctx, cancel := context.WithCancel(context.Background())
	w.cancelOCR = cancel
	w.setBusy("正在识别文字……")
	report := func(text string) {
		safeSynchronize(w.mw, func() { w.status.SetText(text) })
	}
	go func() {
		out, err := w.app.ocr.recognize(ctx, img, engine, proxy, report)
		// 不能在协程里 defer cancel()：Synchronize 只是排队，协程先退出就会把 ctx 取消，
		// 回调里便误以为窗口已关、把结果丢掉。ctx 只由关窗口（Closing）取消，这里在界面线程上判断后再释放。
		safeSynchronize(w.mw, func() {
			closed := ctx.Err() != nil
			cancel()
			w.setIdle()
			if closed {
				return
			}
			if out.fallback != "" {
				w.setWarning("高精度识别不可用，已改用系统 OCR：" + out.fallback)
			}
			if err != nil {
				w.status.SetText("识别失败")
				setEditText(w.src, "识别失败："+err.Error())
				return
			}
			setEditText(w.src, out.res.Text)
			if strings.TrimSpace(out.res.Text) == "" {
				w.status.SetText(fmt.Sprintf("没有识别到文字（%s）", out.engine))
				return
			}
			w.status.SetText(fmt.Sprintf("识别完成（%s）：%d 行，用时 %.1f 秒", out.engine, len(out.res.Lines), out.cost.Seconds()))
			if then {
				w.translate()
			}
		})
	}()
}

// setWarning 把底部提示换成醒目的警告，过长时截断，完整内容放进鼠标提示。
func (w *textWindow) setWarning(text string) {
	short := []rune(text)
	if len(short) > noteMaxRunes {
		short = append(short[:noteMaxRunes-1], '…')
	}
	w.note.SetText(string(short))
	w.note.SetToolTipText(text)
	w.note.SetTextColor(colorWarn)
}

// translate 把原文框里的文字翻译到译文框。
func (w *textWindow) translate() {
	text := getEditText(w.src)
	if strings.TrimSpace(text) == "" {
		w.status.SetText("没有要翻译的文字")
		return
	}
	target := ""
	if i := w.lang.CurrentIndex(); i >= 0 && i < len(translate.Languages) {
		target = translate.Languages[i].Code
	}
	client, err := w.app.translateClient()
	if err != nil {
		w.status.SetText("翻译失败")
		setEditText(w.dst, err.Error())
		return
	}
	if w.cancelReq != nil {
		w.cancelReq()
	}
	ctx, cancel := context.WithTimeout(context.Background(), translateTimeout)
	w.cancelReq = cancel
	w.setBusy("正在翻译……")
	go func() {
		defer cancel()
		start := time.Now()
		res, err := client.Translate(ctx, text, target)
		cost := time.Since(start)
		safeSynchronize(w.mw, func() {
			w.setIdle()
			if err != nil {
				if ctx.Err() == context.Canceled {
					return
				}
				w.status.SetText("翻译失败")
				setEditText(w.dst, err.Error())
				return
			}
			setEditText(w.dst, res.Text)
			w.status.SetText(fmt.Sprintf("翻译完成：%s → %s，用时 %.1f 秒", languageName(res.Source), languageName(res.Target), cost.Seconds()))
		})
	}()
}

// setBusy 显示进行中的提示并暂时禁用翻译按钮。
func (w *textWindow) setBusy(text string) {
	w.status.SetText(text)
	w.btnTrans.SetEnabled(false)
	w.mw.SetCursor(walk.CursorWait())
}

// setIdle 恢复按钮与光标。
func (w *textWindow) setIdle() {
	w.btnTrans.SetEnabled(true)
	w.mw.SetCursor(nil)
}

// copyText 把文本框的内容复制到剪贴板。
func (w *textWindow) copyText(te *walk.TextEdit) {
	text := getEditText(te)
	if strings.TrimSpace(text) == "" {
		return
	}
	if err := walk.Clipboard().SetText(strings.ReplaceAll(text, "\n", "\r\n")); err != nil {
		errorBox(w.mw, "复制失败："+err.Error())
		return
	}
	w.status.SetText("已复制到剪贴板")
}

// Close 关闭窗口，退出程序前调用。
func (w *textWindow) Close() {
	if w.mw != nil && w.mw.Handle() != 0 {
		w.mw.Dispose()
	}
}

// translateClient 按设置生成翻译客户端：代理跟随设置，填了 API Key 就走官方接口。
func (a *App) translateClient() (*translate.Client, error) {
	client, err := httpx.NewClient(a.settings.Proxy, translateTimeout)
	if err != nil {
		return nil, err
	}
	key, err := storage.DecryptSecret(a.settings.GoogleAPIKey)
	if err != nil {
		return nil, fmt.Errorf("读取 API Key 失败：%w。请在「截图设置」里重新填写", err)
	}
	return &translate.Client{HTTP: client, APIKey: key}, nil
}

// languageName 把语言代码换成界面上的名称，认不出的原样返回。
func languageName(code string) string {
	for _, l := range translate.Languages {
		if l.Code != "" && strings.EqualFold(l.Code, code) {
			return l.Name
		}
	}
	if code == "" {
		return "未知语言"
	}
	return code
}

// setEditText 设置多行文本框的内容。文本框只认 \r\n 换行，这里统一转换。
func setEditText(te *walk.TextEdit, text string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	te.SetText(strings.ReplaceAll(text, "\n", "\r\n"))
}

// getEditText 读出多行文本框的内容，换行统一成 \n。
func getEditText(te *walk.TextEdit) string {
	return strings.ReplaceAll(te.Text(), "\r\n", "\n")
}
