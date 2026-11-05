package ocr

import (
	"image"
	"image/draw"
	"image/png"
	"os"
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
	text, score := ctcDecode(probs, len(seq), 5, chars)
	if text != "aab c" {
		t.Errorf("解码结果不对：%q", text)
	}
	if score < 0.89 || score > 0.91 {
		t.Errorf("平均置信度不对：%v", score)
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
	if text, _ := ctcDecode(probs, len(seq), 7, chars); text != "nvm i 装，" {
		t.Errorf("补空格的结果不对：%q", text)
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

	if win, err := Recognize(img); err == nil {
		t.Logf("Windows OCR：错字率 %.1f%%", charErrorRate(win.Text, accuracyTruth)*100)
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
