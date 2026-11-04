package capture

import (
	"bytes"
	"hash/maphash"
	"image"
)

// 长截图拼接。
//
// 输入是同一块屏幕区域在竖向滚动过程中、画面已经停稳的一系列截图。相邻两帧之间：
//   - 顶部、底部若干行不动（吸顶导航、聊天窗的输入框），称为固定区域；
//   - 左右两侧也可能有不动的列（固定的侧边栏），不参与比较；
//   - 中间区域整体上移了 dy 行（内容往下滚了 dy 行）。
//
// 拼接就是求出 dy，再把新帧中间区域最下面的 dy 行接到长图末尾。
// 求 dy 时每行切成若干段分别算指纹：滚动条滑块、闪烁的光标、鼠标悬停高亮
// 这类局部变化只会弄脏其中一段，不至于让整行都对不上。

const (
	// DefaultMaxHeight 是长图的默认高度上限（像素）。
	DefaultMaxHeight = 30000

	// segmentWidth 是每段的目标宽度；段数在 1 到 maxSegments 之间。
	segmentWidth = 64
	maxSegments  = 8
	// segmentMatchRatio 是一行中「非纯色段」至少有多大比例完全一致，才算这一行对上了。
	segmentMatchRatio = 0.75
	// rowMatchRatio 是重叠区中参与比较的行至少有多大比例对上，才相信这个位移。
	rowMatchRatio = 0.85
	// minComparedRows 是重叠区中至少要有多少行参与比较（两边都是纯色的行不算）。
	minComparedRows = 6
	// minMiddleRows 是中间滚动区至少要有的行数，再少就无从比较。
	minMiddleRows = 16
	// maxHashRepeat：同一个段指纹出现在超过这么多行时不参与投票，
	// 例如表格的竖线，每行都一样，投出来的票既多又没有区分度。
	maxHashRepeat = 8
	// candidateCount 是投票后进入逐行校验的候选位移个数。
	candidateCount = 4
	// plainTolerance 是判定一段为「纯色」时允许的灰度起伏。
	plainTolerance = 4
	// maxScrollbarTrim 是每一侧最多当作滚动条剥掉的列宽（像素），200% 缩放下的滚动条也在此之内。
	maxScrollbarTrim = 48
)

// AddStatus 表示一帧加入拼接器后的结果。
type AddStatus int

const (
	// Appended 表示长图向下延长了。
	Appended AddStatus = iota
	// Unchanged 表示画面没有向下滚动（静止、只有局部变化或往回滚），本帧被忽略。
	Unchanged
	// NoMatch 表示找不到可信的对齐位置（滚得太快或内容变化太大），本帧被丢弃。
	NoMatch
	// Full 表示长图已经达到高度上限，不再接收新内容。
	Full
)

// Stitcher 把一系列竖向滚动的区域截图拼成一张长图。
// 不是并发安全的，应只在一个协程里使用。
type Stitcher struct {
	w, h      int
	maxHeight int
	seed      maphash.Seed

	// prev 是最近一次被采纳的帧，下一帧总是和它比较。
	// 往回滚的帧不会被采纳，因此来回滚动也能接上。
	prev *image.RGBA

	// aligned 表示已经成功拼接过至少一次。此前 body 里是完整的首帧（含底栏）。
	aligned bool
	// bottom 是 prev 那一帧的底部固定区高度：body 的最后一行对应 prev 的第 h-bottom-1 行。
	bottom int

	// body 是已拼好的行（不含底部固定区），每行 w*4 字节连续存放。
	body     []byte
	bodyRows int
	// foot 是最近一帧的底部固定区，成图时接在最下面。
	foot []byte
	full bool
}

// NewStitcher 创建拼接器，maxHeight 是长图的高度上限，不大于 0 时使用 DefaultMaxHeight。
func NewStitcher(maxHeight int) *Stitcher {
	if maxHeight <= 0 {
		maxHeight = DefaultMaxHeight
	}
	return &Stitcher{maxHeight: maxHeight, seed: maphash.MakeSeed()}
}

// Height 返回当前长图的总高度。
func (s *Stitcher) Height() int {
	if s.w == 0 {
		return 0
	}
	return s.bodyRows + len(s.foot)/(s.w*4)
}

// Width 返回长图宽度，尚未加入任何帧时为 0。
func (s *Stitcher) Width() int {
	return s.w
}

