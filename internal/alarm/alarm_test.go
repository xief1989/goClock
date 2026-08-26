package alarm

import (
	"testing"
	"time"
)

// at 构造一个本地时区的时间，简化用例书写。
func at(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, time.Local)
}

func TestNextDaily(t *testing.T) {
	a := &Alarm{Title: "喝水", Repeat: RepeatDaily, Hour: 9, Minute: 30}

	// 当天尚未到点，应落在今天。
	got, ok := a.Next(at(2026, 8, 26, 8, 0))
	if !ok || !got.Equal(at(2026, 8, 26, 9, 30)) {
		t.Fatalf("当天未到点：got=%v ok=%v", got, ok)
	}

	// 当天已过点，应顺延到明天。
	got, ok = a.Next(at(2026, 8, 26, 9, 30))
	if !ok || !got.Equal(at(2026, 8, 27, 9, 30)) {
		t.Fatalf("当天已到点：got=%v ok=%v", got, ok)
	}
}

func TestNextWeekly(t *testing.T) {
	// 2026-08-26 是周三，选周一与周五。
	a := &Alarm{Title: "周会", Repeat: RepeatWeekly, Hour: 10, Minute: 0, Weekdays: []int{1, 5}}

	got, ok := a.Next(at(2026, 8, 26, 12, 0))
	if !ok || !got.Equal(at(2026, 8, 28, 10, 0)) {
		t.Fatalf("应落在周五：got=%v ok=%v", got, ok)
	}

	got, _ = a.Next(at(2026, 8, 28, 12, 0))
	if !got.Equal(at(2026, 8, 31, 10, 0)) {
		t.Fatalf("应落在下周一：got=%v", got)
	}
}

func TestNextWorkday(t *testing.T) {
	// 2026-08-29 是周六，下一个工作日为 08-31 周一。
	a := &Alarm{Title: "打卡", Repeat: RepeatWorkday, Hour: 8, Minute: 30}

	got, ok := a.Next(at(2026, 8, 29, 0, 0))
	if !ok || !got.Equal(at(2026, 8, 31, 8, 30)) {
		t.Fatalf("周末应顺延到周一：got=%v ok=%v", got, ok)
	}
}

func TestNextMonthly(t *testing.T) {
	a := &Alarm{Title: "还款", Repeat: RepeatMonthly, Hour: 20, Minute: 0, Days: []int{1, 15}}

	got, _ := a.Next(at(2026, 8, 26, 12, 0))
	if !got.Equal(at(2026, 9, 1, 20, 0)) {
		t.Fatalf("应落在下月 1 日：got=%v", got)
	}

	// 31 日在小月应自动跳过：2026-09 只有 30 天。
	b := &Alarm{Title: "月结", Repeat: RepeatMonthly, Hour: 18, Minute: 0, Days: []int{31}}
	got, _ = b.Next(at(2026, 9, 1, 0, 0))
	if !got.Equal(at(2026, 10, 31, 18, 0)) {
		t.Fatalf("小月应跳过 31 日：got=%v", got)
	}
}

func TestNextMonthlyLastDay(t *testing.T) {
	a := &Alarm{Title: "月末小结", Repeat: RepeatMonthly, Hour: 17, Minute: 0, Days: []int{LastDayOfMonth}}

	got, _ := a.Next(at(2026, 2, 1, 0, 0))
	if !got.Equal(at(2026, 2, 28, 17, 0)) {
		t.Fatalf("平年 2 月末应为 28 日：got=%v", got)
	}

	got, _ = a.Next(at(2028, 2, 1, 0, 0))
	if !got.Equal(at(2028, 2, 29, 17, 0)) {
		t.Fatalf("闰年 2 月末应为 29 日：got=%v", got)
	}
}

