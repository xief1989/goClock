package ui

import (
	"testing"
	"time"
)

// at 构造一个本地时区的时间，简化用例书写。
func at(hh, mm int) time.Time {
	return time.Date(2026, 8, 26, hh, mm, 0, 0, time.Local)
}

func TestQueueAckRemoves(t *testing.T) {
	q := newReminderQueue()
	q.Fire("a", "吃药", "饭后一片", at(9, 0))
	if q.Len() != 1 {
		t.Fatalf("弹出后应有 1 条待确认：got=%d", q.Len())
	}

	q.Ack("a")
	if q.Len() != 0 {
		t.Errorf("用户点掉后应清空：got=%d", q.Len())
	}
	if due := q.TakeDue(at(23, 0)); len(due) != 0 {
		t.Error("已确认的提醒不该再弹出")
	}
}

func TestQueueHideRepeats(t *testing.T) {
	q := newReminderQueue()
	q.Fire("a", "吃药", "饭后一片", at(9, 0))

	// 弹窗自动隐藏，安排 5 分钟后重来。
	q.Hide("a", at(9, 5))

	if due := q.TakeDue(at(9, 4)); len(due) != 0 {
		t.Error("还没到点就不该重弹")
	}

	due := q.TakeDue(at(9, 5))
	if len(due) != 1 {
		t.Fatalf("到点应重弹 1 条：got=%d", len(due))
	}
	if due[0].Attempt != 2 {
		t.Errorf("重弹应记为第 2 次：got=%d", due[0].Attempt)
	}
	if !due[0].FiredAt.Equal(at(9, 0)) {
		t.Errorf("重弹应保留首次触发时间：got=%v", due[0].FiredAt)
	}

	// 已经在展示的提醒不该被重复取出。
	if again := q.TakeDue(at(9, 6)); len(again) != 0 {
		t.Error("正在展示的提醒不该再次取出")
	}
}

func TestQueueRepeatsUntilAcked(t *testing.T) {
	// 一直不点，就应该一直重弹；点掉之后立刻停止。
	q := newReminderQueue()
	q.Fire("a", "吃药", "", at(9, 0))

	now := at(9, 0)
	for round := 2; round <= 5; round++ {
		q.Hide("a", now.Add(nagInterval))
		now = now.Add(nagInterval)
		due := q.TakeDue(now)
		if len(due) != 1 {
			t.Fatalf("第 %d 轮应重弹：got=%d", round, len(due))
		}
		if due[0].Attempt != round {
			t.Errorf("第 %d 轮次数记错：got=%d", round, due[0].Attempt)
		}
	}

	q.Ack("a")
	q.Hide("a", now.Add(nagInterval))
	if due := q.TakeDue(now.Add(time.Hour)); len(due) != 0 {
		t.Error("确认之后不该再弹")
	}
}

func TestQueueRefireResetsAttempt(t *testing.T) {
	// 同一条闹钟再次到点时重新计次，而不是堆积成两条。
	q := newReminderQueue()
	q.Fire("a", "吃药", "", at(9, 0))
	q.Hide("a", at(9, 5))
	q.TakeDue(at(9, 5))

	item := q.Fire("a", "吃药", "", at(12, 0))
	if q.Len() != 1 {
		t.Errorf("同一条闹钟不该堆积：got=%d", q.Len())
	}
	if item.Attempt != 1 {
		t.Errorf("重新触发应从第 1 次开始：got=%d", item.Attempt)
	}
	if !item.FiredAt.Equal(at(12, 0)) {
		t.Errorf("应更新为新的触发时间：got=%v", item.FiredAt)
	}
}

func TestQueueHideUnknownIsNoop(t *testing.T) {
	// 闹钟被删除后，残留弹窗关闭时不应把它重新塞回队列。
	q := newReminderQueue()
	q.Hide("ghost", at(9, 5))
	if q.Len() != 0 {
		t.Errorf("不存在的提醒不该被加进来：got=%d", q.Len())
	}
}

func TestQueueRetain(t *testing.T) {
	q := newReminderQueue()
	q.Fire("keep", "保留", "", at(9, 0))
	q.Fire("drop", "删掉", "", at(9, 0))

	q.Retain(map[string]bool{"keep": true})

	if q.Len() != 1 {
		t.Fatalf("应只剩 1 条：got=%d", q.Len())
	}
	if _, ok := q.items["keep"]; !ok {
		t.Error("该保留的被删掉了")
	}
}
