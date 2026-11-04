// Package ocr 调用 Windows 自带的文字识别（Windows.Media.Ocr）识别图片里的文字，完全离线。
//
// 识别语言取当前用户的系统语言（例如简体中文识别器，它也认英文字母和数字）。
// 中文识别结果默认在每个字之间带空格，这里按文字框的间距重新拼接，去掉不该有的空格。
package ocr

import (
	"errors"
	"image"
	"math"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iidIOcrEngineStatics = windows.GUID{Data1: 0x5bffa85a, Data2: 0x3384, Data3: 0x3540,
		Data4: [8]byte{0x99, 0x40, 0x69, 0x91, 0x20, 0xd4, 0x28, 0xa8}}
	iidISoftwareBitmapStatics = windows.GUID{Data1: 0xdf0385db, Data2: 0x672f, Data3: 0x4a9d,
		Data4: [8]byte{0x80, 0x6e, 0xc2, 0x44, 0x2f, 0x34, 0x3e, 0x86}}
	iidICryptographicBufferStatics = windows.GUID{Data1: 0x320b7e22, Data2: 0x3cb0, Data3: 0x4cdf,
		Data4: [8]byte{0x86, 0x63, 0x1d, 0x28, 0x91, 0x00, 0x65, 0xeb}}
)

const (
	// bitmapPixelFormatBgra8 对应 BitmapPixelFormat.Bgra8。
	bitmapPixelFormatBgra8 = 87
	// recognizeTimeout 是单块图片识别的超时时间。
	recognizeTimeout = 30 * time.Second
	// minSide 是送去识别的图片最短边：太小的图（例如只框了一行字）先放大，识别率高得多。
	minSide = 64
	// tilePixels 是每块（放大后）的像素预算，块越大识别器的精度越差，见 planTiles。
	tilePixels = 6_000_000
	// minTileRows 是每块在原图上至少包含的行数，避免切得过碎。
	minTileRows = 200
)

// ErrNoLanguage 表示系统里没有可用的 OCR 识别语言。
var ErrNoLanguage = errors.New("系统里没有可用的文字识别语言，请在「设置 → 时间和语言 → 语言和区域」里添加中文或英文语言包")

// Line 是识别出的一行文字。
type Line struct {
	// Text 是这一行的文字。
	Text string
	// Rect 是这一行在原图中的位置。
	Rect image.Rectangle
}

// Result 是一次识别的结果。
type Result struct {
	// Text 是全部文字，行与行之间用换行分隔。
	Text string
	// Lines 是逐行结果。
	Lines []Line
	// Language 是所用识别器的语言，如 zh-Hans-CN。
	Language string
}

// Recognize 识别图片中的文字。可以在任意协程调用：内部会在一个锁定的系统线程上完成整个识别过程。
func Recognize(img *image.RGBA) (*Result, error) {
	type outcome struct {
		res *Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		res, err := recognize(img)
		ch <- outcome{res, err}
	}()
	o := <-ch
	return o.res, o.err
}

// session 持有一次识别用到的 WinRT 对象。
type session struct {
	engine   *comObj
	buffers  *comObj
	bitmaps  *comObj
	maxDim   int
	language string
}

// recognize 在当前（已锁定的）线程上执行识别。
func recognize(img *image.RGBA) (*Result, error) {
	if img == nil || img.Rect.Empty() {
		return nil, errors.New("没有可识别的图片")
	}
	needUninit, err := roInit()
	if err != nil {
		return nil, err
	}
	if needUninit {
		defer roUninit()
	}

	s, err := openSession()
	if err != nil {
		return nil, err
	}
	defer s.close()

	res := &Result{Language: s.language}
	for _, t := range planTiles(img, s.maxDim) {
		lines, err := s.recognizeTile(t)
		if err != nil {
			return nil, err
		}
		res.Lines = append(res.Lines, lines...)
	}
	texts := make([]string, len(res.Lines))
	for i, l := range res.Lines {
		texts[i] = l.Text
	}
	res.Text = strings.Join(texts, "\n")
	return res, nil
}

