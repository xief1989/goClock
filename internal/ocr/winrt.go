package ocr

import (
	"errors"
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WinRT / COM 的最小绑定，只覆盖 OCR 用到的激活工厂、HSTRING、异步操作与集合视图。
// 接口指针一律通过输出参数接收（*comObj），不从 uintptr 转换，保证 go vet 干净、GC 安全。

var (
	combase = windows.NewLazySystemDLL("combase.dll")

	procRoInitialize              = combase.NewProc("RoInitialize")
	procRoUninitialize            = combase.NewProc("RoUninitialize")
	procRoGetActivationFactory    = combase.NewProc("RoGetActivationFactory")
	procWindowsCreateString       = combase.NewProc("WindowsCreateString")
	procWindowsDeleteString       = combase.NewProc("WindowsDeleteString")
	procWindowsGetStringRawBuffer = combase.NewProc("WindowsGetStringRawBuffer")
)

const (
	roInitMultiThreaded = 1
	sOK                 = 0
	sFalse              = 1
)

// IInspectable 之后各接口自己的方法从第 6 个虚表槽开始（IUnknown 3 个 + IInspectable 3 个）。
const inspectableBase = 6

// iidIAsyncInfo 是 IAsyncInfo 的接口编号，用来查询异步操作的状态。
var iidIAsyncInfo = windows.GUID{Data1: 0x00000036, Data2: 0x0000, Data3: 0x0000,
	Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}

// comObj 对应一个 COM / WinRT 对象：首字段是指向虚表的指针。这类对象只由系统创建，Go 侧只持有指针。
type comObj struct {
	vtbl *[64]uintptr
}

// call 调用虚表第 i 个方法，返回 HRESULT。
// go:uintptrescapes 让参数里由指针转来的 uintptr 所指对象分配在堆上并在调用期间存活，
// 否则经这一层中转后，栈上的输出变量可能因栈扩容搬家而失效，详见 ort.go 的 ortRuntime.call。
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

// queryInterface 查询另一个接口。
func (o *comObj) queryInterface(iid *windows.GUID) (*comObj, error) {
	var out *comObj
	if hr := o.call(0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out))); failed(hr) {
		return nil, hrError("QueryInterface", hr)
	}
	return out, nil
}

// failed 判断 HRESULT 是否表示失败。
func failed(hr uintptr) bool {
	return int32(uint32(hr)) < 0
}

// hrError 把失败的 HRESULT 包装成错误。
func hrError(what string, hr uintptr) error {
	return fmt.Errorf("%s 失败（HRESULT 0x%08X）", what, uint32(hr))
}

// hstring 是 WinRT 的字符串句柄。
type hstring uintptr

// newHString 创建 HSTRING，用完要 Delete。
func newHString(s string) (hstring, error) {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return 0, err
	}
	var h hstring
	if hr, _, _ := procWindowsCreateString.Call(uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1),
		uintptr(unsafe.Pointer(&h))); failed(hr) {
		return 0, hrError("WindowsCreateString", hr)
	}
	return h, nil
}

// String 读出 HSTRING 的内容。
func (h hstring) String() string {
	if h == 0 {
		return ""
	}
	var n uint32
	p, _, _ := procWindowsGetStringRawBuffer.Call(uintptr(h), uintptr(unsafe.Pointer(&n)))
	if p == 0 || n == 0 {
		return ""
	}
	// 系统返回的是字符串缓冲区地址，不归 Go 管理；按指针重新解释即可，不涉及 GC。
	buf := unsafe.Slice(*(**uint16)(unsafe.Pointer(&p)), n)
	return windows.UTF16ToString(buf)
}

// Delete 释放 HSTRING。
func (h hstring) Delete() {
	if h != 0 {
		procWindowsDeleteString.Call(uintptr(h))
	}
}

