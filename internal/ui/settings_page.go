package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/lxn/walk"
	dcl "github.com/lxn/walk/declarative"

	"goclock/internal/hotkey"
	"goclock/internal/httpx"
	"goclock/internal/ocr"
	"goclock/internal/storage"
	"goclock/internal/translate"
)

// shotHelpText 是「截图设置」页底部的操作说明。
const shotHelpText = "截图：按热键或点托盘菜单「截图」。鼠标移到窗口上会自动识别，单击即选中；也可以直接拖出选区。\r\n" +
	"选区：拖动边框或手柄调整大小，方向键微调 1 像素，按住 Shift 每次 10 像素。\r\n" +
	"标注：矩形、椭圆、箭头、画笔、马赛克 / 模糊（可涂抹也可框选）、文字；Ctrl+Z 撤销。\r\n" +
	"完成：双击选区或按 Enter 复制到剪贴板；Ctrl+S 另存为；Esc 退出；右键先取消选区，再按一次退出。\r\n" +
	"识别文字 / 翻译：工具栏上的「文」「译」按钮。识别在本机完成，默认用高精度引擎（首次使用自动下载模型，下载不了时改用系统 OCR）；翻译会把文字发给 Google。\r\n" +
	"录屏：框好区域后点工具栏上的录屏按钮，录成 MP4（无声音），控制条上可暂停、完成或放弃。\r\n" +
	"取色：放大镜显示时按 C，复制光标处的颜色值（#RRGGBB）。\r\n" +
	"钉图：截图置顶显示在桌面上，拖动移动、滚轮缩放、双击关闭，右键有更多操作。\r\n" +
	"长截图：选好区域后点工具栏上的「长截图」，在框内滚动鼠标滚轮即可自动拼接；也可以点「自动滚动」让程序代劳。"

// recordFPSOptions 是录屏帧率的可选值。
var recordFPSOptions = []int{15, 24, 30, 60}

// ocrEngineOptions 是识别引擎下拉框的选项，Code 对应 Settings.OCREngine。
var ocrEngineOptions = []struct{ Code, Name string }{
	{"", "高精度（PaddleOCR，推荐）"},
	{storage.OCREngineWindows, "系统自带（Windows OCR）"},
}

