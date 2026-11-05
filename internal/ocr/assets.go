package ocr

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PaddleOCR 所需文件的下载：首次使用时自动下载约 35MB，固定版本，逐个校验 SHA256。
//
// ONNX Runtime 取自 PyPI 上微软官方发布的 Python 安装包（本质是 zip），只解出其中的 onnxruntime.dll；
// 模型取自魔搭（ModelScope）上 RapidOCR 仓库的固定版本 v3.10.0。

// paddleSubDir 是文件在本地缓存目录下的子目录，名字里带版本，换版本时不会混用旧文件。
const paddleSubDir = `GoClock\ocr\ppocrv5-ort1.22`

// paddleAsset 描述一个需要下载的文件。
type paddleAsset struct {
	// file 是最终落地的文件名。
	file string
	// size、sha256 是最终文件的大小与校验值。
	size   int64
	sha256 string
	// urls 是依次尝试的下载地址，前一个失败就换下一个。
	urls []string
	// zipEntry 不为空表示下载的是 zip 包，要从中取出这个成员；pkgSize、pkgSHA256 是包本身的大小与校验值。
	zipEntry  string
	pkgSize   int64
	pkgSHA256 string
}

// ortWheelPath 是 onnxruntime 1.22.0（Python 3.11，Windows x64）安装包在 PyPI 文件库中的路径。
const ortWheelPath = "packages/89/a5/1c6c10322201566015183b52ef011dfa932f5dd1b278de8d75c3b948411d/onnxruntime-1.22.0-cp311-cp311-win_amd64.whl"

// modelScopeBase 是 RapidOCR 模型仓库固定版本的下载前缀。
const modelScopeBase = "https://www.modelscope.cn/models/RapidAI/RapidOCR/resolve/v3.10.0/onnx/PP-OCRv5/"

// paddleAssets 是 PaddleOCR 需要的全部文件。下载源按实测速度排序（阿里云、清华、华为的 PyPI 镜像会拒绝程序直接下载）。
var paddleAssets = []paddleAsset{
	{
		file:   ortDLLFile,
		size:   14855216,
		sha256: "20e5f96903515680d100e4a4a1857392264bc265a749f0734f0770906567b359",
		urls: []string{
			"https://files.pythonhosted.org/" + ortWheelPath,
			"https://mirrors.cloud.tencent.com/pypi/" + ortWheelPath,
		},
		zipEntry:  "onnxruntime/capi/onnxruntime.dll",
		pkgSize:   12691517,
		pkgSHA256: "03d3ef7fb11adf154149d6e767e21057e0e577b947dd3f66190b212528e1db31",
	},
	{
		file:   detModelFile,
		size:   4819576,
		sha256: "4d97c44a20d30a81aad087d6a396b08f786c4635742afc391f6621f5c6ae78ae",
		urls:   []string{modelScopeBase + "det/" + detModelFile},
	},
	{
		file:   recModelFile,
		size:   16631306,
		sha256: "5825fc7ebf84ae7a412be049820b4d86d77620f204a041697b0494669b1742c5",
		urls:   []string{modelScopeBase + "rec/" + recModelFile},
	},
}

// PaddleDir 返回 PaddleOCR 文件的本地目录（%LOCALAPPDATA% 下，不随漫游配置同步）。
func PaddleDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("无法定位本地缓存目录：%w", err)
	}
	return filepath.Join(base, paddleSubDir), nil
}

// PaddleReady 判断目录里的文件是否齐全。下载时已逐个校验过 SHA256，这里按大小快速判断。
func PaddleReady(dir string) bool {
	for _, a := range paddleAssets {
		if fi, err := os.Stat(filepath.Join(dir, a.file)); err != nil || fi.Size() != a.size {
			return false
		}
	}
	return true
}

// PaddleDownloadSize 返回还需要下载的字节数。
func PaddleDownloadSize(dir string) int64 {
	var total int64
	for _, a := range missingAssets(dir) {
		total += a.downloadSize()
	}
	return total
}

// missingAssets 返回目录里还没有（或大小不对）的文件。
func missingAssets(dir string) []paddleAsset {
	var out []paddleAsset
	for _, a := range paddleAssets {
		if fi, err := os.Stat(filepath.Join(dir, a.file)); err != nil || fi.Size() != a.size {
			out = append(out, a)
		}
	}
	return out
}

