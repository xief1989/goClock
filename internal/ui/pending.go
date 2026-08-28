package ui

import "time"

// nagInterval 是提醒没有被手动关闭时，再次弹出的间隔。
const nagInterval = 5 * time.Minute

// nagCheckInterval 是轮询待确认提醒的间隔。
// 取 20 秒即可，重弹间隔本身是分钟级的，不需要更高的精度。
const nagCheckInterval = 20 * time.Second

// pendingReminder 是一条已经弹出、但还没有被用户手动关闭的提醒。
type pendingReminder struct {
	// AlarmID 是来源闹钟的标识。
	AlarmID string
	// Title 是提醒标题。
	Title string
	// Message 是提醒正文。
	Message string
	// FiredAt 是首次触发的时刻，重弹时仍然展示这个时间。
	FiredAt time.Time
	// NextAt 是下次重新弹出的时刻，弹窗正显示时无意义。
	NextAt time.Time
	// Attempt 是已经弹出的次数，从 1 开始。
	Attempt int
	// Showing 表示弹窗此刻正显示在屏幕上。
	Showing bool
}

// reminderQueue 保存所有等待用户确认的提醒。
//
// 提醒弹出后如果只是倒计时结束自动隐藏，并不算处理完毕：
// 队列会把它排到 nagInterval 之后再弹一次，直到用户手动关闭为止。
// 队列不做持久化，程序退出后未确认的提醒即被丢弃。
type reminderQueue struct {
	items map[string]*pendingReminder
}

// newReminderQueue 创建一个空队列。
func newReminderQueue() *reminderQueue {
	return &reminderQueue{items: make(map[string]*pendingReminder)}
}

// Fire 记录一条提醒开始展示，返回对应的待确认条目。
// 同一条闹钟再次到点时会重新开始计次，而不是堆积成两条。
func (q *reminderQueue) Fire(id, title, message string, firedAt time.Time) *pendingReminder {
	item := &pendingReminder{
		AlarmID: id,
		Title:   title,
		Message: message,
		FiredAt: firedAt,
		Attempt: 1,
		Showing: true,
	}
	q.items[id] = item
	return item
}

// Ack 记录用户手动关闭了提醒，把它移出队列。
func (q *reminderQueue) Ack(id string) {
	delete(q.items, id)
}

// Hide 记录弹窗自动隐藏（未经确认），安排在 next 时刻重新弹出。
// 队列中没有该提醒时什么也不做。
func (q *reminderQueue) Hide(id string, next time.Time) {
	item, ok := q.items[id]
	if !ok {
		return
	}
	item.Showing = false
	item.NextAt = next
}

// TakeDue 取出到 now 为止需要重新弹出的提醒，并把它们标记为正在展示。
func (q *reminderQueue) TakeDue(now time.Time) []*pendingReminder {
	var due []*pendingReminder
	for _, item := range q.items {
		if item.Showing || item.NextAt.IsZero() || now.Before(item.NextAt) {
			continue
		}
		item.Showing = true
		item.Attempt++
		due = append(due, item)
	}
	return due
}

// Retain 只保留 alive 中列出的闹钟，其余一律丢弃。
// 闹钟被删除、停用，或关掉了「必须手动关闭」时，用它清理残留的待确认提醒。
func (q *reminderQueue) Retain(alive map[string]bool) {
	for id := range q.items {
		if !alive[id] {
			delete(q.items, id)
		}
	}
}

// Len 返回等待确认的提醒条数。
func (q *reminderQueue) Len() int {
	return len(q.items)
}

// Clear 清空队列。
func (q *reminderQueue) Clear() {
	q.items = make(map[string]*pendingReminder)
}
