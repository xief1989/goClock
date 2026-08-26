package ui

import "testing"

func TestTickCountdownCountsDown(t *testing.T) {
	remain, paused, closing := tickCountdown(15, false)
	if remain != 14 || paused || closing {
		t.Fatalf("正常计时：remain=%d paused=%v closing=%v", remain, paused, closing)
	}
}

func TestTickCountdownPausesOnHover(t *testing.T) {
	remain, paused, closing := tickCountdown(9, true)
	if remain != 9 {
		t.Errorf("鼠标停留时剩余秒数不应变化：got=%d want=9", remain)
	}
	if !paused {
		t.Error("鼠标停留时应标记为暂停")
	}
	if closing {
		t.Error("鼠标停留时不应关闭弹窗")
	}
}

func TestTickCountdownResumesFromPause(t *testing.T) {
	// 悬停若干秒后移开，应当从暂停处继续，而不是重新回到 15 秒。
	remain := 15
	for i := 0; i < 3; i++ {
		remain, _, _ = tickCountdown(remain, false)
	}
	if remain != 12 {
		t.Fatalf("前置计时有误：got=%d want=12", remain)
	}

	for i := 0; i < 5; i++ {
		remain, _, _ = tickCountdown(remain, true)
	}
	if remain != 12 {
		t.Fatalf("悬停期间剩余秒数被改动：got=%d want=12", remain)
	}

	remain, paused, closing := tickCountdown(remain, false)
	if remain != 11 {
		t.Errorf("移开后应从 12 继续减到 11：got=%d", remain)
	}
	if paused {
		t.Error("移开后不应仍处于暂停状态")
	}
	if closing {
		t.Error("此时还不该关闭")
	}
}

func TestTickCountdownClosesAtZero(t *testing.T) {
	remain, _, closing := tickCountdown(1, false)
	if remain != 0 || !closing {
		t.Fatalf("归零时应关闭：remain=%d closing=%v", remain, closing)
	}
}

func TestTickCountdownTotalLifetime(t *testing.T) {
	// 悬停 5 秒的弹窗，总存活时长应当是 15 + 5 秒。
	const hoverAt, hoverSeconds = 4, 5
	remain := toastAutoCloseSeconds
	ticks := 0
	for {
		hovered := ticks >= hoverAt && ticks < hoverAt+hoverSeconds
		var closing bool
		remain, _, closing = tickCountdown(remain, hovered)
		ticks++
		if closing {
			break
		}
		if ticks > 100 {
			t.Fatal("倒计时没有结束")
		}
	}
	if want := toastAutoCloseSeconds + hoverSeconds; ticks != want {
		t.Errorf("总存活时长：got=%d 秒 want=%d 秒", ticks, want)
	}
}