// downloadSize 返回这个文件实际要下载的字节数（zip 包按包的大小算）。
func (a paddleAsset) downloadSize() int64 {
	if a.zipEntry != "" {
		return a.pkgSize
	}
	return a.size
}

// DownloadPaddle 下载目录里缺少的文件，progress 报告已下载与总共的字节数（可为 nil）。
// 每个文件写到临时文件、校验通过后才改名，中途失败不会留下半截的文件。
func DownloadPaddle(ctx context.Context, client *http.Client, dir string, progress func(done, total int64)) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建目录失败：%w", err)
	}
	missing := missingAssets(dir)
	var total, done int64
	for _, a := range missing {
		total += a.downloadSize()
	}
	report := func(n int64) {
		if progress != nil {
			progress(done+n, total)
		}
	}
	for _, a := range missing {
		if err := downloadAsset(ctx, client, dir, a, report); err != nil {
			return err
		}
		done += a.downloadSize()
		report(0)
	}
	return nil
}

// downloadAsset 依次尝试各下载源，取得并校验一个文件。
func downloadAsset(ctx context.Context, client *http.Client, dir string, a paddleAsset, report func(int64)) error {
	var errs []string
	for _, url := range a.urls {
		err := tryDownload(ctx, client, dir, a, url, report)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		errs = append(errs, err.Error())
	}
	return fmt.Errorf("下载 %s 失败：%s", a.file, strings.Join(errs, "；"))
}

// downloadIdleTimeout 是下载时允许多久收不到数据：连不上或中途卡住超过它就换下一个源。
// 不设总时长上限，网速慢但一直在走的下载不会被打断。
const downloadIdleTimeout = 20 * time.Second

// tryDownload 从一个地址下载并校验，成功后把最终文件放到位。
func tryDownload(ctx context.Context, client *http.Client, dir string, a paddleAsset, url string, report func(int64)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	idle := time.AfterFunc(downloadIdleTimeout, cancel)
	defer idle.Stop()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s 连不上", hostOf(url))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s 返回 %d", hostOf(url), resp.StatusCode)
	}

	tmp := filepath.Join(dir, a.file+".download")
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("创建临时文件失败：%w", err)
	}
	defer os.Remove(tmp)
	h := sha256.New()
	var got int64
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		idle.Reset(downloadIdleTimeout)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return fmt.Errorf("写入失败：%w", werr)
			}
			h.Write(buf[:n])
			got += int64(n)
			report(got)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			if ctx.Err() != nil {
				return fmt.Errorf("%s 长时间没有数据", hostOf(url))
			}
			return fmt.Errorf("%s 下载中断：%v", hostOf(url), rerr)
		}
	}
	if err := f.Close(); err != nil {
		return err
	}

	final := filepath.Join(dir, a.file)
	if a.zipEntry == "" {
		if sum := hex.EncodeToString(h.Sum(nil)); sum != a.sha256 {
			return fmt.Errorf("%s 下载的文件校验不符", hostOf(url))
		}
		return replaceFile(tmp, final)
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != a.pkgSHA256 {
		return fmt.Errorf("%s 下载的安装包校验不符", hostOf(url))
	}
	return extractEntry(tmp, a.zipEntry, final, a.sha256)
}

// extractEntry 从 zip 包里取出一个成员写到 dst，并校验 SHA256。
func extractEntry(zipPath, entry, dst, wantSHA string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("打开安装包失败：%w", err)
	}
	defer zr.Close()
	for _, zf := range zr.File {
		if zf.Name != entry {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		tmp := dst + ".extract"
		out, err := os.Create(tmp)
		if err != nil {
			return err
		}
		defer os.Remove(tmp)
		h := sha256.New()
		if _, err := io.Copy(io.MultiWriter(out, h), rc); err != nil {
			out.Close()
			return fmt.Errorf("解压失败：%w", err)
		}
		if err := out.Close(); err != nil {
			return err
		}
		if hex.EncodeToString(h.Sum(nil)) != wantSHA {
			return errors.New("解压出的文件校验不符")
		}
		return replaceFile(tmp, dst)
	}
	return fmt.Errorf("安装包里没有 %s", entry)
}

// replaceFile 把临时文件改名为最终文件（已存在时覆盖）。
func replaceFile(tmp, final string) error {
	os.Remove(final)
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("保存文件失败：%w", err)
	}
	return nil
}

// hostOf 取出地址里的主机名，用于错误提示。
func hostOf(url string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}
