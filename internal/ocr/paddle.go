package ocr

import (
	"errors"
	"fmt"
	"image"
	"math"
	"path/filepath"
	"strings"
	"sync"
)

// PaddleOCR（PP-OCRv5 移动版）识别：先用检测模型找出文字行，再逐行用识别模型读出文字。
// 推理交给 ONNX Runtime，全部在本机完成。
//
// 截图里的文字几乎都是横排，检测结果的后处理简化为「连通区域的外接矩形」，
// 省掉通用场景下的旋转框计算。

// PaddleOCR 所需文件在本地目录中的文件名。
const (
	ortDLLFile   = "onnxruntime.dll"
	detModelFile = "ch_PP-OCRv5_det_mobile.onnx"
	recModelFile = "ch_PP-OCRv5_rec_mobile.onnx"
)

// 检测与识别的参数，取值与 PaddleOCR 官方推理脚本一致，另有注明的除外。
const (
	// detMaxSide 是检测输入的最长边：超过就缩小。官方默认 960，截图里字小，放宽到 2560 才不漏小字。
	detMaxSide = 2560
	// detTileRows 是长图每块检测的最大行数，按空白行切块。
	detTileRows = 1600
	// detThresh 是概率图二值化的阈值。
	detThresh = 0.3
	// detBoxThresh 是文字框的平均概率下限，低于它的当作噪点丢掉。
	detBoxThresh = 0.6
	// detUnclipRatio 是把收缩过的文字区域扩回原大小的比例。
	detUnclipRatio = 1.5
	// detMinSize 是文字框短边的下限（像素）。
	detMinSize = 3
	// recHeight 是识别模型要求的输入高度。
	recHeight = 48
	// recMaxWidth 是识别输入的最大宽度，再长的行切段识别。
	recMaxWidth = 3200
	// recMinScore 是一行文字的平均置信度下限，低于它的丢掉（官方 drop_score）。
	recMinScore = 0.5
	// recBatch 是识别时一批最多几行。
	// 实测（81 行、6 核）：逐行 2.9 秒；每批 4 行 4.9 秒、16 行 5.8 秒（补齐的空白也要算）；
	// 多路并行同样更慢（运行库单次推理已经用满各核）。所以逐行、单路、线程数交给运行库。
	recBatch = 1
	// recStride 是识别模型每个时间步对应的输入宽度（像素）。
	recStride = 8
	// spaceGapSteps：两个字符之间空白的时间步数达到它就补一个空格。
	// 实测（字高缩放到 48 后）：单词内部 0～2，偶尔 3；单词之间稳定在 4 以上。
	// 识别模型自己很少输出空格，「nvm install」会变成「nvminstall」，所以按间隔补。
	spaceGapSteps = 4
)

// detMean、detStd 是检测模型的归一化参数（按 B、G、R 顺序，与训练时 OpenCV 读图的通道顺序一致）。
var (
	detMean = [3]float32{0.485, 0.456, 0.406}
	detStd  = [3]float32{0.229, 0.224, 0.225}
)

// Paddle 是加载好的 PaddleOCR 引擎，可以反复使用。所有方法可在任意协程调用，识别过程内部串行。
type Paddle struct {
	mu    sync.Mutex
	rt    *ortRuntime
	det   *ortSession
	rec   *ortSession
	chars []string
}

// OpenPaddle 从目录 dir 加载 ONNX Runtime 与两个模型。
func OpenPaddle(dir string) (*Paddle, error) {
	rt, err := loadORT(filepath.Join(dir, ortDLLFile))
	if err != nil {
		return nil, err
	}
	p := &Paddle{rt: rt}
	if p.det, err = rt.openSession(filepath.Join(dir, detModelFile), 0); err != nil {
		p.Close()
		return nil, err
	}
	if p.rec, err = rt.openSession(filepath.Join(dir, recModelFile), 0); err != nil {
		p.Close()
		return nil, err
	}
	dict, err := p.rec.metadata("character")
	if err != nil || dict == "" {
		p.Close()
		return nil, errors.New("识别模型里没有字典")
	}
	// 下标 0 是 CTC 的空白符，字典按行排列；末尾的空格是否存在由输出维度决定，见 decode。
	p.chars = append([]string{""}, strings.Split(strings.TrimRight(dict, "\n"), "\n")...)
	return p, nil
}

