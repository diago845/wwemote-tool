// Package gui serves the configuration window: a local HTTP server plus the
// state machine the front-end polls. The Go build is self-contained -- the
// HTML is embedded, the renderer/scanner are in-process, and every file a
// configuration run produces lands under <panel>/user.
package gui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// state values mirror the front-end: idle | running | done | error | cancelled
type State struct {
	mu sync.RWMutex

	State      string     `json:"state"`
	Job        *string    `json:"job"`
	Phase      *string    `json:"phase"`
	PhaseIndex int        `json:"phase_index"`
	NPhases    int        `json:"n_phases"`
	Pct        int        `json:"pct"`
	Message    string     `json:"message"`
	Log        []string   `json:"log"`
	Summary    any        `json:"summary"`
	Error      *string    `json:"error"`
	StartedAt  *time.Time `json:"-"`
	EndedAt    *time.Time `json:"-"`
	ModsRoot   *string    `json:"mods_root"`
	LogPath    string     `json:"log_path"`

	elapsed float64
}

func newState(logPath string) *State {
	return &State{
		State: "idle", NPhases: 3, Message: "准备就绪",
		Log: []string{}, LogPath: logPath,
	}
}

func (s *State) reset(job, modsRoot string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.State = "running"
	s.Job = &job
	s.Phase = nil
	s.PhaseIndex = 0
	s.Pct = 0
	s.Message = "准备就绪"
	s.Log = []string{}
	s.Summary = nil
	s.Error = nil
	s.StartedAt = &now
	s.EndedAt = nil
	s.ModsRoot = &modsRoot
}

// setIdle returns the machine to idle without launching a run. A synchronous
// /api/start rejection (e.g. a bad path) uses this so a stale "done" from the
// previous successful run cannot make the front-end poll hide the error box
// that the rejection just displayed. ModsRoot is deliberately kept: the last
// good path stays in the address bar.
func (s *State) setIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.State = "idle"
	s.Job = nil
	s.Phase = nil
	s.EndedAt = nil
	s.Summary = nil
	s.Error = nil
}

func (s *State) push(line string) {
	s.mu.Lock()
	s.Log = append(s.Log, line)
	if len(s.Log) > 800 {
		s.Log = s.Log[len(s.Log)-800:]
	}
	s.mu.Unlock()
	appendLogFile(s.LogPath, line)
}