func TestNextYearlyLeapDay(t *testing.T) {
	// 每年 2 月 29 日只在闰年存在，扫描窗口必须能跨过 4 年。
	a := &Alarm{Title: "闰日", Repeat: RepeatYearly, Hour: 12, Minute: 0, Month: 2, Days: []int{29}}

	got, ok := a.Next(at(2026, 3, 1, 0, 0))
	if !ok || !got.Equal(at(2028, 2, 29, 12, 0)) {
		t.Fatalf("应落在 2028-02-29：got=%v ok=%v", got, ok)
	}
}

func TestNextOnce(t *testing.T) {
	a := &Alarm{Title: "面试", Repeat: RepeatOnce, Hour: 14, Minute: 0, Date: "2026-08-27"}

	got, ok := a.Next(at(2026, 8, 26, 23, 0))
	if !ok || !got.Equal(at(2026, 8, 27, 14, 0)) {
		t.Fatalf("一次性闹钟：got=%v ok=%v", got, ok)
	}

	// 过期后不应再触发。
	if _, ok := a.Next(at(2026, 8, 27, 14, 0)); ok {
		t.Fatal("过期的一次性闹钟不应再触发")
	}
}

func TestNextInterval(t *testing.T) {
	a := &Alarm{Title: "久坐提醒", Repeat: RepeatInterval, Interval: 45}

	// 从未触发过时，以当前时间为起点。
	now := at(2026, 8, 26, 10, 0)
	got, ok := a.Next(now)
	if !ok || !got.Equal(at(2026, 8, 26, 10, 45)) {
		t.Fatalf("首次间隔：got=%v ok=%v", got, ok)
	}

	// 已触发过时，从上次触发时间累加，且必须晚于 now。
	a.LastFire = at(2026, 8, 26, 9, 0)
	got, _ = a.Next(at(2026, 8, 26, 11, 0))
	if !got.Equal(at(2026, 8, 26, 11, 15)) {
		t.Fatalf("间隔累加：got=%v", got)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		alarm   *Alarm
		wantErr bool
	}{
		{"标题为空", &Alarm{Repeat: RepeatDaily}, true},
		{"小时越界", &Alarm{Title: "x", Repeat: RepeatDaily, Hour: 24}, true},
		{"每周未选星期", &Alarm{Title: "x", Repeat: RepeatWeekly}, true},
		{"每月未填日期", &Alarm{Title: "x", Repeat: RepeatMonthly}, true},
		{"间隔为零", &Alarm{Title: "x", Repeat: RepeatInterval}, true},
		{"一次性日期非法", &Alarm{Title: "x", Repeat: RepeatOnce, Date: "2026-13-01"}, true},
		{"合法的每天", &Alarm{Title: "x", Repeat: RepeatDaily, Hour: 9}, false},
	}
	for _, c := range cases {
		if err := c.alarm.Validate(); (err != nil) != c.wantErr {
			t.Errorf("%s：err=%v，期望出错=%v", c.name, err, c.wantErr)
		}
	}
}

func TestParseDays(t *testing.T) {
	got, err := ParseDays("15、1, 月末 ;15")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	want := []int{1, 15, LastDayOfMonth}
	if len(got) != len(want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got=%v want=%v", got, want)
		}
	}

	if _, err := ParseDays("32"); err == nil {
		t.Error("32 日应当被拒绝")
	}
	if _, err := ParseDays("abc"); err == nil {
		t.Error("非数字应当被拒绝")
	}
}

func TestRepeatText(t *testing.T) {
	cases := map[string]*Alarm{
		"每周 一、五":      {Repeat: RepeatWeekly, Weekdays: []int{5, 1}},
		"每月 1、月末 日":   {Repeat: RepeatMonthly, Days: []int{1, LastDayOfMonth}},
		"每年 8 月 26 日": {Repeat: RepeatYearly, Month: 8, Days: []int{26}},
		"每隔 30 分钟":    {Repeat: RepeatInterval, Interval: 30},
	}
	for want, a := range cases {
		if got := a.RepeatText(); got != want {
			t.Errorf("got=%q want=%q", got, want)
		}
	}
}
