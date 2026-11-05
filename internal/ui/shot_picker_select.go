package ui

import (
	"image"
	"strings"
	"unicode"

	"goclock/internal/ocr"
)

// 提取文字窗口的选字逻辑：把识别结果整理成按阅读顺序排列的字，负责命中、选区与取出选中的文字。
// 不依赖窗口，坐标一律是原图坐标。

const (
	// spanGapRatio：同一行里两个字的间距超过字高的这么多倍，就分成两段（表格的两格、并排的两个窗格）。
	// 正常的词距、全角标点的留白都不到一个字高。
	spanGapRatio = 1.5
	// hitRowWeight 是兜底按距离命中时纵向距离的权重：鼠标在行尾右边空白处时仍算这一行，而不是跳到下一行。
	hitRowWeight = 8
	// hitRowReach：鼠标左边没有字、右边同一行的段离鼠标不超过段高的这么多倍时（行首缩进）仍算这一行，
	// 再远就当作另一栏，改找鼠标正上下方的段。
	hitRowReach = 2
)

// pickGlyph 是可以选中的一个字。
type pickGlyph struct {
	r    rune
	rect image.Rectangle
	// line 是所在的行（识别结果 Lines 的下标），span 是所在的段（textPicker.spans 的下标）。
	line int
	span int
	// gap 表示这是段末尾、隔开两段的空格：它占着两段之间的整片空白，不算进段的范围，也不画高亮。
	gap bool
}

// pickSpan 是一行里连续的一段文字，glyphs[from:to] 属于它。
type pickSpan struct {
	from, to int
	rect     image.Rectangle
}

// pickHit 是鼠标命中的位置：caret 是插入点（在第 caret 个字之前），span 是命中的段。
type pickHit struct {
	caret int
	span  int
}

// textPicker 保存全部可选的字与当前选区。
//
// 选区从按下处（anchor）到当前处（focus），按阅读顺序取中间的字。多栏界面里同一行会拼进好几栏的文字，
// 所以中间经过的段只有与起止两段在横向上重叠时才算选中：在左栏往下拖只选左栏，不会夹进右栏。
type textPicker struct {
	glyphs []pickGlyph
	spans  []pickSpan
	anchor pickHit
	focus  pickHit
	// active 表示有选区；all 表示全选（不受分栏限制）。
	active bool
	all    bool
	// bandMin、bandMax 是起止两段的横向范围，中间经过的段要与它重叠才算选中。
	bandMin, bandMax int
}

// newTextPicker 由识别结果的各行建立选字模型；缺逐字位置的行按行框等分估算。
func newTextPicker(lines []ocr.Line) *textPicker {
	tp := &textPicker{}
	for li, l := range lines {
		runes := []rune(l.Text)
		glyphs := l.Glyphs
		if len(glyphs) != len(runes) {
			glyphs = evenGlyphs(l.Rect, len(runes))
		}
		spanStart, lastSolid := len(tp.glyphs), -1
		for i, r := range runes {
			g := glyphs[i]
			if !unicode.IsSpace(r) {
				if lastSolid >= 0 {
					prev := tp.glyphs[lastSolid].rect
					if float64(g.Min.X-prev.Max.X) > spanGapRatio*float64(max(prev.Dy(), g.Dy())) {
						// 中间的空格留在前一段末尾，新段从这个字开始。
						tp.closeSpan(spanStart)
						spanStart = len(tp.glyphs)
					}
				}
				lastSolid = len(tp.glyphs)
			}
			tp.glyphs = append(tp.glyphs, pickGlyph{r: r, rect: g, line: li})
		}
		tp.closeSpan(spanStart)
	}
	return tp
}

// closeSpan 把 glyphs[from:] 收成一段；末尾的空格标成分隔空格，段的范围只算到最后一个字。
func (tp *textPicker) closeSpan(from int) {
	if from >= len(tp.glyphs) {
		return
	}
	s := pickSpan{from: from, to: len(tp.glyphs), rect: tp.glyphs[from].rect}
	end := s.to
	for end > from+1 && unicode.IsSpace(tp.glyphs[end-1].r) {
		end--
	}
	for i := from; i < s.to; i++ {
		tp.glyphs[i].span = len(tp.spans)
		if i < end {
			s.rect = s.rect.Union(tp.glyphs[i].rect)
		} else {
			tp.glyphs[i].gap = true
		}
	}
	tp.spans = append(tp.spans, s)
}

