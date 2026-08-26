package ui

import (
	"fmt"
	"time"

	"github.com/lxn/walk"
	dcl "github.com/lxn/walk/declarative"
	"github.com/lxn/win"

	"goclock/internal/alarm"
	"goclock/internal/autostart"
	"goclock/internal/scheduler"
	"goclock/internal/storage"
)

// App 把托盘、管理窗口、提醒弹窗与调度器组装在一起。
type App struct {
	store  *storage.Store
	sched  *scheduler.Scheduler
	alarms []*alarm.Alarm

	// firstRun 表示本次是首次启动（尚无配置文件），此时直接打开管理窗口。
	firstRun bool

	mw     *walk.MainWindow
	tv     *walk.TableView
	model  *alarmModel
	ni     *walk.NotifyIcon
	toasts *toastManager

	autoCheck  *walk.CheckBox
	autoAction *walk.Action

	// quitting 用于区分「用户关闭窗口」与「真正退出程序」。
	quitting bool
}

// NewApp 创建应用实例并加载已保存的闹钟。
func NewApp() (*App, error) {
	store, err := storage.New()
	if err != nil {
		return nil, err
	}
	firstRun := store.IsFirstRun()
	list, err := store.Load()
	if err != nil {
		return nil, err
	}

	a := &App{
		store:    store,
		alarms:   list,
		toasts:   &toastManager{},
		firstRun: firstRun,
	}
	a.model = &alarmModel{
		items:    list,
		nextAt:   a.nextAt,
		onToggle: a.toggleAlarm,
	}
	return a, nil
}

// Run 构建界面并进入消息循环，窗口退出后返回。
func (a *App) Run() error {
	if err := a.buildWindow(); err != nil {
		return err
	}
	if err := a.buildTray(); err != nil {
		return err
	}

	a.sched = scheduler.New(a.onSchedulerEvent)
	a.sched.SetAlarms(a.alarms)
	a.sched.Start()
	defer a.sched.Stop()

	// 首次启动时打开管理窗口，让用户知道程序装在哪；
	// 之后（包括开机自启）都只驻留托盘，不打扰用户。
	if a.firstRun {
		a.save()
		a.showManager()
	} else {
		a.mw.SetVisible(false)
	}

	// 每分钟刷新一次列表，让「下次提醒」列保持最新。
	go a.refreshLoop()

	a.mw.Run()
	return nil
}

// Activate 让管理窗口显示到前台，可在任意协程调用。
func (a *App) Activate() {
	if a.mw == nil {
		return
	}
	safeSynchronize(a.mw, a.showManager)
}

// buildWindow 创建闹钟管理窗口，窗口创建后保持隐藏。
func (a *App) buildWindow() error {
	var btnAdd, btnEdit, btnDelete, btnTest *walk.PushButton

	err := (dcl.MainWindow{
		AssignTo: &a.mw,
		Title:    appName,
		Icon:     iconProperty(),
		Font:     dcl.Font{Family: uiFontFamily, PointSize: 9},
		MinSize:  dcl.Size{Width: 720, Height: 380},
		Size:     dcl.Size{Width: 900, Height: 480},
		Layout:   dcl.VBox{Margins: dcl.Margins{Left: 10, Top: 10, Right: 10, Bottom: 8}, Spacing: 8},
		Children: []dcl.Widget{
			dcl.Composite{
				Layout: dcl.HBox{MarginsZero: true, Spacing: 6},
				Children: []dcl.Widget{
					dcl.PushButton{
						AssignTo:  &btnAdd,
						Text:      "新增",
						MinSize:   dcl.Size{Width: 78},
						OnClicked: a.addAlarm,
					},
					dcl.PushButton{
						AssignTo:  &btnEdit,
						Text:      "编辑",
						MinSize:   dcl.Size{Width: 78},
						OnClicked: a.editSelected,
					},
					dcl.PushButton{
						AssignTo:  &btnDelete,
						Text:      "删除",
						MinSize:   dcl.Size{Width: 78},
						OnClicked: a.deleteSelected,
					},
					dcl.PushButton{
						AssignTo:  &btnTest,
						Text:      "预览弹窗",
						MinSize:   dcl.Size{Width: 88},
						OnClicked: a.previewSelected,
					},
					dcl.HSpacer{},
					dcl.CheckBox{
						AssignTo:         &a.autoCheck,
						Text:             "开机自动启动",
						Checked:          autostart.Enabled(),
						OnCheckedChanged: a.autostartChanged,
					},
				},
			},
			dcl.TableView{
				AssignTo:            &a.tv,
				Model:               a.model,
				CheckBoxes:          true,
				AlternatingRowBG:    true,
				MultiSelection:      false,
				LastColumnStretched: true,
				OnItemActivated:     a.editSelected,
				Columns: []dcl.TableViewColumn{
					{Title: "提醒标题", Width: 160},
					{Title: "循环方式", Width: 170},
					{Title: "时间", Width: 70, Alignment: dcl.AlignCenter},
					{Title: "下次提醒", Width: 150},
					{Title: "上次提醒", Width: 150},
					{Title: "提醒内容"},
				},
			},
			dcl.Composite{
				Layout: dcl.HBox{MarginsZero: true, Spacing: 8},
				Children: []dcl.Widget{
					dcl.Label{
						Text:      "勾选左侧方框可快速启停；关闭本窗口后程序继续在托盘运行。",
						TextColor: colorMuted,
					},
					dcl.HSpacer{},
					dcl.PushButton{
						Text:      "隐藏到托盘",
						MinSize:   dcl.Size{Width: 96},
						OnClicked: func() { a.mw.SetVisible(false) },
					},
				},
			},
		},
	}).Create()
	if err != nil {
		return fmt.Errorf("创建管理窗口失败：%w", err)
	}

	// 弹窗的倒计时协程只向主窗口投递任务，因此这里把主窗口交给弹窗管理器。
	a.toasts.host = a.mw

	// 点击窗口的关闭按钮时只隐藏窗口，程序继续驻留托盘。
	a.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if !a.quitting {
			*canceled = true
			a.mw.SetVisible(false)
		}
	})

	return nil
}

