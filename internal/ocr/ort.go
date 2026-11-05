package ocr

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ONNX Runtime C 接口的最小绑定（对应 onnxruntime 1.22.0，ORT_API_VERSION 22）。
//
// C 接口是一张函数指针表（struct OrtApi），下标取自官方头文件 onnxruntime_c_api.h 的声明顺序，
// 这张表只追加不改动，新版本运行库同样兼容。对象一律通过输出参数接收，不从 uintptr 转换。

// ortAPIVersion 是本绑定所用头文件的接口版本。
const ortAPIVersion = 22

// OrtApi 函数表下标。
const (
	ortGetErrorMessage                  = 2
	ortCreateEnv                        = 3
	ortDisableTelemetryEvents           = 6
	ortCreateSession                    = 7
	ortRun                              = 9
	ortCreateSessionOptions             = 10
	ortDisableMemPattern                = 17
	ortDisableCpuMemArena               = 19
	ortSetSessionGraphOptimizationLevel = 23
	ortSetIntraOpNumThreads             = 24
	ortSessionGetInputCount             = 30
	ortSessionGetOutputCount            = 31
	ortSessionGetInputName              = 36
	ortSessionGetOutputName             = 37
	ortCreateTensorWithDataAsOrtValue   = 49
	ortGetTensorMutableData             = 51
	ortGetDimensionsCount               = 61
	ortGetDimensions                    = 62
	ortGetTensorTypeAndShape            = 65
	ortCreateCpuMemoryInfo              = 69
	ortAllocatorFree                    = 76
	ortGetAllocatorWithDefaultOptions   = 78
	ortReleaseEnv                       = 92
	ortReleaseStatus                    = 93
	ortReleaseMemoryInfo                = 94
	ortReleaseSession                   = 95
	ortReleaseValue                     = 96
	ortReleaseTensorTypeAndShapeInfo    = 99
	ortReleaseSessionOptions            = 100
	ortSessionGetModelMetadata          = 111
	ortModelMetadataLookupCustomMeta    = 116
	ortReleaseModelMetadata             = 118
)

// 枚举值。
const (
	ortLoggingLevelError   = 3
	ortEnableAll           = 99
	ortArenaAllocator      = 1
	ortMemTypeDefault      = 0
	ortTensorElementFloat  = 1
	ortFunctionTableLength = 318
)

// ortHandle 是 ONNX Runtime 的不透明对象（OrtEnv、OrtSession、OrtValue 等），Go 侧只持有指针。
type ortHandle struct{ _ byte }

// ortApiBase 对应 struct OrtApiBase。
type ortApiBase struct {
	getAPI           uintptr
	getVersionString uintptr
}

// ortRuntime 是加载好的 onnxruntime.dll 与它的函数表。
type ortRuntime struct {
	dll     *windows.DLL
	api     *[ortFunctionTableLength]uintptr
	version string
	env     *ortHandle
	memInfo *ortHandle
	alloc   *ortHandle
}

// loadORT 按完整路径加载 onnxruntime.dll 并取得函数表。
// 必须用完整路径：系统目录里可能也有一份版本不同的 onnxruntime.dll。
func loadORT(path string) (*ortRuntime, error) {
	dll, err := windows.LoadDLL(path)
	if err != nil {
		return nil, fmt.Errorf("加载 ONNX Runtime 失败：%w", err)
	}
	proc, err := dll.FindProc("OrtGetApiBase")
	if err != nil {
		dll.Release()
		return nil, fmt.Errorf("ONNX Runtime 缺少入口函数：%w", err)
	}
	// OrtGetApiBase 返回一个静态结构体的地址，按指针重新解释即可（不归 Go 管理）。
	ret, _, _ := syscall.SyscallN(proc.Addr())
	if ret == 0 {
		dll.Release()
		return nil, errors.New("ONNX Runtime 没有返回接口表")
	}
	base := *(**ortApiBase)(unsafe.Pointer(&ret))
	apiPtr, _, _ := syscall.SyscallN(base.getAPI, ortAPIVersion)
	if apiPtr == 0 {
		dll.Release()
		return nil, fmt.Errorf("ONNX Runtime 版本过旧，不支持接口版本 %d", ortAPIVersion)
	}
	r := &ortRuntime{dll: dll, api: *(**[ortFunctionTableLength]uintptr)(unsafe.Pointer(&apiPtr))}
	if v, _, _ := syscall.SyscallN(base.getVersionString); v != 0 {
		r.version = windows.BytePtrToString(*(**byte)(unsafe.Pointer(&v)))
	}

	if err := r.call(ortCreateEnv, ortLoggingLevelError, uintptr(unsafe.Pointer(cstr("goclock"))),
		uintptr(unsafe.Pointer(&r.env))); err != nil {
		r.close()
		return nil, err
	}
	_ = r.call(ortDisableTelemetryEvents, uintptr(unsafe.Pointer(r.env)))
	if err := r.call(ortCreateCpuMemoryInfo, ortArenaAllocator, ortMemTypeDefault,
		uintptr(unsafe.Pointer(&r.memInfo))); err != nil {
		r.close()
		return nil, err
	}
	if err := r.call(ortGetAllocatorWithDefaultOptions, uintptr(unsafe.Pointer(&r.alloc))); err != nil {
		r.close()
		return nil, err
	}
	return r, nil
}

