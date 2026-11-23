package gui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSanitizeDiagTextPrivacy: a bundle must never carry a user name, a drive
// letter or a launcher install path, no matter how the path is spelled.
func TestSanitizeDiagTextPrivacy(t *testing.T) {
	in := strings.Join([]string{
		`  panel itself：C:\Users\testuser\AppData\Local\Temp\guwindow`,
		`Wrote D:\Games\XXMI Launcher\WWMI\Mods\WWEmote\user\WWEmoteMods.ini（27693 bytes）`,
		`mods = C:\XXMI Launcher\WWMI\Mods`,
		`E:\some\random\place\file.txt`,
	}, "\n")
	out := sanitizeDiagText(in)
	for _, leak := range []string{"testuser", `C:\Users`, `D:\Games`, `C:\XXMI`, `E:\some`, "Users\\"} {
		if strings.Contains(out, leak) {
			t.Errorf("privacy leak %q in:\n%s", leak, out)
		}
	}
	// No absolute path may survive in any form (drive letter followed by colon).
	if strings.Contains(out, `:\`) {
		t.Errorf("an absolute path survived:\n%s", out)
	}
}

// TestSanitizeDiagKeepsDiagnosticShape: the whole point of keeping Mods\ and
// user\ tails is that the author can still tell which mod and which product
// the failure was about. CJK mod names must survive too (they are not privacy).
func TestSanitizeDiagKeepsDiagnosticShape(t *testing.T) {
	in := `Wrote C:\XXMI Launcher\WWMI\Mods\Hsin\HsinMod-thicc\user\WWEmoteMods.ini（27693 bytes）`
	out := sanitizeDiagText(in)
	for _, keep := range []string{`<PATH>\Mods\Hsin\HsinMod-thicc\user\WWEmoteMods.ini`, `27693 bytes`} {
		if !strings.Contains(out, keep) {
			t.Errorf("lost diagnostic info %q from:\n%s", keep, out)
		}
	}
}

// TestSanitizeDiagBoundaryAtFullWidthBracket: the size annotation uses a
// full-width opening bracket; the path regexp must stop there and not swallow
// the Chinese prose after it.
func TestSanitizeDiagBoundaryAtFullWidthBracket(t *testing.T) {
	in := `C:\Mods\WWEmote\user\user.ini（2924 bytes）`
	out := sanitizeDiagText(in)
	if want := `<PATH>\Mods\WWEmote\user\user.ini（2924 bytes）`; out != want {
		t.Errorf("bracket boundary: got %q want %q", out, want)
	}
}

// TestDiagBundleTextHasLogAndNoPrivacy is the end-to-end contract: the thing
// the user copies to the author must carry run-log substance and no paths.
func TestDiagBundleTextHasLogAndNoPrivacy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WWEMOTE_STATE_DIR", dir)
	logPath := filepath.Join(dir, "WWEmote.log")
	logBody := strings.Join([]string{
		`WWEmote configure - C:\Users\testuser\WWMI\Mods`,
		`  indexed 174 ini`,
		`  OK hsin: 5 sets, 2945 bytes`,
		`  wrote C:\Users\testuser\WWMI\Mods\WWEmote\user\WWEmoteMods.ini（27693 bytes）`,
		`  fail detail: missing base mesh at Mods\Hsin\HsinMod-thicc`,
	}, "\n")
	if err := os.WriteFile(logPath, []byte(logBody), 0o644); err != nil {
		t.Fatal(err)
	}

	report := diagBundleText()

	// Diagnostic substance from the log made it in.
	for _, keep := range []string{"indexed 174 ini", "OK hsin: 5 sets, 2945 bytes", "missing base mesh", `Mods\Hsin\HsinMod-thicc`} {
		if !strings.Contains(report, keep) {
			t.Errorf("report missing log substance %q:\n%s", keep, report)
		}
	}
	// Log tail marker tells the author where the log begins.
	if !strings.Contains(report, "log lines (sanitized)") {
		t.Errorf("report missing log-section marker:\n%s", report)
	}
	// And no privacy, including from the environment header (exe/panel/mods).
	for _, leak := range []string{"testuser", `C:\Users`, `:\`} {
		if strings.Contains(report, leak) {
			t.Errorf("report leaks %q:\n%s", leak, report)
		}
	}
}

// TestFailureSnapshotCapturesInputs: the failure's own config is frozen at the
// moment it happens, because retries push the cause out of the rolling log.
func TestFailureSnapshotCapturesInputs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WWEMOTE_STATE_DIR", dir)
	udir := t.TempDir()
	cfg := `{"pvb_paths":["Resource\\Mods\\Hsin\\HsinMod thicc\\Hsin Mod\\1.ini\\PositionBuffer"]}`
	if err := os.WriteFile(filepath.Join(udir, "hsin.mods.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	captureFailure(`Hsin: fragment "1.ini" matches 2 of 3 pvb_paths`, `C:\XXMI Launcher\WWMI\Mods`, `C:\XXMI Launcher\WWMI\Mods\Hsin\HsinMod thicc`, udir)

	report := diagBundleText()
	marker := "--- last failed run: inputs captured at failure (sanitized) ---"
	if !strings.Contains(report, marker) {
		t.Fatalf("report missing failure snapshot section:\n%s", report)
	}
	for _, keep := range []string{`fragment "1.ini" matches 2 of 3`, "--- hsin.mods.json ---", `Resource\\Mods\\Hsin`, `<PATH>\Mods\Hsin\HsinMod thicc`} {
		if !strings.Contains(report, keep) {
			t.Errorf("snapshot lost %q:\n%s", keep, report)
		}
	}
	for _, leak := range []string{"C:\\XXMI", `:\`} {
		if strings.Contains(report, leak) {
			t.Errorf("snapshot leaked %q:\n%s", leak, report)
		}
	}
}

// TestFailureSnapshotClearedOnSuccess: a stale failure must not be reported
// as the cause of a later problem.
func TestFailureSnapshotClearedOnSuccess(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WWEMOTE_STATE_DIR", dir)
	captureFailure("old failure", "m", "s", t.TempDir())
	if !strings.Contains(diagBundleText(), "old failure") {
		t.Fatal("snapshot not captured")
	}
	clearFailure()
	if strings.Contains(diagBundleText(), "old failure") {
		t.Error("stale failure survived a successful run")
	}
}

// TestRecentLogTextCapsLines: a long log is trimmed to the tail so a bundle
// stays pasteable, and the newest lines win.
func TestRecentLogTextCapsLines(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WWEMOTE_STATE_DIR", dir)
	var b strings.Builder
	for i := 0; i < 400; i++ {
		b.WriteString("line-")
		b.WriteString(strconv.Itoa(i))
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "WWEmote.log"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out := recentLogText(10)
	if got := strings.Count(out, "line-"); got != 10 {
		t.Errorf("recentLogText(10) returned %d lines, want 10", got)
	}
	// The tail is lines 390..399 - the newest, not the oldest.
	for _, want := range []string{"line-390", "line-399"} {
		if !strings.Contains(out, want) {
			t.Errorf("recentLogText missing newest line %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "line-389") {
		t.Errorf("recentLogText kept a line outside the tail window:\n%s", out)
	}
}

// TestRecentLogTextMissingIsBenign: a fresh install has no log yet.
func TestRecentLogTextMissingIsBenign(t *testing.T) {
	t.Setenv("WWEMOTE_STATE_DIR", t.TempDir())
	if got := recentLogText(10); !strings.Contains(got, "no log yet") {
		t.Errorf("missing log: got %q", got)
	}
}
