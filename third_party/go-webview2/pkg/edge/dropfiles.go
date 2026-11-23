//go:build windows
// +build windows

package edge

import (
	"unsafe"

	"github.com/jchv/go-webview2/internal/w32"
	"golang.org/x/sys/windows"
)

// Web-message additional-object bridge.
//
// Chromium deliberately withholds the absolute path of a file dropped on a
// WebView2 page: the DOM side only sees File.name and File.webkitRelativePath.
// To let the user drop a folder onto the address bar we use the documented
// WebView2 bridge: the page posts the dropped File objects through
// chrome.webview.postMessageWithAdditionalObjects, and the host reads their
// real paths from WebMessageReceivedEventArgs2.AdditionalObjects (each entry
// is an ICoreWebView2File whose Path is the absolute disk path).
//
// The binding in this fork stops at v1, so the v2 interfaces are mirrored here
// following the same hand-rolled vtable pattern used elsewhere in this package.
// All IIDs and slot orders come from the official WebView2 IDL.

// DropMessageSentinel is the web-message text the page sends together with
// dropped File objects. The native side keys on it to distinguish a drop from
// the library's regular RPC messages.
const DropMessageSentinel = "__wwemote_drop__"

// iUnknown is a minimal IUnknown-shaped interface pointer. COM APIs handing
// back IUnknown** need a QueryInterface before the concrete interface may be
// used. Casting that pointer straight to a derived type reads past a 3-slot
// vtable whenever the object only exposes plain IUnknown at this slot.
type iUnknown struct {
	vtbl *_IUnknownVtbl
}

func (u *iUnknown) Release() {
	if u == nil {
		return
	}
	_, _, _ = u.vtbl.Release.Call(uintptr(unsafe.Pointer(u)))
}

// ICoreWebView2ObjectCollectionView
// uuid(b547d2d4-b8d5-4278-a544-c54e66b5f45d)
// vtable: IUnknown(3), get_Count, GetValueAtIndex
type iCoreWebView2ObjectCollectionViewVtbl struct {
	_IUnknownVtbl
	GetCount        ComProc
	GetValueAtIndex ComProc
}

type iCoreWebView2ObjectCollectionView struct {
	vtbl *iCoreWebView2ObjectCollectionViewVtbl
}

func (c *iCoreWebView2ObjectCollectionView) Count() uint32 {
	if c == nil {
		return 0
	}
	var n uint32
	_, _, _ = c.vtbl.GetCount.Call(uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(&n)))
	return n
}

// ValueAtIndex returns the ICoreWebView2File at index i. The official ABI
// hands back IUnknown**, so the concrete interface must be obtained with
// QueryInterface before GetPath (slot 4) can be called safely.
func (c *iCoreWebView2ObjectCollectionView) ValueAtIndex(i uint32) *iCoreWebView2File {
	if c == nil {
		return nil
	}
	var unk *iUnknown
	hr, _, _ := c.vtbl.GetValueAtIndex.Call(
		uintptr(unsafe.Pointer(c)),
		uintptr(i),
		uintptr(unsafe.Pointer(&unk)),
	)
	if hr != 0 || unk == nil {
		return nil
	}
	defer unk.Release()

	iid := NewGUID("{f2c19559-6bc1-4583-a757-90021be9afec}")
	var f *iCoreWebView2File
	hr, _, _ = unk.vtbl.QueryInterface.Call(
		uintptr(unsafe.Pointer(unk)),
		uintptr(unsafe.Pointer(iid)),
		uintptr(unsafe.Pointer(&f)),
	)
	if hr != 0 || f == nil {
		return nil
	}
	return f
}

func (c *iCoreWebView2ObjectCollectionView) Release() {
	if c == nil {
		return
	}
	_, _, _ = c.vtbl.Release.Call(uintptr(unsafe.Pointer(c)))
}

// ICoreWebView2File
// uuid(f2c19559-6bc1-4583-a757-90021be9afec)
// vtable: IUnknown(3), get_Path
type iCoreWebView2FileVtbl struct {
	_IUnknownVtbl
	GetPath ComProc
}

type iCoreWebView2File struct {
	vtbl *iCoreWebView2FileVtbl
}

func (f *iCoreWebView2File) Path() string {
	if f == nil {
		return ""
	}
	var p *uint16
	hr, _, _ := f.vtbl.GetPath.Call(uintptr(unsafe.Pointer(f)), uintptr(unsafe.Pointer(&p)))
	if hr != 0 || p == nil {
		return ""
	}
	s := w32.Utf16PtrToString(p)
	windows.CoTaskMemFree(unsafe.Pointer(p))
	return s
}

