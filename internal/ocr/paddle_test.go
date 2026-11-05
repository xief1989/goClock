package ocr

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"
)

func TestCTCDecode(t *testing.T) {
	chars := []string{"", "a", "b", "c"} // 0 是空白符，4 是空格
	// 时间步的最大类别依次为：a a 空白 a b 空格 空格 c
	seq := []int{1, 1, 0, 1, 2, 4, 4, 3}
	probs := make([]float32, len(seq)*5)
	for t, c := range seq {
		probs[t*5+c] = 0.9
	}
	text, score, at := ctcDecode(probs, len(seq), 5, chars)
	if text != "aab c" {
		t.Errorf("解码结果不对：%q", text)
	}
	if score < 0.89 || score > 0.91 {
		t.Errorf("平均置信度不对：%v", score)
	}
	// 第一个 a 占第 0～1 步取中间，模型输出的空格占第 5～6 步。
	if want := []float64{0.5, 3, 4, 5.5, 7}; !equalFloats(at, want) {
		t.Errorf("逐字时间步 %v，应为 %v", at, want)
	}
}

func equalFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestGlyphRects(t *testing.T) {
	box := image.Rect(100, 10, 200, 40)
	got := glyphRects(box, []float64{110, 130, 150})
	want := []image.Rectangle{image.Rect(100, 10, 120, 40), image.Rect(120, 10, 140, 40), image.Rect(140, 10, 160, 40)}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 个字 %v，应为 %v", i, got[i], want[i])
		}
	}
	// 外推超出文字框时截到框内；只有一个字时占满整个框。
	if g := glyphRects(box, []float64{102, 198}); g[0].Min.X != 100 || g[1].Max.X != 200 {
		t.Errorf("应截在文字框内：%v", g)
	}
	if g := glyphRects(box, []float64{150}); g[0] != box {
		t.Errorf("单个字应占满文字框：%v", g)
	}
}

func TestCTCDecodeInsertsWordSpaces(t *testing.T) {
	chars := []string{"", "n", "v", "m", "i", "装", "，"}
	// n v m，隔 4 个空白时间步后是 i：应补出词间空格；「装」与「，」之间隔得再远也不补。
	seq := []int{1, 0, 2, 3, 0, 0, 0, 0, 4, 0, 0, 0, 0, 0, 5, 0, 0, 0, 0, 0, 6}
	probs := make([]float32, len(seq)*7)
	for t, c := range seq {
		probs[t*7+c] = 0.9
	}
	text, _, at := ctcDecode(probs, len(seq), 7, chars)
	if text != "nvm i 装，" {
		t.Errorf("补空格的结果不对：%q", text)
	}
	// 补的空格落在前后两个字的时间步中间。
	if want := []float64{0, 2, 3, 5.5, 8, 11, 14, 20}; !equalFloats(at, want) {
		t.Errorf("逐字时间步 %v，应为 %v", at, want)
	}
}

func TestSnapGlyphs(t *testing.T) {
	// 三个「字」占 [10,16) [20,26) [30,36)，推算出的框整体偏左；吸附后边界应落在字缝里，首尾贴着笔画。
	// 浅底深字与深底浅字各测一遍。
	for _, dark := range []bool{false, true} {
		bg, fg := color.RGBA{255, 255, 255, 255}, color.RGBA{30, 30, 30, 255}
		if dark {
			bg, fg = fg, color.RGBA{230, 230, 230, 255}
		}
		img := image.NewRGBA(image.Rect(0, 0, 50, 20))
		draw.Draw(img, img.Rect, image.NewUniform(bg), image.Point{}, draw.Src)
		for _, x := range []int{10, 20, 30} {
			draw.Draw(img, image.Rect(x, 5, x+6, 15), image.NewUniform(fg), image.Point{}, draw.Src)
		}
		box := image.Rect(5, 0, 40, 20)
		glyphs := []image.Rectangle{image.Rect(5, 0, 15, 20), image.Rect(15, 0, 25, 20), image.Rect(25, 0, 40, 20)}
		snapGlyphs(img, box, glyphs)
		want := []image.Rectangle{image.Rect(10, 0, 16, 20), image.Rect(16, 0, 26, 20), image.Rect(26, 0, 36, 20)}
		for i := range want {
			if glyphs[i] != want[i] {
				t.Errorf("深底=%v 第 %d 个字 %v，应为 %v", dark, i, glyphs[i], want[i])
			}
		}
	}
}

