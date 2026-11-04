// Package record 把屏幕上的一块区域录成 MP4（H.264，无声音）。
//
// 录制在一个独立的、锁定了系统线程的协程里进行：按帧率抓屏、画上鼠标指针、编码写入。
// 时间戳按真实经过的时间打（暂停的时间扣掉），编码偶尔跟不上只会丢帧，视频不会变快变慢。
package record

import (
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"goclock/internal/capture"
)

// 码率的上下限（比特/秒）。
const (
	minBitrate = 800_000
	maxBitrate = 16_000_000
)

// Options 描述一次录屏。
type Options struct {
	// Rect 是录制区域，屏幕坐标、物理像素；宽高为奇数时会各裁掉 1 像素（H.264 要求偶数）。
	Rect image.Rectangle
	// FPS 是帧率。
	FPS int
	// Cursor 表示是否把鼠标指针画进视频。
	Cursor bool
	// Path 是输出文件路径，目录不存在时自动创建。
	Path string
}

// Recorder 是一次进行中的录制。所有方法都可以在任意协程调用。
type Recorder struct {
	opts Options

	stopOnce sync.Once
	stopCh   chan struct{}
	done     chan error
	result   error
	finished bool
	resultMu sync.Mutex

	mu          sync.Mutex
	start       time.Time
	pausedTotal time.Duration
	pauseStart  time.Time
	paused      bool

	frames atomic.Int64
}

// EvenRect 把矩形的宽高各裁成偶数，H.264 编码器只接受偶数尺寸。
func EvenRect(r image.Rectangle) image.Rectangle {
	r.Max.X -= r.Dx() % 2
	r.Max.Y -= r.Dy() % 2
	return r
}

// Bitrate 按画面大小与帧率估算码率：屏幕内容以文字为主，每像素 0.1 比特足够清晰。
func Bitrate(w, h, fps int) int {
	b := int(float64(w*h*fps) * 0.1)
	return max(minBitrate, min(b, maxBitrate))
}

// Start 开始录制。编码器创建失败（例如系统缺少 H.264 编码器）时直接返回错误。
func Start(opts Options) (*Recorder, error) {
	opts.Rect = EvenRect(opts.Rect)
	if opts.Rect.Dx() < 16 || opts.Rect.Dy() < 16 {
		return nil, errors.New("录制区域太小")
	}
	if opts.FPS < 1 || opts.FPS > 60 {
		opts.FPS = 30
	}
	if err := os.MkdirAll(filepath.Dir(opts.Path), 0o755); err != nil {
		return nil, fmt.Errorf("创建目录失败：%w", err)
	}

	r := &Recorder{opts: opts, stopCh: make(chan struct{}), done: make(chan error, 1)}
	ready := make(chan error, 1)
	go r.run(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return r, nil
}

// Path 返回输出文件路径。
func (r *Recorder) Path() string {
	return r.opts.Path
}

// Rect 返回实际录制的区域（已裁成偶数尺寸）。
func (r *Recorder) Rect() image.Rectangle {
	return r.opts.Rect
}

// Frames 返回已写入的帧数。
func (r *Recorder) Frames() int64 {
	return r.frames.Load()
}

// Elapsed 返回已录下的时长（不含暂停的时间）。
func (r *Recorder) Elapsed() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.elapsedLocked(time.Now())
}

// elapsedLocked 计算到 now 为止录下的时长，调用方需持有 mu。
func (r *Recorder) elapsedLocked(now time.Time) time.Duration {
	if r.start.IsZero() {
		return 0
	}
	d := now.Sub(r.start) - r.pausedTotal
	if r.paused {
		d -= now.Sub(r.pauseStart)
	}
	return max(d, 0)
}

// Paused 返回是否处于暂停状态。
func (r *Recorder) Paused() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paused
}

// SetPaused 暂停或继续录制。
func (r *Recorder) SetPaused(p bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p == r.paused {
		return
	}
	now := time.Now()
	if p {
		r.pauseStart = now
	} else {
		r.pausedTotal += now.Sub(r.pauseStart)
	}
	r.paused = p
}

// Stop 结束录制并等待文件写完，返回录制过程中的第一个错误。重复调用返回同一个结果。
func (r *Recorder) Stop() error {
	r.stopOnce.Do(func() { close(r.stopCh) })
	r.resultMu.Lock()
	defer r.resultMu.Unlock()
	if !r.finished {
		r.result = <-r.done
		r.finished = true
	}
	return r.result
}

// Cancel 放弃录制：结束后删除已写的文件。
func (r *Recorder) Cancel() {
	_ = r.Stop()
	os.Remove(r.opts.Path)
}

// run 是录制协程：锁定系统线程（COM 与 DPI 上下文都是按线程的），在里面完成全部抓屏与编码。
func (r *Recorder) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	capture.WithPerMonitorDPI(func() {
		r.done <- r.loop(ready)
	})
}

// loop 创建抓取器与编码器，然后按帧率循环抓帧，直到收到停止信号。
func (r *Recorder) loop(ready chan<- error) error {
	o := r.opts
	w, h := o.Rect.Dx(), o.Rect.Dy()
	grab, err := capture.NewGrabber(o.Rect)
	if err != nil {
		ready <- err
		return err
	}
	defer grab.Close()

	enc, err := newEncoder(o.Path, w, h, o.FPS, Bitrate(w, h, o.FPS))
	if err != nil {
		os.Remove(o.Path)
		err = fmt.Errorf("无法创建视频编码器：%w", err)
		ready <- err
		return err
	}

	r.mu.Lock()
	r.start = time.Now()
	r.mu.Unlock()
	ready <- nil

	frameDur := int64(10_000_000 / o.FPS)
	ticker := time.NewTicker(time.Second / time.Duration(o.FPS))
	defer ticker.Stop()
	last := int64(-1)
	var loopErr error
	for loopErr == nil {
		select {
		case <-r.stopCh:
			return enc.finish()
		case <-ticker.C:
		}
		r.mu.Lock()
		paused := r.paused
		ts := int64(r.elapsedLocked(time.Now()) / 100)
		r.mu.Unlock()
		if paused {
			continue
		}
		if ts <= last {
			ts = last + 1
		}
		pix, err := grab.Snap(o.Cursor)
		if err != nil {
			// 锁屏、切换桌面时抓屏会暂时失败，跳过这一帧即可。
			continue
		}
		if loopErr = enc.writeFrame(pix, ts, frameDur); loopErr == nil {
			last = ts
			r.frames.Add(1)
		}
	}
	// 写入出错时也尽量收尾，让已录下的部分可以播放。
	if err := enc.finish(); err != nil {
		return errors.Join(loopErr, err)
	}
	return loopErr
}
