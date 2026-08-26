// Package scheduler 负责按闹钟的循环规则定时触发提醒。
//
// 调度器内部保存闹钟的副本并在后台协程中推进时间，界面线程与它之间
// 只通过 SetAlarms 与事件回调交换数据，因此不会与界面共享可变状态。
package scheduler

import (
	"sync"
	"time"

	"goclock/internal/alarm"
)

// tickInterval 是扫描到点闹钟的间隔。取 5 秒既能让提醒基本落在整分钟上，
// 也能在系统休眠唤醒后较快恢复检查。
const tickInterval = 5 * time.Second

// missTolerance 是允许的迟到时长。电脑休眠或关机期间错过的提醒，
// 只要不超过该时长仍会补弹一次，超过则静默跳过，避免开机后集中弹窗。
const missTolerance = 30 * time.Minute

// Event 描述调度器产生的一次变化，由界面线程负责落到数据与界面上。
type Event struct {
	// ID 是发生变化的闹钟标识。
	ID string
	// Title 是提醒标题。
	Title string
	// Message 是提醒正文。
	Message string
	// At 是本次触发的时刻，仅在 Fired 为真时有意义。
	At time.Time
	// Fired 表示需要弹出提醒。
	Fired bool
	// Expired 表示该闹钟已不会再触发，应当自动停用。
	Expired bool
}

// Scheduler 持有闹钟副本并在到点时回调。除 Start 外的方法均可在任意协程调用。
type Scheduler struct {
	mu     sync.Mutex
	alarms []*alarm.Alarm
	next   map[string]time.Time

	// onEvent 在后台协程中被调用，实现方需要自行切回界面线程。
	onEvent func(ev Event)

	stop chan struct{}
	once sync.Once
}

// New 创建调度器。onEvent 会在后台协程中被调用。
func New(onEvent func(ev Event)) *Scheduler {
	return &Scheduler{
		next:    make(map[string]time.Time),
		onEvent: onEvent,
		stop:    make(chan struct{}),
	}
}

// SetAlarms 用给定列表替换调度内容并重新计算全部下次触发时间。
// 传入的闹钟会被复制一份，调用方之后修改原对象不会影响调度。
func (s *Scheduler) SetAlarms(list []*alarm.Alarm) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.alarms = make([]*alarm.Alarm, 0, len(list))
	s.next = make(map[string]time.Time, len(list))
	now := time.Now()
	for _, a := range list {
		if a == nil {
			continue
		}
		c := a.Clone()
		s.alarms = append(s.alarms, c)
		if !c.Enabled {
			continue
		}
		if t, ok := c.Next(now); ok {
			s.next[c.ID] = t
		}
	}
}

// NextAt 返回指定闹钟的下次触发时间，闹钟已停用或不会再触发时返回 false。
func (s *Scheduler) NextAt(id string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.next[id]
	return t, ok
}

// Start 启动后台扫描协程。
func (s *Scheduler) Start() {
	go func() {
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				s.tick(time.Now())
			}
		}
	}()
}

// Stop 停止后台扫描，可重复调用。
func (s *Scheduler) Stop() {
	s.once.Do(func() {
		close(s.stop)
	})
}

// tick 检查所有闹钟，收集到点事件并推进下次触发时间。
func (s *Scheduler) tick(now time.Time) {
	s.mu.Lock()

	var events []Event
	for _, a := range s.alarms {
		if !a.Enabled {
			continue
		}
		due, ok := s.next[a.ID]
		if !ok || now.Before(due) {
			continue
		}

		ev := Event{ID: a.ID, Title: a.Title, Message: a.Message, At: now}
		if now.Sub(due) <= missTolerance {
			a.LastFire = now
			ev.Fired = true
		}

		if t, ok := a.Next(now); ok {
			s.next[a.ID] = t
		} else {
			delete(s.next, a.ID)
			a.Enabled = false
			ev.Expired = true
		}
		events = append(events, ev)
	}
	s.mu.Unlock()

	if s.onEvent == nil {
		return
	}
	for _, ev := range events {
		s.onEvent(ev)
	}
}