func TestDBBoxesFindsSeparatedLines(t *testing.T) {
	// 40×20 的概率图上有两块高概率区域（模拟两行字），中间隔着低概率的空隙。
	w, h := 40, 20
	prob := make([]float32, w*h)
	for y := 2; y < 6; y++ {
		for x := 3; x < 30; x++ {
			prob[y*w+x] = 0.9
		}
	}
	for y := 12; y < 16; y++ {
		for x := 5; x < 20; x++ {
			prob[y*w+x] = 0.8
		}
	}
	// 一个平均概率不够的小噪点，应被丢弃。
	prob[18*w+35], prob[18*w+36], prob[18*w+37] = 0.35, 0.35, 0.35
	boxes := dbBoxes(prob, w, h)
	if len(boxes) != 2 {
		t.Fatalf("应找到 2 个文字框：%v", boxes)
	}
	if !boxes[0].Overlaps(image.Rect(3, 2, 30, 6)) || boxes[0].Dy() <= 4 {
		t.Errorf("第一个框应覆盖第一行并向外扩开：%v", boxes[0])
	}
}

// accuracyTruth 是 1.png（GoClock 推送说明那段对话截图）里的原文，用来量错字率。
const accuracyTruth = `已推送：87bde08 推到了 GitHub 的 xief1989/goClock，main 从 e380ca8 前进到 87bde08。之后重新拉
取远端核对过，本地和 origin/main 完全一致，没有待推送的提交。
开发环境管理还在暂停状态。哪天要继续，先要你拍板三件事：
1. Node 是用 GoClock 自己下载放进 nvm 目录，还是继续调用 nvm install。
2. uv 安装的 Python 默认不允许全局 pip install，这个限制要不要保留。
3. 实测时能不能改你的 PATH、弹 UAC。
这些我已经记在记忆里了，下次恢复时会直接接上。`

// charErrorRate 计算字符错误率：去掉空白、全角 ASCII 换成半角后，编辑距离除以原文长度。
func charErrorRate(got, want string) float64 {
	norm := func(s string) []rune {
		var out []rune
		for _, r := range s {
			if unicode.IsSpace(r) {
				continue
			}
			if r >= 0xFF01 && r <= 0xFF5E {
				r -= 0xFEE0
			}
			out = append(out, r)
		}
		return out
	}
	a, b := norm(got), norm(want)
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return float64(prev[len(b)]) / float64(len(b))
}

// loadPNG 读一张 PNG 并转成 RGBA，可裁掉顶部若干行。
func loadPNG(t *testing.T, path string, cropTop int) *image.RGBA {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	b := src.Bounds()
	img := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()-cropTop))
	draw.Draw(img, img.Rect, src, image.Pt(b.Min.X, b.Min.Y+cropTop), draw.Src)
	return img
}

