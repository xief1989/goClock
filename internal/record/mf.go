package record

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Media Foundation 的最小绑定：用 Sink Writer 把 RGB32 帧编码成 H.264，写进 MP4。
// 系统自带 H.264 编码器（能用显卡就用显卡），不需要额外下载任何东西。
// 接口指针一律通过输出参数接收（*comObj），保证 go vet 干净、GC 安全。

var (
	mfplat      = windows.NewLazySystemDLL("mfplat.dll")
	mfreadwrite = windows.NewLazySystemDLL("mfreadwrite.dll")
	ole32       = windows.NewLazySystemDLL("ole32.dll")

	procMFStartup                 = mfplat.NewProc("MFStartup")
	procMFShutdown                = mfplat.NewProc("MFShutdown")
	procMFCreateMediaType         = mfplat.NewProc("MFCreateMediaType")
	procMFCreateSample            = mfplat.NewProc("MFCreateSample")
	procMFCreateMemoryBuffer      = mfplat.NewProc("MFCreateMemoryBuffer")
	procMFCreateAttributes        = mfplat.NewProc("MFCreateAttributes")
	procMFCreateSinkWriterFromURL = mfreadwrite.NewProc("MFCreateSinkWriterFromURL")
	procCoInitializeEx            = ole32.NewProc("CoInitializeEx")
	procCoUninitialize            = ole32.NewProc("CoUninitialize")
)

const (
	// mfVersion 对应 MF_VERSION（MF_SDK_VERSION 0x0002 << 16 | MF_API_VERSION 0x0070）。
	mfVersion             = 0x00020070
	mfStartupFull         = 0
	coinitMultithreaded   = 0
	videoInterlaceProgres = 2 // MFVideoInterlace_Progressive
	rpcEChangedMode       = 0x80010106
)

// guid 从字段构造 GUID。
func guid(d1 uint32, d2, d3 uint16, d4 ...byte) windows.GUID {
	g := windows.GUID{Data1: d1, Data2: d2, Data3: d3}
	copy(g.Data4[:], d4)
	return g
}

// 用到的 Media Foundation 属性与类型编号。
var (
	mfMTMajorType       = guid(0x48eba18e, 0xf8c9, 0x4687, 0xbf, 0x11, 0x0a, 0x74, 0xc9, 0xf9, 0x6a, 0x8f)
	mfMTSubtype         = guid(0xf7e34c9a, 0x42e8, 0x4714, 0xb7, 0x4b, 0xcb, 0x29, 0xd7, 0x2c, 0x35, 0xe5)
	mfMTAvgBitrate      = guid(0x20332624, 0xfb0d, 0x4d9e, 0xbd, 0x0d, 0xcb, 0xf6, 0x78, 0x6c, 0x10, 0x2e)
	mfMTInterlaceMode   = guid(0xe2724bb8, 0xe676, 0x4806, 0xb4, 0xb2, 0xa8, 0xd6, 0xef, 0xb4, 0x4c, 0xcd)
	mfMTFrameSize       = guid(0x1652c33d, 0xd6b2, 0x4012, 0xb8, 0x34, 0x72, 0x03, 0x08, 0x49, 0xa3, 0x7d)
	mfMTFrameRate       = guid(0xc459a2e8, 0x3d2c, 0x4e44, 0xb1, 0x32, 0xfe, 0xe5, 0x15, 0x6c, 0x7b, 0xb0)
	mfMTPixelAspect     = guid(0xc6376a1e, 0x8d0a, 0x4027, 0xbe, 0x45, 0x6d, 0x9a, 0x0a, 0xd3, 0x9b, 0xb6)
	mfMTDefaultStride   = guid(0x644b4e48, 0x1e02, 0x4516, 0xb0, 0xeb, 0xc0, 0x1c, 0xa9, 0xd4, 0x9a, 0xc6)
	mfMediaTypeVideo    = guid(0x73646976, 0x0000, 0x0010, 0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71)
	mfVideoFormatH264   = guid(0x34363248, 0x0000, 0x0010, 0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71)
	mfVideoFormatRGB32  = guid(0x00000016, 0x0000, 0x0010, 0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71)
	mfReadwriteEnableHW = guid(0xa634a91c, 0x822b, 0x41b9, 0xa4, 0x94, 0x4d, 0xe4, 0x64, 0x36, 0x12, 0xb0)
)

