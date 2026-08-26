package scheduler

import (
	"testing"
	"time"

	"goclock/internal/alarm"
)

// newTestScheduler 构造一个不启动后台协程的调度器，并直接设定内部状态，
// 以便用固定时间驱动 tick。
func newTestScheduler(a *alarm.Alarm, due time.Time) (*Scheduler, *[]Event) {
	var got []Event
	s := New(func(ev Event) { got = append(got, ev) })
	s.alarms = []*alarm.Alarm{a}
	s.next = map[string]time.Time{a.ID: due}
	return s, &got
}

func TestTickBeforeDue(t *testing.T) {
	due := time.Date(2026, 8, 26, 10, 0, 0, 0, time.Local)
	a := &alarm.Alarm{ID: "a", Title: "喝水", Enabled: true, Repeat: alarm.RepeatDaily, Hour: 10}
	s, got := newTestScheduler(a, due)

	s.tick(due.Add(-time.Second))
	if len(*got) != 0 {
		t.Fatalf("未到点不应产生事件，实际 %d 个", len(*got))
	}
}

func TestTickFires(t *testing.T) {
	due := time.Date(2026, 8, 26, 10, 0, 0, 0, time.Local)
	a := &alarm.Alarm{ID: "a", Title: "喝水", Message: "起来走走", Enabled: true, Repeat: alarm.RepeatDaily, Hour: 10}
	s, got := newTestScheduler(a, due)

	now := due.Add(2 * time.Second)
	s.tick(now)

	if len(*got) != 1 {
		t.Fatalf("应产生 1 个事件，实际 %d 个", len(*got))
	}
	ev := (*got)[0]
	if !ev.Fired || ev.Expired {
		t.Errorf("事件标记有误：Fired=%v Expired=%v", ev.Fired, ev.Expired)
	}
	if ev.Title != "喝水" || ev.Message != "起来走走" {
		t.Errorf("事件内容有误：%+v", ev)
	}

	// 下次触发时间应推进到第二天。
	next, ok := s.NextAt("a")
	if !ok || !next.Equal(due.AddDate(0, 0, 1)) {
		t.Errorf("下次触发时间有误：%v ok=%v", next, ok)
	}
}

func TestTickSkipsLongMiss(t *testing.T) {
	// 休眠或关机导致迟到过久时，不应补弹提醒，但仍要把时间推进到下一次。
	due := time.Date(2026, 8, 26, 10, 0, 0, 0, time.Local)
	a := &alarm.Alarm{ID: "a", Title: "喝水", Enabled: true, Repeat: alarm.RepeatDaily, Hour: 10}
	s, got := newTestScheduler(a, due)

	now := due.Add(missTolerance + time.Minute)
	s.tick(now)

	if len(*got) != 1 {
		t.Fatalf("应产生 1 个事件，实际 %d 个", len(*got))
	}
	if (*got)[0].Fired {
		t.Error("迟到过久时不应弹出提醒")
	}
	if !a.LastFire.IsZero() {
		t.Error("未真正提醒时不应记录触发时间")
	}
	if next, ok := s.NextAt("a"); !ok || !next.Equal(due.AddDate(0, 0, 1)) {
		t.Errorf("下次触发时间有误：%v ok=%v", next, ok)
	}
}

func TestTickExpiresOnce(t *testing.T) {
	// 一次性提醒触发后不再有下一次，应自动停用。
	due := time.Date(2026, 8, 26, 10, 0, 0, 0, time.Local)
	a := &alarm.Alarm{
		ID: "a", Title: "面试", Enabled: true,
		Repeat: alarm.RepeatOnce, Hour: 10, Date: "2026-08-26",
	}
	s, got := newTestScheduler(a, due)

	s.tick(due.Add(time.Second))

	if len(*got) != 1 {
		t.Fatalf("应产生 1 个事件，实际 %d 个", len(*got))
	}
	ev := (*got)[0]
	if !ev.Fired || !ev.Expired {
		t.Errorf("一次性提醒应同时标记 Fired 与 Expired：%+v", ev)
	}
	if _, ok := s.NextAt("a"); ok {
		t.Error("过期的提醒不应再有下次触发时间")
	}
}

func TestSetAlarmsCopies(t *testing.T) {
	// 调度器持有的是副本，界面线程之后修改原对象不应影响调度。
	a := &alarm.Alarm{ID: "a", Title: "原标题", Enabled: true, Repeat: alarm.RepeatDaily, Hour: 10}
	s := New(nil)
	s.SetAlarms([]*alarm.Alarm{a})

	a.Title = "改过的标题"
	if s.alarms[0].Title != "原标题" {
		t.Error("SetAlarms 应当复制闹钟，而不是共享同一个对象")
	}
}

func TestSetAlarmsSkipsDisabled(t *testing.T) {
	a := &alarm.Alarm{ID: "a", Title: "停用的", Enabled: false, Repeat: alarm.RepeatDaily, Hour: 10}
	s := New(nil)
	s.SetAlarms([]*alarm.Alarm{a})

	if _, ok := s.NextAt("a"); ok {
		t.Error("停用的闹钟不应参与调度")
	}
}