// TestPaddleAccuracy 是 PaddleOCR 的验收测试：对话截图的错字率必须不超过 3%，
// 并与 Windows 自带 OCR 对比。需要准备好运行库、模型与两张样图，默认跳过：
//
//	$env:GOCLOCK_PADDLE_DIR="D:\paddle"; $env:GOCLOCK_ACC_CHAT="D:\1.png"; $env:GOCLOCK_ACC_LONG="D:\long.png"
//	go test ./internal/ocr -run TestPaddleAccuracy -v
func TestPaddleAccuracy(t *testing.T) {
	dir := os.Getenv("GOCLOCK_PADDLE_DIR")
	chat := os.Getenv("GOCLOCK_ACC_CHAT")
	if dir == "" || chat == "" {
		t.Skip("未设置 GOCLOCK_PADDLE_DIR / GOCLOCK_ACC_CHAT，跳过")
	}
	start := time.Now()
	p, err := OpenPaddle(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	t.Logf("加载用时 %v（ONNX Runtime %s）", time.Since(start), p.Version())

	// 顶部 22 行是被截掉一半的标题，不计入。
	img := loadPNG(t, chat, 22)
	start = time.Now()
	res, err := p.Recognize(img)
	if err != nil {
		t.Fatal(err)
	}
	paddleCER := charErrorRate(res.Text, accuracyTruth)
	t.Logf("PaddleOCR：错字率 %.1f%%，%d 行，用时 %v\n%s", paddleCER*100, len(res.Lines), time.Since(start), res.Text)
	checkGlyphs(t, "PaddleOCR", img, res)

	if win, err := Recognize(img); err == nil {
		t.Logf("Windows OCR：错字率 %.1f%%", charErrorRate(win.Text, accuracyTruth)*100)
		checkGlyphs(t, "Windows OCR", img, win)
	}
	if paddleCER > 0.03 {
		t.Errorf("错字率 %.1f%% 超过 3%% 的验收线", paddleCER*100)
	}

	if long := os.Getenv("GOCLOCK_ACC_LONG"); long != "" {
		img := loadPNG(t, long, 0)
		start := time.Now()
		res, err := p.Recognize(img)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("长截图：%d 行，用时 %v；完整句 %d/120，「序号」%d/120，「缺失」%d/120，节标题 %d/40",
			len(res.Lines), time.Since(start),
			strings.Count(res.Text, "这是一段用来测试长截图拼接的文字"), strings.Count(res.Text, "序号"),
			strings.Count(res.Text, "缺失"), strings.Count(res.Text, "滚动拼接的内容"))
	}
}

// checkGlyphs 检查每行的逐字位置与文字一一对应、都落在行框内。
// 设置了 GOCLOCK_GLYPH_OUT（目录）时，把每个字的框交替用两种颜色画到图上存成 PNG，供肉眼核对位置。
func checkGlyphs(t *testing.T, name string, img *image.RGBA, res *Result) {
	t.Helper()
	for i, l := range res.Lines {
		if n := len([]rune(l.Text)); len(l.Glyphs) != n {
			t.Errorf("%s 第 %d 行逐字位置 %d 个，文字 %d 个字：%q", name, i+1, len(l.Glyphs), n, l.Text)
			continue
		}
		for j, g := range l.Glyphs {
			if !g.In(l.Rect) && !g.Empty() {
				t.Errorf("%s 第 %d 行第 %d 个字 %v 超出行框 %v", name, i+1, j+1, g, l.Rect)
			}
		}
	}
	dir := os.Getenv("GOCLOCK_GLYPH_OUT")
	if dir == "" {
		return
	}
	out := image.NewRGBA(img.Rect)
	draw.Draw(out, out.Rect, img, img.Rect.Min, draw.Src)
	colors := [2]color.RGBA{{255, 0, 0, 255}, {0, 120, 255, 255}}
	for _, l := range res.Lines {
		for j, g := range l.Glyphs {
			c := colors[j%2]
			for x := g.Min.X; x < g.Max.X; x++ {
				out.Set(x, g.Max.Y-1, c)
			}
			for y := g.Min.Y; y < g.Max.Y; y++ {
				out.Set(g.Min.X, y, c)
			}
		}
	}
	path := filepath.Join(dir, strings.ReplaceAll(name, " ", "")+"-glyphs.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, out); err != nil {
		t.Fatal(err)
	}
	t.Logf("逐字框已画到 %s", path)
}

// TestPaddleReopenAfterClose 确认引擎关闭后能重新加载（闲置释放后再次识别走的就是这条路），
// 关闭后的引擎报错而不是崩溃，进度回调走到底。条件同 TestPaddleAccuracy。
func TestPaddleReopenAfterClose(t *testing.T) {
	dir := os.Getenv("GOCLOCK_PADDLE_DIR")
	chat := os.Getenv("GOCLOCK_ACC_CHAT")
	if dir == "" || chat == "" {
		t.Skip("未设置 GOCLOCK_PADDLE_DIR / GOCLOCK_ACC_CHAT，跳过")
	}
	img := loadPNG(t, chat, 22)
	for round := 1; round <= 2; round++ {
		p, err := OpenPaddle(dir)
		if err != nil {
			t.Fatalf("第 %d 次加载失败：%v", round, err)
		}
		var done, total int
		res, err := p.RecognizeProgress(img, func(d, n int) { done, total = d, n })
		if err != nil {
			t.Fatalf("第 %d 次识别失败：%v", round, err)
		}
		if total == 0 || done != total {
			t.Errorf("第 %d 次进度没有走到底：%d/%d", round, done, total)
		}
		if cer := charErrorRate(res.Text, accuracyTruth); cer > 0.03 {
			t.Errorf("第 %d 次错字率 %.1f%%", round, cer*100)
		}
		p.Close()
		if _, err := p.Recognize(img); err == nil {
			t.Error("关闭后识别应当报错")
		}
	}
}
