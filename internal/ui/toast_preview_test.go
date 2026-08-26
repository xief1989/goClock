package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lxn/walk"
)

// TestRenderToastPreview 把提醒卡片离屏渲染成 PNG，用于人工检查外观。
// 渲染走的是弹窗真正使用的测量与绘制函数，因此结果与实际弹窗一致。
//
// 默认跳过，需要时这样执行：
//
//	$env:GOCLOCK_PREVIEW_DIR="C:\temp"; go test ./internal/ui -run TestRenderToastPreview -v
func TestRenderToastPreview(t *testing.T) {
	dir := os.Getenv("GOCLOCK_PREVIEW_DIR")
	if dir == "" {
		t.Skip("未设置 GOCLOCK_PREVIEW_DIR，跳过卡片预览渲染")
	}

	// walk 的内部 GDI 资源（绘制用的画笔等）只在创建第一个窗口时初始化，
	// 因此这里先建一个始终不显示的窗口把它带起来。
	//
	// 测试二进制不包含 Common Controls 6 清单（清单只链接进 GoClock.exe），
	// 建窗口会在创建提示条时报错；但初始化在那之前就已经完成，
	// 绘制所需的资源都已就绪，所以这个错误可以安全忽略。
	if mw, err := walk.NewMainWindow(); err == nil {
		defer mw.Dispose()
	}

	firedAt := time.Date(2026, 8, 26, 14, 30, 0, 0, time.Local)
	cases := []struct {
		name    string
		title   string
		message string
		paused  bool
	}{
		{"01-short", "喝水提醒", "起来接杯水。", false},
		{"02-normal", "测试提醒 · 弹窗效果", "这是一条用于验证右下角弹窗的提醒内容，文字较长时会自动折行显示，15 秒后自动关闭。", false},
		{"03-empty", "只有标题的提醒", "", false},
		{"04-overflow", "超长内容会被裁掉", strings.Repeat("这一段内容很长，超出四行的部分不再显示。", 6), false},
		{"05-longtitle", "标题特别长的时候应该以省略号结尾而不是把卡片撑宽", "标题过长时用省略号收尾。", false},
		{"06-paused", "鼠标悬停时暂停", "鼠标停在卡片上时倒计时暂停，移开后从暂停处继续。", true},
	}

	for _, c := range cases {
		item := &toast{
			title:   c.title,
			message: c.message,
			firedAt: firedAt,
			remain:  9,
			paused:  c.paused,
		}
		if err := renderPreview(item, filepath.Join(dir, c.name+".png")); err != nil {
			t.Fatalf("%s 渲染失败：%v", c.name, err)
		}
		t.Logf("%s：正文 %d，卡片 %dx%d", c.name, item.bodyHeight, toastWidth, item.cardHeight)
	}
}

// renderPreview 把一张卡片渲染到 PNG 文件。
func renderPreview(t *toast, path string) error {
	// 先用一张临时位图测量正文高度，确定卡片总高。
	probe, err := walk.NewBitmap(walk.Size{Width: toastWidth, Height: 400})
	if err != nil {
		return err
	}
	defer probe.Dispose()

	probeCanvas, err := walk.NewCanvasFromImage(probe)
	if err != nil {
		return err
	}
	t.bodyHeight = t.measureBodyOn(probeCanvas)
	probeCanvas.Dispose()

	t.applyLayout()

	// 再按实际尺寸绘制一遍。
	bmp, err := walk.NewBitmap(walk.Size{Width: toastWidth, Height: t.cardHeight})
	if err != nil {
		return err
	}
	defer bmp.Dispose()

	canvas, err := walk.NewCanvasFromImage(bmp)
	if err != nil {
		return err
	}
	if err := t.paint(canvas, walk.Rectangle{Width: toastWidth, Height: t.cardHeight}); err != nil {
		canvas.Dispose()
		return err
	}
	canvas.Dispose()

	img, err := bmp.ToImage()
	if err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
