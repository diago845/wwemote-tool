package gui

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"wwemote/internal/render"
)

// leftOverTemp reports temp files in dir, which an atomic write must not leak.
func leftOverTemp(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			out = append(out, e.Name())
		}
	}
	return out
}

// A truncated <char>.mods.json is a dead end: the next run fails with
// "cannot read the character data" and the only way out is deleting the file
// by hand. Every write therefore has to be all-or-nothing.
func TestWriteFileAtomicLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "Hsin.mods.json")
	if err := writeFileAtomic(p, []byte("first")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writeFileAtomic(p, []byte("second")); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(raw) != "second" {
		t.Fatalf("content = %q, want %q", raw, "second")
	}
	if left := leftOverTemp(t, dir); len(left) != 0 {
		t.Fatalf("atomic write left temp files: %v", left)
	}
}

// The user ini is read by the game. A half-written one is worse than an old
// one: 3DMigoto parses it and silently binds nothing.
func TestWriteIfChangedIsAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "WWEmoteMods.ini")
	if err := render.WriteIfChanged(p, "a"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := render.WriteIfChanged(p, "b"); err != nil {
		t.Fatalf("second: %v", err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(raw) != "b" {
		t.Fatalf("content = %q, want %q", raw, "b")
	}
	if left := leftOverTemp(t, dir); len(left) != 0 {
		t.Fatalf("WriteIfChanged left temp files: %v", left)
	}
	// Unchanged content must not touch the file at all (mtime stays put).
	st1, _ := os.Stat(p)
	if err := render.WriteIfChanged(p, "b"); err != nil {
		t.Fatalf("third: %v", err)
	}
	st2, _ := os.Stat(p)
	if !st1.ModTime().Equal(st2.ModTime()) || st1.Size() != st2.Size() {
		t.Fatal("rewrote an identical file")
	}
}

func TestAppendLogFileRotates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "WWEmote.log")
	if err := os.WriteFile(p, bytes.Repeat([]byte("x"), logMaxBytes+1), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	appendLogFile(p, "after rollover")

	if _, err := os.Stat(p + ".1"); err != nil {
		t.Fatalf("expected rollover file %s.1: %v", p, err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat log: %v", err)
	}
	if st.Size() > 64 {
		t.Fatalf("log kept %d bytes after rollover, want only the new line", st.Size())
	}
}

func TestPruneDiagFiles(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 15; i++ {
		n := filepath.Join(dir, fmt.Sprintf("diag_20260101_%06d.txt", i))
		if err := os.WriteFile(n, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	// A non-diag file must survive: the same directory holds language.json.
	keep := filepath.Join(dir, "language.json")
	os.WriteFile(keep, []byte("{}"), 0o644)

	pruneDiagFiles(dir, 10)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var diag []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "diag_") {
			diag = append(diag, e.Name())
		}
	}
	if len(diag) != 10 {
		t.Fatalf("kept %d diag files, want 10: %v", len(diag), diag)
	}
	sort.Strings(diag)
	// The newest (highest timestamp) survive.
	if !strings.Contains(diag[0], "000005") {
		t.Fatalf("pruned the wrong end: oldest kept is %s", diag[0])
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("prune removed a non-diag file: %v", err)
	}
}

// Two clicks on Cancel used to be able to close the same channel twice, which
// panics and takes the window down with no message.
func TestCancelTwiceDoesNotPanic(t *testing.T) {
	s := NewServer(nil)
	ch := make(chan struct{})
	s.runMu.Lock()
	s.running = true
	s.cancelCh = ch
	s.runMu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.cancel(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/cancel", nil))
		}()
	}
	wg.Wait()

	select {
	case <-ch:
	default:
		t.Fatal("cancel did not close the channel")
	}
}

// Closing the window mid-run must ask the pipeline to stop first, so it does
// not tear the process down while a write is in flight.
func TestQuitCancelsRunningJob(t *testing.T) {
	s := NewServer(nil)
	ch := make(chan struct{})
	s.runMu.Lock()
	s.running = true
	s.cancelCh = ch
	s.runMu.Unlock()

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/quit", nil))
	if w.Code != 200 {
		t.Fatalf("POST /api/quit = %d, want 200", w.Code)
	}
	select {
	case <-ch:
	default:
		t.Fatal("quit left the job running")
	}
}

// A GET must not open a modal dialog or quit the app: the window has no
// address bar, but a prefetcher or a stray fetch should never have side effects.
func TestEndpointsRejectGet(t *testing.T) {
	s := NewServer(nil)
	for _, path := range []string{"/api/pick-folder", "/api/quit"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Errorf("GET %s = %d, want 404", path, w.Code)
		}
	}
}

// The error text must describe this binary. It used to say the runtime payload
// was missing "python/", which the Go build does not ship at all.
func TestRootErrorMessageMatchesGoBuild(t *testing.T) {
	h, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatalf("read page: %v", err)
	}
	page := string(h)
	if strings.Contains(page, "python/") || strings.Contains(page, "python") &&
		strings.Contains(page, "payload") {
		t.Fatal("page still tells the user a python runtime is missing")
	}
	for _, needle := range []string{"WWEmote.ini", "chars/"} {
		if !strings.Contains(page, needle) {
			t.Errorf("root error message does not mention %q", needle)
		}
	}
}