// openSession 创建识别引擎以及构造位图所需的静态工厂。
func openSession() (*session, error) {
	statics, err := activationFactory("Windows.Media.Ocr.OcrEngine", &iidIOcrEngineStatics)
	if err != nil {
		return nil, err
	}
	defer statics.release()

	s := &session{maxDim: 10000}
	// IOcrEngineStatics：get_MaxImageDimension(6) get_AvailableRecognizerLanguages(7)
	// IsLanguageSupported(8) TryCreateFromLanguage(9) TryCreateFromUserProfileLanguages(10)
	var maxDim uint32
	if hr := statics.call(inspectableBase, uintptr(unsafe.Pointer(&maxDim))); !failed(hr) && maxDim > 0 {
		s.maxDim = int(maxDim)
	}
	if hr := statics.call(inspectableBase+4, uintptr(unsafe.Pointer(&s.engine))); failed(hr) {
		return nil, hrError("创建识别引擎", hr)
	}
	if s.engine == nil {
		return nil, ErrNoLanguage
	}
	// IOcrEngine：RecognizeAsync(6) get_RecognizerLanguage(7)；ILanguage：get_LanguageTag(6)
	var lang *comObj
	if hr := s.engine.call(inspectableBase+1, uintptr(unsafe.Pointer(&lang))); !failed(hr) && lang != nil {
		s.language, _ = getString(lang, inspectableBase)
		lang.release()
	}

	if s.buffers, err = activationFactory("Windows.Security.Cryptography.CryptographicBuffer", &iidICryptographicBufferStatics); err != nil {
		s.close()
		return nil, err
	}
	if s.bitmaps, err = activationFactory("Windows.Graphics.Imaging.SoftwareBitmap", &iidISoftwareBitmapStatics); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

// close 释放会话持有的对象。
func (s *session) close() {
	s.bitmaps.release()
	s.buffers.release()
	s.engine.release()
}

// tile 是送去识别的一块图片，以及它与原图坐标的换算关系。
type tile struct {
	img *image.RGBA
	// offsetY 是这一块在原图中的起始行。
	offsetY int
	// scale 是这一块相对原图的缩放比例。
	scale float64
}

// recognizeTile 识别一块图片，返回按原图坐标换算后的各行文字。
func (s *session) recognizeTile(t tile) ([]Line, error) {
	w, h := t.img.Rect.Dx(), t.img.Rect.Dy()
	bgra := toBGRA(t.img)

	var buffer *comObj
	// ICryptographicBufferStatics.CreateFromByteArray(9)：把字节数组包装成 IBuffer。
	if hr := s.buffers.call(inspectableBase+3, uintptr(len(bgra)), uintptr(unsafe.Pointer(&bgra[0])),
		uintptr(unsafe.Pointer(&buffer))); failed(hr) {
		return nil, hrError("准备图片数据", hr)
	}
	defer buffer.release()
	runtime.KeepAlive(bgra)

	var bitmap *comObj
	// ISoftwareBitmapStatics.CreateCopyFromBuffer(9)：source, format, width, height。
	if hr := s.bitmaps.call(inspectableBase+3, uintptr(unsafe.Pointer(buffer)), bitmapPixelFormatBgra8,
		uintptr(w), uintptr(h), uintptr(unsafe.Pointer(&bitmap))); failed(hr) {
		return nil, hrError("创建位图", hr)
	}
	defer bitmap.release()

	var op *comObj
	if hr := s.engine.call(inspectableBase, uintptr(unsafe.Pointer(bitmap)), uintptr(unsafe.Pointer(&op))); failed(hr) {
		return nil, hrError("开始识别", hr)
	}
	defer op.release()

	result, err := awaitOperation(op, recognizeTimeout)
	if err != nil {
		return nil, err
	}
	defer result.release()
	return readLines(result, t)
}

// ocrRect 对应 Windows.Foundation.Rect。
type ocrRect struct {
	X, Y, Width, Height float32
}

// word 是识别出的一个词（中文通常是一个字）。
type word struct {
	text string
	rect ocrRect
}

// readLines 从 OcrResult 中逐行逐词读出文字与位置。
func readLines(result *comObj, t tile) ([]Line, error) {
	// IOcrResult：get_Lines(6) get_TextAngle(7) get_Text(8)
	var linesObj *comObj
	if hr := result.call(inspectableBase, uintptr(unsafe.Pointer(&linesObj))); failed(hr) {
		return nil, hrError("读取识别结果", hr)
	}
	lines := vectorView{linesObj}
	defer lines.release()
	n, err := lines.size()
	if err != nil {
		return nil, err
	}

	var out []Line
	for i := uint32(0); i < n; i++ {
		lineObj, err := lines.at(i)
		if err != nil {
			return nil, err
		}
		words, err := readWords(lineObj)
		lineObj.release()
		if err != nil {
			return nil, err
		}
		if len(words) == 0 {
			continue
		}
		text, r := joinWords(words)
		out = append(out, Line{Text: text, Rect: toImageRect(r, t)})
	}
	return mergeRows(out), nil
}

// mergeRows 把同一水平位置上的碎片合并成一行，并按从上到下的阅读顺序排列。
//
// 识别器常在全角标点或稍大的字距处把一整行拆成几段，还按区块分组返回，直接拼起来顺序是乱的。
// 这里按纵向重叠把碎片归到同一行，行内按横坐标排序后再拼。
func mergeRows(lines []Line) []Line {
	if len(lines) < 2 {
		return lines
	}
	sorted := append([]Line(nil), lines...)
	sortLines(sorted, func(a, b Line) bool {
		return a.Rect.Min.Y+a.Rect.Max.Y < b.Rect.Min.Y+b.Rect.Max.Y
	})

	var rows [][]Line
	for _, l := range sorted {
		if n := len(rows); n > 0 && sameRow(rowRect(rows[n-1]), l.Rect) {
			rows[n-1] = append(rows[n-1], l)
			continue
		}
		rows = append(rows, []Line{l})
	}

	out := make([]Line, 0, len(rows))
	for _, row := range rows {
		sortLines(row, func(a, b Line) bool { return a.Rect.Min.X < b.Rect.Min.X })
		words := make([]word, len(row))
		for i, l := range row {
			words[i] = word{text: l.Text, rect: ocrRect{
				X: float32(l.Rect.Min.X), Y: float32(l.Rect.Min.Y),
				Width: float32(l.Rect.Dx()), Height: float32(l.Rect.Dy()),
			}}
		}
		text, _ := joinWords(words)
		out = append(out, Line{Text: text, Rect: rowRect(row)})
	}
	return out
}

// sameRow 判断矩形 b 是否与行 a 在同一水平位置：纵向重叠超过两者较矮者高度的一半。
func sameRow(a, b image.Rectangle) bool {
	overlap := min(a.Max.Y, b.Max.Y) - max(a.Min.Y, b.Min.Y)
	return overlap*2 > min(a.Dy(), b.Dy())
}

// rowRect 返回一行里所有碎片的外接矩形。
func rowRect(row []Line) image.Rectangle {
	r := row[0].Rect
	for _, l := range row[1:] {
		r = r.Union(l.Rect)
	}
	return r
}

// sortLines 用插入排序整理行（每块图片的行数很少，没必要引入 sort 包的闭包开销）。
func sortLines(lines []Line, less func(a, b Line) bool) {
	for i := 1; i < len(lines); i++ {
		for j := i; j > 0 && less(lines[j], lines[j-1]); j-- {
			lines[j], lines[j-1] = lines[j-1], lines[j]
		}
	}
}

// readWords 读出一行里的全部词。
func readWords(line *comObj) ([]word, error) {
	// IOcrLine：get_Words(6) get_Text(7)
	var wordsObj *comObj
	if hr := line.call(inspectableBase, uintptr(unsafe.Pointer(&wordsObj))); failed(hr) {
		return nil, hrError("读取词语", hr)
	}
	words := vectorView{wordsObj}
	defer words.release()
	n, err := words.size()
	if err != nil {
		return nil, err
	}
	out := make([]word, 0, n)
	for i := uint32(0); i < n; i++ {
		w, err := words.at(i)
		if err != nil {
			return nil, err
		}
		// IOcrWord：get_BoundingRect(6) get_Text(7)
		var r ocrRect
		w.call(inspectableBase, uintptr(unsafe.Pointer(&r)))
		text, err := getString(w, inspectableBase+1)
		w.release()
		if err != nil {
			return nil, err
		}
		out = append(out, word{text: text, rect: r})
	}
	return out, nil
}

// joinWords 把一行里的词拼成文字。
//
// 两边都是西文时一律加空格；只要有一边是中日韩文字，就看两个文字框之间的空隙：
// 空隙超过字高的三分之一才算真的有空格，否则紧挨着拼（去掉识别器在每个汉字之间插的空格）。
func joinWords(words []word) (string, ocrRect) {
	var b strings.Builder
	r := words[0].rect
	b.WriteString(words[0].text)
	for i := 1; i < len(words); i++ {
		prev, cur := words[i-1], words[i]
		last, _ := utf8.DecodeLastRuneInString(prev.text)
		first, _ := utf8.DecodeRuneInString(cur.text)
		gap := cur.rect.X - (prev.rect.X + prev.rect.Width)
		height := math.Max(float64(prev.rect.Height), float64(cur.rect.Height))
		switch {
		case isCJKPunct(last) || isCJKPunct(first):
			// 全角标点自带间距，前后都不需要空格。
		case !isCJK(last) && !isCJK(first):
			b.WriteByte(' ')
		case float64(gap) > height/3:
			b.WriteByte(' ')
		}
		b.WriteString(cur.text)
		r = unionRect(r, cur.rect)
	}
	return b.String(), r
}

// isCJK 判断字符是否是中日韩文字或全角标点。
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r) || (r >= 0x3000 && r <= 0x303F) || (r >= 0xFF00 && r <= 0xFFEF)
}