// Close 释放模型与运行库环境。正在识别时会等识别结束再释放。
func (p *Paddle) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rec != nil {
		p.rec.close()
		p.rec = nil
	}
	if p.det != nil {
		p.det.close()
		p.det = nil
	}
	if p.rt != nil {
		p.rt.close()
		p.rt = nil
	}
}

// Version 返回所用 ONNX Runtime 的版本，引擎关闭后返回空。
func (p *Paddle) Version() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rt == nil {
		return ""
	}
	return p.rt.version
}

// Recognize 识别图片中的文字。
func (p *Paddle) Recognize(img *image.RGBA) (*Result, error) {
	return p.RecognizeProgress(img, nil)
}

// RecognizeProgress 识别图片中的文字，每识别完一段文字调用一次 progress（可为 nil），报告已完成与总段数。
// 长截图有几百行，逐行识别要十几秒，界面靠它显示进度。
func (p *Paddle) RecognizeProgress(img *image.RGBA, progress func(done, total int)) (*Result, error) {
	if img == nil || img.Rect.Empty() {
		return nil, errors.New("没有可识别的图片")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rec == nil {
		return nil, errors.New("识别引擎已关闭")
	}

	img = subImage(img, image.Rect(0, 0, img.Rect.Dx(), img.Rect.Dy()))
	boxes, err := p.detect(img)
	if err != nil {
		return nil, err
	}
	texts, scores, err := p.recognizeBoxes(img, boxes, progress)
	if err != nil {
		return nil, err
	}
	var lines []Line
	for i, b := range boxes {
		text := strings.TrimSpace(texts[i])
		if text == "" || scores[i] < recMinScore {
			continue
		}
		lines = append(lines, Line{Text: text, Rect: b})
	}
	lines = mergeRows(lines)
	joined := make([]string, len(lines))
	for i, l := range lines {
		joined[i] = l.Text
	}
	return &Result{Text: strings.Join(joined, "\n"), Lines: lines, Language: "PaddleOCR PP-OCRv5"}, nil
}

// detect 找出图片里的全部文字框（原图坐标）。长图按空白行切块分别检测。
func (p *Paddle) detect(img *image.RGBA) ([]image.Rectangle, error) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	var boxes []image.Rectangle
	for y := 0; y < h; {
		end := min(h, y+detTileRows)
		if end < h {
			end = blankRowNear(img, end, max(y+detTileRows/2, end-300))
		}
		part, err := p.detectTile(subImage(img, image.Rect(0, y, w, end)))
		if err != nil {
			return nil, err
		}
		for _, b := range part {
			boxes = append(boxes, b.Add(image.Pt(0, y)))
		}
		y = end
	}
	return boxes, nil
}

// detectTile 对一块图片做文字检测。
func (p *Paddle) detectTile(img *image.RGBA) ([]image.Rectangle, error) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	scale := 1.0
	if m := max(w, h); m > detMaxSide {
		scale = float64(detMaxSide) / float64(m)
	}
	src := img
	if scale != 1 {
		src = resize(img, scale)
	}
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	// 长宽补到 32 的倍数，补出来的部分填白，不会检测出东西。
	pw, ph := roundUp32(sw), roundUp32(sh)
	input := make([]float32, 3*pw*ph)
	plane := pw * ph
	for y := 0; y < ph; y++ {
		for x := 0; x < pw; x++ {
			r, g, b := uint8(255), uint8(255), uint8(255)
			if x < sw && y < sh {
				i := y*src.Stride + x*4
				r, g, b = src.Pix[i], src.Pix[i+1], src.Pix[i+2]
			}
			o := y*pw + x
			input[o] = (float32(b)/255 - detMean[0]) / detStd[0]
			input[plane+o] = (float32(g)/255 - detMean[1]) / detStd[1]
			input[2*plane+o] = (float32(r)/255 - detMean[2]) / detStd[2]
		}
	}
	out, err := p.det.run(input, []int64{1, 3, int64(ph), int64(pw)})
	if err != nil {
		return nil, fmt.Errorf("文字检测失败：%w", err)
	}
	if len(out.shape) != 4 || int(out.shape[2]) != ph || int(out.shape[3]) != pw {
		return nil, fmt.Errorf("检测模型输出形状不对：%v", out.shape)
	}

	var boxes []image.Rectangle
	for _, b := range dbBoxes(out.data, pw, ph) {
		r := image.Rect(
			int(float64(b.Min.X)/scale), int(float64(b.Min.Y)/scale),
			int(math.Ceil(float64(b.Max.X)/scale)), int(math.Ceil(float64(b.Max.Y)/scale)),
		).Intersect(img.Rect)
		if r.Dx() >= detMinSize && r.Dy() >= detMinSize {
			boxes = append(boxes, r)
		}
	}
	return boxes, nil
}