// Add 加入一帧，返回结果状态与本次新增的行数。
// 所有帧的尺寸必须与第一帧相同，尺寸不符的帧按 NoMatch 丢弃。
func (s *Stitcher) Add(frame *image.RGBA) (AddStatus, int) {
	w, h := frame.Rect.Dx(), frame.Rect.Dy()
	if s.prev == nil {
		if w <= 0 || h <= 0 {
			return NoMatch, 0
		}
		s.w, s.h = w, h
		s.prev = frame
		s.body = make([]byte, 0, w*4*h*4)
		for y := 0; y < h; y++ {
			s.body = append(s.body, row(frame, y, w)...)
		}
		s.bodyRows = h
		return Appended, h
	}
	if w != s.w || h != s.h {
		return NoMatch, 0
	}
	if s.full {
		return Full, 0
	}

	c0, c1, changed := changedColumns(s.prev, frame, w, h)
	if !changed {
		return Unchanged, 0
	}
	edges := pairEdges{raw0: c0, raw1: c1}
	c0, c1 = trimScrollbars(s.prev, frame, c0, c1, h)
	edges.c0, edges.c1 = c0, c1

	n := (c1 - c0) / segmentWidth
	n = max(1, min(n, maxSegments))
	a := signature(s.prev, c0, c1, n, s.seed)
	b := signature(frame, c0, c1, n, s.seed)

	// 固定区每次都重新估计：空白行恰好对齐时会被多算进去，但只是暂时的，下一帧就纠正了。
	top, bottom := staticBands(&a, &b, h)
	dy, ok, tried := 0, false, false
	for _, m := range matchRegions(&b, top, bottom, h) {
		if m[1]-m[0] < minMiddleRows {
			continue
		}
		tried = true
		if dy, ok = findOffset(&a, &b, m[0], m[1]); ok {
			break
		}
	}
	if !tried {
		// 几乎整块都没动，说明只是局部变化，或者这块区域根本不能滚动。
		return Unchanged, 0
	}
	if !ok {
		// 光标闪烁、悬停高亮、动图这类局部变化也会走到这里，它们不是「滚太快」。
		if localChange(&a, &b, h) {
			return Unchanged, 0
		}
		return NoMatch, 0
	}
	if dy <= 0 {
		return Unchanged, 0
	}
	edges.top, edges.dy = top, dy
	edges.headerEnd = top
	for edges.headerEnd > 0 && rowAllPlain(&b, edges.headerEnd-1) {
		edges.headerEnd--
	}
	return s.accept(frame, dy, bottom, edges)
}

// pairEdges 记录一次成功对齐时的参数，用于清理新接上的行里不随内容滚动的列。
type pairEdges struct {
	// raw0、raw1 是两帧之间有变化的列区间；c0、c1 是从两端剥掉滚动条之后参与比较的区间。
	// [raw0, c0) 与 [c1, raw1) 就是「变过、但被当作滚动条剥掉」的列。
	raw0, raw1 int
	c0, c1     int
	top        int
	dy         int
	// headerEnd 是顶部固定区去掉内侧空白行之后的下沿：固定区可能多估了几行恰好对齐的空白，
	// 涂首屏滚动条时要从真正的顶栏下沿开始，否则会漏掉几行滑块。
	headerEnd int
}

// scrollbarColumn 判断第 x 列是否是被当作滚动条剥掉的列。
func (e pairEdges) scrollbarColumn(x int) bool {
	return (x >= e.raw0 && x < e.c0) || (x >= e.c1 && x < e.raw1)
}

