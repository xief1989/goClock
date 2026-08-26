package ui

import (
	"time"

	"github.com/lxn/walk"

	"goclock/internal/alarm"
)

// alarmModel 是闹钟列表的表格数据源，同时支持通过复选框直接启停闹钟。
type alarmModel struct {
	walk.TableModelBase

	items []*alarm.Alarm
	// nextAt 查询指定闹钟的下次触发时间。
	nextAt func(id string) (time.Time, bool)
	// onToggle 在用户点击复选框时被调用。
	onToggle func(a *alarm.Alarm, enabled bool)
}

// RowCount 返回表格行数。
func (m *alarmModel) RowCount() int {
	return len(m.items)
}

// Value 返回单元格要展示的内容。
func (m *alarmModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.items) {
		return ""
	}
	a := m.items[row]
	switch col {
	case 0:
		return a.Title
	case 1:
		return a.RepeatText()
	case 2:
		return a.TimeText()
	case 3:
		if !a.Enabled {
			return "已停用"
		}
		t, ok := m.nextAt(a.ID)
		return humanTime(t, ok)
	case 4:
		if a.LastFire.IsZero() {
			return "—"
		}
		return humanTime(a.LastFire, true)
	case 5:
		return a.Message
	default:
		return ""
	}
}

// Checked 返回该行闹钟是否启用。
func (m *alarmModel) Checked(row int) bool {
	if row < 0 || row >= len(m.items) {
		return false
	}
	return m.items[row].Enabled
}

// SetChecked 在用户勾选复选框时启停闹钟。
func (m *alarmModel) SetChecked(row int, checked bool) error {
	if row < 0 || row >= len(m.items) {
		return nil
	}
	if m.onToggle != nil {
		m.onToggle(m.items[row], checked)
	}
	return nil
}

// setItems 替换数据并通知表格整体刷新。
func (m *alarmModel) setItems(items []*alarm.Alarm) {
	m.items = items
	m.PublishRowsReset()
}

// refresh 在数据行数不变、仅内容变化时刷新表格。
func (m *alarmModel) refresh() {
	if len(m.items) == 0 {
		m.PublishRowsReset()
		return
	}
	m.PublishRowsChanged(0, len(m.items)-1)
}

// humanTime 把时间格式化为便于阅读的相对描述。
func humanTime(t time.Time, ok bool) string {
	if !ok || t.IsZero() {
		return "—"
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	diff := int(day.Sub(today).Hours() / 24)

	switch {
	case diff == 0:
		return "今天 " + t.Format("15:04")
	case diff == 1:
		return "明天 " + t.Format("15:04")
	case diff == -1:
		return "昨天 " + t.Format("15:04")
	case diff > 1 && diff < 7:
		return t.Format("01-02") + " " + alarm.WeekdayName(int(t.Weekday())) + " " + t.Format("15:04")
	default:
		return t.Format("2006-01-02 15:04")
	}
}
