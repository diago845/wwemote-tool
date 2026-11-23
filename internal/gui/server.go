package gui

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed web/index.html
var webFS embed.FS

// Server owns the local HTTP endpoint and the job state. A single job runs at
// a time (matches the Python backend's _RUN_LOCK).
type Server struct {
	st       *State
	quit     func()
	runMu    sync.Mutex
	running  bool
	cancelCh chan struct{}
}

func NewServer(quit func()) *Server {
	lp := filepathJoin(diagDir(), "WWEmote.log")
	return &Server{st: newState(lp), quit: quit}
}

// Handler returns the mux. Kept as a method so tests can drive it directly
// without opening a window.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.serveRoot)
	mux.HandleFunc("/api/status", s.getStatus)
	mux.HandleFunc("/api/health", s.getHealth)
	mux.HandleFunc("/api/language", s.language)
	mux.HandleFunc("/api/diag", s.getDiag)
	mux.HandleFunc("/api/js-error", s.jsError)
	mux.HandleFunc("/api/diag-bundle", s.diagBundle)
	mux.HandleFunc("/api/pick-folder", s.pickFolder)
	mux.HandleFunc("/api/start", s.startJob)
	mux.HandleFunc("/api/update", s.startJob)
	mux.HandleFunc("/api/cancel", s.cancel)
	mux.HandleFunc("/api/quit", s.quitAPI)
	return mux
}

func (s *Server) State() *State { return s.st }

// SetQuit installs the callback that closes the window (wired by the platform
// layer). It is nil in tests that only drive the HTTP handler.
func (s *Server) SetQuit(fn func()) { s.quit = fn }

// ---- helpers ---------------------------------------------------------

func writeJSON(w http.ResponseWriter, v any, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func writeText(w http.ResponseWriter, code int, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	io.WriteString(w, text)
}

func readBody(r *http.Request) map[string]any {
	n := r.ContentLength
	if n <= 0 {
		return map[string]any{}
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return map[string]any{}
	}
	var v map[string]any
	if json.Unmarshal(raw, &v) != nil {
		return map[string]any{}
	}
	return v
}

func str(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func boolean(m map[string]any, k string) bool {
	v, _ := m[k].(bool)
	return v
}

func zh() bool {
	l := savedLanguage()
	return l == "zh-CN" || l == "zh-TW"
}

func (s *Server) statusExtra() map[string]any {
	rootOK := false
	if p, err := guardPanel(); err == nil && p != "" {
		rootOK = true
	}
	return map[string]any{
		"last_mods":   savedMods(),
		"root_ok":     rootOK,
		"py":          "", // no external interpreter in the Go build
		"phase_label": phaseLabels(zh()),
		"update":      map[string]any{"phase": "unavailable"},
		"update_url":  false,
	}
}

// ---- GET -------------------------------------------------------------

func (s *Server) serveRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		writeText(w, 404, "not found")
		return
	}
	raw, err := webFS.ReadFile("web/index.html")
	if err != nil {
		writeText(w, 500, "GUI file not found")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(raw)
}

func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.st.snapshot(s.statusExtra()), 200)
}

func (s *Server) getHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"ok": true}, 200)
}

func (s *Server) language(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		code := str(readBody(r), "language")
		writeJSON(w, map[string]any{"ok": saveLanguage(code), "language": savedLanguage()}, 200)
		return
	}
	writeJSON(w, map[string]any{"language": savedLanguage()}, 200)
}

func (s *Server) getDiag(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"ok": true, "summary": diagBundleText(), "text": diagBundleText(),
		"counts": map[string]int{}, "panel": sanitizeDiagText(panelDir()), "mods_root": "",
		"dir": diagDir(),
	}, 200)
}

// ---- POST ------------------------------------------------------------

func (s *Server) jsError(w http.ResponseWriter, r *http.Request) {
	b := readBody(r)
	line := fmt.Sprintf("[JS] %s  %s:%s  %s", str(b, "msg"), str(b, "src"),
		fmt.Sprint(b["line"]), str(b, "stack"))
	s.st.push(line)
	writeJSON(w, map[string]any{"ok": true}, 200)
}

func (s *Server) diagBundle(w http.ResponseWriter, r *http.Request) {
	p, err := writeDiagBundle()
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()}, 200)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "path": p, "dir": diagDir(),
		"summary": diagBundleText()}, 200)
}

func (s *Server) pickFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeText(w, 404, "not found")
		return
	}
	b := readBody(r)
	initial := str(b, "initial")
	// The dialog's start folder is only a hint inside the browser window; the
	// box itself stays empty on a first run (see savedMods).
	if initial == "" {
		initial = savedMods()
		if initial == "" {
			initial = autodetectMods()
		}
	}
	p, err := pickFolderNative(T("选择你的 Mods 目录（WWMI 下面的那个 Mods）",
		"Pick your Mods folder (the Mods under WWMI)"), initial)
	if err != nil {
		writeJSON(w, map[string]any{"path": nil, "error": err.Error()}, 200)
		return
	}
	writeJSON(w, map[string]any{"path": p}, 200)
}