// accept 采纳一帧：把新帧里紧接在 body 末尾之后、底部固定区之前的行接上去。
//
// body 的最后一行对应 prev 的第 h-s.bottom-1 行，也就是新帧的第 h-s.bottom-1-dy 行，
// 所以新帧从 start = h-s.bottom-dy 行起是新内容，一直到新帧的固定区边界 h-bottom。
// 这一帧估出的底栏比上一帧高时，边界会落在 start 之前，多出来的那几行改由 foot 承担，
// 从 body 末尾退掉即可。
func (s *Stitcher) accept(frame *image.RGBA, dy, bottom int, edges pairEdges) (AddStatus, int) {
	first := !s.aligned
	if first {
		// 第一次成功拼接时才知道首帧的底栏有多高：body 里只有首帧，去掉它的底栏，
		// 底栏改由 foot 保存，成图时接到最下面。
		s.aligned = true
		s.bottom = bottom
		s.bodyRows = s.h - bottom
		s.body = s.body[:s.bodyRows*s.w*4]
	}

	stride := s.w * 4
	start := s.h - s.bottom - dy
	end := s.h - bottom
	status := Appended
	added := 0
	if end < start {
		drop := min(start-end, s.bodyRows)
		s.bodyRows -= drop
		s.body = s.body[:s.bodyRows*stride]
	} else {
		if s.bodyRows+(end-start)+bottom >= s.maxHeight {
			end = start + max(s.maxHeight-bottom-s.bodyRows, 0)
			s.full = true
			status = Full
		}
		for y := start; y < end; y++ {
			s.body = append(s.body, row(frame, y, s.w)...)
		}
		added = end - start
		s.bodyRows += added
		s.fillStaticColumns(s.prev, frame, bottom, edges, added, first)
	}

	s.foot = s.foot[:0]
	for y := s.h - bottom; y < s.h; y++ {
		s.foot = append(s.foot, row(frame, y, s.w)...)
	}
	s.bottom = bottom
	s.prev = frame
	return status, added
}

// fillStaticColumns 把刚接上的 rows 行里「不随内容滚动」的列涂成该列的底色。
//
// 两侧没参与比较的列可能是固定侧边栏或滚动条，它们不随内容移动，原样接进来会在长图边上
// 留下一截截重复的侧边栏、滑块与箭头按钮。判断方法是用已求出的位移逐列核对：
// 跟着内容滚动的列满足 b[y] == a[y+dy]（贴边的实心色块也满足，不会被误涂），不满足的才涂掉。
//
// 第一次对齐时（firstPair），滚动条的列连首屏固定栏以下的部分也一起涂掉，长图里就不会留着首屏的滑块和箭头；
// 固定侧边栏两帧之间根本没变，不算滚动条，首屏里的那一份保留。
func (s *Stitcher) fillStaticColumns(a, b *image.RGBA, bottom int, e pairEdges, rows int, firstPair bool) {
	if rows <= 0 {
		return
	}
	stride := s.w * 4
	from := s.bodyRows - rows
	for x := 0; x < s.w; x++ {
		if x >= e.c0 && x < e.c1 {
			continue
		}
		if followsScroll(a, b, x, e.top, s.h-bottom-e.dy, e.dy) {
			continue
		}
		start := from
		if firstPair && e.scrollbarColumn(x) {
			start = e.headerEnd
		}
		c := columnMode(b, x, s.h)
		for r := start; r < s.bodyRows; r++ {
			copy(s.body[r*stride+x*4:r*stride+x*4+4], c[:])
		}
	}
}

// followsScroll 判断第 x 列在重叠区内是否随内容一起滚动了 dy 行：b 的第 y 行等于 a 的第 y+dy 行。
// 允许个别像素不符（光标、抗锯齿），超过 2 行对不上才算不随内容滚动。
func followsScroll(a, b *image.RGBA, x, y0, y1, dy int) bool {
	bad := 0
	for y := y0; y < y1; y++ {
		pa := a.Pix[a.PixOffset(a.Rect.Min.X+x, a.Rect.Min.Y+y+dy):]
		pb := b.Pix[b.PixOffset(b.Rect.Min.X+x, b.Rect.Min.Y+y):]
		if pa[0] != pb[0] || pa[1] != pb[1] || pa[2] != pb[2] {
			bad++
			if bad > 2 {
				return false
			}
		}
	}
	return true
}

// columnMode 返回第 x 列出现次数最多的颜色，作为这一列的底色。
func columnMode(img *image.RGBA, x, h int) [4]byte {
	counts := map[[4]byte]int{}
	var best [4]byte
	bestN := 0
	for y := 0; y < h; y++ {
		p := img.Pix[img.PixOffset(img.Rect.Min.X+x, img.Rect.Min.Y+y):]
		c := [4]byte{p[0], p[1], p[2], p[3]}
		counts[c]++
		if counts[c] > bestN {
			best, bestN = c, counts[c]
		}
	}
	return best
}

// Image 返回拼好的长图。尚未加入任何帧时返回 nil。
func (s *Stitcher) Image() *image.RGBA {
	total := s.Height()
	if total == 0 {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, s.w, total))
	n := copy(img.Pix, s.body[:s.bodyRows*s.w*4])
	copy(img.Pix[n:], s.foot)
	return img
}

