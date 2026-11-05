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
		{text: "使", rect: ocrRect{0, 0, 14, 16}},
		{text: "用", rect: ocrRect{15, 0, 14, 16}},
		{text: "Go", rect: ocrRect{36, 0, 20, 16}},
		{text: "语", rect: ocrRect{63, 0, 14, 16}},
		{text: "言", rect: ocrRect{78, 0, 14, 16}},
		{text: "Hello", rect: ocrRect{100, 0, 40, 16}},
		{text: "world", rect: ocrRect{145, 0, 40, 16}},
		{text: "。", rect: ocrRect{186, 0, 14, 16}},
	}
	text, r, glyphs := joinWords(words)
	if text != "使用 Go 语言 Hello world。" {
		t.Errorf("拼接结果不对：%q", text)
	}
	if r.X != 0 || r.Width != 200 {
		t.Errorf("整行范围不对：%+v", r)
	}
	runes := []rune(text)
	if len(glyphs) != len(runes) {
		t.Fatalf("逐字位置 %d 个，文字 %d 个字", len(glyphs), len(runes))
	}
	// 「用」与「Go」之间补的空格占两者之间的空隙；「Go」按词框等分成两半。
	if g := glyphs[2]; runes[2] != ' ' || g.Min.X != 29 || g.Max.X != 36 {
		t.Errorf("补的空格位置不对：%q %v", runes[2], g)
	}
	if glyphs[3] != image.Rect(36, 0, 46, 16) || glyphs[4] != image.Rect(46, 0, 56, 16) {
		t.Errorf("多字词应按字数等分：%v %v", glyphs[3], glyphs[4])
	}
}

func TestMergeRowsKeepsGlyphs(t *testing.T) {
	// 同一行被识别器拆成两段，各自带着逐字位置；合并后位置要跟着文字走。
	left := Line{Text: "提交", Rect: image.Rect(0, 0, 40, 20),
		Glyphs: []image.Rectangle{image.Rect(0, 0, 20, 20), image.Rect(20, 0, 40, 20)}}
	right := Line{Text: "OK", Rect: image.Rect(60, 2, 80, 20),
		Glyphs: []image.Rectangle{image.Rect(60, 2, 70, 20), image.Rect(70, 2, 80, 20)}}
	below := Line{Text: "下", Rect: image.Rect(0, 30, 20, 50), Glyphs: []image.Rectangle{image.Rect(0, 30, 20, 50)}}
	rows := mergeRows([]Line{right, below, left})
	if len(rows) != 2 || rows[0].Text != "提交 OK" || rows[1].Text != "下" {
		t.Fatalf("合并结果不对：%+v", rows)
	}
	want := []image.Rectangle{left.Glyphs[0], left.Glyphs[1], image.Rect(40, 0, 60, 20), right.Glyphs[0], right.Glyphs[1]}
	if len(rows[0].Glyphs) != len(want) {
		t.Fatalf("逐字位置个数不对：%v", rows[0].Glyphs)
	}
	for i := range want {
		if rows[0].Glyphs[i] != want[i] {
			t.Errorf("第 %d 个字的位置 %v，应为 %v", i, rows[0].Glyphs[i], want[i])
		}
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
