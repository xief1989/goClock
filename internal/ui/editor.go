package ui

import (
	"strings"
	"time"

	"github.com/lxn/walk"
	dcl "github.com/lxn/walk/declarative"

	"goclock/internal/alarm"
)

// labelWidth 是编辑对话框中左侧标签的固定宽度，用于让各行对齐。
const labelWidth = 68

// editor 持有编辑对话框中的各个控件。
type editor struct {
	dlg *walk.Dialog

	titleEdit    *walk.LineEdit
	messageEdit  *walk.TextEdit
	repeatCombo  *walk.ComboBox
	enabledCheck *walk.CheckBox

	dateRow  *walk.Composite
	dateEdit *walk.DateEdit

	weekRow    *walk.Composite
	weekChecks [7]*walk.CheckBox

	monthRow   *walk.Composite
	monthEdit  *walk.NumberEdit
	daysRow    *walk.Composite
	daysEdit   *walk.LineEdit
	timeRow    *walk.Composite
	hourEdit   *walk.NumberEdit
	minuteEdit *walk.NumberEdit

	intervalRow  *walk.Composite
	intervalEdit *walk.NumberEdit
}

// RunAlarmEditor 打开闹钟编辑对话框，用户点击保存时把结果写回 a 并返回 true。
func RunAlarmEditor(owner walk.Form, a *alarm.Alarm, title string) bool {
	e := &editor{}
	var acceptBtn, cancelBtn *walk.PushButton

	dialog := dcl.Dialog{
		AssignTo:      &e.dlg,
		Title:         title,
		Icon:          iconProperty(),
		Font:          dcl.Font{Family: uiFontFamily, PointSize: 9},
		DefaultButton: &acceptBtn,
		CancelButton:  &cancelBtn,
		MinSize:       dcl.Size{Width: 430, Height: 400},
		Size:          dcl.Size{Width: 430, Height: 400},
		Layout:        dcl.VBox{Margins: dcl.Margins{Left: 14, Top: 14, Right: 14, Bottom: 12}, Spacing: 8},
		Children: []dcl.Widget{
			formRow("提醒标题", dcl.LineEdit{
				AssignTo:  &e.titleEdit,
				Text:      a.Title,
				MaxLength: 60,
			}),
			formRow("提醒内容", dcl.TextEdit{
				AssignTo: &e.messageEdit,
				Text:     a.Message,
				VScroll:  true,
				MinSize:  dcl.Size{Height: 72},
			}),
			formRow("循环方式", dcl.ComboBox{
				AssignTo:              &e.repeatCombo,
				Model:                 repeatNames(),
				CurrentIndex:          repeatIndex(a.Repeat),
				OnCurrentIndexChanged: func() { e.syncVisibility() },
			}),

			// 以下几行按循环方式动态显示。
			formRowAssign(&e.dateRow, "提醒日期", dcl.DateEdit{
				AssignTo: &e.dateEdit,
				Format:   "yyyy-MM-dd",
				MaxSize:  dcl.Size{Width: 140},
			}),
			formRowAssign(&e.weekRow, "星期", weekdayChecks(&e.weekChecks, a)),
			formRowAssign(&e.monthRow, "月份", dcl.NumberEdit{
				AssignTo:           &e.monthEdit,
				Decimals:           0,
				MinValue:           1,
				MaxValue:           12,
				Suffix:             " 月",
				SpinButtonsVisible: true,
				MaxSize:            dcl.Size{Width: 90},
			}),
			formRowAssign(&e.daysRow, "日期号", dcl.Composite{
				Layout: dcl.HBox{MarginsZero: true, Spacing: 8},
				Children: []dcl.Widget{
					dcl.LineEdit{
						AssignTo: &e.daysEdit,
						MaxSize:  dcl.Size{Width: 120},
					},
					dcl.Label{
						Text:      "多个用逗号分隔，可填「月末」",
						TextColor: colorMuted,
					},
					dcl.HSpacer{},
				},
			}),
			formRowAssign(&e.intervalRow, "间隔", dcl.Composite{
				Layout: dcl.HBox{MarginsZero: true, Spacing: 8},
				Children: []dcl.Widget{
					dcl.NumberEdit{
						AssignTo:           &e.intervalEdit,
						Decimals:           0,
						MinValue:           1,
						MaxValue:           1440,
						Suffix:             " 分钟",
						SpinButtonsVisible: true,
						MaxSize:            dcl.Size{Width: 110},
					},
					dcl.Label{
						Text:      "从软件启动后开始计时",
						TextColor: colorMuted,
					},
					dcl.HSpacer{},
				},
			}),
			formRowAssign(&e.timeRow, "提醒时间", dcl.Composite{
				Layout: dcl.HBox{MarginsZero: true, Spacing: 6},
				Children: []dcl.Widget{
					dcl.NumberEdit{
						AssignTo:           &e.hourEdit,
						Decimals:           0,
						MinValue:           0,
						MaxValue:           23,
						SpinButtonsVisible: true,
						MaxSize:            dcl.Size{Width: 72},
					},
					dcl.Label{Text: "时"},
					dcl.NumberEdit{
						AssignTo:           &e.minuteEdit,
						Decimals:           0,
						MinValue:           0,
						MaxValue:           59,
						SpinButtonsVisible: true,
						MaxSize:            dcl.Size{Width: 72},
					},
					dcl.Label{Text: "分"},
					dcl.HSpacer{},
				},
			}),

			dcl.CheckBox{
				AssignTo: &e.enabledCheck,
				Text:     "启用这条提醒",
				Checked:  a.Enabled,
			},
			dcl.VSpacer{},
			dcl.Composite{
				Layout: dcl.HBox{MarginsZero: true, Spacing: 8},
				Children: []dcl.Widget{
					dcl.HSpacer{},
					dcl.PushButton{
						AssignTo:  &acceptBtn,
						Text:      "保存",
						MinSize:   dcl.Size{Width: 84},
						OnClicked: func() { e.accept(a) },
					},
					dcl.PushButton{
						AssignTo:  &cancelBtn,
						Text:      "取消",
						MinSize:   dcl.Size{Width: 84},
						OnClicked: func() { e.dlg.Cancel() },
					},
				},
			},
		},
	}

	if err := dialog.Create(owner); err != nil {
		errorBox(owner, "无法打开编辑窗口："+err.Error())
		return false
	}

	e.load(a)
	e.syncVisibility()

	return e.dlg.Run() == walk.DlgCmdOK
}