// Thumbnail 把当前长图按宽度 width 等比缩小，只返回最下面不超过 maxHeight 行，
// 供预览面板实时显示。按区域取平均缩放，文字缩小后不会满屏噪点。
func (s *Stitcher) Thumbnail(width, maxHeight int) *image.RGBA {
	total := s.Height()
	if total == 0 || width <= 0 || maxHeight <= 0 {
		return nil
	}
	scale := float64(width) / float64(s.w)
	if scale > 1 {
		scale = 1
		width = s.w
	}
	thumbH := int(float64(total) * scale)
	thumbH = max(1, min(thumbH, maxHeight))
	// 只取长图最下面那一截。
	srcTop := total - int(float64(thumbH)/scale)
	srcTop = max(srcTop, 0)

	img := image.NewRGBA(image.Rect(0, 0, width, thumbH))
	footStart := s.bodyRows
	rowAt := func(y int) []byte {
		if y < footStart {
			return s.body[y*s.w*4 : (y+1)*s.w*4]
		}
		off := (y - footStart) * s.w * 4
		return s.foot[off : off+s.w*4]
	}
	for ty := 0; ty < thumbH; ty++ {
		y0 := srcTop + int(float64(ty)/scale)
		y1 := srcTop + int(float64(ty+1)/scale)
		y1 = max(y0+1, min(y1, total))
		for tx := 0; tx < width; tx++ {
			x0 := int(float64(tx) / scale)
			x1 := int(float64(tx+1) / scale)
			x1 = max(x0+1, min(x1, s.w))
			var r, g, b, cnt int
			for y := y0; y < y1; y++ {
				src := rowAt(y)
				for x := x0; x < x1; x++ {
					r += int(src[x*4])
					g += int(src[x*4+1])
					b += int(src[x*4+2])
					cnt++
				}
			}
			i := ty*img.Stride + tx*4
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] =
				uint8(r/cnt), uint8(g/cnt), uint8(b/cnt), 255
		}
	}
	return img
}

// row 返回图片第 y 行（相对图片左上角）的 w 个像素。
func row(img *image.RGBA, y, w int) []byte {
	off := img.PixOffset(img.Rect.Min.X, img.Rect.Min.Y+y)
	return img.Pix[off : off+w*4]
}

// changedColumns 找出两帧同一位置上有变化的列，返回包住它们的最小列区间 [c0, c1)。
// 两侧不动的列（固定侧边栏）由此被排除在比较之外；两帧完全相同时 changed 为 false。
func changedColumns(a, b *image.RGBA, w, h int) (c0, c1 int, changed bool) {
	c0, c1 = w, 0
	for y := 0; y < h; y++ {
		ra, rb := row(a, y, w), row(b, y, w)
		if bytes.Equal(ra, rb) {
			continue
		}
		for x := 0; x < w; x++ {
			i := x * 4
			if ra[i] != rb[i] || ra[i+1] != rb[i+1] || ra[i+2] != rb[i+2] {
				c0 = min(c0, x)
				break
			}
		}
		for x := w - 1; x >= 0; x-- {
			i := x * 4
			if ra[i] != rb[i] || ra[i+1] != rb[i+1] || ra[i+2] != rb[i+2] {
				c1 = max(c1, x+1)
				break
			}
		}
	}
	return c0, c1, c1 > c0
}

// trimScrollbars 从列区间两端剥掉像滚动条的窄列带，返回剩下的区间。
//
// 滚动条滑块挪了位置之后，它所在的列在两帧同一位置上只会变出一两段连续的行
// （旧位置、新位置）；真正的内容列随滚动整体换掉，会变出很多段。
// 从两端往里，变化段数不超过 2 的列一律剥掉，最多剥 maxScrollbarTrim 宽，避免误伤大片内容。
func trimScrollbars(a, b *image.RGBA, c0, c1, h int) (int, int) {
	limit := min(maxScrollbarTrim, (c1-c0)/6)
	for n := 0; n < limit && c1-c0 > minMiddleRows && changedRuns(a, b, c1-1, h) <= 2; n++ {
		c1--
	}
	for n := 0; n < limit && c1-c0 > minMiddleRows && changedRuns(a, b, c0, h) <= 2; n++ {
		c0++
	}
	return c0, c1
}

