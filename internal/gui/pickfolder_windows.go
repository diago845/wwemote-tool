//go:build windows

package gui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	ole32   = windows.NewLazySystemDLL("ole32.dll")

	procSHCreateItem     = shell32.NewProc("SHCreateItemFromParsingName")
	procShellExecuteW    = shell32.NewProc("ShellExecuteW")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree    = ole32.NewProc("CoTaskMemFree")
)

// CLSID_FileOpenDialog = DC1C5A9C-E88A-4DDE-A5A1-60F82A20AEF7
//
// This is the one CLSID the folder picker needs. A wrong GUID does not fail
// loudly: CoCreateInstance returns REGDB_E_CLASSNOTREG (0x80040154) and the
// Browse button looks dead, which is exactly how it read in the field.
var clsidFileOpenDialog = windows.GUID{
	Data1: 0xDC1C5A9C, Data2: 0xE88A, Data3: 0x4DDE,
	Data4: [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7},
}

// IID_IFileDialog = 42F85136-DB7E-439C-85F1-E4075D135FC8
var iidIFileDialog = windows.GUID{
	Data1: 0x42F85136, Data2: 0xDB7E, Data3: 0x439C,
	Data4: [8]byte{0x85, 0xF1, 0xE4, 0x07, 0x5D, 0x13, 0x5F, 0xC8},
}

// IID_IShellItem = 43826D1E-E718-42EE-BC55-A1E261C37BFE
var iidIShellItem = windows.GUID{
	Data1: 0x43826D1E, Data2: 0xE718, Data3: 0x42EE,
	Data4: [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE},
}

// Vtable slots. The numbers are positional, not decorative: IFileDialog is
// IUnknown(0-2) + IModalWindow::Show(3) + IFileDialog methods in declaration
// order, so SetOptions is 9, SetFolder 12, SetTitle 17, GetResult 20. Getting
// one of them wrong still "works" -- the call lands on a different method and
// the dialog silently never appears -- so they are named, not inline.
const (
	iunkRelease       = 2
	ifdShow           = 3
	ifdSetOptions     = 9
	ifdGetOptions     = 10
	ifdSetFolder      = 12
	ifdSetTitle       = 17
	ifdGetResult      = 20
	isiGetDisplayName = 5 // IShellItem: QI/AddRef/Release, BindToHandler, GetParent, GetDisplayName
)

// FOS_* values (shobjidl.h). FOS_PICKFOLDERS is what makes the dialog a folder
// picker at all; 0x200 is FOS_ALLOWMULTISELECT and yields a file-open dialog.
const (
	fosPickFolders     = 0x00000020
	fosForceFilesystem = 0x00000040
	fosNoValidate      = 0x00000100
	fosPathMustExist   = 0x00000800
	fosDontAddToRecent = 0x02000000
)

const (
	cosInitApartment = 0x2
	clsCtxAll        = 23
	sigdnFileSysPath = 0x80058000
	sFalse           = 0x00000001
	errCanceled      = 0x800704C7
	rpcEChangedMode  = 0x80010106
)

type dialogResult struct {
	path string
	err  error
}

// ownerHWND is the app window, so the picker opens owned by it (always in
// front of the borderless window instead of behind it).
var ownerHWND atomic.Uintptr

func SetOwnerWindow(h uintptr) { ownerHWND.Store(h) }

// pickFolderNative opens the modern folder picker. IFileDialog is
// apartment-threaded, so COM runs on a dedicated OS-locked goroutine.
func pickFolderNative(title, initial string) (string, error) {
	ch := make(chan dialogResult, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		ch <- dialogImpl(title, initial)
	}()
	r := <-ch
	return r.path, r.err
}