// shotSettingsPage 生成「截图设置」页签。
func (a *App) shotSettingsPage() dcl.TabPage {
	return dcl.TabPage{
		Title:  "截图设置",
		Layout: dcl.VBox{Margins: dcl.Margins{Left: 12, Top: 12, Right: 12, Bottom: 10}, Spacing: 10},
		Children: []dcl.Widget{
			formRow("截图热键", dcl.Composite{
				Layout: dcl.HBox{MarginsZero: true, Spacing: 8},
				Children: []dcl.Widget{
					dcl.LineEdit{
						AssignTo: &a.hotkeyEdit,
						Text:     a.settings.ShotHotkey,
						MinSize:  dcl.Size{Width: 160},
						MaxSize:  dcl.Size{Width: 160},
					},
					dcl.PushButton{
						Text:      "应用",
						MinSize:   dcl.Size{Width: 72},
						OnClicked: a.applyHotkeyFromEdit,
					},
					dcl.Label{
						AssignTo:  &a.hotkeyStatus,
						TextColor: colorMuted,
					},
					dcl.HSpacer{},
				},
			}),
			leftRow(dcl.Label{
				Text:      "写法如 Ctrl+Alt+A、Ctrl+Shift+X、F1；留空表示不用热键（仍可从托盘菜单截图）。",
				TextColor: colorMuted,
			}),
			formRow("保存目录", dcl.Composite{
				Layout: dcl.HBox{MarginsZero: true, Spacing: 8},
				Children: []dcl.Widget{
					dcl.LineEdit{
						AssignTo: &a.saveDirEdit,
						Text:     a.saveDir(),
						ReadOnly: true,
					},
					dcl.PushButton{
						Text:      "浏览…",
						MinSize:   dcl.Size{Width: 72},
						OnClicked: a.browseSaveDir,
					},
					dcl.PushButton{
						Text:      "打开",
						MinSize:   dcl.Size{Width: 72},
						OnClicked: a.openSaveDir,
					},
				},
			}),
			leftRow(dcl.CheckBox{
				AssignTo:         &a.autoSaveCheck,
				Text:             "完成截图（复制到剪贴板）时，同时保存一份 PNG 到保存目录",
				Checked:          a.settings.AutoSave,
				OnCheckedChanged: a.autoSaveChanged,
			}),
			formRow("录屏", dcl.Composite{
				Layout: dcl.HBox{MarginsZero: true, Spacing: 8},
				Children: []dcl.Widget{
					dcl.Label{Text: "帧率"},
					dcl.ComboBox{
						AssignTo:              &a.fpsCombo,
						Model:                 fpsNames(),
						CurrentIndex:          fpsIndex(a.settings.EffectiveRecordFPS()),
						OnCurrentIndexChanged: a.fpsChanged,
						MaxSize:               dcl.Size{Width: 110},
					},
					dcl.CheckBox{
						AssignTo:         &a.cursorCheck,
						Text:             "录制鼠标指针",
						Checked:          !a.settings.RecordHideCursor,
						OnCheckedChanged: a.cursorChanged,
					},
					dcl.HSpacer{},
				},
			}),
			formRow("识别引擎", dcl.Composite{
				Layout: dcl.HBox{MarginsZero: true, Spacing: 8},
				Children: []dcl.Widget{
					dcl.ComboBox{
						AssignTo:              &a.ocrCombo,
						Model:                 ocrEngineNames(),
						CurrentIndex:          ocrEngineIndex(a.settings.OCREngine),
						OnCurrentIndexChanged: a.ocrEngineChanged,
						MinSize:               dcl.Size{Width: 260},
					},
					dcl.Label{AssignTo: &a.ocrStatus, TextColor: colorMuted},
					dcl.HSpacer{},
				},
			}),
			formRow("翻译目标", dcl.Composite{
				Layout: dcl.HBox{MarginsZero: true, Spacing: 8},
				Children: []dcl.Widget{
					dcl.ComboBox{
						AssignTo:              &a.targetCombo,
						Model:                 languageNames(),
						CurrentIndex:          languageIndex(a.settings.TranslateTarget),
						OnCurrentIndexChanged: a.targetChanged,
						MinSize:               dcl.Size{Width: 260},
					},
					dcl.HSpacer{},
				},
			}),
			formRow("API Key", dcl.Composite{
				Layout: dcl.HBox{MarginsZero: true, Spacing: 8},
				Children: []dcl.Widget{
					dcl.LineEdit{
						AssignTo:     &a.apiKeyEdit,
						PasswordMode: true,
						MinSize:      dcl.Size{Width: 260},
						MaxSize:      dcl.Size{Width: 260},
					},
					dcl.PushButton{Text: "保存", MinSize: dcl.Size{Width: 72}, OnClicked: a.saveAPIKey},
					dcl.Label{AssignTo: &a.apiKeyStatus, TextColor: colorMuted},
					dcl.HSpacer{},
				},
			}),
			formRow("网络代理", dcl.Composite{
				Layout: dcl.HBox{MarginsZero: true, Spacing: 8},
				Children: []dcl.Widget{
					dcl.LineEdit{
						AssignTo: &a.proxyEdit,
						Text:     a.settings.Proxy,
						MinSize:  dcl.Size{Width: 260},
						MaxSize:  dcl.Size{Width: 260},
					},
					dcl.PushButton{Text: "应用", MinSize: dcl.Size{Width: 72}, OnClicked: a.applyProxy},
					dcl.Label{Text: "留空跟随系统代理，例如 http://127.0.0.1:7890", TextColor: colorMuted},
					dcl.HSpacer{},
				},
			}),
			formRow("操作说明", dcl.TextEdit{
				Text:     shotHelpText,
				ReadOnly: true,
				VScroll:  true,
				MinSize:  dcl.Size{Height: 90},
			}),
		},
	}
}

// fpsNames 返回帧率下拉框的选项文字。
func fpsNames() []string {
	names := make([]string, len(recordFPSOptions))
	for i, f := range recordFPSOptions {
		names[i] = fmt.Sprintf("%d 帧/秒", f)
	}
	return names
}

