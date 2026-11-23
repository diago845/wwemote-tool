package main

import (
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
	"wwemote/internal/gui"
)

var (
	user32                = windows.NewLazySystemDLL("user32.dll")
	dwmapi                = windows.NewLazySystemDLL("dwmapi.dll")
	procGetWindowLongPtrW = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW = user32.NewProc("SetWindowLongPtrW")
	procSetWindowPos      = user32.NewProc("SetWindowPos")
	procShowWindow        = user32.NewProc("ShowWindow")
	procSetForeground     = user32.NewProc("SetForegroundWindow")
	procSetFocus          = user32.NewProc("SetFocus")
	procSendMessageW      = user32.NewProc("SendMessageW")
	procReleaseCapture    = user32.NewProc("ReleaseCapture")
	procGetSystemMetrics  = user32.NewProc("GetSystemMetrics")
	procGetWindowRect     = user32.NewProc("GetWindowRect")
	procDwmSetWindowAttr  = dwmapi.NewProc("DwmSetWindowAttribute")
)

// rect mirrors Win32 RECT. GetWindowRect fills it with screen coordinates.
type rect struct {
	Left, Top, Right, Bottom int32
}

func windowRect(h windows.HWND) rect {
	var r rect
	procGetWindowRect.Call(uintptr(h), uintptr(unsafe.Pointer(&r)))
	return r
}

const (
	gwlStyle        = -16
	wsCaption       = 0x00C00000
	wsThickFrame    = 0x00040000
	wsSysMenu       = 0x00080000
	wsMinimizeBox   = 0x00020000
	wsMaximizeBox   = 0x00010000
	swpNoZOrder     = 0x0004
	swpNoMove       = 0x0002
	swpFrameChanged = 0x0020
	dwmCornerPref   = 33
	dwmCornerRound  = 2
	winWidth        = 650
	winHeight       = 400
	wmNcLButtonDown = 0x00A1
	htCaption       = 0x0002
	swHide          = 0
	swShow          = 5
	smCxScreen      = 0
	smCyScreen      = 1
)

func getLong(h windows.HWND, idx int) uint32 {
	v, _, _ := procGetWindowLongPtrW.Call(uintptr(h), uintptr(idx))
	return uint32(v)
}
func setLong(h windows.HWND, idx int, v uint32) {
	procSetWindowLongPtrW.Call(uintptr(h), uintptr(idx), uintptr(v))
}
func setPos(h windows.HWND, x, y, cx, cy int32, flags uint32) {
	procSetWindowPos.Call(uintptr(h), 0, uintptr(x), uintptr(y), uintptr(cx), uintptr(cy), uintptr(flags))
}
func roundCorners(h windows.HWND) {
	v := int32(dwmCornerRound)
	procDwmSetWindowAttr.Call(uintptr(h), uintptr(dwmCornerPref),
		uintptr(unsafe.Pointer(&v)), unsafe.Sizeof(v))
}

// dragScript makes only the title bar draggable: a left mousedown on the
// header is forwarded to dragWindow() as a caption hit, while controls keep
// normal behaviour and class="no-drag" opts out. Binding is deferred to
// DOMContentLoaded because AddScriptToExecuteOnDocumentCreated runs before the
// DOM exists, when querying header.bar would silently attach nothing.
const dragScript = `
	(function () {
		var BLOCK = 'button, input, textarea, select, option, a, label, [contenteditable], .no-drag';
		function attach() {
			var bar = document.querySelector('header.bar');
			if (!bar) { return false; }
			bar.addEventListener('mousedown', function (e) {
				if (e.button !== 0) { return; }
				var t = e.target;
				if (t && t.closest && t.closest(BLOCK)) { return; }
				if (!window.dragWindow) { return; }
				e.preventDefault();
				window.dragWindow();
			}, true);
			return true;
		}
		if (document.readyState === 'loading') {
			document.addEventListener('DOMContentLoaded', attach);
		} else {
			attach();
		}
	})();
`

