// Package alarm 定义闹钟的数据模型，以及根据循环规则推算下次触发时间的逻辑。
package alarm

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Repeat 表示闹钟的循环方式。
type Repeat string

const (
	// RepeatOnce 仅提醒一次，需要指定具体日期。
	RepeatOnce Repeat = "once"
	// RepeatDaily 每天提醒。
	RepeatDaily Repeat = "daily"
	// RepeatWorkday 每个工作日（周一至周五）提醒。
	RepeatWorkday Repeat = "workday"
	// RepeatWeekly 每周的指定星期几提醒，可多选。
	RepeatWeekly Repeat = "weekly"
	// RepeatMonthly 每月的指定日期号提醒，可多选。
	RepeatMonthly Repeat = "monthly"
	// RepeatYearly 每年的指定月份与日期号提醒。
	RepeatYearly Repeat = "yearly"
	// RepeatInterval 每隔固定分钟数提醒一次。
	RepeatInterval Repeat = "interval"
)

// LastDayOfMonth 是 Days 字段的特殊取值，表示「当月最后一天」。
const LastDayOfMonth = -1

// DateLayout 是 Date 字段使用的日期格式。
const DateLayout = "2006-01-02"

// scanDays 是逐日推算下次触发时间时最多向后扫描的天数。
// 需要覆盖「每年 2 月 29 日」这类最长可达 8 年的间隔，故取 9 年。
const scanDays = 366 * 9

// weekdayNames 是星期几的中文名称，下标与 time.Weekday 一致。
var weekdayNames = [7]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

// Alarm 是一条闹钟配置。
type Alarm struct {
	// ID 是闹钟的唯一标识。
	ID string `json:"id"`
	// Title 是提醒弹窗的标题。
	Title string `json:"title"`
	// Message 是提醒弹窗的正文内容，可以为空。
	Message string `json:"message"`
	// Enabled 表示该闹钟是否启用。
	Enabled bool `json:"enabled"`
	// RequireAck 表示提醒必须由用户手动关闭。
	// 开启后，弹窗自动隐藏不算处理完毕，会每隔一段时间重新弹出，直到用户点掉为止。
	RequireAck bool `json:"requireAck,omitempty"`
	// Repeat 是循环方式。
	Repeat Repeat `json:"repeat"`
	// Hour 是触发时刻的小时（0-23），RepeatInterval 时忽略。
	Hour int `json:"hour"`
	// Minute 是触发时刻的分钟（0-59），RepeatInterval 时忽略。
	Minute int `json:"minute"`
	// Weekdays 是 RepeatWeekly 时选中的星期几，0 表示周日、6 表示周六。
	Weekdays []int `json:"weekdays,omitempty"`
	// Days 是 RepeatMonthly、RepeatYearly 时选中的日期号（1-31），
	// 取值 LastDayOfMonth 表示当月最后一天。
	Days []int `json:"days,omitempty"`
	// Month 是 RepeatYearly 时选中的月份（1-12）。
	Month int `json:"month,omitempty"`
	// Date 是 RepeatOnce 时的触发日期，格式见 DateLayout。
	Date string `json:"date,omitempty"`
	// Interval 是 RepeatInterval 时的间隔分钟数。
	Interval int `json:"interval,omitempty"`
	// LastFire 是上次实际触发的时间，用于 RepeatInterval 的推算与界面展示。
	LastFire time.Time `json:"lastFire,omitempty"`
}

// RepeatOption 描述一种循环方式，供界面下拉框使用。
type RepeatOption struct {
	// Value 是循环方式的枚举值。
	Value Repeat
	// Name 是显示给用户的中文名称。
	Name string
}

// RepeatOptions 返回全部循环方式，顺序即界面下拉框的展示顺序。
func RepeatOptions() []RepeatOption {
	return []RepeatOption{
		{RepeatOnce, "仅一次"},
		{RepeatDaily, "每天"},
		{RepeatWorkday, "每个工作日"},
		{RepeatWeekly, "每周"},
		{RepeatMonthly, "每月"},
		{RepeatYearly, "每年"},
		{RepeatInterval, "间隔提醒"},
	}
}

// RepeatName 返回循环方式的中文名称。
func RepeatName(r Repeat) string {
	for _, opt := range RepeatOptions() {
		if opt.Value == r {
			return opt.Name
		}
	}
	return string(r)
}

// WeekdayName 返回星期几的中文名称，wd 取值 0-6。
func WeekdayName(wd int) string {
	if wd < 0 || wd > 6 {
		return ""
	}
	return weekdayNames[wd]
}