// fpsIndex 返回帧率在下拉框中的位置，不在列表里时指向 30。
func fpsIndex(fps int) int {
	for i, f := range recordFPSOptions {
		if f == fps {
			return i
		}
	}
	return 2
}

// languageNames 返回翻译目标语言下拉框的选项文字。
func languageNames() []string {
	names := make([]string, len(translate.Languages))
	for i, l := range translate.Languages {
		names[i] = l.Name
	}
	return names
}

// languageIndex 返回语言代码在下拉框中的位置，认不出时指向「自动」。
func languageIndex(code string) int {
	for i, l := range translate.Languages {
		if l.Code == code {
			return i
		}
	}
	return 0
}

// ocrEngineNames 返回识别引擎下拉框的选项文字。
func ocrEngineNames() []string {
	names := make([]string, len(ocrEngineOptions))
	for i, o := range ocrEngineOptions {
		names[i] = o.Name
	}
	return names
}

// ocrEngineIndex 返回引擎在下拉框中的位置，认不出时指向高精度。
func ocrEngineIndex(code string) int {
	for i, o := range ocrEngineOptions {
		if o.Code == code {
			return i
		}
	}
	return 0
}

// ocrEngineChanged 保存识别引擎。
func (a *App) ocrEngineChanged() {
	if i := a.ocrCombo.CurrentIndex(); i >= 0 && i < len(ocrEngineOptions) {
		a.settings.OCREngine = ocrEngineOptions[i].Code
		a.saveSettings()
		a.refreshOCRStatus()
	}
}

// refreshOCRStatus 显示高精度识别的模型是否已下载。
func (a *App) refreshOCRStatus() {
	if a.ocrStatus == nil {
		return
	}
	if a.settings.OCREngine == storage.OCREngineWindows {
		a.ocrStatus.SetText("速度快、不用下载，精度一般")
		return
	}
	dir, err := ocr.PaddleDir()
	switch {
	case err != nil:
		a.ocrStatus.SetText(err.Error())
	case ocr.PaddleReady(dir):
		a.ocrStatus.SetText("模型已下载")
	default:
		a.ocrStatus.SetText(fmt.Sprintf("首次识别时自动下载模型（%s）", formatMB(ocr.PaddleDownloadSize(dir))))
	}
}

// fpsChanged 保存录屏帧率。
func (a *App) fpsChanged() {
	if i := a.fpsCombo.CurrentIndex(); i >= 0 && i < len(recordFPSOptions) {
		a.settings.RecordFPS = recordFPSOptions[i]
		a.saveSettings()
	}
}

// cursorChanged 保存「录制鼠标指针」。
func (a *App) cursorChanged() {
	a.settings.RecordHideCursor = !a.cursorCheck.Checked()
	a.saveSettings()
}

// targetChanged 保存默认的翻译目标语言。
func (a *App) targetChanged() {
	if i := a.targetCombo.CurrentIndex(); i >= 0 && i < len(translate.Languages) {
		a.settings.TranslateTarget = translate.Languages[i].Code
		a.saveSettings()
	}
}

// saveAPIKey 加密保存 Google API Key；留空保存表示改回免 Key 接口。
func (a *App) saveAPIKey() {
	key := strings.TrimSpace(a.apiKeyEdit.Text())
	enc, err := storage.EncryptSecret(key)
	if err != nil {
		errorBox(a.mw, err.Error())
		return
	}
	a.settings.GoogleAPIKey = enc
	a.saveSettings()
	a.apiKeyEdit.SetText("")
	a.refreshAPIKeyStatus()
}

// refreshAPIKeyStatus 显示当前用的是哪种翻译接口。Key 本身不回显，只说明是否已保存。
func (a *App) refreshAPIKeyStatus() {
	if a.apiKeyStatus == nil {
		return
	}
	if a.settings.GoogleAPIKey == "" {
		a.apiKeyStatus.SetText("未填写：使用免 Key 接口")
		return
	}
	a.apiKeyStatus.SetText("已加密保存：使用官方接口（清空后保存可改回）")
}