// isCJKPunct 判断字符是否是全角标点（，。、；：！？「」等）。
func isCJKPunct(r rune) bool {
	return (r >= 0x3000 && r <= 0x303F) || (r >= 0xFF01 && r <= 0xFF0F) || (r >= 0xFF1A && r <= 0xFF20) ||
		(r >= 0xFF3B && r <= 0xFF40) || (r >= 0xFF5B && r <= 0xFF65)
}

// unionRect 返回两个矩形的并集。
func unionRect(a, b ocrRect) ocrRect {
	x0 := min(a.X, b.X)
	y0 := min(a.Y, b.Y)
	x1 := max(a.X+a.Width, b.X+b.Width)
	y1 := max(a.Y+a.Height, b.Y+b.Height)
	return ocrRect{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}
}

// toImageRect 把识别块内的坐标换算回原图坐标。
func toImageRect(r ocrRect, t tile) image.Rectangle {
	inv := 1 / t.scale
	return image.Rect(
		int(float64(r.X)*inv), int(float64(r.Y)*inv)+t.offsetY,
		int(math.Ceil(float64(r.X+r.Width)*inv)), int(math.Ceil(float64(r.Y+r.Height)*inv))+t.offsetY)
}

// toBGRA 把 RGBA 像素转成 OCR 需要的 BGRA 字节序列，并强制不透明。
func toBGRA(img *image.RGBA) []byte {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	out := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		src := img.Pix[img.PixOffset(img.Rect.Min.X, img.Rect.Min.Y+y):]
		dst := out[y*w*4:]
		for x := 0; x < w; x++ {
			dst[x*4], dst[x*4+1], dst[x*4+2], dst[x*4+3] = src[x*4+2], src[x*4+1], src[x*4], 255
		}
	}
	return out
}
