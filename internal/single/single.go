// Package single 基于命名内核对象实现单实例运行。
// 除了阻止重复启动，还提供一个命名事件：重复启动的进程可以借此唤醒已运行的实例。
package single

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// mutexName 是用于判定实例是否已存在的互斥体名称。
const mutexName = `Local\GoClock.SingleInstance`

// eventName 是用于唤醒已运行实例的事件名称。
const eventName = `Local\GoClock.Activate`

// Instance 表示当前进程持有的单实例凭据。
type Instance struct {
	mutex windows.Handle
	event windows.Handle
	done  chan struct{}
}

// Acquire 尝试成为唯一实例。
// 第二个返回值为 false 表示已有实例在运行，调用方应当调用 Signal 后退出。
func Acquire() (*Instance, bool) {
	name, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return nil, true
	}
	mutex, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		if mutex != 0 {
			windows.CloseHandle(mutex)
		}
		return nil, false
	}
	if err != nil && mutex == 0 {
		// 无法创建互斥体时不阻塞正常启动。
		return &Instance{done: make(chan struct{})}, true
	}

	inst := &Instance{mutex: mutex, done: make(chan struct{})}
	if evtName, err := windows.UTF16PtrFromString(eventName); err == nil {
		// 自动重置事件，初始为无信号状态。
		if evt, err := windows.CreateEvent(nil, 0, 0, evtName); err == nil {
			inst.event = evt
		}
	}
	return inst, true
}

// OnActivate 在后台等待其他实例发来的唤醒信号，收到后调用 fn。
// fn 在独立协程中执行，界面相关操作需自行切回 UI 线程。
func (i *Instance) OnActivate(fn func()) {
	if i == nil || i.event == 0 || fn == nil {
		return
	}
	go func() {
		for {
			select {
			case <-i.done:
				return
			default:
			}
			// 定时超时唤醒，保证 Release 后协程能及时退出。
			ret, err := windows.WaitForSingleObject(i.event, 1000)
			if err != nil {
				return
			}
			if ret == uint32(windows.WAIT_OBJECT_0) {
				fn()
			}
		}
	}()
}

// Release 释放持有的内核对象。
func (i *Instance) Release() {
	if i == nil {
		return
	}
	select {
	case <-i.done:
	default:
		close(i.done)
	}
	if i.event != 0 {
		windows.CloseHandle(i.event)
		i.event = 0
	}
	if i.mutex != 0 {
		windows.CloseHandle(i.mutex)
		i.mutex = 0
	}
}

// Signal 唤醒已经在运行的实例，让它把管理窗口显示出来。
func Signal() error {
	name, err := windows.UTF16PtrFromString(eventName)
	if err != nil {
		return err
	}
	evt, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil {
		return fmt.Errorf("无法唤醒已运行的实例：%w", err)
	}
	defer windows.CloseHandle(evt)
	return windows.SetEvent(evt)
}
