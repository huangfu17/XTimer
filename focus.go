//go:build windows
// +build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	user32              = syscall.NewLazyDLL("user32.dll")
	procSetForeground   = user32.NewProc("SetForegroundWindow")
	procShowWindow      = user32.NewProc("ShowWindow")
	procSetWindowPos    = user32.NewProc("SetWindowPos")
	procAttachThread    = user32.NewProc("AttachThreadInput")
	procGetWindowThread = user32.NewProc("GetWindowThreadProcessId")
	procGetForeground   = user32.NewProc("GetForegroundWindow")
)

const (
	SW_RESTORE     = 9
	HWND_TOPMOST   = -1
	SWP_NOSIZE     = 0x0001
	SWP_NOMOVE     = 0x0002
	SWP_SHOWWINDOW = 0x0040
)

func (p *MyApp) ForceFocusWindow() {
	hwnd := p.window.Handle()

	// 获取当前前台窗口
	foreground, _, _ := procGetForeground.Call()

	// 如果已经有焦点，直接返回
	if uintptr(hwnd) == foreground {
		return
	}

	// 获取线程ID
	var currentThreadID uint32
	procGetWindowThread.Call(uintptr(0), uintptr(0), uintptr(unsafe.Pointer(&currentThreadID)))

	var targetThreadID uint32
	procGetWindowThread.Call(hwnd, uintptr(0), uintptr(unsafe.Pointer(&targetThreadID)))

	// 附加线程输入
	if currentThreadID != targetThreadID {
		procAttachThread.Call(uintptr(currentThreadID), uintptr(targetThreadID), 1)
		defer procAttachThread.Call(uintptr(currentThreadID), uintptr(targetThreadID), 0)
	}

	// 恢复窗口（如果最小化）
	procShowWindow.Call(hwnd, SW_RESTORE)

	// 置顶窗口
	procSetWindowPos.Call(
		hwnd,
		HWND_TOPMOST,
		0, 0, 0, 0,
		uintptr(SWP_NOSIZE|SWP_NOMOVE|SWP_SHOWWINDOW),
	)

	// 设置前景窗口
	procSetForeground.Call(hwnd)

	// 取消置顶
	procSetWindowPos.Call(
		hwnd,
		0, // 取消置顶
		0, 0, 0, 0,
		uintptr(SWP_NOSIZE|SWP_NOMOVE|SWP_SHOWWINDOW),
	)
}