func (s *Server) startJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeText(w, 404, "not found")
		return
	}
	b := readBody(r)
	modsInput := str(b, "mods")
	opts := ConfigOptions{
		IgnoreDisabled: boolean(b, "ignore_disabled"),
		CleanStale:     boolean(b, "clean_stale"),
	}

	s.runMu.Lock()
	if s.running {
		s.runMu.Unlock()
		writeJSON(w, map[string]any{"ok": false,
			"error": T("配置正在进行中\n等它结束，或者点取消。",
				"Setup is already running.\nWait for it to finish, or click Cancel.")}, 200)
		return
	}
	// Resolve synchronously: a bad path must not occupy the lock.
	modsRoot, wwmi, scanRoot, err := resolveMods(modsInput)
	if err != nil {
		s.st.setIdle()
		s.runMu.Unlock()
		writeJSON(w, map[string]any{"ok": false, "error": twoLine(err)}, 200)
		return
	}
	panelDir, err := guardPanel()
	if err != nil {
		s.st.setIdle()
		s.runMu.Unlock()
		writeJSON(w, map[string]any{"ok": false, "error": twoLine(err)}, 200)
		return
	}
	s.running = true
	cancel := make(chan struct{})
	s.cancelCh = cancel
	s.st.reset("config", modsRoot)
	s.runMu.Unlock()
	// Only a path that survived resolveMods + guardPanel is remembered, so the
	// box can never reopen with something that would fail again.
	// Remember the folder the user actually picked, not the inferred Mods root:
	// a scoped run must reopen as the same scoped run. Both paths survived
	// resolveMods + guardPanel, so neither can make the box reopen invalid.
	saveMods(scanRoot)
	udir := filepath.Join(panelDir, "user")

	go func() {
		defer func() {
			s.runMu.Lock()
			s.running = false
			s.cancelCh = nil
			s.runMu.Unlock()
		}()
		// A panic here would take the whole process down: the window vanishes
		// and the log gets no line at all. Turn it into an ordinary failed run
		// so the user sees a message instead of an empty desktop.
		defer func() {
			if r := recover(); r != nil {
				s.st.push("✗ panic: " + fmt.Sprint(r))
				captureFailure("panic: "+fmt.Sprint(r), modsRoot, scanRoot, udir)
				s.st.failAtStage(T("配置失败", "Setup failed") + " —— " + fmt.Sprint(r))
			}
		}()
		summary, err := s.st.pipeline(modsRoot, wwmi, scanRoot, panelDir, udir, opts, cancel)
		if err == errCancelled {
			s.st.fail(T("已取消", "cancelled"), true)
			s.st.push("✗ " + T("已中止", "aborted"))
			return
		}
		if err != nil {
			// The box fits two lines, so it gets exactly two: the conclusion and
			// the next step. The raw cause goes to the log only -- it is what a
			// Diagnostics bundle carries, and it is noise for the user.
			s.st.push("✗ " + err.Error())
			// Freeze the failing inputs now: the log tail is a rolling window
			// and a few retries would push this cause out of the report.
			captureFailure(err.Error(), modsRoot, scanRoot, udir)
			var ue *uiErr
			var msg string
			if errors.As(err, &ue) {
				msg = T("配置失败", "Setup failed") + " —— " + ue.what
				if ue.how != "" {
					msg += "\n" + ue.how
				}
			} else {
				// Unexpected error shape: fall back to the old "pick the real
				// cause out of the log" behaviour rather than showing nothing.
				reason := s.st.lastReason()
				if reason == "" {
					reason = " —— " + err.Error()
				}
				msg = T("配置失败", "Setup failed") + reason
			}
			s.st.failAtStage(msg)
			return
		}
		s.st.finish(summary, false)
		clearFailure()
		s.st.push("")
		s.st.push("✓ " + T("配置完成 —— 回到游戏即可看到效果。", "Setup complete -- switch back to the game to see it."))
	}()

	writeJSON(w, map[string]any{"ok": true, "mods_root": modsRoot, "job": "config"}, 200)
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	// Check-and-close under the same lock startJob uses to publish the
	// channel. Without it two concurrent cancels can both see the channel
	// open and the second close panics.
	s.runMu.Lock()
	if ch := s.cancelCh; ch != nil {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
	s.runMu.Unlock()
	s.st.push(T("（已请求中止…）", "(abort requested...)"))
	writeJSON(w, map[string]any{"ok": true}, 200)
}

func (s *Server) quitAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeText(w, 404, "not found")
		return
	}
	// A configuration may still be running. Ask it to stop and give it a
	// moment to reach its next checkpoint, so the process is not torn down in
	// the middle of the pipeline. Aborting is safe: every artifact is written
	// through a temp file + rename, so a partial run leaves the previous
	// version in place rather than a truncated file.
	s.runMu.Lock()
	running := s.cancelCh
	if running != nil {
		select {
		case <-running:
		default:
			close(running)
		}
	}
	s.runMu.Unlock()

	writeJSON(w, map[string]any{"ok": true}, 200)
	delay := 120 * time.Millisecond
	if running != nil {
		delay = 400 * time.Millisecond
	}
	// Give the response a moment to flush, then close the window.
	time.AfterFunc(delay, func() {
		if s.quit != nil {
			s.quit()
		}
	})
}

func filepathJoin(parts ...string) string { return strings.Join(parts, string(os.PathSeparator)) }
