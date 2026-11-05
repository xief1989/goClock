package ocr

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeAssets 用测试服务器上的小文件替换真实的下载清单，测试结束后恢复。
func fakeAssets(t *testing.T, list []paddleAsset) {
	t.Helper()
	old := paddleAssets
	paddleAssets = list
	t.Cleanup(func() { paddleAssets = old })
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestDownloadPaddleVerifiesAndFallsBack(t *testing.T) {
	model := []byte("pretend this is an onnx model")
	dll := []byte("pretend this is onnxruntime.dll")
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	w, _ := zw.Create("onnxruntime/capi/onnxruntime.dll")
	w.Write(dll)
	zw.Close()
	wheel := zbuf.Bytes()

	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/broken.whl":
			rw.Write([]byte("被篡改的内容"))
		case "/good.whl":
			rw.Write(wheel)
		case "/model.onnx":
			rw.Write(model)
		default:
			http.NotFound(rw, r)
		}
	}))
	defer srv.Close()

	fakeAssets(t, []paddleAsset{
		{
			file: "onnxruntime.dll", size: int64(len(dll)), sha256: sha(dll),
			// 第一个源 404、第二个源内容被篡改，第三个才是好的：应自动换源并靠校验挡住坏文件。
			urls:     []string{srv.URL + "/missing.whl", srv.URL + "/broken.whl", srv.URL + "/good.whl"},
			zipEntry: "onnxruntime/capi/onnxruntime.dll", pkgSize: int64(len(wheel)), pkgSHA256: sha(wheel),
		},
		{file: "det.onnx", size: int64(len(model)), sha256: sha(model), urls: []string{srv.URL + "/model.onnx"}},
	})

	dir := t.TempDir()
	if PaddleReady(dir) {
		t.Fatal("空目录不应判为就绪")
	}
	if PaddleDownloadSize(dir) != int64(len(wheel)+len(model)) {
		t.Errorf("待下载大小不对：%d", PaddleDownloadSize(dir))
	}
	var last, total int64
	err := DownloadPaddle(context.Background(), srv.Client(), dir, func(d, tot int64) { last, total = d, tot })
	if err != nil {
		t.Fatalf("下载失败：%v", err)
	}
	if !PaddleReady(dir) {
		t.Fatal("下载完应当就绪")
	}
	if last != total || total != int64(len(wheel)+len(model)) {
		t.Errorf("进度没有走到底：%d/%d", last, total)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "onnxruntime.dll"))
	if !bytes.Equal(got, dll) {
		t.Error("解出的 DLL 内容不对")
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.download")); len(leftovers) > 0 {
		t.Errorf("不应留下临时文件：%v", leftovers)
	}
	if PaddleDownloadSize(dir) != 0 {
		t.Error("齐全之后不应再需要下载")
	}
}

// TestPaddleSourcesLive 逐个下载真实的下载源，确认地址可用、固定的校验值没写错。
// 要下载约 60MB，默认跳过：$env:GOCLOCK_LIVE_DOWNLOAD="1"; go test ./internal/ocr -run TestPaddleSourcesLive -v
func TestPaddleSourcesLive(t *testing.T) {
	if os.Getenv("GOCLOCK_LIVE_DOWNLOAD") == "" {
		t.Skip("未设置 GOCLOCK_LIVE_DOWNLOAD，跳过")
	}
	for _, a := range paddleAssets {
		for _, url := range a.urls {
			dir := t.TempDir()
			start := time.Now()
			if err := tryDownload(context.Background(), http.DefaultClient, dir, a, url, func(int64) {}); err != nil {
				t.Errorf("%s：%v", url, err)
				continue
			}
			t.Logf("%s：%s 校验通过，用时 %v", hostOf(url), a.file, time.Since(start).Round(time.Millisecond))
		}
	}
}

func TestDownloadPaddleRejectsTamperedFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Write([]byte("被篡改的内容"))
	}))
	defer srv.Close()
	fakeAssets(t, []paddleAsset{{file: "det.onnx", size: 3, sha256: sha([]byte("abc")), urls: []string{srv.URL + "/m"}}})
	dir := t.TempDir()
	if err := DownloadPaddle(context.Background(), srv.Client(), dir, nil); err == nil {
		t.Fatal("校验不符应当报错")
	}
	if _, err := os.Stat(filepath.Join(dir, "det.onnx")); err == nil {
		t.Error("校验不符时不应留下文件")
	}
}