// applyProxy 校验并保存网络代理。
func (a *App) applyProxy() {
	proxy := strings.TrimSpace(a.proxyEdit.Text())
	if _, err := httpx.NewClient(proxy, time.Second); err != nil {
		errorBox(a.mw, err.Error())
		return
	}
	a.settings.Proxy = proxy
	a.saveSettings()
}

// leftRow 生成一行与表单控件左对齐、但没有标签的布局。
// 不能直接用 formRow 传空标签：标签、复选框这类不横向拉伸的控件会被挤到行中间。
func leftRow(child dcl.Widget) dcl.Widget {
	return dcl.Composite{
		Layout: dcl.HBox{MarginsZero: true, Spacing: 8},
		Children: []dcl.Widget{
			dcl.HSpacer{MinSize: dcl.Size{Width: labelWidth}, MaxSize: dcl.Size{Width: labelWidth}},
			child,
			dcl.HSpacer{},
		},
	}
}

// applyHotkeyFromEdit 应用输入框里的热键；注册失败时恢复原来的热键并提示原因。
func (a *App) applyHotkeyFromEdit() {
	text := strings.TrimSpace(a.hotkeyEdit.Text())
	spec := text
	if text != "" {
		combo, err := hotkey.Parse(text)
		if err != nil {
			errorBox(a.mw, err.Error())
			return
		}
		spec = combo.String()
	}
	if err := a.registerShotHotkey(spec); err != nil {
		errorBox(a.mw, err.Error())
		if restoreErr := a.registerShotHotkey(a.settings.ShotHotkey); restoreErr != nil {
			a.hotkeyErr = restoreErr.Error()
		}
		a.refreshHotkeyUI()
		return
	}
	a.hotkeyErr = ""
	a.settings.ShotHotkey = spec
	a.saveSettings()
	a.refreshHotkeyUI()
}

// refreshHotkeyUI 让设置页、托盘菜单上的热键显示与实际注册状态一致。
func (a *App) refreshHotkeyUI() {
	status := "未设置热键"
	switch {
	case a.shotHotkeyID != 0:
		status = "已生效"
	case a.hotkeyErr != "":
		status = "未生效：" + a.hotkeyErr
	}
	if a.hotkeyEdit != nil {
		a.hotkeyEdit.SetText(a.settings.ShotHotkey)
	}
	if a.hotkeyStatus != nil {
		a.hotkeyStatus.SetText(status)
	}
	if a.shotAction != nil {
		text := "截图"
		if a.shotHotkeyID != 0 {
			text += "（" + a.settings.ShotHotkey + "）"
		}
		a.shotAction.SetText(text)
	}
	a.refreshAPIKeyStatus()
	a.refreshOCRStatus()
}

// browseSaveDir 选择截图保存目录。
func (a *App) browseSaveDir() {
	dlg := walk.FileDialog{Title: "选择截图保存目录", InitialDirPath: a.saveDir()}
	ok, err := dlg.ShowBrowseFolder(a.mw)
	if err != nil {
		errorBox(a.mw, "无法打开目录选择框："+err.Error())
		return
	}
	if !ok || strings.TrimSpace(dlg.FilePath) == "" {
		return
	}
	a.settings.SaveDir = dlg.FilePath
	a.saveDirEdit.SetText(a.saveDir())
	a.saveSettings()
}

// openSaveDir 在资源管理器中打开截图保存目录，目录不存在时先创建。
func (a *App) openSaveDir() {
	dir := a.saveDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		errorBox(a.mw, "无法创建目录："+err.Error())
		return
	}
	if err := exec.Command("explorer.exe", dir).Start(); err != nil {
		errorBox(a.mw, "无法打开目录："+err.Error())
	}
}

// autoSaveChanged 响应「同时保存」复选框。
func (a *App) autoSaveChanged() {
	a.settings.AutoSave = a.autoSaveCheck.Checked()
	a.saveSettings()
}

// saveSettings 保存程序设置，失败时提示。
func (a *App) saveSettings() {
	if err := a.settingsStore.Save(a.settings); err != nil {
		errorBox(a.mw, err.Error())
	}
}
