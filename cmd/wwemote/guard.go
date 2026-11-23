package main

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32         = windows.NewLazySystemDLL("kernel32.dll")
	procCreateMutexW = kernel32.NewProc("CreateMutexW")
	procFindWindowW  = user32.NewProc("FindWindowW")
	procMessageBoxW  = user32.NewProc("MessageBoxW")
)

const (
	mbOK        = 0x00000000
	mbIconError = 0x00000010
)

// messageBox is the only way to say anything before the window exists. The
// binary is linked with -H=windowsgui, so anything written to stderr is lost:
// a failed start would look exactly like "double-clicked, nothing happened".
func messageBox(text string) {
	t, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	c, _ := windows.UTF16PtrFromString("WWEmote")
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(t)),
		uintptr(unsafe.Pointer(c)), mbOK|mbIconError)
}

// acquireSingleInstance returns false when another copy already owns the
// mutex. Two copies share <panel>/user, so they could interleave writes into
// the same ini and leave the panel half-configured with no error anywhere.
func acquireSingleInstance() bool {
	name, err := windows.UTF16PtrFromString(`Local\WWEmote-Panel-Single`)
	if err != nil {
		return true
	}
	h, _, e := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		// The mutex could not be created at all; never block the app on that.
		return true
	}
	if errno, ok := e.(syscall.Errno); ok && errno == windows.ERROR_ALREADY_EXISTS {
		return false
	}
	return true
}

// focusRunningWindow brings the existing window forward, so a second launch
// reads as "it is already open" instead of "nothing happened".
func focusRunningWindow() {
	title, err := windows.UTF16PtrFromString("WWEmote")
	if err != nil {
		return
	}
	hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(title)))
	if hwnd == 0 {
		messageBox("WWEmote is already running.")
		return
	}
	// SW_SHOW also covers the case where the first copy is still parked
	// offscreen during WebView2 initialisation.
	procShowWindow.Call(hwnd, swShow)
	procSetForeground.Call(hwnd)
}
