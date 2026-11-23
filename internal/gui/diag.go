package gui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// diag is deliberately self-contained: the Go build ships no Python diagnose
// module. The report is short enough to paste into a chat.
type diagItem struct{ Key, Value string }

// diagLogLines caps how much of WWEmote.log goes into a bundle. One configure
// run is ~14 lines, so 150 covers the last several runs - including the one the
// user was looking at when they pressed the button - without turning the report
// into something they cannot paste into a chat.
const diagLogLines = 150

// diagAbsPath matches a Windows absolute path inside a log line. Paths in this
// log end either at end of line or at a full-width opening bracket (the Go
// side writes sizes as "（1234 字节）"); matching only those boundaries keeps a
// path token from swallowing the Chinese prose that follows it.
var diagAbsPath = regexp.MustCompile(`[A-Za-z]:\\[^（\r\n]*`)

// sanitizeDiagPath keeps only the part of a path that helps diagnose: the
// layout under Mods\ or user\. The drive letter, the Windows user name and the
// launcher install directory are replaced by a single <PATH> marker. It does
// not hard-code any of them, so it works for every user. Mod names (which may
// contain spaces or CJK) survive because they live after the anchor.
func sanitizeDiagPath(p string) string {
	lower := strings.ToLower(p)
	for _, anchor := range []string{`\mods\`, `\user\`} {
		if i := strings.LastIndex(lower, anchor); i >= 0 {
			return "<PATH>" + p[i:]
		}
	}
	if strings.HasSuffix(lower, `\mods`) {
		return `<PATH>\Mods`
	}
	return "<PATH>"
}

// sanitizeDiagText removes every absolute path from a block of text. It is
// applied to the whole report - summary lines and log body alike - so a bundle
// never leaks a user name or install location.
func sanitizeDiagText(s string) string {
	return diagAbsPath.ReplaceAllStringFunc(s, sanitizeDiagPath)
}

// recentLogText returns the tail of WWEmote.log, sanitized. A missing log is
// normal for a fresh install and reported as such rather than treated as an
// error.
func recentLogText(max int) string {
	p := filepathJoin(diagDir(), "WWEmote.log")
	b, err := os.ReadFile(p)
	if err != nil || len(b) == 0 {
		return "(no log yet)\n"
	}
	lines := strings.Split(strings.TrimRight(string(b), "\r\n"), "\n")
	if len(lines) > max {
		lines = lines[len(lines)-max:]
	}
	return sanitizeDiagText(strings.Join(lines, "\n")) + "\n"
}

func collectDiag() []diagItem {
	exe, _ := os.Executable()
	items := []diagItem{
		{"app", "WWEmote"},
		{"os", runtime.GOOS + "/" + runtime.GOARCH},
		{"exe", exe},
		{"panel", panelDir()},
		{"user", userDir()},
		{"language", savedLanguage()},
		{"log", filepathJoin(diagDir(), "WWEmote.log")},
		{"time", time.Now().Format("2006-01-02 15:04:05")},
	}
	// Panel markers: what actually exists on disk.
	for _, m := range []string{"WWEmote.ini", "chars", "Shaders", "Textures"} {
		p := filepath.Join(panelDir(), m)
		state := "missing"
		if isFile(p) {
			state = "file"
		} else if isDir(p) {
			state = "dir"
		}
		items = append(items, diagItem{"panel/" + m, state})
	}
	// user-layer products.
	for _, m := range []string{"WWEmoteMods.ini", "user.ini"} {
		p := filepath.Join(userDir(), m)
		state := "missing"
		if st, err := os.Stat(p); err == nil {
			state = fmt.Sprintf("%d bytes", st.Size())
		}
		items = append(items, diagItem{"user/" + m, state})
	}
	mods := savedMods()
	if mods == "" {
		mods = autodetectMods()
	}
	items = append(items, diagItem{"mods", mods})
	if mods != "" {
		nini, ndis := countIni(mods)
		items = append(items, diagItem{"mods/ini", fmt.Sprint(nini)},
			diagItem{"mods/DISABLED_", fmt.Sprint(ndis)})
	}
	return items
}

func countIni(root string) (total, disabled int) {
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if strings.HasPrefix(strings.ToUpper(info.Name()), "DISABLED_") {
				disabled++
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(info.Name()), ".ini") {
			total++
		}
		return nil
	})
	return
}

// diagText is the environment header only. Every value goes through the
// sanitizer so the summary the user copies cannot carry a path either.
func diagText(items []diagItem) string {
	var b strings.Builder
	b.WriteString("WWEmote diagnostic report (paths sanitized)\n")
	b.WriteString("-------------------------------------------\n")
	for _, it := range items {
		b.WriteString(fmt.Sprintf("%-22s %s\n", it.Key, sanitizeDiagText(it.Value)))
	}
	return b.String()
}

// failureSnapshot is the failing run's inputs, captured at the moment of the
// failure. A report built only from the rolling log loses the cause after a
// few more runs push the error out of the tail window. Keeping the inputs that
// produced the error lets the author replay the exact case offline, which is
// the only way to fix a bug that cannot be reproduced on demand.
//
// It is plain text, not JSON: the payload IS a set of .json files, and nesting
// them in JSON would double-escape every backslash and make the report hard
// to read. Each config is copied verbatim -- stripping fields would strip the
// evidence. Sanitize each piece once, before assembling.
const failureSnapshotName = "last_failure.txt"

// captureFailure writes the error, the two roots, and the character configs
// that were on disk when the run failed.
func captureFailure(errText, modsRoot, scanRoot, userDirPath string) {
	var b strings.Builder
	fmt.Fprintf(&b, "at: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "error: %s\n", sanitizeDiagText(errText))
	fmt.Fprintf(&b, "mods_root: %s\n", sanitizeDiagText(modsRoot))
	fmt.Fprintf(&b, "scan_root: %s\n", sanitizeDiagText(scanRoot))
	b.WriteString("\n--- character configs at failure ---\n")
	if entries, err := os.ReadDir(userDirPath); err == nil {
		var configs []string
		for _, en := range entries {
			if en.IsDir() || !strings.HasSuffix(strings.ToLower(en.Name()), ".mods.json") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(userDirPath, en.Name()))
			if err != nil {
				continue
			}
			configs = append(configs, sanitizeDiagText("--- "+en.Name()+" ---\n"+string(raw)))
		}
		sort.Strings(configs)
		for _, c := range configs {
			b.WriteString(c)
			b.WriteString("\n")
		}
	}
	_ = writeFileAtomic(filepath.Join(diagDir(), failureSnapshotName), []byte(b.String()))
}

// clearFailure removes the snapshot after a successful run, so a stale failure
// cannot be mistaken for the cause of a later problem.
func clearFailure() {
	_ = os.Remove(filepath.Join(diagDir(), failureSnapshotName))
}

func failureSnapshotText() string {
	raw, err := os.ReadFile(filepath.Join(diagDir(), failureSnapshotName))
	if err != nil || len(raw) == 0 {
		return ""
	}
	return "\n--- last failed run: inputs captured at failure (sanitized) ---\n" +
		string(raw) + "\n"
}

// diagBundleText is what the user sends to the author: the sanitized
// environment header, the tail of the run log, and the captured inputs of the
// last failure. The log gives the sequence; the snapshot gives the evidence.
func diagBundleText() string {
	return diagText(collectDiag()) +
		"\n--- last " + strconv.Itoa(diagLogLines) + " log lines (sanitized) ---\n" +
		recentLogText(diagLogLines) + failureSnapshotText()
}

func writeDiagBundle() (string, error) {
	dir := diagDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := filepath.Join(dir, fmt.Sprintf("diag_%s.txt", time.Now().Format("20060102_150405")))
	// writeFileAtomic uses tmp+rename, so pressing the button twice in the same
	// second cannot hit the EACCES that overwriting an existing file triggers.
	if err := writeFileAtomic(name, []byte(diagBundleText())); err != nil {
		return "", err
	}
	pruneDiagFiles(dir, 10)
	openFolder(dir)
	return name, nil
}

// pruneDiagFiles keeps the newest few reports. The button is meant to be
// pressed whenever something looks off, so the folder would otherwise fill
// with hundreds of near-identical files. Names carry a sortable timestamp.
func pruneDiagFiles(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, "diag_") || !strings.HasSuffix(n, ".txt") {
			continue
		}
		names = append(names, n)
	}
	if len(names) <= keep {
		return
	}
	sort.Strings(names)
	for _, n := range names[:len(names)-keep] {
		os.Remove(filepath.Join(dir, n))
	}
}
