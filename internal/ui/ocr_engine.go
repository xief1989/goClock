package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"sync"
	"time"

	"goclock/internal/httpx"
	"goclock/internal/ocr"
	"goclock/internal/storage"
)

const (
	// paddleIdleRelease 是高精度识别引擎闲置多久后释放：加载后常驻几百 MB 内存，用完不该一直占着。
	paddleIdleRelease = 5 * time.Minute
	// paddleRetryAfter 是自动下载失败后多久内不再重试，期间直接用系统 OCR，免得每次识别都等网络超时。
	paddleRetryAfter = 10 * time.Minute
	// progressInterval 是进度提示的最短刷新间隔，下载与长图识别的回调很密，不必每次都刷界面。
	progressInterval = 150 * time.Millisecond
)

// ocrOutcome 是一次识别的结果。
type ocrOutcome struct {
	res *ocr.Result
	// engine 是界面上显示的引擎名。
	engine string
	// cost 是识别本身的用时，不含下载与加载模型。
	cost time.Duration
	// fallback 不为空表示高精度识别不可用、已改用系统 OCR，内容是原因。
	fallback string
}

// ocrEngines 管理高精度识别引擎：首次使用时下载文件，加载后缓存复用，闲置一段时间后释放。
// 所有方法可在任意协程调用。
type ocrEngines struct {
	// dir 是模型文件目录，为空时用 ocr.PaddleDir()；测试时指向临时目录。
	dir string

	mu     sync.Mutex
	paddle *ocr.Paddle
	// busy 是正在用 paddle 识别的次数，大于 0 时不释放。
	busy   int
	idle   *time.Timer
	closed bool

	// dlMu 保证同一时间只有一个下载；failedAt、failErr 记录最近一次下载失败，受 dlMu 保护。
	dlMu     sync.Mutex
	failedAt time.Time
	failErr  string

	// onDownloaded 在下载完成后调用（在下载所在的协程里），用来刷新设置页的状态。
	onDownloaded func()
}

// recognize 按设置的引擎识别图片文字，report 报告进度文字（在后台协程里调用）。
// 高精度识别不可用（下载失败、加载失败、识别出错）时自动改用系统 OCR，原因放在 fallback 里。
// 只有 ctx 被取消或系统 OCR 也失败时才返回错误。
func (e *ocrEngines) recognize(ctx context.Context, img *image.RGBA, engine, proxy string, report func(string)) (ocrOutcome, error) {
	if engine == storage.OCREngineWindows {
		return windowsOCR(img, "", report)
	}
	p, err := e.acquire(ctx, proxy, report)
	if err == nil {
		report("正在识别文字……")
		tick := every(progressInterval)
		start := time.Now()
		res, rerr := p.RecognizeProgress(img, func(done, total int) {
			if done == total || tick() {
				report(fmt.Sprintf("正在识别文字 %d/%d", done, total))
			}
		})
		cost := time.Since(start)
		e.releaseLater()
		if rerr == nil {
			return ocrOutcome{res: res, engine: "高精度", cost: cost}, nil
		}
		err = rerr
	}
	if ctx.Err() != nil {
		return ocrOutcome{}, ctx.Err()
	}
	return windowsOCR(img, err.Error(), report)
}

// windowsOCR 用系统自带的 OCR 识别，fallback 是改用它的原因（主动选择时为空）。
func windowsOCR(img *image.RGBA, fallback string, report func(string)) (ocrOutcome, error) {
	if fallback != "" {
		report("高精度识别不可用，改用系统 OCR……")
	} else {
		report("正在识别文字……")
	}
	start := time.Now()
	res, err := ocr.Recognize(img)
	return ocrOutcome{res: res, engine: "系统 OCR", cost: time.Since(start), fallback: fallback}, err
}