// 虚表下标。IMFAttributes 在 IUnknown（0~2）之后依次排列，IMFMediaType、IMFSample 都继承它。
const (
	attrSetUINT32 = 21
	attrSetUINT64 = 22
	attrSetGUID   = 24

	sampleSetSampleTime     = 36
	sampleSetSampleDuration = 38
	sampleAddBuffer         = 42

	bufferLock             = 3
	bufferUnlock           = 4
	bufferSetCurrentLength = 6

	writerAddStream         = 3
	writerSetInputMediaType = 4
	writerBeginWriting      = 5
	writerWriteSample       = 6
	writerFinalize          = 11
)

// comObj 对应一个 COM 对象：首字段是指向虚表的指针。
type comObj struct {
	vtbl *[64]uintptr
}

// call 调用虚表第 i 个方法，返回 HRESULT。
//
// go:uintptrescapes 不能省：参数里的 uintptr(unsafe.Pointer(&x)) 经过这一层中转才到 SyscallN，
// 编译器只对直接写在 SyscallN 参数里的转换做保护。没有这条指示，栈上的输出变量可能因栈扩容搬家，
// C 代码把结果写进旧地址；有了它，x 一律分配在堆上并在调用期间存活，与 x/sys/windows 的 Proc.Call 同理。
//
//go:uintptrescapes
func (o *comObj) call(i int, args ...uintptr) uintptr {
	all := make([]uintptr, 0, len(args)+1)
	all = append(all, uintptr(unsafe.Pointer(o)))
	all = append(all, args...)
	r, _, _ := syscall.SyscallN(o.vtbl[i], all...)
	return r
}

// release 释放对象引用，nil 时什么也不做。
func (o *comObj) release() {
	if o != nil {
		o.call(2)
	}
}

// failed 判断 HRESULT 是否表示失败。
func failed(hr uintptr) bool {
	return int32(uint32(hr)) < 0
}

// check 把失败的 HRESULT 转成带说明的错误。
func check(what string, hr uintptr) error {
	if failed(hr) {
		return fmt.Errorf("%s失败（HRESULT 0x%08X）", what, uint32(hr))
	}
	return nil
}

// setGUID 设置一个 GUID 类型的属性。
func setGUID(o *comObj, key, value *windows.GUID) error {
	return check("设置媒体属性", o.call(attrSetGUID, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(value))))
}

// setUint32 设置一个 UINT32 类型的属性。
func setUint32(o *comObj, key *windows.GUID, v uint32) error {
	return check("设置媒体属性", o.call(attrSetUINT32, uintptr(unsafe.Pointer(key)), uintptr(v)))
}

// setRatio 设置一个由两个 UINT32 拼成的 UINT64 属性（帧尺寸、帧率、像素宽高比都是这种格式）。
func setRatio(o *comObj, key *windows.GUID, hi, lo uint32) error {
	return check("设置媒体属性", o.call(attrSetUINT64, uintptr(unsafe.Pointer(key)), uintptr(hi)<<32|uintptr(lo)))
}

// encoder 把一帧帧画面编码写入 MP4 文件。必须在同一个锁定的系统线程上创建、使用和关闭。
type encoder struct {
	writer   *comObj
	stream   uint32
	frameLen int
	coInit   bool
	mfInit   bool
}

// newEncoder 创建编码器，开始向 path 写 MP4。宽高必须是偶数。
func newEncoder(path string, width, height, fps, bitrate int) (*encoder, error) {
	e := &encoder{frameLen: width * height * 4}
	ok := false
	defer func() {
		if !ok {
			e.close()
		}
	}()

	hr, _, _ := procCoInitializeEx.Call(0, coinitMultithreaded)
	switch {
	case !failed(hr):
		e.coInit = true
	case uint32(hr) != rpcEChangedMode:
		return nil, check("初始化 COM ", hr)
	}
	if hr, _, _ := procMFStartup.Call(mfVersion, mfStartupFull); failed(hr) {
		return nil, check("启动 Media Foundation ", hr)
	}
	e.mfInit = true

	var attrs *comObj
	if hr, _, _ := procMFCreateAttributes.Call(uintptr(unsafe.Pointer(&attrs)), 1); failed(hr) {
		return nil, check("创建属性 ", hr)
	}
	defer attrs.release()
	// 允许使用显卡的硬件编码器，录全屏时 CPU 占用低很多。
	if err := setUint32(attrs, &mfReadwriteEnableHW, 1); err != nil {
		return nil, err
	}

	u, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	if hr, _, _ := procMFCreateSinkWriterFromURL.Call(uintptr(unsafe.Pointer(u)), 0,
		uintptr(unsafe.Pointer(attrs)), uintptr(unsafe.Pointer(&e.writer))); failed(hr) {
		return nil, check("创建视频文件 ", hr)
	}

	out, err := newVideoType(&mfVideoFormatH264, width, height, fps)
	if err != nil {
		return nil, err
	}
	defer out.release()
	if err := setUint32(out, &mfMTAvgBitrate, uint32(bitrate)); err != nil {
		return nil, err
	}
	if err := check("添加视频流", e.writer.call(writerAddStream, uintptr(unsafe.Pointer(out)),
		uintptr(unsafe.Pointer(&e.stream)))); err != nil {
		return nil, err
	}

	in, err := newVideoType(&mfVideoFormatRGB32, width, height, fps)
	if err != nil {
		return nil, err
	}
	defer in.release()
	// 步长为正表示自上而下的行序，与抓屏得到的位图一致；不设的话 RGB32 默认按自下而上处理，视频会上下颠倒。
	if err := setUint32(in, &mfMTDefaultStride, uint32(width*4)); err != nil {
		return nil, err
	}
	if err := check("设置输入格式", e.writer.call(writerSetInputMediaType, uintptr(e.stream),
		uintptr(unsafe.Pointer(in)), 0)); err != nil {
		return nil, err
	}
	if err := check("开始写入", e.writer.call(writerBeginWriting)); err != nil {
		return nil, err
	}
	ok = true
	return e, nil
}