// call 调用返回 OrtStatus* 的函数：返回空指针表示成功，否则取出错误信息并释放状态对象。
//
// go:uintptrescapes 不能省：参数里的 uintptr(unsafe.Pointer(&x)) 经过这一层中转才到 SyscallN，
// 编译器只对直接写在 SyscallN 参数里的转换做保护。没有这条指示，x 可能留在栈上，
// 中途栈一扩容整体搬家，C 代码就把结果写进旧地址（实测：Run 成功但拿到空的输出，随即崩溃）。
// 有了它，x 一律分配在堆上（Go 的堆对象不会移动）并在调用期间保持存活，与 x/sys/windows 的 Proc.Call 同理。
//
//go:uintptrescapes
func (r *ortRuntime) call(index int, args ...uintptr) error {
	status, _, _ := syscall.SyscallN(r.api[index], args...)
	if status == 0 {
		return nil
	}
	msg, _, _ := syscall.SyscallN(r.api[ortGetErrorMessage], status)
	text := "未知错误"
	if msg != 0 {
		text = windows.BytePtrToString(*(**byte)(unsafe.Pointer(&msg)))
	}
	syscall.SyscallN(r.api[ortReleaseStatus], status)
	return fmt.Errorf("ONNX Runtime：%s", text)
}

// release 调用某个 Release 函数释放对象，nil 时什么也不做。
func (r *ortRuntime) release(index int, h *ortHandle) {
	if h != nil {
		syscall.SyscallN(r.api[index], uintptr(unsafe.Pointer(h)))
	}
}

// close 释放环境对象。DLL 本身不卸载：卸载后若还有线程池在收尾会直接崩溃，进程退出时由系统回收。
func (r *ortRuntime) close() {
	r.release(ortReleaseMemoryInfo, r.memInfo)
	r.memInfo = nil
	r.release(ortReleaseEnv, r.env)
	r.env = nil
}

// ortSession 是加载好的一个模型。
type ortSession struct {
	rt      *ortRuntime
	h       *ortHandle
	inputs  []string
	outputs []string
}