// acquire 取得可用的高精度识别引擎，必要时先下载文件、加载模型。成功后必须配对调用 releaseLater。
func (e *ocrEngines) acquire(ctx context.Context, proxy string, report func(string)) (*ocr.Paddle, error) {
	if p := e.take(); p != nil {
		return p, nil
	}
	dir := e.dir
	if dir == "" {
		var err error
		if dir, err = ocr.PaddleDir(); err != nil {
			return nil, err
		}
	}
	if err := e.ensureFiles(ctx, dir, proxy, report); err != nil {
		return nil, err
	}

	report("正在加载高精度识别模型……")
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, errors.New("程序正在退出")
	}
	if e.paddle == nil {
		p, err := ocr.OpenPaddle(dir)
		if err != nil {
			return nil, fmt.Errorf("加载高精度识别模型失败：%w", err)
		}
		e.paddle = p
	}
	e.busy++
	e.stopIdle()
	return e.paddle, nil
}

// take 在引擎已加载时直接占用它，否则返回 nil。
func (e *ocrEngines) take() *ocr.Paddle {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.paddle == nil {
		return nil
	}
	e.busy++
	e.stopIdle()
	return e.paddle
}

// ensureFiles 确保高精度识别所需的文件已经下载好，缺了就下载。
func (e *ocrEngines) ensureFiles(ctx context.Context, dir, proxy string, report func(string)) error {
	if ocr.PaddleReady(dir) {
		return nil
	}
	report("正在准备高精度识别……")
	e.dlMu.Lock()
	defer e.dlMu.Unlock()
	// 等锁期间别的识别窗口可能已经下载完了。
	if ocr.PaddleReady(dir) {
		return nil
	}
	if !e.failedAt.IsZero() && time.Since(e.failedAt) < paddleRetryAfter {
		return fmt.Errorf("%s（稍后会自动重试）", e.failErr)
	}
	client, err := httpx.NewClient(proxy, 0)
	if err != nil {
		return err
	}
	report(fmt.Sprintf("首次使用，正在下载高精度识别模型（%s）……", formatMB(ocr.PaddleDownloadSize(dir))))
	tick := every(progressInterval)
	err = ocr.DownloadPaddle(ctx, client, dir, func(done, total int64) {
		if done == total || tick() {
			report(fmt.Sprintf("首次使用，正在下载高精度识别模型 %s / %s", formatMB(done), formatMB(total)))
		}
	})
	if err != nil {
		if ctx.Err() == nil {
			e.failedAt, e.failErr = time.Now(), err.Error()
		}
		return err
	}
	e.failedAt, e.failErr = time.Time{}, ""
	if e.onDownloaded != nil {
		e.onDownloaded()
	}
	return nil
}

// releaseLater 结束一次占用；没人在用时开始计时，闲置到期后释放引擎。
func (e *ocrEngines) releaseLater() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.busy--
	if e.busy > 0 || e.paddle == nil {
		return
	}
	if e.closed {
		e.paddle.Close()
		e.paddle = nil
		return
	}
	if e.idle == nil {
		e.idle = time.AfterFunc(paddleIdleRelease, e.releaseIdle)
	} else {
		e.idle.Reset(paddleIdleRelease)
	}
}

// releaseIdle 在闲置到期时释放引擎；计时期间又被占用的话什么也不做。
func (e *ocrEngines) releaseIdle() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.busy == 0 && e.paddle != nil {
		e.paddle.Close()
		e.paddle = nil
	}
}

// stopIdle 停掉闲置计时，调用方持有 mu。
func (e *ocrEngines) stopIdle() {
	if e.idle != nil {
		e.idle.Stop()
	}
}

// close 在退出程序时释放引擎；正在识别的那次结束后由 releaseLater 释放。
func (e *ocrEngines) close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	e.stopIdle()
	if e.busy == 0 && e.paddle != nil {
		e.paddle.Close()
		e.paddle = nil
	}
}

// every 返回一个限流判断：距上次返回 true 已过 d 才再返回 true。
func every(d time.Duration) func() bool {
	var last time.Time
	return func() bool {
		if now := time.Now(); now.Sub(last) >= d {
			last = now
			return true
		}
		return false
	}
}

// formatMB 把字节数写成「12.3 MB」。
func formatMB(n int64) string {
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}