// changedRuns 统计第 x 列在两帧同一位置上「有变化的行」构成了几段连续区间。
func changedRuns(a, b *image.RGBA, x, h int) int {
	runs := 0
	inRun := false
	for y := 0; y < h; y++ {
		pa := a.Pix[a.PixOffset(a.Rect.Min.X+x, a.Rect.Min.Y+y):]
		pb := b.Pix[b.PixOffset(b.Rect.Min.X+x, b.Rect.Min.Y+y):]
		diff := pa[0] != pb[0] || pa[1] != pb[1] || pa[2] != pb[2]
		if diff && !inRun {
			runs++
		}
		inRun = diff
	}
	return runs
}

// rowSig 是一帧在列区间 [c0, c1) 内各行的分段指纹。
type rowSig struct {
	// n 是每行的段数。
	n int
	// hash[y*n+k] 是第 y 行第 k 段的指纹。
	hash []uint64
	// plain[y*n+k] 表示第 y 行第 k 段是否近乎纯色。
	plain []bool
}

// signature 计算一帧在列区间 [c0, c1) 内每行每段的指纹与纯色标记。
func signature(img *image.RGBA, c0, c1, n int, seed maphash.Seed) rowSig {
	h := img.Rect.Dy()
	sig := rowSig{n: n, hash: make([]uint64, h*n), plain: make([]bool, h*n)}
	span := c1 - c0
	for y := 0; y < h; y++ {
		r := row(img, y, c1)
		for k := 0; k < n; k++ {
			x0 := c0 + span*k/n
			x1 := c0 + span*(k+1)/n
			seg := r[x0*4 : x1*4]
			sig.hash[y*n+k] = maphash.Bytes(seed, seg)
			sig.plain[y*n+k] = isPlain(seg)
		}
	}
	return sig
}

// isPlain 判断一段像素是否近乎纯色：灰度最大最小值相差不超过 plainTolerance。
// 纯色段（空白、纯色背景、竖向渐变）没有区分度，不参与位移的判断。
func isPlain(seg []byte) bool {
	lo, hi := 255, 0
	for i := 0; i+3 < len(seg); i += 4 {
		g := (int(seg[i])*77 + int(seg[i+1])*150 + int(seg[i+2])*29) >> 8
		lo, hi = min(lo, g), max(hi, g)
		if hi-lo > plainTolerance {
			return false
		}
	}
	return true
}

// rowsMatch 比较 a 的第 ya 行与 b 的第 yb 行。
// compared 为 false 表示两行的各段在两边都是纯色、没有比较价值；
// 否则 matched 表示非纯色段中是否有足够比例完全一致。
func rowsMatch(a, b *rowSig, ya, yb int) (compared, matched bool) {
	n := a.n
	considered, equal := 0, 0
	for k := 0; k < n; k++ {
		ia, ib := ya*n+k, yb*n+k
		if a.plain[ia] && b.plain[ib] {
			continue
		}
		considered++
		if a.hash[ia] == b.hash[ib] {
			equal++
		}
	}
	if considered == 0 {
		return false, false
	}
	return true, float64(equal) >= segmentMatchRatio*float64(considered)
}

// sameRow 判断两帧同一位置的一行是否「没动」，用于识别固定区域。
// 纯色行要求各段完全相同，避免白底行和灰底行被当成同一行。
func sameRow(a, b *rowSig, y int) bool {
	compared, matched := rowsMatch(a, b, y, y)
	if compared {
		return matched
	}
	for k := 0; k < a.n; k++ {
		if a.hash[y*a.n+k] != b.hash[y*b.n+k] {
			return false
		}
	}
	return true
}

// localChange 判断两帧之间是否只是局部变化：同一位置上变了的行不到四分之一。
// 真正的滚动会让几乎所有带内容的行都换掉，局部变化只影响少数几行。
func localChange(a, b *rowSig, h int) bool {
	changed := 0
	for y := 0; y < h; y++ {
		if !sameRow(a, b, y) {
			changed++
		}
	}
	return changed*4 < h
}

// rowAllPlain 判断一行的各段是否都是纯色。
func rowAllPlain(s *rowSig, y int) bool {
	for k := 0; k < s.n; k++ {
		if !s.plain[y*s.n+k] {
			return false
		}
	}
	return true
}