// openSession 加载模型文件。threads 是单次推理内部使用的线程数，0 表示由运行库按物理核数决定；
// noArena 为 true 时关掉 CPU 内存池，每次推理用完的内存当场还给系统。
func (r *ortRuntime) openSession(modelPath string, threads int, noArena bool) (*ortSession, error) {
	var opts *ortHandle
	if err := r.call(ortCreateSessionOptions, uintptr(unsafe.Pointer(&opts))); err != nil {
		return nil, err
	}
	defer r.release(ortReleaseSessionOptions, opts)
	if err := r.call(ortSetSessionGraphOptimizationLevel, uintptr(unsafe.Pointer(opts)), ortEnableAll); err != nil {
		return nil, err
	}
	if err := r.call(ortSetIntraOpNumThreads, uintptr(unsafe.Pointer(opts)), uintptr(threads)); err != nil {
		return nil, err
	}
	// 每张图、每行文字的尺寸都不一样，内存模式优化按形状规划内存，形状一变就得重来，反而拖慢，官方建议此时关掉。
	if err := r.call(ortDisableMemPattern, uintptr(unsafe.Pointer(opts))); err != nil {
		return nil, err
	}
	if noArena {
		if err := r.call(ortDisableCpuMemArena, uintptr(unsafe.Pointer(opts))); err != nil {
			return nil, err
		}
	}

	wpath, err := syscall.UTF16PtrFromString(modelPath)
	if err != nil {
		return nil, err
	}
	s := &ortSession{rt: r}
	if err := r.call(ortCreateSession, uintptr(unsafe.Pointer(r.env)), uintptr(unsafe.Pointer(wpath)),
		uintptr(unsafe.Pointer(opts)), uintptr(unsafe.Pointer(&s.h))); err != nil {
		return nil, fmt.Errorf("加载模型 %s 失败：%w", modelPath, err)
	}
	if s.inputs, err = s.names(ortSessionGetInputCount, ortSessionGetInputName); err != nil {
		s.close()
		return nil, err
	}
	if s.outputs, err = s.names(ortSessionGetOutputCount, ortSessionGetOutputName); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

// names 读出模型的输入或输出名称。
func (s *ortSession) names(countFn, nameFn int) ([]string, error) {
	var n uintptr
	if err := s.rt.call(countFn, uintptr(unsafe.Pointer(s.h)), uintptr(unsafe.Pointer(&n))); err != nil {
		return nil, err
	}
	out := make([]string, 0, n)
	for i := uintptr(0); i < n; i++ {
		var p *byte
		if err := s.rt.call(nameFn, uintptr(unsafe.Pointer(s.h)), i, uintptr(unsafe.Pointer(s.rt.alloc)),
			uintptr(unsafe.Pointer(&p))); err != nil {
			return nil, err
		}
		out = append(out, windows.BytePtrToString(p))
		s.rt.free(unsafe.Pointer(p))
	}
	return out, nil
}

// free 用默认分配器释放运行库分配给我们的内存。
func (r *ortRuntime) free(p unsafe.Pointer) {
	_ = r.call(ortAllocatorFree, uintptr(unsafe.Pointer(r.alloc)), uintptr(p))
}

// metadata 读出模型自定义元数据里某个键的值，不存在时返回空字符串。
func (s *ortSession) metadata(key string) (string, error) {
	var md *ortHandle
	if err := s.rt.call(ortSessionGetModelMetadata, uintptr(unsafe.Pointer(s.h)), uintptr(unsafe.Pointer(&md))); err != nil {
		return "", err
	}
	defer s.rt.release(ortReleaseModelMetadata, md)
	var p *byte
	if err := s.rt.call(ortModelMetadataLookupCustomMeta, uintptr(unsafe.Pointer(md)), uintptr(unsafe.Pointer(s.rt.alloc)),
		uintptr(unsafe.Pointer(cstr(key))), uintptr(unsafe.Pointer(&p))); err != nil {
		return "", err
	}
	if p == nil {
		return "", nil
	}
	v := windows.BytePtrToString(p)
	s.rt.free(unsafe.Pointer(p))
	return v, nil
}

// tensor 是一次推理的输出：形状与数据（已复制到 Go 内存）。
type tensor struct {
	shape []int64
	data  []float32
}

// run 用一个 float32 输入跑一次推理，返回第一个输出。
func (s *ortSession) run(input []float32, shape []int64) (*tensor, error) {
	if len(input) == 0 {
		return nil, errors.New("输入为空")
	}
	var in *ortHandle
	if err := s.rt.call(ortCreateTensorWithDataAsOrtValue, uintptr(unsafe.Pointer(s.rt.memInfo)),
		uintptr(unsafe.Pointer(&input[0])), uintptr(len(input)*4),
		uintptr(unsafe.Pointer(&shape[0])), uintptr(len(shape)), ortTensorElementFloat,
		uintptr(unsafe.Pointer(&in))); err != nil {
		return nil, err
	}
	defer s.rt.release(ortReleaseValue, in)

	inName := cstr(s.inputs[0])
	outName := cstr(s.outputs[0])
	inNames := []*byte{inName}
	outNames := []*byte{outName}
	inputs := []*ortHandle{in}
	var out *ortHandle
	err := s.rt.call(ortRun, uintptr(unsafe.Pointer(s.h)), 0,
		uintptr(unsafe.Pointer(&inNames[0])), uintptr(unsafe.Pointer(&inputs[0])), 1,
		uintptr(unsafe.Pointer(&outNames[0])), 1, uintptr(unsafe.Pointer(&out)))
	// 运行库直接读这些 Go 内存，调用结束前不能被回收或移动。
	runtime.KeepAlive(input)
	runtime.KeepAlive(shape)
	runtime.KeepAlive(inNames)
	runtime.KeepAlive(outNames)
	if err != nil {
		return nil, err
	}
	defer s.rt.release(ortReleaseValue, out)
	return s.rt.readTensor(out)
}

// readTensor 读出输出张量的形状并把数据复制到 Go 内存。
func (r *ortRuntime) readTensor(v *ortHandle) (*tensor, error) {
	var info *ortHandle
	if err := r.call(ortGetTensorTypeAndShape, uintptr(unsafe.Pointer(v)), uintptr(unsafe.Pointer(&info))); err != nil {
		return nil, err
	}
	defer r.release(ortReleaseTensorTypeAndShapeInfo, info)
	var n uintptr
	if err := r.call(ortGetDimensionsCount, uintptr(unsafe.Pointer(info)), uintptr(unsafe.Pointer(&n))); err != nil {
		return nil, err
	}
	shape := make([]int64, n)
	if n > 0 {
		if err := r.call(ortGetDimensions, uintptr(unsafe.Pointer(info)), uintptr(unsafe.Pointer(&shape[0])), n); err != nil {
			return nil, err
		}
	}
	total := 1
	for _, d := range shape {
		total *= int(d)
	}
	var p *float32
	if err := r.call(ortGetTensorMutableData, uintptr(unsafe.Pointer(v)), uintptr(unsafe.Pointer(&p))); err != nil {
		return nil, err
	}
	data := make([]float32, total)
	if total > 0 {
		copy(data, unsafe.Slice(p, total))
	}
	return &tensor{shape: shape, data: data}, nil
}

// close 释放模型。
func (s *ortSession) close() {
	s.rt.release(ortReleaseSession, s.h)
	s.h = nil
}

// cstr 生成以 0 结尾的 C 字符串。
func cstr(s string) *byte {
	b := append([]byte(s), 0)
	return &b[0]
}