// dbBoxes 是 DB（可微分二值化）检测的后处理：概率图二值化后取连通区域，
// 平均概率够高的区域按比例往外扩一圈，得到文字框（概率图坐标）。
func dbBoxes(prob []float32, w, h int) []image.Rectangle {
	visited := make([]bool, w*h)
	var boxes []image.Rectangle
	stack := make([]int, 0, 1024)
	for start := range prob {
		if visited[start] || prob[start] <= detThresh {
			continue
		}
		// 四邻域泛洪，统计外接矩形与平均概率。
		x0, y0, x1, y1 := w, h, -1, -1
		var sum float64
		n := 0
		stack = append(stack[:0], start)
		visited[start] = true
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			x, y := i%w, i/w
			x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x), max(y1, y)
			sum += float64(prob[i])
			n++
			for _, j := range [4]int{i - 1, i + 1, i - w, i + w} {
				if j < 0 || j >= len(prob) || visited[j] || prob[j] <= detThresh {
					continue
				}
				// 左右邻居不能跨行。
				if (j == i-1 && x == 0) || (j == i+1 && x == w-1) {
					continue
				}
				visited[j] = true
				stack = append(stack, j)
			}
		}
		bw, bh := x1-x0+1, y1-y0+1
		if min(bw, bh) < detMinSize || sum/float64(n) < detBoxThresh {
			continue
		}
		// 训练时文字区域按 (1-r²)·面积/周长 收缩过，这里按 面积·比例/周长 扩回去。
		d := int(math.Round(float64(bw*bh) * detUnclipRatio / float64(2*(bw+bh))))
		boxes = append(boxes, image.Rect(x0-d, y0-d, x1+1+d, y1+1+d).Intersect(image.Rect(0, 0, w, h)))
	}
	return boxes
}

// recPiece 是一段待识别的行图片（已缩放到识别高度）。超长的行会切成几段，owner 指向所属的文字框。
type recPiece struct {
	owner int
	img   *image.RGBA
}

// recognizeBoxes 识别全部文字框，返回每个框的文字与平均置信度。
// 各段按宽度排序后成批推理，同一批补齐到最宽的那段，补出来的部分不参与解码。
func (p *Paddle) recognizeBoxes(img *image.RGBA, boxes []image.Rectangle, progress func(done, total int)) ([]string, []float64, error) {
	var pieces []recPiece
	for i, box := range boxes {
		scale := float64(recHeight) / float64(box.Dy())
		line := resize(subImage(img, box), scale)
		// 按识别宽度上限等宽切段，切出来的段按顺序拼回去。
		for x := 0; x < line.Rect.Dx(); x += recMaxWidth {
			pieces = append(pieces, recPiece{owner: i,
				img: subImage(line, image.Rect(x, 0, min(x+recMaxWidth, line.Rect.Dx()), line.Rect.Dy()))})
		}
	}
	order := make([]int, len(pieces))
	for i := range order {
		order[i] = i
	}
	sortInts(order, func(a, b int) bool { return pieces[a].img.Rect.Dx() < pieces[b].img.Rect.Dx() })

	texts := make([]string, len(pieces))
	scores := make([]float64, len(pieces))
	for start := 0; start < len(order); start += recBatch {
		batch := order[start:min(start+recBatch, len(order))]
		if err := p.recognizeBatch(pieces, batch, texts, scores); err != nil {
			return nil, nil, err
		}
		if progress != nil {
			progress(start+len(batch), len(order))
		}
	}

	outText := make([]string, len(boxes))
	outScore := make([]float64, len(boxes))
	counts := make([]int, len(boxes))
	for i, pc := range pieces {
		outText[pc.owner] += texts[i]
		outScore[pc.owner] += scores[i]
		counts[pc.owner]++
	}
	for i := range outScore {
		if counts[i] > 0 {
			outScore[i] /= float64(counts[i])
		}
	}
	return outText, outScore, nil
}

