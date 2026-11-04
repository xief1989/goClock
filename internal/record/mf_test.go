package record

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestEncoderWritesPlayableMP4 用合成画面（渐变底上一个移动的方块）编码 2 秒视频，
// 再检查文件的顶层结构：MP4 必须有 ftyp、moov、mdat 三个盒子，缺 moov 说明收尾没做完、播放器打不开。
func TestEncoderWritesPlayableMP4(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	const w, h, fps = 320, 240, 30
	path := filepath.Join(t.TempDir(), "test.mp4")
	enc, err := newEncoder(path, w, h, fps, 1_000_000)
	if err != nil {
		t.Fatalf("创建编码器失败：%v", err)
	}
	frame := make([]byte, w*h*4)
	const dur = 10_000_000 / fps
	for i := 0; i < fps*2; i++ {
		fillFrame(frame, w, h, i)
		if err := enc.writeFrame(frame, int64(i)*dur, dur); err != nil {
			enc.close()
			t.Fatalf("第 %d 帧写入失败：%v", i, err)
		}
	}
	if err := enc.finish(); err != nil {
		t.Fatalf("收尾失败：%v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	boxes := topLevelBoxes(data)
	for _, want := range []string{"ftyp", "moov", "mdat"} {
		if !boxes[want] {
			t.Errorf("MP4 缺少 %s 盒子，现有：%v", want, boxes)
		}
	}
	if len(data) < 5000 {
		t.Errorf("文件太小，可能没写进画面：%d 字节", len(data))
	}
	t.Logf("写出 %d 字节", len(data))

	// 需要用播放器肉眼确认画面时，设置 GOCLOCK_MP4_OUT 把视频另存一份。
	if out := os.Getenv("GOCLOCK_MP4_OUT"); out != "" {
		if err := os.WriteFile(out, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// fillFrame 生成第 i 帧：横向渐变底色上，一个白色方块从左往右移动。
func fillFrame(frame []byte, w, h, i int) {
	bx := (i * 4) % (w - 40)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			p := (y*w + x) * 4
			if x >= bx && x < bx+40 && y >= 100 && y < 140 {
				frame[p], frame[p+1], frame[p+2] = 255, 255, 255
			} else {
				frame[p], frame[p+1], frame[p+2] = byte(x*255/w), byte(y*255/h), 80
			}
			frame[p+3] = 255
		}
	}
}

// topLevelBoxes 列出 MP4 文件的顶层盒子类型。
func topLevelBoxes(data []byte) map[string]bool {
	boxes := map[string]bool{}
	for off := 0; off+8 <= len(data); {
		size := int(binary.BigEndian.Uint32(data[off:]))
		typ := string(data[off+4 : off+8])
		boxes[typ] = true
		if size == 1 && off+16 <= len(data) {
			size = int(binary.BigEndian.Uint64(data[off+8:]))
		}
		if size < 8 {
			break
		}
		off += size
	}
	return boxes
}