// formRow 生成一行「标签 + 控件」的布局。
func formRow(label string, child dcl.Widget) dcl.Widget {
	return formRowAssign(nil, label, child)
}

// formRowAssign 生成一行布局，并可把该行的容器指针赋给 assignTo，便于控制显隐。
func formRowAssign(assignTo **walk.Composite, label string, child dcl.Widget) dcl.Widget {
	return dcl.Composite{
		AssignTo: assignTo,
		Layout:   dcl.HBox{MarginsZero: true, Spacing: 8},
		Children: []dcl.Widget{
			dcl.Label{
				Text:          label,
				MinSize:       dcl.Size{Width: labelWidth},
				MaxSize:       dcl.Size{Width: labelWidth},
				TextAlignment: dcl.AlignFar,
			},
			child,
		},
	}
}

// weekdayChecks 生成星期几的复选框行。
func weekdayChecks(checks *[7]*walk.CheckBox, a *alarm.Alarm) dcl.Widget {
	children := make([]dcl.Widget, 0, 8)
	for i := 0; i < 7; i++ {
		children = append(children, dcl.CheckBox{
			AssignTo: &checks[i],
			Text:     strings.TrimPrefix(alarm.WeekdayName(i), "周"),
			MaxSize:  dcl.Size{Width: 42},
		})
	}
	children = append(children, dcl.HSpacer{})
	return dcl.Composite{
		Layout:   dcl.HBox{MarginsZero: true, Spacing: 2},
		Children: children,
	}
}

// repeatNames 返回下拉框使用的循环方式名称列表。
func repeatNames() []string {
	opts := alarm.RepeatOptions()
	names := make([]string, 0, len(opts))
	for _, opt := range opts {
		names = append(names, opt.Name)
	}
	return names
}