func (f *iCoreWebView2File) Release() {
	if f == nil {
		return
	}
	_, _, _ = f.vtbl.Release.Call(uintptr(unsafe.Pointer(f)))
}

// ICoreWebView2WebMessageReceivedEventArgs2
// uuid(50a798ac-40fa-4634-8fb7-9419e5e8a3f8), extends v1.
// vtable: IUnknown(3), get_Source, get_WebMessageAsJson,
// TryGetWebMessageAsString, get_AdditionalObjects
type iCoreWebView2WebMessageReceivedEventArgs2Vtbl struct {
	_IUnknownVtbl
	GetSource                ComProc
	GetWebMessageAsJSON      ComProc
	TryGetWebMessageAsString ComProc
	GetAdditionalObjects     ComProc
}

type iCoreWebView2WebMessageReceivedEventArgs2 struct {
	vtbl *iCoreWebView2WebMessageReceivedEventArgs2Vtbl
}

// queryMessageArgs2 promotes a v1 WebMessageReceivedEventArgs to v2.
// Returns nil when the runtime supports no additional objects at all.
func queryMessageArgs2(args *iCoreWebView2WebMessageReceivedEventArgs) *iCoreWebView2WebMessageReceivedEventArgs2 {
	if args == nil {
		return nil
	}
	iid := NewGUID("{50a798ac-40fa-4634-8fb7-9419e5e8a3f8}")
	var v2 *iCoreWebView2WebMessageReceivedEventArgs2
	hr, _, _ := args.vtbl.QueryInterface.Call(
		uintptr(unsafe.Pointer(args)),
		uintptr(unsafe.Pointer(iid)),
		uintptr(unsafe.Pointer(&v2)),
	)
	if hr == 0 && v2 != nil {
		return v2
	}

	// Some WebView2 runtimes hand the handler an args object whose vtable
	// already carries the v2 methods (get_AdditionalObjects at slot 6) but
	// whose QueryInterface does not advertise the v2 IID. QI above fails with
	// E_NOINTERFACE even though the method exists. In that case reinterpret
	// the SAME COM object (same `this`, same vtable memory) as the v2 view and
	// call slot 6 directly. COM interfaces only differ by vtable layout, so
	// the object pointer is identical across all of its interfaces.
	// AddRef here balances the caller's deferred Release (QI does this on the
	// success path); without it we would release a borrowed reference.
	args.vtbl.AddRef.Call(uintptr(unsafe.Pointer(args)))
	v2 = (*iCoreWebView2WebMessageReceivedEventArgs2)(unsafe.Pointer(args))
	v2.vtbl = (*iCoreWebView2WebMessageReceivedEventArgs2Vtbl)(unsafe.Pointer(args.vtbl))
	return v2
}

func (a *iCoreWebView2WebMessageReceivedEventArgs2) AdditionalObjects() *iCoreWebView2ObjectCollectionView {
	if a == nil {
		return nil
	}
	var c *iCoreWebView2ObjectCollectionView
	hr, _, _ := a.vtbl.GetAdditionalObjects.Call(uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(&c)))
	if hr != 0 {
		return nil
	}
	return c
}

func (a *iCoreWebView2WebMessageReceivedEventArgs2) Release() {
	if a == nil {
		return
	}
	_, _, _ = a.vtbl.Release.Call(uintptr(unsafe.Pointer(a)))
}

// droppedPaths reads the absolute paths of all ICoreWebView2File entries
// attached to a web message. It never fails the caller: any COM problem just
// yields a shorter list, so a missing bridge degrades to "no paths".
func droppedPaths(args *iCoreWebView2WebMessageReceivedEventArgs) []string {
	v2 := queryMessageArgs2(args)
	if v2 == nil {
		return nil
	}
	defer v2.Release()

	coll := v2.AdditionalObjects()
	if coll == nil {
		return nil
	}
	defer coll.Release()

	n := coll.Count()
	if n == 0 || n > 256 {
		return nil
	}
	paths := make([]string, 0, n)
	for i := uint32(0); i < n; i++ {
		f := coll.ValueAtIndex(i)
		if f == nil {
			continue
		}
		if p := f.Path(); p != "" {
			paths = append(paths, p)
		}
		f.Release()
	}
	return paths
}