// buildTray 创建托盘图标与右键菜单。
func (a *App) buildTray() error {
	ni, err := walk.NewNotifyIcon(a.mw)
	if err != nil {
		return fmt.Errorf("创建托盘图标失败：%w", err)
	}
	a.ni = ni

	if ic := appIcon(); ic != nil {
		ni.SetIcon(ic)
	}
	ni.SetToolTip(appName)

	// 左键单击托盘图标直接打开管理窗口。
	ni.MouseUp().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			a.showManager()
		}
	})

	openAction := walk.NewAction()
	openAction.SetText("打开闹钟管理")
	openAction.Triggered().Attach(a.showManager)
	if err := ni.ContextMenu().Actions().Add(openAction); err != nil {
		return err
	}

	addAction := walk.NewAction()
	addAction.SetText("新增提醒")
	addAction.Triggered().Attach(a.addAlarm)
	if err := ni.ContextMenu().Actions().Add(addAction); err != nil {
		return err
	}

	if err := ni.ContextMenu().Actions().Add(walk.NewSeparatorAction()); err != nil {
		return err
	}

	a.autoAction = walk.NewAction()
	a.autoAction.SetText("开机自动启动")
	a.autoAction.SetCheckable(true)
	a.autoAction.SetChecked(autostart.Enabled())
	a.autoAction.Triggered().Attach(func() {
		a.setAutostart(a.autoAction.Checked())
	})
	if err := ni.ContextMenu().Actions().Add(a.autoAction); err != nil {
		return err
	}

	if err := ni.ContextMenu().Actions().Add(walk.NewSeparatorAction()); err != nil {
		return err
	}

	quitAction := walk.NewAction()
	quitAction.SetText("退出")
	quitAction.Triggered().Attach(a.quit)
	if err := ni.ContextMenu().Actions().Add(quitAction); err != nil {
		return err
	}

	return ni.SetVisible(true)
}

// showManager 显示管理窗口并置于最前。
func (a *App) showManager() {
	a.model.refresh()
	a.mw.SetVisible(true)
	hwnd := a.mw.Handle()
	if win.IsIconic(hwnd) {
		win.ShowWindow(hwnd, win.SW_RESTORE)
	}
	win.SetForegroundWindow(hwnd)
}

// quit 退出程序。
func (a *App) quit() {
	a.quitting = true
	if a.sched != nil {
		a.sched.Stop()
	}
	a.toasts.CloseAll()
	if a.ni != nil {
		a.ni.Dispose()
	}
	a.mw.Close()
}

// refreshLoop 定时刷新列表中的相对时间显示。
func (a *App) refreshLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		if a.quitting {
			return
		}
		safeSynchronize(a.mw, func() {
			if a.mw.Visible() {
				a.model.refresh()
			}
		})
	}
}