// roInit 在当前线程初始化 WinRT（多线程套间），返回是否需要配对调用 roUninit。
func roInit() (bool, error) {
	hr, _, _ := procRoInitialize.Call(roInitMultiThreaded)
	switch {
	case hr == sOK || hr == sFalse:
		return true, nil
	case failed(hr):
		// RPC_E_CHANGED_MODE：线程已按单线程套间初始化过，此时照样可以用，只是不能由我们反初始化。
		if uint32(hr) == 0x80010106 {
			return false, nil
		}
		return false, hrError("RoInitialize", hr)
	}
	return true, nil
}

// roUninit 反初始化 WinRT。
func roUninit() {
	procRoUninitialize.Call()
}

// activationFactory 取得某个运行时类的激活工厂（静态方法所在的接口）。
func activationFactory(class string, iid *windows.GUID) (*comObj, error) {
	h, err := newHString(class)
	if err != nil {
		return nil, err
	}
	defer h.Delete()
	var f *comObj
	if hr, _, _ := procRoGetActivationFactory.Call(uintptr(h), uintptr(unsafe.Pointer(iid)),
		uintptr(unsafe.Pointer(&f))); failed(hr) {
		return nil, hrError("获取 "+class, hr)
	}
	return f, nil
}

// 异步操作的状态值（AsyncStatus）。
const (
	asyncStarted   = 0
	asyncCompleted = 1
	asyncCanceled  = 2
	asyncError     = 3
)

// awaitOperation 等待一个 IAsyncOperation<T> 完成并取出结果对象。
// 用轮询 IAsyncInfo 的状态代替注册回调：回调要在 Go 里实现 COM 对象，复杂得多。
func awaitOperation(op *comObj, timeout time.Duration) (*comObj, error) {
	info, err := op.queryInterface(&iidIAsyncInfo)
	if err != nil {
		return nil, err
	}
	defer info.release()

	deadline := time.Now().Add(timeout)
	for {
		var status int32
		// IAsyncInfo：get_Id(6) get_Status(7) get_ErrorCode(8) Cancel(9) Close(10)
		if hr := info.call(inspectableBase+1, uintptr(unsafe.Pointer(&status))); failed(hr) {
			return nil, hrError("查询识别进度", hr)
		}
		switch status {
		case asyncCompleted:
			var result *comObj
			// IAsyncOperation<T>：put_Completed(6) get_Completed(7) GetResults(8)
			if hr := op.call(inspectableBase+2, uintptr(unsafe.Pointer(&result))); failed(hr) {
				return nil, hrError("取识别结果", hr)
			}
			return result, nil
		case asyncError:
			var code int32
			info.call(inspectableBase+2, uintptr(unsafe.Pointer(&code)))
			return nil, fmt.Errorf("识别出错（HRESULT 0x%08X）", uint32(code))
		case asyncCanceled:
			return nil, errors.New("识别被取消")
		}
		if time.Now().After(deadline) {
			info.call(inspectableBase + 3)
			return nil, errors.New("识别超时")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// vectorView 包装 IVectorView<T>：GetAt(6) get_Size(7)。
type vectorView struct {
	*comObj
}

// size 返回元素个数。
func (v vectorView) size() (uint32, error) {
	var n uint32
	if hr := v.call(inspectableBase+1, uintptr(unsafe.Pointer(&n))); failed(hr) {
		return 0, hrError("读取集合大小", hr)
	}
	return n, nil
}

// at 取第 i 个元素，调用方负责 release。
func (v vectorView) at(i uint32) (*comObj, error) {
	var item *comObj
	if hr := v.call(inspectableBase, uintptr(i), uintptr(unsafe.Pointer(&item))); failed(hr) {
		return nil, hrError("读取集合元素", hr)
	}
	return item, nil
}

// getString 调用一个返回 HSTRING 的属性读取方法。
func getString(o *comObj, slot int) (string, error) {
	var h hstring
	if hr := o.call(slot, uintptr(unsafe.Pointer(&h))); failed(hr) {
		return "", hrError("读取文字", hr)
	}
	defer h.Delete()
	return h.String(), nil
}
