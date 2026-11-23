//go:build windows

package gui

import (
	"runtime"
	"testing"
	"unsafe"
)

// TestFileDialogVtable pins the COM vtable slots and the FOS_* flags. Both fail
// silently: a wrong slot lands on a different method (Show becomes SetTitle and
// the dialog never appears), and 0x200 instead of FOS_PICKFOLDERS turns the
// picker into a file-open dialog. SetOptions/GetOptions round-trips through the
// real object, so a shifted index shows up here instead of in the field.
func TestFileDialogVtable(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if hr, _, _ := procCoInitializeEx.Call(0, cosInitApartment); uint32(hr) != 0 &&
		uint32(hr) != rpcEChangedMode {
		t.Fatalf("CoInitializeEx failed: 0x%08x", uint32(hr))
	}
	defer procCoUninitialize.Call()

	var p unsafe.Pointer
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidFileOpenDialog)), 0, clsCtxAll,
		uintptr(unsafe.Pointer(&iidIFileDialog)), uintptr(unsafe.Pointer(&p)))
	if uint32(hr) != 0 || p == nil {
		// 0x80040154 = REGDB_E_CLASSNOTREG: the CLSID constant is wrong.
		t.Fatalf("CLSID_FileOpenDialog not creatable: 0x%08x", uint32(hr))
	}
	d := &iFileDialog{p: p}
	defer releaseIface(p)

	want := uint32(fosPickFolders | fosForceFilesystem | fosPathMustExist |
		fosNoValidate | fosDontAddToRecent)
	if code := d.call(ifdSetOptions, uintptr(want)); code != 0 {
		t.Fatalf("SetOptions (slot %d) = 0x%08x", ifdSetOptions, code)
	}
	var got uint32
	if code := d.call(ifdGetOptions, uintptr(unsafe.Pointer(&got))); code != 0 {
		t.Fatalf("GetOptions (slot %d) = 0x%08x", ifdGetOptions, code)
	}
	if got&uint32(fosPickFolders) == 0 {
		t.Errorf("options 0x%08x are not folder-picker options", got)
	}
	if got&want != want {
		t.Errorf("options round-trip = 0x%08x, want 0x%08x", got, want)
	}

	// SetFolder needs a real IShellItem; the old code passed a UTF-16 path and
	// would have been read as an interface pointer.
	item := shellItemFromPath(`C:\Windows`)
	if item == nil {
		t.Fatal("SHCreateItemFromParsingName failed for C:\\Windows")
	}
	defer releaseIface(item)
	if code := d.call(ifdSetFolder, uintptr(item)); code != 0 {
		t.Errorf("SetFolder (slot %d) = 0x%08x", ifdSetFolder, code)
	}
}

// A go test binary must never launch Explorer: the diag-bundle route test calls
// writeDiagBundle, whose temp <panel>/user/data does not exist as a real folder
// for Windows to open, producing a "location unavailable" modal on every test
// run. The guard keys off the binary name, which is stable across test hosts.
func TestIsTestBinary(t *testing.T) {
	if !isTestBinary() {
		t.Fatal("a go test process must identify as a test binary")
	}
}
