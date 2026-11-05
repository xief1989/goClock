package ui

import (
	"errors"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lxn/walk"

	"goclock/internal/capture"
	"goclock/internal/hotkey"
	"goclock/internal/record"
)

// shotMenuDelay 是从托盘菜单触发截图时的等待时间：菜单收起有淡出动画，立刻冻结屏幕会把菜单截进去。
const shotMenuDelay = 250 * time.Millisecond

// startShot 开始一次截图。截图遮罩或长截图正在进行时忽略重复触发；录屏时不截图，免得遮罩被录进视频。
func (a *App) startShot() {
	if a.overlay != nil || a.long != nil {
		return
	}
	if a.recording != nil {
		a.showBalloon("正在录屏", "先点控制条上的「完成」或托盘菜单里的「停止录屏」，再截图。", "")
		return
	}
	o, err := newShotOverlay(func(res shotResult) {
		// 遮罩是在自己的窗口过程里结束的，后续动作（弹保存框、开始长截图）放回消息循环里再做。
		safeSynchronize(a.mw, func() { a.onShotDone(res) })
	})
	if err != nil {
		errorBox(a.mw, "无法开始截图："+err.Error())
		return
	}
	a.overlay = o
}

// startShotFromMenu 从托盘菜单触发截图，等菜单收起之后再冻结屏幕。
func (a *App) startShotFromMenu() {
	time.AfterFunc(shotMenuDelay, func() {
		safeSynchronize(a.mw, a.startShot)
	})
}

// onShotDone 处理截图遮罩交回的结果。
func (a *App) onShotDone(res shotResult) {
	a.overlay = nil
	if res.err != nil {
		errorBox(a.mw, "生成截图失败："+res.err.Error())
		return
	}
	switch res.action {
	case shotCopy:
		a.copyImage(res.img, a.settings.AutoSave)
	case shotSave:
		a.saveImageAs(res.img)
	case shotPin:
		a.pinImage(res.img, res.rect.Min, 1)
	case shotLong:
		a.startLongShot(res.rect)
	case shotOCR:
		a.openTextPicker(res.img, res.rect.Min)
	case shotTranslate:
		a.openTextWindow(textJob{img: res.img, translate: true})
	case shotRecord:
		a.startRecording(res.rect)
	case shotColor:
		if err := walk.Clipboard().SetText(res.text); err != nil {
			errorBox(a.mw, "复制颜色值失败："+err.Error())
		}
	}
}

// copyImage 把图片复制到剪贴板；autoSave 为 true 时再按设置保存一份到截图目录。
// 只有「完成截图」才自动保存，从钉图里复制不算，免得同一张图存好几份。
func (a *App) copyImage(img *image.RGBA, autoSave bool) {
	if err := capture.CopyImage(a.mw.Handle(), img); err != nil {
		errorBox(a.mw, "复制到剪贴板失败："+err.Error())
		return
	}
	if autoSave {
		if _, err := capture.SaveToDir(img, a.saveDir(), time.Now()); err != nil {
			errorBox(a.mw, "自动保存截图失败："+err.Error())
		}
	}
}

// saveDir 返回截图保存目录：设置里没填就用「图片\GoClock」。
func (a *App) saveDir() string {
	if dir := strings.TrimSpace(a.settings.SaveDir); dir != "" {
		return dir
	}
	return capture.DefaultSaveDir()
}

// saveImageAs 弹出「另存为」对话框保存图片。
func (a *App) saveImageAs(img *image.RGBA) {
	dir := a.saveDir()
	_ = os.MkdirAll(dir, 0o755)
	dlg := walk.FileDialog{
		Title:          "保存截图",
		Filter:         "PNG 图片 (*.png)|*.png",
		FilePath:       filepath.Join(dir, capture.FileName(time.Now())),
		InitialDirPath: dir,
	}
	ok, err := dlg.ShowSave(nil)
	if err != nil {
		errorBox(a.mw, "无法打开保存对话框："+err.Error())
		return
	}
	if !ok {
		return
	}
	path := dlg.FilePath
	if !strings.EqualFold(filepath.Ext(path), ".png") {
		path += ".png"
	}
	if err := capture.SavePNG(img, path); err != nil {
		errorBox(a.mw, "保存截图失败："+err.Error())
	}
}

// pinImage 把图片钉在屏幕坐标 at 处，zoom 是初始缩放比例。
func (a *App) pinImage(img *image.RGBA, at image.Point, zoom float64) {
	p, err := newPinWindow(img, at, zoom, pinActions{
		copy: func(img *image.RGBA) { a.copyImage(img, false) },
		save: a.saveImageAs,
		recognize: func(img *image.RGBA, translate bool) {
			if translate {
				a.openTextWindow(textJob{img: img, translate: true})
				return
			}
			a.openTextPicker(img, capture.CursorPos())
		},
		closed: func(p *pinWindow) { delete(a.pins, p) },
	})
	if err != nil {
		errorBox(a.mw, "钉图失败："+err.Error())
		return
	}
	a.pins[p] = struct{}{}
}

// startLongShot 在区域 rect（屏幕坐标）上开始长截图，完成后打开长图预览窗。
func (a *App) startLongShot(rect image.Rectangle) {
	l, err := newLongShot(rect, func(img *image.RGBA) {
		safeSynchronize(a.mw, func() {
			a.long = nil
			if img != nil {
				a.showLongResult(img, rect)
			}
		})
	})
	if err != nil {
		errorBox(a.mw, "无法开始长截图："+err.Error())
		return
	}
	a.long = l
}

