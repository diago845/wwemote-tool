package gui

import (
	"os"
	"strings"
	"testing"
)

// A bad path is rejected synchronously by /api/start. The state machine must
// leave "done": a stale done state made the front-end poll clear the red error
// box one frame after it appeared. setIdle is the backend half of that fix.
func TestSetIdleClearsDoneState(t *testing.T) {
	st := newState("")
	st.reset("config", "C:/mods")
	st.finish(nil, false)
	if st.State != "done" {
		t.Fatalf("precondition: state = %q, want done", st.State)
	}
	st.setIdle()
	if st.State != "idle" {
		t.Errorf("after setIdle state = %q, want idle", st.State)
	}
	if st.Summary != nil {
		t.Errorf("after setIdle summary = %v, want nil", st.Summary)
	}
	if st.Error != nil {
		t.Errorf("after setIdle error = %v, want nil", st.Error)
	}
	if st.Job != nil {
		t.Errorf("after setIdle job = %v, want nil", st.Job)
	}
	// The last good path is intentionally kept so the box does not reopen empty.
	root := "C:/mods"
	if st.ModsRoot == nil || *st.ModsRoot != root {
		t.Errorf("after setIdle mods_root = %v, want %q", st.ModsRoot, root)
	}
}

// The embedded page must swallow drags that land outside the address bar.
// Without a document-level handler, Chromium navigates a dropped folder as
// file:// and paints a giant white page over the panel.
func TestPageBlocksStrayFolderDrop(t *testing.T) {
	raw, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)
	for _, must := range []string{
		`document.addEventListener(type, function (e)`,
		`if (inDropWrap(e.target)) return;`,
		`e.dataTransfer.dropEffect = "none";`,
		`"dragenter", "dragover", "dragleave", "drop"`,
	} {
		if !strings.Contains(page, must) {
			t.Errorf("page missing document-level drop guard: %s", must)
		}
	}
}

// stickyError must win over a stale "done" state: the old code cleared the box
// on state === "done" before checking the local error, so path-validation
// errors flashed for one frame. Guard the order in the embedded page.
func TestPageStickyErrorBeatsStaleDone(t *testing.T) {
	raw, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)
	sticky := strings.Index(page, "} else if (stickyError) {")
	done := strings.Index(page, `} else if (s.state === "done") {`)
	if sticky < 0 || done < 0 {
		t.Fatalf("stickyError branch (%d) or done branch (%d) missing", sticky, done)
	}
	if sticky > done {
		t.Errorf("stickyError branch (%d) must come BEFORE done branch (%d)", sticky, done)
	}
	// No unconditional className reset may sit in the done+summary render path.
	old := `sumEl.className = "summary show";` + "\n" + `        if (errEl) errEl.className = "errbox";`
	if strings.Contains(page, old) {
		t.Error("done+summary still clears errbox unconditionally")
	}
}