// lastReason mirrors the Python backend's _last_reason(): pick the real cause
// out of the log so the red error line never shows only a generic wrapper.
// Prefer the last "✗" line, then "★"/"⇒"; never invent a cause when none exists.
func (s *State) lastReason() string {
	s.mu.RLock()
	lines := append([]string(nil), s.Log...)
	s.mu.RUnlock()
	for i := len(lines) - 1; i >= 0; i-- {
		ln := strings.TrimSpace(lines[i])
		if strings.HasPrefix(ln, "✗") {
			return " —— " + strings.TrimSpace(strings.TrimPrefix(ln, "✗"))
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		ln := strings.TrimSpace(lines[i])
		if strings.HasPrefix(ln, "★") || strings.HasPrefix(ln, "⇒") {
			return " —— " + ln
		}
	}
	return ""
}

func (s *State) setPhase(idx, pct int, phase, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PhaseIndex = idx
	s.Pct = pct
	s.Phase = &phase
	s.Message = msg
}

func (s *State) setPct(pct int) {
	s.mu.Lock()
	if pct > s.Pct && pct < 100 {
		s.Pct = pct
	}
	s.mu.Unlock()
}

func (s *State) finish(summary any, cancelled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.EndedAt = &now
	if cancelled {
		s.State = "cancelled"
	} else {
		s.State = "done"
		s.Summary = summary
		s.Pct = 100
	}
	s.Message = "配置完成"
}

func (s *State) fail(msg string, cancelled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.EndedAt = &now
	if cancelled {
		s.State = "cancelled"
	} else {
		s.State = "error"
	}
	s.Error = &msg
	s.Message = msg
}

// failAtStage reports a failed run without printing the same sentence twice.
// The error box carries the two-line message; the progress line keeps the stage
// name, which is the only thing that line can still add once the run is over.
func (s *State) failAtStage(msg string) {
	prefix := T("停止于 ", "Stopped at ")
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.EndedAt = &now
	s.State = "error"
	s.Error = &msg
	s.Message = prefix + s.Message
}

// snapshot is the /api/status payload. It is a plain map so the field set can
// be kept identical to the Python backend the front-end was written against.
func (s *State) snapshot(extra map[string]any) map[string]any {
	s.mu.RLock()
	elapsed := 0.0
	if s.StartedAt != nil {
		end := time.Now()
		if s.EndedAt != nil {
			end = *s.EndedAt
		}
		elapsed = float64(int(end.Sub(*s.StartedAt).Seconds()*10)) / 10
	}
	job, phase, modsRoot, errStr := "", "", "", ""
	if s.Job != nil {
		job = *s.Job
	}
	if s.Phase != nil {
		phase = *s.Phase
	}
	if s.ModsRoot != nil {
		modsRoot = *s.ModsRoot
	}
	if s.Error != nil {
		errStr = *s.Error
	}
	log := append([]string(nil), s.Log...)
	snap := map[string]any{
		"state": s.State, "job": job, "phase": phase,
		"phase_index": s.PhaseIndex, "n_phases": s.NPhases, "pct": s.Pct,
		"message": s.Message, "log": log, "summary": s.Summary,
		"error": errStr, "mods_root": modsRoot, "log_path": s.LogPath,
		"elapsed": elapsed,
	}
	s.mu.RUnlock()
	for k, v := range extra {
		snap[k] = v
	}
	return snap
}

// ------------------------------------------------------------------
// Writable roots. One directory holds all small persistent state; config
// products (ini / json / buf) go under <panel>/user by design.
// ------------------------------------------------------------------

// dataRoot is the single durable folder for everything the panel writes:
// language.json, mods.txt (remembered Mods folder), WWEmote.log and the
// diag_*.txt reports. It sits at <panel>/user/data so nothing ever lands
// outside the WWEmote folder. WWEMOTE_STATE_DIR is an explicit override used
// by tests and portable runs.
func dataRoot() string {
	if base := os.Getenv("WWEMOTE_STATE_DIR"); base != "" {
		return base
	}
	return filepath.Join(userDir(), "data")
}

// diagDir shares dataRoot: the rolling log and diagnostic reports live beside
// the small settings files. Kept as a separate name so call sites stay clear
// about which kind of data they are reading or writing.
func diagDir() string { return dataRoot() }

// panelDir is where WWEmoteTool.exe itself sits: that is the panel the user
// unpacked. Its user/ subdir is the single durable root for config products.
func panelDir() string {
	exe, err := os.Executable()
	if err != nil {
		wd, _ := os.Getwd()
		return wd
	}
	return filepath.Dir(exe)
}

func userDir() string { return filepath.Join(panelDir(), "user") }

// ------------------------------------------------------------------
// Language (single file, atomic replace)
// ------------------------------------------------------------------

var languageCodes = map[string]bool{"zh-CN": true, "zh-TW": true, "en": true, "ja": true, "ko": true}

func languagePath() string { return filepath.Join(dataRoot(), "language.json") }

func savedLanguage() string {
	raw, err := os.ReadFile(languagePath())
	if err != nil {
		return "en"
	}
	var v struct {
		Language string `json:"language"`
	}
	if json.Unmarshal(raw, &v) != nil || !languageCodes[v.Language] {
		return "en"
	}
	return v.Language
}

func saveLanguage(code string) bool {
	if !languageCodes[code] {
		return false
	}
	p := languagePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return false
	}
	raw, _ := json.Marshal(map[string]string{"language": code})
	tmp := p + ".tmp"
	if os.WriteFile(tmp, raw, 0o644) != nil {
		return false
	}
	return os.Rename(tmp, p) == nil
}

// ------------------------------------------------------------------
// Last Mods folder (one line, remembered after a job is accepted)
// ------------------------------------------------------------------

// A first run must show an empty box: the only path this build could guess is
// the author's own, and a wrong guess looks like a valid answer. Once the user
// has run a configuration, the box reopens with that folder so they do not have
// to pick it again.
func modsPath() string { return filepath.Join(dataRoot(), "mods.txt") }

func savedMods() string {
	raw, err := os.ReadFile(modsPath())
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(string(raw))
	if p == "" || !isDir(p) {
		return ""
	}
	return p
}