// nextAt 查询闹钟的下次触发时间。
func (a *App) nextAt(id string) (time.Time, bool) {
	if a.sched == nil {
		return time.Time{}, false
	}
	return a.sched.NextAt(id)
}

// selected 返回列表中当前选中的闹钟。
func (a *App) selected() *alarm.Alarm {
	idx := a.tv.CurrentIndex()
	if idx < 0 || idx >= len(a.alarms) {
		return nil
	}
	return a.alarms[idx]
}

// addAlarm 新增一条闹钟。
func (a *App) addAlarm() {
	a.showManager()
	item := alarm.New()
	if !RunAlarmEditor(a.mw, item, "新增提醒") {
		return
	}
	a.alarms = append(a.alarms, item)
	a.applyChanges()
	a.tv.SetCurrentIndex(len(a.alarms) - 1)
}

// editSelected 编辑当前选中的闹钟。
func (a *App) editSelected() {
	item := a.selected()
	if item == nil {
		errorBox(a.mw, "请先在列表中选择一条提醒。")
		return
	}
	draft := item.Clone()
	if !RunAlarmEditor(a.mw, draft, "编辑提醒") {
		return
	}
	*item = *draft
	a.applyChanges()
}

// deleteSelected 删除当前选中的闹钟。
func (a *App) deleteSelected() {
	item := a.selected()
	if item == nil {
		errorBox(a.mw, "请先在列表中选择一条提醒。")
		return
	}
	if !confirmBox(a.mw, "确定要删除「"+item.Title+"」吗？") {
		return
	}
	idx := a.tv.CurrentIndex()
	a.alarms = append(a.alarms[:idx], a.alarms[idx+1:]...)
	a.applyChanges()
}

// previewSelected 立即弹出一次选中闹钟的提醒，用于确认效果。
func (a *App) previewSelected() {
	item := a.selected()
	if item == nil {
		errorBox(a.mw, "请先在列表中选择一条提醒。")
		return
	}
	a.toasts.Show(item.Title, item.Message, time.Now())
}

// toggleAlarm 启停闹钟，由列表中的复选框触发。
func (a *App) toggleAlarm(item *alarm.Alarm, enabled bool) {
	if item.Enabled == enabled {
		return
	}
	item.Enabled = enabled
	if enabled {
		// 已经过期的一次性提醒无法再启用，直接告知用户。
		if _, ok := item.Next(time.Now()); !ok {
			item.Enabled = false
			errorBox(a.mw, "这条提醒的时间点已经过去了，请修改后再启用。")
		}
	}
	a.applyChanges()
}

// applyChanges 保存闹钟、刷新调度与界面，任何增删改之后都应调用。
func (a *App) applyChanges() {
	a.model.setItems(a.alarms)
	a.sched.SetAlarms(a.alarms)
	a.save()
	a.model.refresh()
}

// save 持久化闹钟列表。
func (a *App) save() {
	if err := a.store.Save(a.alarms); err != nil {
		errorBox(a.mw, err.Error())
	}
}

// onSchedulerEvent 处理调度器事件，切回界面线程后更新数据与界面。
func (a *App) onSchedulerEvent(ev scheduler.Event) {
	safeSynchronize(a.mw, func() {
		for _, item := range a.alarms {
			if item.ID != ev.ID {
				continue
			}
			if ev.Fired {
				item.LastFire = ev.At
			}
			if ev.Expired {
				item.Enabled = false
			}
			break
		}
		if ev.Fired {
			a.toasts.Show(ev.Title, ev.Message, ev.At)
		}
		a.save()
		a.model.refresh()
	})
}

// autostartChanged 响应管理窗口中的开机自启复选框。
func (a *App) autostartChanged() {
	a.setAutostart(a.autoCheck.Checked())
}

// setAutostart 开启或关闭开机自启，并让两处入口的状态保持一致。
func (a *App) setAutostart(enable bool) {
	if err := autostart.Set(enable); err != nil {
		errorBox(a.mw, err.Error())
	}
	actual := autostart.Enabled()
	if a.autoCheck != nil && a.autoCheck.Checked() != actual {
		a.autoCheck.SetChecked(actual)
	}
	if a.autoAction != nil && a.autoAction.Checked() != actual {
		a.autoAction.SetChecked(actual)
	}
}