// recognizeBatch 把一批行图片补齐到同一宽度后一次推理，结果写回 texts、scores 的对应位置。
func (p *Paddle) recognizeBatch(pieces []recPiece, batch []int, texts []string, scores []float64) error {
	maxW := 16
	for _, i := range batch {
		maxW = max(maxW, pieces[i].img.Rect.Dx())
	}
	n, h := len(batch), recHeight
	input := make([]float32, n*3*h*maxW)
	plane := h * maxW
	for bi, i := range batch {
		line := pieces[i].img
		base := bi * 3 * plane
		lh := min(h, line.Rect.Dy())
		for y := 0; y < lh; y++ {
			for x := 0; x < line.Rect.Dx(); x++ {
				px := line.Pix[y*line.Stride+x*4:]
				o := base + y*maxW + x
				input[o] = (float32(px[2])/255 - 0.5) / 0.5
				input[plane+o] = (float32(px[1])/255 - 0.5) / 0.5
				input[2*plane+o] = (float32(px[0])/255 - 0.5) / 0.5
			}
		}
	}
	out, err := p.rec.run(input, []int64{int64(n), 3, int64(h), int64(maxW)})
	if err != nil {
		return fmt.Errorf("文字识别失败：%w", err)
	}
	if len(out.shape) != 3 || int(out.shape[0]) != n {
		return fmt.Errorf("识别模型输出形状不对：%v", out.shape)
	}
	steps, classes := int(out.shape[1]), int(out.shape[2])
	for bi, i := range batch {
		// 只解码这一段实际宽度对应的时间步，补齐出来的灰色区域不算。
		used := min(steps, (pieces[i].img.Rect.Dx()+recStride-1)/recStride+1)
		row := out.data[bi*steps*classes : (bi*steps+used)*classes]
		texts[i], scores[i] = ctcDecode(row, used, classes, p.chars)
	}
	return nil
}

// ctcDecode 对识别模型的输出做 CTC 贪心解码：每个时间步取概率最大的字符，
// 去掉空白符（下标 0）与连续重复，返回文字与所保留字符的平均概率。
// 字典之外多出的最后一类是空格（官方 use_space_char）；
// 模型漏掉的词间空格按字符间的空白时间步数补上，见 spaceGapSteps。
func ctcDecode(probs []float32, steps, classes int, chars []string) (string, float64) {
	var out []rune
	var sum float64
	kept := 0
	prev, lastActive := -1, -1
	for t := 0; t < steps; t++ {
		row := probs[t*classes : (t+1)*classes]
		best, bestP := 0, row[0]
		for c := 1; c < classes; c++ {
			if row[c] > bestP {
				best, bestP = c, row[c]
			}
		}
		if best != 0 && best != prev {
			ch := " "
			if best < len(chars) {
				ch = chars[best]
			}
			r := []rune(ch)
			if len(out) > 0 && len(r) > 0 && t-lastActive-1 >= spaceGapSteps && needSpace(out[len(out)-1], r[0]) {
				out = append(out, ' ')
			}
			out = append(out, r...)
			sum += float64(bestP)
			kept++
		}
		if best != 0 {
			lastActive = t
		}
		prev = best
	}
	if kept == 0 {
		return "", 0
	}
	return string(out), sum / float64(kept)
}

// needSpace 判断间隔较大的两个相邻字符之间是否该补空格：至少一边是西文，两边都不是标点或空格。
func needSpace(a, b rune) bool {
	if a == ' ' || b == ' ' || isCJKPunct(a) || isCJKPunct(b) || isASCIIPunct(a) || isASCIIPunct(b) {
		return false
	}
	return !(isCJK(a) && isCJK(b))
}

// isASCIIPunct 判断是否是半角标点。
func isASCIIPunct(r rune) bool {
	return r < 0x80 && (r >= '!' && r <= '/' || r >= ':' && r <= '@' || r >= '[' && r <= '`' || r >= '{' && r <= '~')
}

// sortInts 用插入排序整理下标（一张截图的文字行最多几百行）。
func sortInts(a []int, less func(x, y int) bool) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && less(a[j], a[j-1]); j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// roundUp32 把 v 向上取整到 32 的倍数（至少 32）。
func roundUp32(v int) int {
	return max(32, (v+31)/32*32)
}
