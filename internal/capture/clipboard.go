package capture

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/win"
)

var procRegisterClipboardFormat = user32.NewProc("RegisterClipboardFormatW")

// gmemMoveable 对应 GMEM_MOVEABLE，剪贴板数据必须用可移动内存。
const gmemMoveable = 0x0002

// CopyImage 把图片放进剪贴板，同时提供 CF_DIB 与 PNG 两种格式：
// 大多数程序认 CF_DIB；Office、浏览器等支持 PNG 的程序会优先取 PNG，画质无损。
//
// owner 必须是一个有效的窗口句柄——以空句柄打开剪贴板时，系统会让后续写入失败。
func CopyImage(owner win.HWND, img *image.RGBA) error {
	dib := encodeDIB(img)
	var pngBuf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&pngBuf, img); err != nil {
		return fmt.Errorf("编码 PNG 失败：%w", err)
	}

	if err := openClipboard(owner); err != nil {
		return err
	}
	defer win.CloseClipboard()

	if !win.EmptyClipboard() {
		return errors.New("清空剪贴板失败")
	}
	if err := setClipboardBytes(win.CF_DIB, dib); err != nil {
		return err
	}
	if name, err := syscall.UTF16PtrFromString("PNG"); err == nil {
		if format, _, _ := procRegisterClipboardFormat.Call(uintptr(unsafe.Pointer(name))); format != 0 {
			// PNG 只是锦上添花，写不进去也不影响 CF_DIB 的粘贴。
			_ = setClipboardBytes(uint32(format), pngBuf.Bytes())
		}
	}
	return nil
}

// openClipboard 打开剪贴板。别的程序可能正占着它，因此失败时短暂重试几次。
func openClipboard(owner win.HWND) error {
	for i := 0; i < 10; i++ {
		if win.OpenClipboard(owner) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("剪贴板正被其他程序占用，请稍后再试")
}

// setClipboardBytes 把一段字节以指定格式写入已打开的剪贴板。
func setClipboardBytes(format uint32, data []byte) error {
	mem := win.GlobalAlloc(gmemMoveable, uintptr(len(data)))
	if mem == 0 {
		return errors.New("分配剪贴板内存失败")
	}
	p := win.GlobalLock(mem)
	if p == nil {
		win.GlobalFree(mem)
		return errors.New("锁定剪贴板内存失败")
	}
	copy(unsafe.Slice((*byte)(p), len(data)), data)
	win.GlobalUnlock(mem)

	if win.SetClipboardData(format, win.HANDLE(mem)) == 0 {
		win.GlobalFree(mem)
		return errors.New("写入剪贴板失败")
	}
	// 写入成功后内存归剪贴板所有，不能再释放。
	return nil
}

// encodeDIB 把图片编码成 CF_DIB 格式：BITMAPINFOHEADER 加 24 位自底向上的像素数据。
// 选 24 位而不是 32 位，是因为有些老程序会把 32 位 DIB 的 Alpha 当真，粘贴出来一片透明。
func encodeDIB(img *image.RGBA) []byte {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	stride := (w*3 + 3) &^ 3
	var hdr win.BITMAPINFOHEADER
	hdrSize := int(unsafe.Sizeof(hdr))
	hdr.BiSize = uint32(hdrSize)
	hdr.BiWidth = int32(w)
	hdr.BiHeight = int32(h)
	hdr.BiPlanes = 1
	hdr.BiBitCount = 24
	hdr.BiCompression = win.BI_RGB
	hdr.BiSizeImage = uint32(stride * h)

	buf := make([]byte, hdrSize+stride*h)
	copy(buf, unsafe.Slice((*byte)(unsafe.Pointer(&hdr)), hdrSize))
	for y := 0; y < h; y++ {
		src := img.Pix[y*img.Stride:]
		dst := buf[hdrSize+(h-1-y)*stride:]
		for x := 0; x < w; x++ {
			dst[x*3], dst[x*3+1], dst[x*3+2] = src[x*4+2], src[x*4+1], src[x*4]
		}
	}
	return buf
}