// showLongResult 打开长图预览窗。
func (a *App) showLongResult(img *image.RGBA, rect image.Rectangle) {
	v, err := newLongViewer(img, rect.Min, viewerActions{
		copy:      func(img *image.RGBA) { a.copyImage(img, a.settings.AutoSave) },
		save:      a.saveImageAs,
		recognize: func(img *image.RGBA) { a.openTextPicker(img, rect.Min) },
		pin: func(img *image.RGBA, at image.Point) {
			// 长图原样钉出来会比屏幕还高，先缩到显示器可用高度的八成以内。
			work := capture.MonitorAt(at).Work
			zoom := min(1, float64(work.Dy())*0.8/float64(img.Rect.Dy()))
			a.pinImage(img, at, zoom)
		},
		closed: func(v *longViewer) { delete(a.viewers, v) },
	})
	if err != nil {
		errorBox(a.mw, "无法打开长图预览："+err.Error())
		return
	}
	a.viewers[v] = struct{}{}
}

// closeShots 关闭所有截图相关的窗口，退出程序前调用。正在录屏的话先把视频保存好。
func (a *App) closeShots() {
	if a.overlay != nil {
		a.overlay.finish(shotResult{action: shotCancel})
		a.overlay = nil
	}
	if a.long != nil {
		a.long.Cancel()
		a.long = nil
	}
	if a.recording != nil {
		a.recording.stopNow()
		a.recording = nil
	}
	for p := range a.pins {
		p.Close()
	}
	for v := range a.viewers {
		v.Close()
	}
	for w := range a.textWindows {
		w.Close()
	}
	for p := range a.pickers {
		p.Close()
	}
}

// startRecording 开始录制屏幕区域 rect（屏幕坐标），文件保存到截图目录。
func (a *App) startRecording(rect image.Rectangle) {
	path := filepath.Join(a.saveDir(), capture.FileNameExt(time.Now(), ".mp4"))
	s, err := newRecordSession(record.Options{
		Rect:   rect,
		FPS:    a.settings.EffectiveRecordFPS(),
		Cursor: !a.settings.RecordHideCursor,
		Path:   path,
	}, a.mw, a.onRecordEnd)
	if err != nil {
		errorBox(a.mw, "无法开始录屏："+err.Error())
		return
	}
	a.recording = s
	if a.stopRecordAction != nil {
		a.stopRecordAction.SetVisible(true)
	}
}

// stopRecording 从托盘菜单结束录屏并保存。
func (a *App) stopRecording() {
	if a.recording != nil {
		a.recording.end(true)
	}
}

// onRecordEnd 在录屏结束、文件写完之后调用：保存成功时弹托盘提示，点提示可在资源管理器里找到视频。
func (a *App) onRecordEnd(path string, saved bool, err error) {
	a.recording = nil
	if a.stopRecordAction != nil {
		a.stopRecordAction.SetVisible(false)
	}
	switch {
	case err != nil:
		errorBox(a.mw, "录屏保存失败："+err.Error())
	case saved:
		a.showBalloon("录屏已保存", filepath.Base(path)+"\r\n点这里打开所在文件夹。", path)
	}
}

// showBalloon 在托盘弹出一条提示；target 不为空时，点击提示会在资源管理器中选中这个文件。
func (a *App) showBalloon(title, info, target string) {
	if a.ni == nil {
		return
	}
	a.balloonTarget = target
	a.ni.ShowInfo(title, info)
}

// onBalloonClicked 响应托盘提示的点击。
func (a *App) onBalloonClicked() {
	if a.balloonTarget == "" {
		return
	}
	// explorer 的 /select 参数要求逗号紧跟着带引号的完整路径。
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + a.balloonTarget + `"`}
	_ = cmd.Start()
}

// openTranslateFromTray 从托盘打开「文本翻译」窗口：剪贴板里有文字就直接拿来翻译。
func (a *App) openTranslateFromTray() {
	text, _ := walk.Clipboard().Text()
	text = strings.TrimSpace(text)
	a.openTextWindow(textJob{text: text, translate: text != ""})
}

// initHotkeys 创建热键管理器并注册截图热键。热键被占用不影响程序启动，只在托盘上提示一下。
func (a *App) initHotkeys() {
	m, err := hotkey.New()
	if err != nil {
		a.hotkeyErr = err.Error()
		a.refreshHotkeyUI()
		return
	}
	a.hotkeys = m
	if err := a.registerShotHotkey(a.settings.ShotHotkey); err != nil {
		a.hotkeyErr = err.Error()
		if a.ni != nil {
			a.ni.ShowWarning("截图热键没有生效", err.Error()+"。可以在「截图设置」里换一个。")
		}
	}
	a.refreshHotkeyUI()
}

// registerShotHotkey 把截图热键换成 spec 描述的组合；spec 为空表示不使用热键。
func (a *App) registerShotHotkey(spec string) error {
	if a.hotkeys == nil {
		return errors.New("热键功能不可用")
	}
	if a.shotHotkeyID != 0 {
		a.hotkeys.Unregister(a.shotHotkeyID)
		a.shotHotkeyID = 0
	}
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil
	}
	combo, err := hotkey.Parse(spec)
	if err != nil {
		return err
	}
	id, err := a.hotkeys.Register(combo, a.startShot)
	if err != nil {
		return err
	}
	a.shotHotkeyID = id
	return nil
}