// repeatIndex 返回循环方式在下拉框中的位置。
func repeatIndex(r alarm.Repeat) int {
	for i, opt := range alarm.RepeatOptions() {
		if opt.Value == r {
			return i
		}
	}
	return 0
}

// currentRepeat 返回下拉框当前选中的循环方式。
func (e *editor) currentRepeat() alarm.Repeat {
	opts := alarm.RepeatOptions()
	idx := e.repeatCombo.CurrentIndex()
	if idx < 0 || idx >= len(opts) {
		return alarm.RepeatDaily
	}
	return opts[idx].Value
}

// load 把闹钟数据填入各个控件。
func (e *editor) load(a *alarm.Alarm) {
	date, err := time.Parse(alarm.DateLayout, a.Date)
	if err != nil {
		date = time.Now()
	}
	e.dateEdit.SetDate(date)

	for i := 0; i < 7; i++ {
		e.weekChecks[i].SetChecked(containsInt(a.Weekdays, i))
	}

	month := a.Month
	if month < 1 || month > 12 {
		month = int(time.Now().Month())
	}
	e.monthEdit.SetValue(float64(month))

	days := a.Days
	if len(days) == 0 {
		days = []int{time.Now().Day()}
	}
	e.daysEdit.SetText(alarm.FormatDays(days))

	interval := a.Interval
	if interval < 1 {
		interval = 30
	}
	e.intervalEdit.SetValue(float64(interval))

	e.hourEdit.SetValue(float64(a.Hour))
	e.minuteEdit.SetValue(float64(a.Minute))
}

// syncVisibility 根据当前循环方式显示或隐藏对应的输入行。
func (e *editor) syncVisibility() {
	r := e.currentRepeat()
	e.dateRow.SetVisible(r == alarm.RepeatOnce)
	e.weekRow.SetVisible(r == alarm.RepeatWeekly)
	e.monthRow.SetVisible(r == alarm.RepeatYearly)
	e.daysRow.SetVisible(r == alarm.RepeatMonthly || r == alarm.RepeatYearly)
	e.intervalRow.SetVisible(r == alarm.RepeatInterval)
	e.timeRow.SetVisible(r != alarm.RepeatInterval)
}

// accept 校验输入并写回闹钟数据，校验不通过时保持对话框打开。
func (e *editor) accept(a *alarm.Alarm) {
	draft := a.Clone()
	draft.Title = strings.TrimSpace(e.titleEdit.Text())
	draft.Message = strings.TrimSpace(e.messageEdit.Text())
	draft.Enabled = e.enabledCheck.Checked()
	draft.Repeat = e.currentRepeat()
	draft.Hour = int(e.hourEdit.Value())
	draft.Minute = int(e.minuteEdit.Value())
	draft.Month = int(e.monthEdit.Value())
	draft.Interval = int(e.intervalEdit.Value())
	draft.Date = e.dateEdit.Date().Format(alarm.DateLayout)

	weekdays := make([]int, 0, 7)
	for i := 0; i < 7; i++ {
		if e.weekChecks[i].Checked() {
			weekdays = append(weekdays, i)
		}
	}
	draft.Weekdays = weekdays

	if draft.Repeat == alarm.RepeatMonthly || draft.Repeat == alarm.RepeatYearly {
		days, err := alarm.ParseDays(e.daysEdit.Text())
		if err != nil {
			errorBox(e.dlg, err.Error())
			return
		}
		draft.Days = days
	}

	if err := draft.Validate(); err != nil {
		errorBox(e.dlg, err.Error())
		return
	}

	// 一次性提醒若已经过了时间点，直接提示用户改期，避免保存后不会触发。
	if draft.Repeat == alarm.RepeatOnce && draft.Enabled {
		if _, ok := draft.Next(time.Now()); !ok {
			errorBox(e.dlg, "该时间点已经过去了，请重新选择日期或时间。")
			return
		}
	}

	*a = *draft
	e.dlg.Accept()
}

// containsInt 判断切片中是否包含指定元素。
func containsInt(list []int, v int) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