// New 返回一条填好默认值的新闹钟。
func New() *Alarm {
	now := time.Now()
	return &Alarm{
		ID:         newID(now),
		Title:      "新提醒",
		Enabled:    true,
		Repeat:     RepeatDaily,
		Hour:       now.Hour(),
		Minute:     now.Minute(),
		Weekdays:   []int{int(now.Weekday())},
		Days:       []int{now.Day()},
		Month:      int(now.Month()),
		Date:       now.Format(DateLayout),
		Interval:   30,
		RequireAck: true,
	}
}

// newID 基于时间戳生成一个足够唯一的闹钟标识。
func newID(now time.Time) string {
	return fmt.Sprintf("a%d", now.UnixNano())
}

// Clone 返回闹钟的深拷贝，便于在编辑对话框中取消修改。
func (a *Alarm) Clone() *Alarm {
	if a == nil {
		return nil
	}
	c := *a
	c.Weekdays = append([]int(nil), a.Weekdays...)
	c.Days = append([]int(nil), a.Days...)
	return &c
}

// Validate 校验闹钟配置是否完整合法，返回的错误信息可直接展示给用户。
func (a *Alarm) Validate() error {
	if a == nil {
		return errors.New("闹钟不存在")
	}
	if strings.TrimSpace(a.Title) == "" {
		return errors.New("请填写提醒标题")
	}
	if a.Repeat != RepeatInterval {
		if a.Hour < 0 || a.Hour > 23 {
			return errors.New("小时需要在 0 到 23 之间")
		}
		if a.Minute < 0 || a.Minute > 59 {
			return errors.New("分钟需要在 0 到 59 之间")
		}
	}
	switch a.Repeat {
	case RepeatOnce:
		if _, err := time.Parse(DateLayout, a.Date); err != nil {
			return errors.New("请选择有效的提醒日期")
		}
	case RepeatWeekly:
		if len(a.Weekdays) == 0 {
			return errors.New("请至少选择一个星期几")
		}
		for _, wd := range a.Weekdays {
			if wd < 0 || wd > 6 {
				return errors.New("星期几取值不合法")
			}
		}
	case RepeatMonthly:
		if err := validateDays(a.Days); err != nil {
			return err
		}
	case RepeatYearly:
		if a.Month < 1 || a.Month > 12 {
			return errors.New("请选择 1 到 12 之间的月份")
		}
		if err := validateDays(a.Days); err != nil {
			return err
		}
	case RepeatInterval:
		if a.Interval < 1 {
			return errors.New("间隔分钟数需要大于 0")
		}
	case RepeatDaily, RepeatWorkday:
		// 这两种方式不需要额外参数。
	default:
		return fmt.Errorf("未知的循环方式：%s", a.Repeat)
	}
	return nil
}

// validateDays 校验日期号列表。
func validateDays(days []int) error {
	if len(days) == 0 {
		return errors.New("请至少填写一个日期号")
	}
	for _, d := range days {
		if d == LastDayOfMonth {
			continue
		}
		if d < 1 || d > 31 {
			return errors.New("日期号需要在 1 到 31 之间，或填写「月末」")
		}
	}
	return nil
}

// Next 返回晚于 after 的下一次触发时间。
// 第二个返回值为 false 表示该闹钟不会再触发（例如已过期的一次性闹钟）。
func (a *Alarm) Next(after time.Time) (time.Time, bool) {
	if a.Validate() != nil {
		return time.Time{}, false
	}

	switch a.Repeat {
	case RepeatInterval:
		step := time.Duration(a.Interval) * time.Minute
		if a.LastFire.IsZero() {
			return after.Add(step), true
		}
		next := a.LastFire.In(after.Location()).Add(step)
		for !next.After(after) {
			next = next.Add(step)
		}
		return next, true

	case RepeatOnce:
		d, err := time.ParseInLocation(DateLayout, a.Date, after.Location())
		if err != nil {
			return time.Time{}, false
		}
		t := time.Date(d.Year(), d.Month(), d.Day(), a.Hour, a.Minute, 0, 0, after.Location())
		if t.After(after) {
			return t, true
		}
		return time.Time{}, false
	}

	// 其余循环方式统一按「从今天起逐日向后找第一个匹配的日子」推算。
	start := time.Date(after.Year(), after.Month(), after.Day(), a.Hour, a.Minute, 0, 0, after.Location())
	for i := 0; i < scanDays; i++ {
		t := start.AddDate(0, 0, i)
		if !t.After(after) {
			continue
		}
		if a.matches(t) {
			return t, true
		}
	}
	return time.Time{}, false
}

