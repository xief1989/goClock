package record

import (
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"goclock/internal/capture"
)

func TestEvenRectAndBitrate(t *testing.T) {
	if r := EvenRect(image.Rect(10, 20, 111, 221)); r.Dx() != 100 || r.Dy() != 200 {
		t.Errorf("宽高应裁成偶数：%v", r)
	}
	if b := Bitrate(100, 100, 30); b != minBitrate {
		t.Errorf("小画面码率应取下限：%d", b)
	}
	if b := Bitrate(3840, 2160, 60); b != maxBitrate {
		t.Errorf("大画面码率应取上限：%d", b)
	}
}

// TestRecordScreenRegion 真实录制屏幕左上角一小块区域 1.5 秒，中途暂停 0.5 秒，
// 检查视频文件结构完整、帧数与时长大致对得上。会抓取真实屏幕，默认跳过：
//
//	$env:GOCLOCK_RECORD_TEST="1"; go test ./internal/record -run TestRecordScreenRegion -v
func TestRecordScreenRegion(t *testing.T) {
	if os.Getenv("GOCLOCK_RECORD_TEST") == "" {
		t.Skip("未设置 GOCLOCK_RECORD_TEST，跳过真实录屏")
	}
	vs := capture.VirtualScreen()
	path := filepath.Join(t.TempDir(), "screen.mp4")
	rec, err := Start(Options{Rect: image.Rect(vs.Min.X, vs.Min.Y, vs.Min.X+401, vs.Min.Y+301), FPS: 20, Cursor: true, Path: path})
	if err != nil {
		t.Fatalf("开始录制失败：%v", err)
	}
	if rec.Rect().Dx() != 400 || rec.Rect().Dy() != 300 {
		t.Errorf("录制区域应裁成偶数：%v", rec.Rect())
	}
	time.Sleep(700 * time.Millisecond)
	rec.SetPaused(true)
	time.Sleep(500 * time.Millisecond)
	rec.SetPaused(false)
	time.Sleep(800 * time.Millisecond)
	elapsed := rec.Elapsed()
	if err := rec.Stop(); err != nil {
		t.Fatalf("结束录制失败：%v", err)
	}
	if err := rec.Stop(); err != nil {
		t.Errorf("重复调用 Stop 应返回同样的结果：%v", err)
	}

	if elapsed < 1300*time.Millisecond || elapsed > 1900*time.Millisecond {
		t.Errorf("录下的时长应约 1.5 秒（不含暂停）：%v", elapsed)
	}
	frames := rec.Frames()
	if frames < 20 || frames > 35 {
		t.Errorf("20 帧/秒录 1.5 秒，帧数应在 30 左右：%d", frames)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	boxes := topLevelBoxes(data)
	if !boxes["moov"] || !boxes["mdat"] {
		t.Errorf("视频文件结构不完整：%v", boxes)
	}
	t.Logf("时长 %v，%d 帧，%d 字节", elapsed, frames, len(data))
}