// newVideoType 创建一个视频媒体类型：给定子类型、尺寸与帧率，逐行扫描、像素宽高比 1:1。
func newVideoType(subtype *windows.GUID, width, height, fps int) (*comObj, error) {
	var mt *comObj
	if hr, _, _ := procMFCreateMediaType.Call(uintptr(unsafe.Pointer(&mt))); failed(hr) {
		return nil, check("创建媒体类型 ", hr)
	}
	steps := []func() error{
		func() error { return setGUID(mt, &mfMTMajorType, &mfMediaTypeVideo) },
		func() error { return setGUID(mt, &mfMTSubtype, subtype) },
		func() error { return setUint32(mt, &mfMTInterlaceMode, videoInterlaceProgres) },
		func() error { return setRatio(mt, &mfMTFrameSize, uint32(width), uint32(height)) },
		func() error { return setRatio(mt, &mfMTFrameRate, uint32(fps), 1) },
		func() error { return setRatio(mt, &mfMTPixelAspect, 1, 1) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			mt.release()
			return nil, err
		}
	}
	return mt, nil
}

// writeFrame 写入一帧。bgra 是自上而下、每行 width*4 字节的像素；时间单位是 100 纳秒。
func (e *encoder) writeFrame(bgra []byte, timestamp, duration int64) error {
	if len(bgra) != e.frameLen {
		return fmt.Errorf("帧大小不对：%d != %d", len(bgra), e.frameLen)
	}
	var buf *comObj
	if hr, _, _ := procMFCreateMemoryBuffer.Call(uintptr(e.frameLen), uintptr(unsafe.Pointer(&buf))); failed(hr) {
		return check("分配帧缓冲 ", hr)
	}
	defer buf.release()

	var data *byte
	if err := check("锁定帧缓冲", buf.call(bufferLock, uintptr(unsafe.Pointer(&data)), 0, 0)); err != nil {
		return err
	}
	copy(unsafe.Slice(data, e.frameLen), bgra)
	buf.call(bufferUnlock)
	if err := check("设置帧长度", buf.call(bufferSetCurrentLength, uintptr(e.frameLen))); err != nil {
		return err
	}

	var sample *comObj
	if hr, _, _ := procMFCreateSample.Call(uintptr(unsafe.Pointer(&sample))); failed(hr) {
		return check("创建帧 ", hr)
	}
	defer sample.release()
	if err := check("添加帧数据", sample.call(sampleAddBuffer, uintptr(unsafe.Pointer(buf)))); err != nil {
		return err
	}
	if err := check("设置时间戳", sample.call(sampleSetSampleTime, uintptr(timestamp))); err != nil {
		return err
	}
	if err := check("设置帧时长", sample.call(sampleSetSampleDuration, uintptr(duration))); err != nil {
		return err
	}
	return check("写入帧", e.writer.call(writerWriteSample, uintptr(e.stream), uintptr(unsafe.Pointer(sample))))
}

// finish 收尾：把编码器里剩余的数据写完并补上 MP4 索引，然后释放资源。
func (e *encoder) finish() error {
	var err error
	if e.writer != nil {
		err = check("完成写入", e.writer.call(writerFinalize))
	}
	e.close()
	return err
}

// close 释放资源（不补写索引，文件可能不完整）。重复调用是安全的。
func (e *encoder) close() {
	if e.writer != nil {
		e.writer.release()
		e.writer = nil
	}
	if e.mfInit {
		procMFShutdown.Call()
		e.mfInit = false
	}
	if e.coInit {
		procCoUninitialize.Call()
		e.coInit = false
	}
}