func dialogImpl(title, initial string) dialogResult {
	hr, _, _ := procCoInitializeEx.Call(0, cosInitApartment)
	// S_FALSE means "already initialised on this thread in the same mode" --
	// a success that still requires CoUninitialize. Comparing `!= 0` treated
	// it as a failure, so a reused worker thread reported
	// "CoInitializeEx failed: 0x00000001" and the dialog never opened.
	// RPC_E_CHANGED_MODE is the opposite case: another apartment model owns
	// the thread, this call added no reference, so CoUninitialize must not run.
	switch code := uint32(hr); code {
	case 0, sFalse:
		defer procCoUninitialize.Call()
	case rpcEChangedMode:
		// COM is usable; the walk below reports a real error if the dialog
		// itself cannot be created in this apartment.
	default:
		return dialogResult{err: fmt.Errorf("CoInitializeEx failed: 0x%08x", code)}
	}

	var p unsafe.Pointer
	hr, _, _ = procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidFileOpenDialog)), 0, clsCtxAll,
		uintptr(unsafe.Pointer(&iidIFileDialog)), uintptr(unsafe.Pointer(&p)))
	if uint32(hr) != 0 || p == nil {
		return dialogResult{err: fmt.Errorf("IFileDialog create failed: 0x%08x", uint32(hr))}
	}
	d := &iFileDialog{p: p}
	defer releaseIface(p)

	d.call(ifdSetOptions, fosPickFolders|fosForceFilesystem|fosPathMustExist|
		fosNoValidate|fosDontAddToRecent)

	// SetFolder wants an IShellItem, not a path string: handing it a UTF-16
	// buffer would be read as an interface pointer.
	if initial != "" {
		if item := shellItemFromPath(initial); item != nil {
			d.call(ifdSetFolder, uintptr(item))
			releaseIface(item)
		}
	}
	if wp, e := windows.UTF16PtrFromString(title); e == nil {
		d.call(ifdSetTitle, uintptr(unsafe.Pointer(wp)))
	}

	hr, _, _ = d.callHr(ifdShow, ownerHWND.Load())
	if code := uint32(hr); code == errCanceled {
		return dialogResult{}
	} else if code != 0 {
		return dialogResult{err: fmt.Errorf("IFileDialog.Show failed: 0x%08x", code)}
	}

	var item unsafe.Pointer
	hr, _, _ = d.callHr(ifdGetResult, uintptr(unsafe.Pointer(&item)))
	if uint32(hr) != 0 || item == nil {
		return dialogResult{err: fmt.Errorf("GetResult failed: 0x%08x", uint32(hr))}
	}
	defer releaseIface(item)

	var psz *uint16
	hr, _, _ = shellItemCall(item, isiGetDisplayName, sigdnFileSysPath,
		uintptr(unsafe.Pointer(&psz)))
	if uint32(hr) != 0 || psz == nil {
		return dialogResult{err: fmt.Errorf("GetDisplayName failed: 0x%08x", uint32(hr))}
	}
	path := windows.UTF16PtrToString(psz)
	procCoTaskMemFree.Call(uintptr(unsafe.Pointer(psz)))
	return dialogResult{path: path}
}

// ---- minimal COM helpers ----------------------------------------------

type iFileDialog struct{ p unsafe.Pointer }

// callHr keeps the HRESULT as a full 32-bit value; the uintptr return of a
// syscall is sign-extended on amd64, so 0x800704C7 would not compare equal to
// the Go constant without the truncation.
func (d *iFileDialog) callHr(index int, args ...uintptr) (uintptr, uintptr, error) {
	vt := *(*[32]uintptr)(unsafe.Pointer(*(*uintptr)(d.p)))
	all := append([]uintptr{uintptr(d.p)}, args...)
	ret, _, err := syscall.SyscallN(vt[index], all...)
	return uintptr(uint32(ret)), ret, err
}

func (d *iFileDialog) call(index int, args ...uintptr) uintptr {
	ret, _, _ := d.callHr(index, args...)
	return ret
}

func shellItemFromPath(p string) unsafe.Pointer {
	wp, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return nil
	}
	var item unsafe.Pointer
	hr, _, _ := procSHCreateItem.Call(uintptr(unsafe.Pointer(wp)), 0,
		uintptr(unsafe.Pointer(&iidIShellItem)), uintptr(unsafe.Pointer(&item)))
	if uint32(hr) != 0 {
		return nil
	}
	return item
}

func shellItemCall(item unsafe.Pointer, index int, args ...uintptr) (uintptr, uintptr, error) {
	vt := *(*[8]uintptr)(unsafe.Pointer(*(*uintptr)(item)))
	all := append([]uintptr{uintptr(item)}, args...)
	ret, _, err := syscall.SyscallN(vt[index], all...)
	return uintptr(uint32(ret)), ret, err
}

func releaseIface(p unsafe.Pointer) {
	if p == nil {
		return
	}
	vt := *(*[3]uintptr)(unsafe.Pointer(*(*uintptr)(p)))
	syscall.SyscallN(vt[iunkRelease], uintptr(p))
}

// isTestBinary reports whether this process is a `go test` binary. Test names
// end in ".test" (Windows appends ".exe"), while the shipped exe is
// WWEmoteTool.exe. Tests that exercise /api/diag-bundle must not pop Explorer
// on a temp dir Windows then reports as unavailable.
func isTestBinary() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	base := filepath.Base(exe)
	return strings.HasSuffix(base, ".test") || strings.HasSuffix(base, ".test.exe")
}

func openFolder(dir string) {
	if dir == "" || isTestBinary() {
		return
	}
	wp, e := windows.UTF16PtrFromString(dir)
	if e != nil {
		return
	}
	op, _ := windows.UTF16PtrFromString("open")
	procShellExecuteW.Call(0, uintptr(unsafe.Pointer(op)), uintptr(unsafe.Pointer(wp)), 0, 0, 1)
}