// matches 判断 t 所在的日子是否满足循环规则（不比较时分）。
func (a *Alarm) matches(t time.Time) bool {
	switch a.Repeat {
	case RepeatDaily:
		return true
	case RepeatWorkday:
		wd := t.Weekday()
		return wd >= time.Monday && wd <= time.Friday
	case RepeatWeekly:
		return containsInt(a.Weekdays, int(t.Weekday()))
	case RepeatMonthly:
		return matchDay(a.Days, t)
	case RepeatYearly:
		return int(t.Month()) == a.Month && matchDay(a.Days, t)
	default:
		return false
	}
}

// matchDay 判断 t 的日期号是否落在 days 中，并处理「月末」这一特殊取值。
func matchDay(days []int, t time.Time) bool {
	if containsInt(days, t.Day()) {
		return true
	}
	if containsInt(days, LastDayOfMonth) {
		return t.Day() == daysInMonth(t.Year(), t.Month())
	}
	return false
}

// daysInMonth 返回指定年月的天数。
func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
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

// TimeText 返回触发时刻的展示文本。
func (a *Alarm) TimeText() string {
	if a.Repeat == RepeatInterval {
		return "—"
	}
	return fmt.Sprintf("%02d:%02d", a.Hour, a.Minute)
}

// RepeatText 返回循环规则的中文描述，用于列表展示。
func (a *Alarm) RepeatText() string {
	switch a.Repeat {
	case RepeatOnce:
		return "仅一次（" + a.Date + "）"
	case RepeatDaily:
		return "每天"
	case RepeatWorkday:
		return "每个工作日"
	case RepeatWeekly:
		return "每周 " + formatWeekdays(a.Weekdays)
	case RepeatMonthly:
		return "每月 " + FormatDays(a.Days) + " 日"
	case RepeatYearly:
		return fmt.Sprintf("每年 %d 月 %s 日", a.Month, FormatDays(a.Days))
	case RepeatInterval:
		return fmt.Sprintf("每隔 %d 分钟", a.Interval)
	default:
		return string(a.Repeat)
	}
}

// formatWeekdays 把星期几列表格式化为「一、三、五」这样的文本。
func formatWeekdays(weekdays []int) string {
	sorted := append([]int(nil), weekdays...)
	sort.Ints(sorted)
	parts := make([]string, 0, len(sorted))
	for _, wd := range sorted {
		name := WeekdayName(wd)
		if name == "" {
			continue
		}
		parts = append(parts, strings.TrimPrefix(name, "周"))
	}
	return strings.Join(parts, "、")
}

// dayOrder 返回日期号的排序权重，使「月末」始终排在最后。
func dayOrder(d int) int {
	if d == LastDayOfMonth {
		return 32
	}
	return d
}

// sortDays 就地排序日期号列表。
func sortDays(days []int) {
	sort.Slice(days, func(i, j int) bool {
		return dayOrder(days[i]) < dayOrder(days[j])
	})
}

// FormatDays 把日期号列表格式化为「1、15、月末」这样的文本。
func FormatDays(days []int) string {
	sorted := append([]int(nil), days...)
	sortDays(sorted)
	parts := make([]string, 0, len(sorted))
	for _, d := range sorted {
		if d == LastDayOfMonth {
			parts = append(parts, "月末")
			continue
		}
		parts = append(parts, strconv.Itoa(d))
	}
	return strings.Join(parts, "、")
}

// ParseDays 解析用户输入的日期号列表，支持逗号、顿号、空格分隔，
// 其中「月末」「末」「last」「-1」均表示当月最后一天。
func ParseDays(s string) ([]int, error) {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '，' || r == '、' || r == ' ' || r == ';' || r == '；'
	})
	days := make([]int, 0, len(fields))
	seen := make(map[int]bool, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		var d int
		switch strings.ToLower(f) {
		case "月末", "末", "last", "-1":
			d = LastDayOfMonth
		default:
			n, err := strconv.Atoi(f)
			if err != nil {
				return nil, fmt.Errorf("无法识别的日期号：%s", f)
			}
			d = n
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		days = append(days, d)
	}
	if err := validateDays(days); err != nil {
		return nil, err
	}
	sortDays(days)
	return days, nil
}
