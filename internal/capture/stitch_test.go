package capture

import (
	"bytes"
	"image"
	"image/color"
	"math/rand/v2"
	"testing"
)

// 测试用的视口布局：顶部固定栏、中间滚动区、底部固定栏。
const (
	testWidth  = 320
	testHeader = 36
	testMiddle = 240
	testFooter = 28
)

// makePage 生成一张合成的「网页」：白底上按行排布随机长短的深色块模拟文字，
// 段落之间夹着大小不一的空白，偶尔插一张带噪点的「图片」。
func makePage(w, h int, seed uint64) *image.RGBA {
	rng := rand.New(rand.NewPCG(seed, seed^0x5DEECE66D))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fillRect(img, img.Rect, color.RGBA{255, 255, 255, 255})

	y := 8
	for y < h {
		switch rng.IntN(10) {
		case 0:
			// 图片块：逐像素随机颜色，区分度很高。
			bh := 40 + rng.IntN(60)
			for yy := y; yy < min(y+bh, h); yy++ {
				for x := 20; x < w-20; x++ {
					img.SetRGBA(x, yy, color.RGBA{uint8(rng.IntN(256)), uint8(rng.IntN(256)), uint8(rng.IntN(256)), 255})
				}
			}
			y += bh + 12
		case 1:
			// 段落间的空白。
			y += 20 + rng.IntN(30)
		case 2:
			// 贴着两侧留白的实心色块：它的边缘列只有一两段变化、内部又是纯色，
			// 长得很像滚动条，但它是跟着内容滚动的，不能被当成滚动条涂掉。
			bh := 30 + rng.IntN(30)
			c := color.RGBA{uint8(150 + rng.IntN(100)), uint8(150 + rng.IntN(100)), uint8(150 + rng.IntN(100)), 255}
			fillRect(img, image.Rect(12, y, w-12, min(y+bh, h)), c)
			y += bh + 12
		default:
			// 一行「文字」：若干个随机宽度的深色块，块内用不重复的纹理模拟笔画。
			lineH := 12 + rng.IntN(6)
			x := 12 + rng.IntN(20)
			for x < w-40 {
				ww := 8 + rng.IntN(50)
				shade := uint8(20 + rng.IntN(80))
				for yy := y; yy < min(y+lineH, h); yy++ {
					for xx := x; xx < min(x+ww, w-12); xx++ {
						if (xx*7+yy*13+xx*yy+int(shade))%5 != 0 {
							img.SetRGBA(xx, yy, color.RGBA{shade, shade, shade + 30, 255})
						}
					}
				}
				x += ww + 4 + rng.IntN(10)
			}
			y += lineH + 6 + rng.IntN(8)
		}
	}
	return img
}