// staticBands 识别两帧之间顶部、底部不动的行数。
//
// 恰好对齐的空白行也会被算进来。拼接时估多了不要紧：内容反正会从中间区域经过一次，
// 不会丢；估少了才有害——把固定栏的留白当成滚动内容接进长图，后面的内容就错位了。
func staticBands(a, b *rowSig, h int) (top, bottom int) {
	for top < h && sameRow(a, b, top) {
		top++
	}
	for bottom < h-top && sameRow(a, b, h-1-bottom) {
		bottom++
	}
	return top, bottom
}

// matchRegions 返回求位移时依次尝试的中间区域 [m0, m1)。
//
// 先用固定区原样划出的范围；不行再把固定区内侧挨着的纯色行退还给中间区域再试一次。
// 两种情况各有对错：固定栏自带的留白若被退还，比对时必然对不上；
// 恰好对齐的空白内容若留在固定区里，重叠区又会被压得太小。都试一遍，取先成功的那个。
// 拼接本身始终按原样的固定区来接，见 accept。
func matchRegions(b *rowSig, top, bottom, h int) [][2]int {
	regions := [][2]int{{top, h - bottom}}
	t2, b2 := top, bottom
	for t2 > 0 && rowAllPlain(b, t2-1) {
		t2--
	}
	for b2 > 0 && rowAllPlain(b, h-b2) {
		b2--
	}
	if t2 != top || b2 != bottom {
		regions = append(regions, [2]int{t2, h - b2})
	}
	return regions
}

// findOffset 在中间区域 [m0, m1) 内求 b 相对 a 的上移量 dy，满足 b[y] ≈ a[y+dy]。
//
// 先用分段指纹投票选出几个候选位移，再逐行校验重叠区，取对上比例最高的一个。
// 找不到可信的位移时 ok 为 false。
func findOffset(a, b *rowSig, m0, m1 int) (dy int, ok bool) {
	n := a.n
	span := m1 - m0

	// 以 a 的非纯色段建立「指纹 → 行号」索引；同一指纹混入段号，不同列的同样内容不会串票。
	index := make(map[uint64][]int32, span*n)
	for y := m0; y < m1; y++ {
		for k := 0; k < n; k++ {
			i := y*n + k
			if a.plain[i] {
				continue
			}
			key := a.hash[i] ^ (uint64(k+1) * 0x9E3779B97F4A7C15)
			index[key] = append(index[key], int32(y))
		}
	}

	// votes[d+span] 记录位移 d 得到的票数，d 的范围是 (-span, span)。
	votes := make([]int, 2*span+1)
	for y := m0; y < m1; y++ {
		for k := 0; k < n; k++ {
			i := y*n + k
			if b.plain[i] {
				continue
			}
			key := b.hash[i] ^ (uint64(k+1) * 0x9E3779B97F4A7C15)
			list := index[key]
			if len(list) == 0 || len(list) > maxHashRepeat {
				continue
			}
			for _, ya := range list {
				votes[int(ya)-y+span]++
			}
		}
	}

	bestRatio, bestCompared := -1.0, 0
	for _, d := range topCandidates(votes, span, candidateCount) {
		compared, matched := 0, 0
		for y := m0; y < m1; y++ {
			ya := y + d
			if ya < m0 || ya >= m1 {
				continue
			}
			if c, m := rowsMatch(a, b, ya, y); c {
				compared++
				if m {
					matched++
				}
			}
		}
		if compared < minComparedRows {
			continue
		}
		ratio := float64(matched) / float64(compared)
		if ratio < rowMatchRatio {
			continue
		}
		// 比例相当时取参与比较行数更多的，也就是重叠更大、更可信的那个。
		if ratio > bestRatio+0.02 || (ratio > bestRatio-0.02 && compared > bestCompared) {
			bestRatio, bestCompared, dy = ratio, compared, d
			ok = true
		}
	}
	return dy, ok
}

// topCandidates 返回票数最高的至多 k 个位移（票数为 0 的不算）。
func topCandidates(votes []int, span, k int) []int {
	var picked []int
	for len(picked) < k {
		best, bestVotes := 0, 0
		for i, v := range votes {
			if v <= bestVotes {
				continue
			}
			d := i - span
			if containsInt(picked, d) {
				continue
			}
			best, bestVotes = d, v
		}
		if bestVotes == 0 {
			break
		}
		picked = append(picked, best)
	}
	return picked
}

// containsInt 判断切片中是否包含指定元素。
func containsInt(list []int, v int) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
