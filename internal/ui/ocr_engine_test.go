package ui

import (
	"context"
	"image"
	"image/draw"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"goclock/internal/storage"
)

// loadTestPNG 读一张 PNG 转成 RGBA。
func loadTestPNG(t *testing.T, path string) *image.RGBA {
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
	img := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	draw.Draw(img, img.Rect, src, src.Bounds().Min, draw.Src)
	return img
}

// TestOCRFallsBackWhenDownloadFails 确认下载失败时改用系统 OCR 并说明原因，
// 且失败后的一段时间内不再联网重试（第二次应立刻返回）。
// 用到系统 OCR，默认跳过：$env:GOCLOCK_OCR_IMAGE="D:\1.png"; go test ./internal/ui -run TestOCR -v
func TestOCRFallsBackWhenDownloadFails(t *testing.T) {
	path := os.Getenv("GOCLOCK_OCR_IMAGE")
	if path == "" {
		t.Skip("未设置 GOCLOCK_OCR_IMAGE，跳过")
	}
	img := loadTestPNG(t, path)
	e := &ocrEngines{dir: t.TempDir()}
	defer e.close()

	// 代理指向一个没人监听的端口，下载必然连不上。
	const deadProxy = "http://127.0.0.1:1"
	out, err := e.recognize(context.Background(), img, "", deadProxy, func(string) {})
	if err != nil {
		t.Fatalf("改用系统 OCR 后不应失败：%v", err)
	}
	if out.engine != "系统 OCR" || out.fallback == "" || strings.TrimSpace(out.res.Text) == "" {
		t.Fatalf("应改用系统 OCR 并给出原因：engine=%q fallback=%q", out.engine, out.fallback)
	}
	t.Logf("改用原因：%s", out.fallback)

	start := time.Now()
	out, err = e.recognize(context.Background(), img, "", deadProxy, func(string) {})
	if err != nil || out.fallback == "" {
		t.Fatalf("第二次也应改用系统 OCR：err=%v fallback=%q", err, out.fallback)
	}
	if !strings.Contains(out.fallback, "稍后会自动重试") {
		t.Errorf("失败后短时间内不应重新下载：%s", out.fallback)
	}
	t.Logf("第二次用时 %v（不联网）", time.Since(start))

	out, err = e.recognize(context.Background(), img, storage.OCREngineWindows, deadProxy, func(string) {})
	if err != nil || out.fallback != "" || out.engine != "系统 OCR" {
		t.Errorf("主动选择系统 OCR 时不应带改用原因：err=%v fallback=%q", err, out.fallback)
	}
}

// TestOCRFirstUseDownloadsAndReleases 走一遍首次使用的完整流程：真实下载 → 加载 → 识别 → 闲置释放 → 再次识别。
// 要联网下载约 33MB，默认跳过：
//
//	$env:GOCLOCK_LIVE_DOWNLOAD="1"; $env:GOCLOCK_OCR_IMAGE="D:\1.png"; go test ./internal/ui -run TestOCR -v
func TestOCRFirstUseDownloadsAndReleases(t *testing.T) {
	path := os.Getenv("GOCLOCK_OCR_IMAGE")
	if path == "" || os.Getenv("GOCLOCK_LIVE_DOWNLOAD") == "" {
		t.Skip("未设置 GOCLOCK_OCR_IMAGE / GOCLOCK_LIVE_DOWNLOAD，跳过")
	}
	img := loadTestPNG(t, path)
	// 不用 t.TempDir：onnxruntime.dll 加载后不再卸载，测试进程里删不掉，自动清理会判失败。尽力清理即可。
	dir, err := os.MkdirTemp("", "goclock-ocr-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	downloaded := 0
	e := &ocrEngines{dir: dir, onDownloaded: func() { downloaded++ }}
	defer e.close()

	// 同一阶段的进度只留最后一条（按第一个数字之前的文字区分阶段）。
	var stages []string
	kind := func(s string) string {
		if i := strings.IndexAny(s, "0123456789"); i >= 0 {
			return s[:i]
		}
		return s
	}
	report := func(s string) {
		if n := len(stages); n > 0 && kind(stages[n-1]) == kind(s) {
			stages[n-1] = s
			return
		}
		stages = append(stages, s)
	}
	start := time.Now()
	out, err := e.recognize(context.Background(), img, "", "", report)
	if err != nil {
		t.Fatal(err)
	}
	if out.fallback != "" || out.engine != "高精度" {
		t.Fatalf("应当用高精度识别：engine=%q fallback=%q", out.engine, out.fallback)
	}
	if downloaded != 1 {
		t.Errorf("下载完成回调应调用一次，实际 %d 次", downloaded)
	}
	t.Logf("首次（含下载）用时 %v，识别 %v；进度：\n%s\n结果：\n%s",
		time.Since(start), out.cost, strings.Join(stages, "\n"), out.res.Text)

	e.mu.Lock()
	loaded := e.paddle != nil
	e.mu.Unlock()
	if !loaded {
		t.Fatal("识别后引擎应当缓存")
	}
	e.releaseIdle()
	e.mu.Lock()
	loaded = e.paddle != nil
	e.mu.Unlock()
	if loaded {
		t.Fatal("闲置到期后引擎应当释放")
	}

	start = time.Now()
	out, err = e.recognize(context.Background(), img, "", "", func(string) {})
	if err != nil || out.engine != "高精度" {
		t.Fatalf("释放后应能重新加载：err=%v engine=%q fallback=%q", err, out.engine, out.fallback)
	}
	if downloaded != 1 {
		t.Errorf("文件齐全后不应再次下载")
	}
	t.Logf("释放后再次识别（含加载）用时 %v", time.Since(start))
}