// fillRect 用纯色填充矩形。
func fillRect(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

// drawBar 画一条带色块的固定栏，模拟吸顶导航或底部输入框。
func drawBar(img *image.RGBA, r image.Rectangle, bg, fg color.RGBA) {
	fillRect(img, r, bg)
	for x := r.Min.X + 10; x+30 < r.Max.X; x += 46 {
		fillRect(img, image.Rect(x, r.Min.Y+8, x+30, r.Max.Y-8), fg)
	}
}

// viewport 描述一帧画面里除滚动内容之外的装饰。
type viewport struct {
	sidebar   int  // 左侧固定侧边栏宽度，0 表示没有
	scrollbar bool // 右侧是否有随滚动移动的滚动条
}

// frameAt 截出页面滚动到 s 时视口里的画面。
func frameAt(page *image.RGBA, s int, vp viewport) *image.RGBA {
	h := testHeader + testMiddle + testFooter
	img := image.NewRGBA(image.Rect(0, 0, testWidth, h))
	drawBar(img, image.Rect(0, 0, testWidth, testHeader), color.RGBA{30, 41, 59, 255}, color.RGBA{148, 163, 184, 255})
	for y := 0; y < testMiddle; y++ {
		copy(img.Pix[(testHeader+y)*img.Stride:(testHeader+y+1)*img.Stride],
			page.Pix[(s+y)*page.Stride:(s+y+1)*page.Stride])
	}
	drawBar(img, image.Rect(0, h-testFooter, testWidth, h), color.RGBA{241, 245, 249, 255}, color.RGBA{100, 116, 139, 255})

	if vp.sidebar > 0 {
		// 侧边栏每行内容都不同，但不随滚动移动。
		for y := testHeader; y < testHeader+testMiddle; y++ {
			for x := 0; x < vp.sidebar; x++ {
				img.SetRGBA(x, y, color.RGBA{uint8(y * 7), uint8(x * 13), uint8((x + y) * 3), 255})
			}
		}
	}
	if vp.scrollbar {
		thumbTop := testHeader + s*testMiddle/page.Rect.Dy()
		fillRect(img, image.Rect(testWidth-10, testHeader, testWidth, testHeader+testMiddle), color.RGBA{235, 235, 235, 255})
		fillRect(img, image.Rect(testWidth-9, thumbTop, testWidth-1, thumbTop+30), color.RGBA{150, 150, 150, 255})
	}
	return img
}

// expectedLong 返回滚动到 last 为止应得到的长图：顶栏 + 页面 [0, last+中间高) + 底栏。
func expectedLong(page *image.RGBA, last int) *image.RGBA {
	h := testHeader + last + testMiddle + testFooter
	img := image.NewRGBA(image.Rect(0, 0, testWidth, h))
	first := frameAt(page, 0, viewport{})
	copy(img.Pix, first.Pix[:testHeader*first.Stride])
	copy(img.Pix[testHeader*img.Stride:], page.Pix[:(last+testMiddle)*page.Stride])
	copy(img.Pix[(h-testFooter)*img.Stride:], first.Pix[(testHeader+testMiddle)*first.Stride:])
	return img
}

// feed 依次把各个滚动位置的帧喂给拼接器，返回每一帧的结果。
func feed(st *Stitcher, page *image.RGBA, offsets []int, vp viewport) []AddStatus {
	var got []AddStatus
	for _, s := range offsets {
		status, _ := st.Add(frameAt(page, s, vp))
		got = append(got, status)
	}
	return got
}

func TestStitchReconstructsPage(t *testing.T) {
	for seed := uint64(1); seed <= 20; seed++ {
		page := makePage(testWidth, 2400, seed)
		// 步长从 1 行到中间区高度的一半不等（自动滚动每次滚 45% 左右）。
		// 跨度再大，重叠区里有内容的行太少，拒绝才是对的，见 TestStitchBlankOverlapIsNotGuessed。
		offsets := []int{0, 37, 130, 250, 251, 370, 490, 600, 720, 840}
		st := NewStitcher(0)
		statuses := feed(st, page, offsets, viewport{})
		for i, s := range statuses {
			if s != Appended {
				t.Fatalf("seed=%d 第 %d 帧（偏移 %d）应当拼上：got=%v", seed, i, offsets[i], s)
			}
		}
		want := expectedLong(page, offsets[len(offsets)-1])
		got := st.Image()
		if got.Rect != want.Rect {
			t.Fatalf("seed=%d 尺寸不对：got=%v want=%v", seed, got.Rect, want.Rect)
		}
		if !bytes.Equal(got.Pix, want.Pix) {
			t.Fatalf("seed=%d 拼出来的长图与原页面不一致，首个不同行：%d", seed, firstDiffRow(got, want))
		}
	}
}

func TestStitchIgnoresScrollBack(t *testing.T) {
	page := makePage(testWidth, 1600, 7)
	st := NewStitcher(0)
	statuses := feed(st, page, []int{0, 120, 60, 200, 200}, viewport{})
	want := []AddStatus{Appended, Appended, Unchanged, Appended, Unchanged}
	for i := range want {
		if statuses[i] != want[i] {
			t.Errorf("第 %d 帧：got=%v want=%v", i, statuses[i], want[i])
		}
	}
	if !bytes.Equal(st.Image().Pix, expectedLong(page, 200).Pix) {
		t.Error("往回滚之后再往下滚，结果应与一路往下滚一致")
	}
}

func TestStitchRejectsJumpWithoutOverlap(t *testing.T) {
	page := makePage(testWidth, 2000, 9)
	st := NewStitcher(0)
	statuses := feed(st, page, []int{0, testMiddle + 80}, viewport{})
	if statuses[1] != NoMatch {
		t.Errorf("两帧之间没有重叠时应当丢弃：got=%v", statuses[1])
	}
	if st.Height() != testHeader+testMiddle+testFooter {
		t.Errorf("丢弃的帧不应改变长图：height=%d", st.Height())
	}
}

func TestStitchToleratesSidebarAndScrollbar(t *testing.T) {
	page := makePage(testWidth, 2000, 11)
	offsets := []int{0, 90, 170, 333, 500}
	for _, vp := range []viewport{{sidebar: 60}, {scrollbar: true}, {sidebar: 40, scrollbar: true}} {
		st := NewStitcher(0)
		statuses := feed(st, page, offsets, vp)
		for i, s := range statuses {
			if s != Appended {
				t.Fatalf("%+v 第 %d 帧应当拼上：got=%v", vp, i, s)
			}
		}
		got := st.Image()
		want := expectedLong(page, offsets[len(offsets)-1])
		if got.Rect != want.Rect {
			t.Fatalf("%+v 尺寸不对：got=%v want=%v", vp, got.Rect, want.Rect)
		}
		// 页面内容那几列必须与原页面逐像素一致。
		x0, x1 := vp.sidebar, testWidth
		if vp.scrollbar {
			x1 = testWidth - 10
		}
		for y := testHeader; y < got.Rect.Dy()-testFooter; y++ {
			a := got.Pix[y*got.Stride+x0*4 : y*got.Stride+x1*4]
			b := want.Pix[y*want.Stride+x0*4 : y*want.Stride+x1*4]
			if !bytes.Equal(a, b) {
				t.Fatalf("%+v 第 %d 行页面内容对不上", vp, y)
			}
		}

		// 首屏之后接上的行里，侧边栏与滚动条不随内容滚动，应当被涂成纯色，
		// 不能出现一截截重复的侧边栏和滑块。
		appended := image.Rect(0, testHeader+testMiddle, testWidth, got.Rect.Dy()-testFooter)
		if vp.scrollbar {
			// 滚动条连首屏里的滑块也一起涂掉，从顶栏下面开始就应当是纯色。
			assertPlainColumns(t, got, image.Rect(testWidth-10, testHeader, testWidth, appended.Max.Y), "滚动条")
		}
		if vp.sidebar > 0 {
			assertPlainColumns(t, got, image.Rect(0, appended.Min.Y, vp.sidebar, appended.Max.Y), "侧边栏")
		}
	}
}

// assertPlainColumns 断言区域 r 内每一列都只有一种颜色。
func assertPlainColumns(t *testing.T, img *image.RGBA, r image.Rectangle, what string) {
	t.Helper()
	for x := r.Min.X; x < r.Max.X; x++ {
		first := img.RGBAAt(x, r.Min.Y)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			if img.RGBAAt(x, y) != first {
				t.Fatalf("%s第 %d 列在第 %d 行出现了别的颜色，说明不随滚动的内容被重复接进了长图", what, x, y)
			}
		}
	}
}