// runWindow starts the local API server and opens the borderless configuration
// window. The GUI, renderer and scanner all live inside this single binary.
func runWindow() int {
	// Upgrades from the old build keep their language and remembered Mods
	// folder: copy the small state out of %LOCALAPPDATA% into user/data
	// before anything reads it. Copy-only, so the old files stay untouched.
	gui.MigrateLegacyState()
	// Height is remembered from a previous vertical drag; fall back to the
	// build default on a fresh install. Width is fixed at winWidth.
	winH := gui.SavedWindowHeight()
	if winH == 0 {
		winH = winHeight
	}
	srv := gui.NewServer(nil)

	// WebView2's component updater downloads ~74 MB of extras a local page never
	// uses; the flag cuts the deployed profile to ~10 MB.
	setWebViewNoUpdate()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		messageBox("Cannot start the local service.\n\n" + err.Error() +
			"\n\n无法启动本地服务。\n\n" + err.Error())
		return 1
	}
	// The first /api/status request proves the real page (and its CSS) loaded.
	// The fork suppresses the library's premature window display; the host
	// reveals it only at this point. Wrap the handler to observe that request.
	pageLoaded := make(chan struct{})
	inner := srv.Handler()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-pageLoaded:
		default:
			if r.URL.Path == "/api/status" {
				close(pageLoaded)
			}
		}
		inner.ServeHTTP(w, r)
	})
	httpSrv := &http.Server{Handler: handler}
	go httpSrv.Serve(ln)
	url := "http://" + ln.Addr().String() + "/"

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug: false, AutoFocus: true,
		// Real paths of folders dropped on the address bar arrive here from
		// the native additional-objects bridge; the library forwards them to
		// the page via window.onNativeDrop. No server state is needed because
		// a drop can only happen after the page (and that handler) is loaded.
		DropCallback: func([]string) {},
		WindowOptions: webview2.WindowOptions{
			Title: "WWEmote", Width: winWidth, Height: uint(winH), Center: true,
		},
	})
	if w == nil {
		// No window exists yet, so this is the only channel the user has. The
		// usual cause is a Windows 10 install without the Evergreen WebView2
		// runtime -- without this box the exe simply does nothing.
		messageBox("WWEmote needs the Microsoft Edge WebView2 Runtime, " +
			"which is missing or failed to start.\n\n" +
			"Install \"Microsoft Edge WebView2 Runtime\" and run WWEmote again.\n\n" +
			"WWEmote 需要「Microsoft Edge WebView2 运行库」，当前缺失或启动失败。\n" +
			"请安装后重新运行。")
		httpSrv.Close()
		return 1
	}
	defer w.Destroy()

	// Quit callback: terminate the message loop so Run() returns and the process
	// exits (the old pywebview build only shut the server and left a dead window).
	srv.SetQuit(func() { w.Dispatch(w.Terminate) })

	hwnd := windows.HWND(uintptr(w.Window()))
	// The folder picker opens owned by this window; without an owner it can
	// appear behind the borderless frame and read as "the button does nothing".
	gui.SetOwnerWindow(uintptr(hwnd))
	// The fork parks the window far offscreen (but visible) through WebView2
	// init, so the compositor renders without a white flash. Set its style
	// while offscreen, then move it to screen center on the first /api/status
	// request (which the page issues only after it and its CSS have loaded).
	st := getLong(hwnd, gwlStyle)
	setLong(hwnd, gwlStyle, st&^(wsCaption|wsThickFrame|wsSysMenu|wsMinimizeBox|wsMaximizeBox))
	setPos(hwnd, 0, 0, winWidth, int32(winH), swpNoMove|swpNoZOrder|swpFrameChanged)
	roundCorners(hwnd)

	var revealOnce sync.Once
	reveal := func() {
		revealOnce.Do(func() {
			// Move from the offscreen parking spot to screen center. Do not
			// pass SWP_NOMOVE here — that would leave the window offscreen.
			cx, _, _ := procGetSystemMetrics.Call(smCxScreen)
			cy, _, _ := procGetSystemMetrics.Call(smCyScreen)
			x := (int32(cx) - winWidth) / 2
			y := (int32(cy) - int32(winH)) / 2
			setPos(hwnd, x, y, winWidth, int32(winH), swpNoZOrder|swpFrameChanged)
			procShowWindow.Call(uintptr(hwnd), swShow)
			procSetForeground.Call(uintptr(hwnd))
			procSetFocus.Call(uintptr(hwnd))
		})
	}
	go func() {
		<-pageLoaded
		w.Dispatch(reveal)
	}()
	// If the page never loads (runtime/HTTP failure), reveal anyway so the
	// window can never get stuck invisible.
	revealTimer := time.AfterFunc(3*time.Second, func() { w.Dispatch(reveal) })
	defer revealTimer.Stop()

	w.Init(dragScript)
	w.Bind("dragWindow", func() {
		// The page (or WebView2's child window) owns the mouse capture; without
		// releasing it the caption move loop never sees the button as down.
		procReleaseCapture.Call()
		procSendMessageW.Call(uintptr(w.Window()), wmNcLButtonDown, htCaption, 0)
	})

	// resizeHeight changes only the height. Width (winWidth) is passed back
	// unchanged on every SetWindowPos, so dragging an edge can never make the
	// window wider or narrower. For "top", the bottom edge stays put, so the
	// window's y origin shifts by the same delta in the opposite direction.
	// The JS sends an incremental screenY delta each move.
	w.Bind("resizeHeight", func(edge string, dy int32) {
		w.Dispatch(func() {
			r := windowRect(hwnd)
			curH := r.Bottom - r.Top
			var newH, newY int32
			if edge == "top" {
				newH = int32(gui.ClampWindowHeight(int(curH - dy)))
				newY = r.Bottom - newH // keep bottom edge fixed
			} else {
				newH = int32(gui.ClampWindowHeight(int(curH + dy)))
				newY = r.Top // keep top edge fixed
			}
			setPos(hwnd, r.Left, newY, winWidth, newH, swpNoZOrder|swpFrameChanged)
		})
	})
	w.Bind("commitWindowHeight", func() {
		w.Dispatch(func() {
			r := windowRect(hwnd)
			gui.SaveWindowHeight(int(r.Bottom - r.Top))
		})
	})

	w.Navigate(url)
	w.Run()
	httpSrv.Close()

	// Let the HTTP response for /api/quit flush before the process ends.
	time.Sleep(50 * time.Millisecond)
	return 0
}

func setWebViewNoUpdate() {
	key := "WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS"
	arg := "--disable-component-update"
	prev := os.Getenv(key)
	if containsWord(prev, arg) {
		return
	}
	merged := arg
	if prev != "" {
		merged = prev + " " + arg
	}
	os.Setenv(key, merged)
}

func containsWord(s, word string) bool {
	for _, p := range strings.Fields(s) {
		if p == word {
			return true
		}
	}
	return false
}