// evenGlyphs 把行框按字数等分，给缺逐字位置的行兜底。
func evenGlyphs(r image.Rectangle, n int) []image.Rectangle {
	out := make([]image.Rectangle, n)
	for i := range out {
		out[i] = image.Rect(r.Min.X+r.Dx()*i/n, r.Min.Y, r.Min.X+r.Dx()*(i+1)/n, r.Max.Y)
	}
	return out
}

// empty 表示没有识别出任何字。
func (tp *textPicker) empty() bool {
	return len(tp.spans) == 0
}

// onText 判断点 p 是否落在文字上（用来切换成文本光标）。
func (tp *textPicker) onText(p image.Point) bool {
	for _, s := range tp.spans {
		if p.In(s.rect) {
			return true
		}
	}
	return false
}

// hit 找到点 p 对应的段，再按横坐标定出插入点：落在字的左半边算字前，右半边算字后。没有字时返回零值。
//
// 依次找：同一高度上 p 所在或 p 左边最近的段（行内、行尾右侧）；p 正上下方最近的段（拖到一栏末尾下面的空白处
// 仍算这一栏，不跳到旁边同高的另一栏）；都没有时按距离找最近的段。
func (tp *textPicker) hit(p image.Point) pickHit {
	if tp.empty() {
		return pickHit{}
	}
	best := tp.rowSpan(p)
	if best < 0 {
		if best = tp.columnSpan(p); best >= 0 {
			// 在这一段正上方算段首，正下方算段尾，与编辑器里拖到行外的习惯一致。
			s := tp.spans[best]
			if p.Y < s.rect.Min.Y {
				return pickHit{caret: s.from, span: best}
			}
			return pickHit{caret: s.to, span: best}
		}
		best = tp.nearestSpan(p)
	}
	s := tp.spans[best]
	caret := s.to
	for i := s.from; i < s.to; i++ {
		g := tp.glyphs[i].rect
		if p.X < (g.Min.X+g.Max.X)/2 {
			caret = i
			break
		}
	}
	return pickHit{caret: caret, span: best}
}

// rowSpan 在与 p 同高的段里找：p 落在其中或在它右边的段取最靠右的一个；p 左边没有段时，
// 右边最近的段离得不远（行首缩进）才算。找不到返回 -1。
func (tp *textPicker) rowSpan(p image.Point) int {
	left, right, rightGap := -1, -1, 0
	for i, s := range tp.spans {
		if p.Y < s.rect.Min.Y || p.Y >= s.rect.Max.Y {
			continue
		}
		switch {
		case s.rect.Min.X <= p.X:
			if left < 0 || s.rect.Min.X > tp.spans[left].rect.Min.X {
				left = i
			}
		case right < 0 || s.rect.Min.X-p.X < rightGap:
			right, rightGap = i, s.rect.Min.X-p.X
		}
	}
	if left >= 0 {
		return left
	}
	if right >= 0 && rightGap <= hitRowReach*tp.spans[right].rect.Dy() {
		return right
	}
	return -1
}

// columnSpan 在横向范围盖住 p 的段里找纵向最近的一个，找不到返回 -1。
func (tp *textPicker) columnSpan(p image.Point) int {
	best, bestDy := -1, 0
	for i, s := range tp.spans {
		if p.X < s.rect.Min.X || p.X >= s.rect.Max.X {
			continue
		}
		if dy := max(0, s.rect.Min.Y-p.Y, p.Y-s.rect.Max.Y+1); best < 0 || dy < bestDy {
			best, bestDy = i, dy
		}
	}
	return best
}

// nearestSpan 按「横向距离 + 加权纵向距离」找最近的段。
func (tp *textPicker) nearestSpan(p image.Point) int {
	best, bestScore := 0, -1
	for i, s := range tp.spans {
		dx := max(0, s.rect.Min.X-p.X, p.X-s.rect.Max.X+1)
		dy := max(0, s.rect.Min.Y-p.Y, p.Y-s.rect.Max.Y+1)
		if score := dx + hitRowWeight*dy; bestScore < 0 || score < bestScore {
			best, bestScore = i, score
		}
	}
	return best
}