func TestStitchUnchangedFrames(t *testing.T) {
	page := makePage(testWidth, 1200, 3)
	st := NewStitcher(0)
	first := frameAt(page, 0, viewport{})
	st.Add(first)

	if s, _ := st.Add(frameAt(page, 0, viewport{})); s != Unchanged {
		t.Errorf("完全相同的帧应判为没变化：got=%v", s)
	}

	// 模拟闪烁的光标：一小块区域变了，内容并没有滚动。
	caret := frameAt(page, 0, viewport{})
	fillRect(caret, image.Rect(150, 100, 152, 140), color.RGBA{0, 0, 0, 255})
	if s, _ := st.Add(caret); s != Unchanged {
		t.Errorf("局部变化不应当被当成滚动或对不上：got=%v", s)
	}
}

func TestStitchBlankOverlapIsNotGuessed(t *testing.T) {
	// 页面中间是一大片空白：在空白里滚动时无从判断位移，不能瞎拼。
	page := makePage(testWidth, 1600, 5)
	fillRect(page, image.Rect(0, 300, testWidth, 1300), color.RGBA{255, 255, 255, 255})
	st := NewStitcher(0)
	statuses := feed(st, page, []int{0, 400, 520}, viewport{})
	for i, s := range statuses[1:] {
		if s == Appended {
			t.Errorf("第 %d 帧整屏空白，不该被拼上", i+1)
		}
	}
}

func TestStitchStopsAtMaxHeight(t *testing.T) {
	page := makePage(testWidth, 3000, 13)
	limit := 600
	st := NewStitcher(limit)
	statuses := feed(st, page, []int{0, 150, 300, 450, 600}, viewport{})
	sawFull := false
	for _, s := range statuses {
		if s == Full {
			sawFull = true
		}
	}
	if !sawFull {
		t.Errorf("超过高度上限时应返回 Full：%v", statuses)
	}
	if st.Height() != limit {
		t.Errorf("长图高度应正好等于上限：got=%d want=%d", st.Height(), limit)
	}
	if s, n := st.Add(frameAt(page, 700, viewport{})); s != Full || n != 0 {
		t.Errorf("达到上限后不再接收：got=%v,%d", s, n)
	}
}

func TestThumbnailKeepsBottom(t *testing.T) {
	// 用拼接测试里验证过的页面与步长准备数据，这里只关心缩略图本身。
	page := makePage(testWidth, 2400, 1)
	st := NewStitcher(0)
	for _, s := range feed(st, page, []int{0, 37, 130, 250, 370, 490, 600}, viewport{}) {
		if s != Appended {
			t.Fatalf("准备数据时每一帧都应拼上：got=%v", s)
		}
	}
	thumb := st.Thumbnail(80, 100)
	if thumb.Rect.Dx() != 80 || thumb.Rect.Dy() != 100 {
		t.Fatalf("缩略图尺寸不对：%v", thumb.Rect)
	}
	if st.Thumbnail(0, 100) != nil {
		t.Error("宽度为 0 时应返回 nil")
	}
}

// firstDiffRow 返回两张同尺寸图片第一处不同的行号，相同时返回 -1。
func firstDiffRow(a, b *image.RGBA) int {
	for y := 0; y < a.Rect.Dy(); y++ {
		if !bytes.Equal(a.Pix[y*a.Stride:(y+1)*a.Stride], b.Pix[y*b.Stride:(y+1)*b.Stride]) {
			return y
		}
	}
	return -1
}
