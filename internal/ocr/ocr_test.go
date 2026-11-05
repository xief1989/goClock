package ocr

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"testing"
	"time"
)

func TestJoinWordsRemovesSpacesBetweenHanzi(t *testing.T) {
	// 识别器把中文拆成一个字一个词，字与字之间几乎没有空隙；英文单词之间有正常的词距。
	words := []word{
		{"使", ocrRect{0, 0, 14, 16}},
		{"用", ocrRect{15, 0, 14, 16}},
		{"Go", ocrRect{36, 0, 20, 16}},
		{"语", ocrRect{63, 0, 14, 16}},
		{"言", ocrRect{78, 0, 14, 16}},
		{"Hello", ocrRect{100, 0, 40, 16}},
		{"world", ocrRect{145, 0, 40, 16}},
		{"。", ocrRect{186, 0, 14, 16}},
	}
	text, r := joinWords(words)
	if text != "使用 Go 语言 Hello world。" {
		t.Errorf("拼接结果不对：%q", text)
	}
	if r.X != 0 || r.Width != 200 {
		t.Errorf("整行范围不对：%+v", r)
	}
}

func TestPlanTilesUpscalesSmallImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 300, 40))
	tiles := planTiles(img, 10000)
	if len(tiles) != 1 || tiles[0].scale < windowsUpscale || tiles[0].img.Rect.Dy() < minSide {
		t.Fatalf("小图应当放大到短边至少 %d：%+v", minSide, tiles[0].img.Rect)
	}
}

func TestPlanTilesSplitsTallImageAtBlankRows(t *testing.T) {
	// 一张 900×2500 的「长截图」，识别上限 1000：每 50 行有一行字（深色），其余是白底。
	img := image.NewRGBA(image.Rect(0, 0, 900, 2500))
	for y := 0; y < 2500; y++ {
		c := color.RGBA{255, 255, 255, 255}
		if y%50 < 20 {
			c = color.RGBA{30, 30, 30, 255}
			if y%50 >= 10 {
				c = color.RGBA{255, 255, 255, 255}
			}
		}
		for x := 0; x < 900; x++ {
			if y%50 < 10 && x%7 == 0 {
				img.SetRGBA(x, y, color.RGBA{30, 30, 30, 255})
			} else {
				img.SetRGBA(x, y, c)
			}
		}
	}
	tiles := planTiles(img, 1000)
	total := 0
	for i, tl := range tiles {
		if tl.img.Rect.Dx() > 1000 || tl.img.Rect.Dy() > 1000 {
			t.Errorf("第 %d 块超过边长上限：%v", i, tl.img.Rect)
		}
		if tl.offsetY != total {
			t.Errorf("第 %d 块起点不连续：%d != %d", i, tl.offsetY, total)
		}
		if tl.offsetY > 0 && !rowIsPlain(img, tl.offsetY-1) {
			t.Errorf("第 %d 块的切口应落在空白行：%d", i, tl.offsetY)
		}
		total += int(float64(tl.img.Rect.Dy())/tl.scale + 0.5)
	}
	if total != 2500 {
		t.Errorf("各块加起来应覆盖整张图：%d", total)
	}
}

// TestRecognizeFile 用系统 OCR 识别一张 PNG 并打印结果，默认跳过：
//
//	$env:GOCLOCK_OCR_IMAGE="D:\shot.png"; go test ./internal/ocr -run TestRecognizeFile -v
func TestRecognizeFile(t *testing.T) {
	path := os.Getenv("GOCLOCK_OCR_IMAGE")
	if path == "" {
		t.Skip("未设置 GOCLOCK_OCR_IMAGE，跳过实机识别")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(src.Bounds())
	draw.Draw(img, img.Rect, src, src.Bounds().Min, draw.Src)

	start := time.Now()
	res, err := Recognize(img)
	if err != nil {
		t.Fatalf("识别失败：%v", err)
	}
	t.Logf("语言 %s，%d 行，用时 %v", res.Language, len(res.Lines), time.Since(start))
	t.Logf("\n%s", res.Text)
}

func TestRecognizeCallsDoNotOverlap(t *testing.T) {
	// 并发调用 Recognize 必须排队执行：重叠时 WinRT 引擎会死锁，见 recognizeMu。
	if os.Getenv("GOCLOCK_OCR_IMAGE") == "" {
		t.Skip("未设置 GOCLOCK_OCR_IMAGE，跳过实机识别")
	}
	img := image.NewRGBA(image.Rect(0, 0, 400, 120))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	done := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() {
			_, err := Recognize(img)
			done <- err
		}()
	}
	timeout := time.After(60 * time.Second)
	for i := 0; i < 4; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("识别失败：%v", err)
			}
		case <-timeout:
			t.Fatal("并发识别 60 秒没有全部完成，可能死锁了")
		}
	}
}

func TestToImageRectUndoesScale(t *testing.T) {
	r := toImageRect(ocrRect{X: 20, Y: 10, Width: 40, Height: 20}, tile{offsetY: 100, scale: 2})
	if r != image.Rect(10, 105, 30, 115) {
		t.Errorf("坐标换算不对：%v", r)
	}
}