func saveMods(p string) {
	if p == "" {
		return
	}
	if err := os.MkdirAll(dataRoot(), 0o755); err != nil {
		return
	}
	tmp := modsPath() + ".tmp"
	if os.WriteFile(tmp, []byte(p), 0o644) != nil {
		return
	}
	os.Rename(tmp, modsPath())
}

// ------------------------------------------------------------------
// Remembered window height
// ------------------------------------------------------------------

// The window can be resized vertically by dragging its top/bottom edges, and
// that height is remembered in <panel>/user/data so it survives a restart.
// Width is never stored: it stays locked at the build default.
func windowHeightPath() string { return filepath.Join(dataRoot(), "winheight.txt") }

const (
	minWindowHeight = 320
	maxWindowHeight = 1200
)

// SavedWindowHeight returns 0 when there is no remembered height (fresh
// install or corrupted file); the caller then uses its own default.
func SavedWindowHeight() int {
	raw, err := os.ReadFile(windowHeightPath())
	if err != nil {
		return 0
	}
	h := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(string(raw)), "%d", &h); err != nil || h < minWindowHeight || h > maxWindowHeight {
		return 0
	}
	return h
}

// ClampWindowHeight keeps an interactive drag within a reopenable range.
func ClampWindowHeight(h int) int {
	if h < minWindowHeight {
		return minWindowHeight
	}
	if h > maxWindowHeight {
		return maxWindowHeight
	}
	return h
}

// SaveWindowHeight clamps to a sane range so a bad drag cannot make the
// window impossible to reopen, then writes atomically.
func SaveWindowHeight(h int) {
	h = ClampWindowHeight(h)
	p := windowHeightPath()
	if os.MkdirAll(filepath.Dir(p), 0o755) != nil {
		return
	}
	writeFileAtomic(p, []byte(strconv.Itoa(h)))
}

// ------------------------------------------------------------------
// Log (append-only, same lines the GUI sees)
// ------------------------------------------------------------------

// logMaxBytes caps WWEmote.log. Every configure run appends a few dozen
// lines, so without a cap the file grows for the lifetime of the install and
// is the first thing a user is asked to send. One rollover file is enough:
// the interesting part is always the latest run.
const logMaxBytes = 2 << 20

func appendLogFile(path string, line string) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if st, err := os.Stat(path); err == nil && st.Size() > logMaxBytes {
		// Best effort: if another process holds the file, keep appending
		// rather than dropping the line.
		os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(strings.TrimRight(line, "\r\n") + "\n")
}

// writeFileAtomic writes data to path through a temp file and a rename, so an
// interrupted run can never leave a truncated file behind. Every artifact the
// pipeline produces (mods json, delta bufs, ini) goes through this shape: a
// half-written <char>.mods.json makes the next run fail with "cannot read the
// character data", which is a dead end for the user.
func writeFileAtomic(path string, data []byte) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// MigrateLegacyState copies the small settings a previous build kept in
// %LOCALAPPDATA%/WWEmote into the new in-panel locations. It is a one-shot,
// copy-only (never move, never delete) best-effort step: a fresh install has
// nothing to copy, and an upgraded one keeps its language and remembered Mods
// folder without the user having to set them again. Called once at startup.
func MigrateLegacyState() {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return
	}
	old := filepath.Join(base, "WWEmote")
	st, err := os.Stat(old)
	if err != nil || !st.IsDir() {
		return
	}
	newRoot := dataRoot()
	if samePath(old, newRoot) {
		return
	}
	os.MkdirAll(newRoot, 0o755)
	for _, name := range []string{"language.json", "mods.txt"} {
		copyIfAbsent(filepath.Join(old, name), filepath.Join(newRoot, name))
	}
	dst := diagDir()
	os.MkdirAll(dst, 0o755)
	copyIfAbsent(filepath.Join(old, "WWEmote.log"), filepath.Join(dst, "WWEmote.log"))
	entries, _ := os.ReadDir(old)
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "diag_") || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		copyIfAbsent(filepath.Join(old, e.Name()), filepath.Join(dst, e.Name()))
	}
}

func copyIfAbsent(src, dst string) {
	if _, err := os.Stat(dst); err == nil {
		return // never overwrite a setting the user may have changed already
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return
	}
	_ = writeFileAtomic(dst, b)
}

func samePath(a, b string) bool {
	aa, ea := filepath.Abs(a)
	bb, eb := filepath.Abs(b)
	return ea == nil && eb == nil && strings.EqualFold(aa, bb)
}