// begin 在 h 处开始一次选择（鼠标按下）。
func (tp *textPicker) begin(h pickHit) {
	tp.anchor, tp.all = h, false
	tp.extend(h)
}

// extend 把选区的另一端移到 h（拖动、Shift+单击）。
func (tp *textPicker) extend(h pickHit) {
	if tp.empty() {
		return
	}
	tp.focus, tp.all = h, false
	tp.active = tp.anchor.caret != tp.focus.caret
	a, f := tp.spans[tp.anchor.span].rect, tp.spans[tp.focus.span].rect
	tp.bandMin, tp.bandMax = min(a.Min.X, f.Min.X), max(a.Max.X, f.Max.X)
}

// selectSpan 选中 h 所在的整段（双击）。
func (tp *textPicker) selectSpan(h pickHit) {
	s := tp.spans[h.span]
	tp.begin(pickHit{caret: s.from, span: h.span})
	tp.extend(pickHit{caret: s.to, span: h.span})
}

// selectAll 全选。
func (tp *textPicker) selectAll() {
	tp.active, tp.all = !tp.empty(), !tp.empty()
}

// clear 取消选区。
func (tp *textPicker) clear() {
	tp.active, tp.all = false, false
}

// hasSelection 表示当前有选中的字。
func (tp *textPicker) hasSelection() bool {
	return tp.active
}

// selectedRange 返回选区覆盖的字的下标范围 [lo, hi)。
func (tp *textPicker) selectedRange() (int, int) {
	switch {
	case tp.all:
		return 0, len(tp.glyphs)
	case !tp.active:
		return 0, 0
	}
	return min(tp.anchor.caret, tp.focus.caret), max(tp.anchor.caret, tp.focus.caret)
}

// selected 判断第 i 个字是否选中。
func (tp *textPicker) selected(i int) bool {
	lo, hi := tp.selectedRange()
	if i < lo || i >= hi {
		return false
	}
	if tp.all {
		return true
	}
	s := tp.glyphs[i].span
	if s == tp.anchor.span || s == tp.focus.span {
		return true
	}
	r := tp.spans[s].rect
	return r.Max.X > tp.bandMin && r.Min.X < tp.bandMax
}

// highlights 返回选中部分的高亮矩形：同一段里连续选中的字合成一块。
func (tp *textPicker) highlights() []image.Rectangle {
	lo, hi := tp.selectedRange()
	var out []image.Rectangle
	cur, curSpan := image.Rectangle{}, -1
	for i := lo; i < hi; i++ {
		if !tp.selected(i) || tp.glyphs[i].gap {
			continue
		}
		g := tp.glyphs[i]
		if g.span == curSpan && i > 0 && tp.selected(i-1) {
			cur = cur.Union(g.rect)
			continue
		}
		if curSpan >= 0 {
			out = append(out, cur)
		}
		cur, curSpan = g.rect, g.span
	}
	if curSpan >= 0 {
		out = append(out, cur)
	}
	return out
}

// selectedText 取出选中的文字：同一行里被跳过的段之间补一个空格，行与行之间换行，每行去掉首尾空白。
func (tp *textPicker) selectedText() string {
	lo, hi := tp.selectedRange()
	var lines []string
	var cur []rune
	line, last := -1, -1
	flush := func() {
		if s := strings.TrimSpace(string(cur)); s != "" {
			lines = append(lines, s)
		}
		cur = cur[:0]
	}
	for i := lo; i < hi; i++ {
		if !tp.selected(i) {
			continue
		}
		g := tp.glyphs[i]
		switch {
		case g.line != line:
			flush()
			line = g.line
		case last != i-1 && len(cur) > 0 && !unicode.IsSpace(cur[len(cur)-1]) && !unicode.IsSpace(g.r):
			cur = append(cur, ' ')
		}
		cur = append(cur, g.r)
		last = i
	}
	flush()
	return strings.Join(lines, "\n")
}
