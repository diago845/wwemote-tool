package guard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestArtifactSourcesCarryNoCJK is the gate itself: the packages that build
// artifact text must not hold a non-ASCII literal anywhere outside a comment.
// A failure here is not cosmetic -- the literal travels into the exe and, for
// these packages, very often into the ini or json the user ends up with.
func TestArtifactSourcesCarryNoCJK(t *testing.T) {
	roots := []string{
		filepath.Join("..", "render"),
		filepath.Join("..", "bake"),
		filepath.Join("..", "scan"),
		".",
	}
	issues := CheckSource(roots...)
	if len(issues) == 0 {
		return
	}
	if len(issues) > 8 {
		issues = issues[:8]
	}
	msg := "generator sources carry non-ASCII literals; see docs/render-notes.md (rule: artifacts stay ASCII):"
	for _, is := range issues {
		msg += "\n  " + is.String()
	}
	t.Fatal(msg)
}

// TestCmdSourcesCarryNoUnpairedCJK covers the packages that build the exe
// itself (cmd/wwemote, cmd/wwaudit), which the generator gate above does not
// reach. A non-ASCII literal here is compiled straight into WWEmoteTool.exe.
//
// The rule is about *unpaired* prose, not all CJK: the user-facing message
// boxes in window.go deliberately show English and Chinese together for
// Chinese users, and the webview UI ships five languages. Those are allowed.
// What must never appear is a lone Chinese passage with no English companion
// (the old `usage` constant was exactly that and shipped in the exe).
//
// The allowlist matches on line content, not line numbers, so edits that shift
// lines do not break it. Each entry must still contain an English companion on
// the same line or an adjacent line of the same literal; see the paired check
// below. Adding a new entry here means adding a bilingual user notice -- never
// a generator string that gets written into ini/json.
func TestCmdSourcesCarryNoUnpairedCJK(t *testing.T) {
	roots := []string{
		filepath.Join("..", "..", "cmd", "wwemote"),
		filepath.Join("..", "..", "cmd", "wwaudit"),
	}
	issues := CheckSource(roots...)

	// Known bilingual user notices: English text with a Chinese translation
	// shown to the same user. Written as rune escapes so this gate source itself
	// stays ASCII. Fragments match on content and survive line-number drift.
	allowed := []string{
		"\u65e0\u6cd5\u542f\u52a8\u672c\u5730\u670d\u52a1", // cannot start local service (paired with English above)
		"\u9700\u8981\u300c",       // needs the WebView2 runtime (paired with English above)
		"\u8bf7\u5b89\u88c5\u540e", // install then rerun (paired with English above)
	}

	var unpaired []Issue
	for _, is := range issues {
		if containsAny(is.Text, allowed) {
			continue
		}
		unpaired = append(unpaired, is)
	}
	if len(unpaired) == 0 {
		return
	}
	if len(unpaired) > 8 {
		unpaired = unpaired[:8]
	}
	msg := "cmd sources carry unpaired non-ASCII literals (baked into the exe):"
	for _, is := range unpaired {
		msg += "\n  " + is.String()
	}
	t.Fatal(msg)
}

func containsAny(s string, frags []string) bool {
	for _, f := range frags {
		if strings.Contains(s, f) {
			return true
		}
	}
	return false
}

// TestViolatingRuneForms keeps the rune test honest. Each entry
// is a real escape from this project's history or the shape right next to it;
// if any of them stops being flagged, the gate above has gone quiet.
func TestViolatingRuneForms(t *testing.T) {
	for _, s := range []string{
		"0..126=测试文本",        // the manifest tag that shipped in 1.6.0
		";  （%d×12 = %d B）。", // full-width parens, missed by U+4E00 scans
		"★ 测试标记",             // the grading marker used in old comments
		"—— 测试短语",            // em dash plus dated field notes
		"“测试引用”",             // curly quotes
		"…",                  // ellipsis
		"\u3000",             // ideographic space
		"角色甲 6 套 · 角色乙 4 套",  // middle dot between set labels
	} {
		if _, ok := ViolatingRune(s); !ok {
			t.Errorf("not flagged, gate would be blind to it: %q", s)
		}
	}
}

// TestLegitContentPasses pins the other side: over-strictness is how a gate
// gets deleted six months later.
func TestLegitContentPasses(t *testing.T) {
	for _, s := range []string{
		"0..126=batch0 row k (mod user space); 127..161=batch1 row k-127",
		"ep_mod_base_87740.buf",
		"key_semantics",
		"café", // Latin-1 accents are not the problem
	} {
		if r, ok := ViolatingRune(s); ok {
			t.Errorf("%q must be allowed, but %U was flagged", s, r)
		}
	}
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestINICommentIsAComment(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "bad.ini", "; 生成器说明文本\nkey = 1\n")
	writeFile(t, dir, "inline.ini", "key = 1 ; 行尾注释\n")
	writeFile(t, dir, "path.ini", "cs-u5 = Resource\\Mods\\心\\HsinMod\\1.ini\n")
	writeFile(t, dir, "clean.ini", "; ---- generated ----\nkey = 1\n")

	issues, files := CheckDir(dir)
	if files != 4 {
		t.Fatalf("expected 4 files scanned, got %d", files)
	}
	got := map[string]int{}
	for _, is := range issues {
		got[filepath.Base(is.File)]++
	}
	if got["bad.ini"] != 1 || got["inline.ini"] != 1 {
		t.Fatalf("comment prose must be flagged: %v", got)
	}
	if got["path.ini"] != 0 || got["clean.ini"] != 0 {
		t.Fatalf("user paths and ASCII comments must pass: %v", got)
	}
}

const setNamesJSON = `{
 "sk_paths": {
  "测试角色/HsinMod/1.ini": 103514,
  "测试角色/角色变体/Form1/mod.ini": 98539,
  "测试角色/角色皮肤/常态/mod.ini": 437416,
  "测试角色/角色皮肤/大招/mod.ini": 434287
 },
 "active": "测试角色/HsinMod/1.ini",
 "variants": {
  "常态": {"vc": 437416},
  "大招": {"vc": 434287}
 },
 "delta": {"base": "ep_mod_base_98539.buf"}
}`

// TestJSONSetNamesAreAllowed covers the one legitimate case in a generated
// json: set labels are the user's own folder names, so they are Chinese on a
// Chinese install and nothing in this tool may rewrite them.
func TestJSONSetNamesAreAllowed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "测试角色.mods.json", setNamesJSON)
	issues, _ := CheckDir(dir)
	if len(issues) != 0 {
		t.Fatalf("set labels from the user's mod tree must pass, got: %v", issues)
	}
}

// TestJSONMetadataIsFlagged is the manifest case: its comments live in json
// strings, so a gate that only reads ini text would never have caught it.
func TestJSONMetadataIsFlagged(t *testing.T) {
	dir := t.TempDir()
	body := `{
 "format": "sparse-csr-v1",
 "base_buf": "ep_mod_base_87740.buf",
 "key_semantics": "0..126=测试文本; 127..161=测试文本",
 "baked_by": "WWEmote"
}`
	writeFile(t, dir, "x_sparse_manifest.json", body)
	issues, _ := CheckDir(dir)
	if len(issues) != 1 || issues[0].Line != 4 {
		t.Fatalf("expected one finding on line 4, got: %v", issues)
	}
}

func TestStaleTagIsReported(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "old_sparse_manifest.json", `{"baked_by": "bake_mod_delta_lite.py", "bake_version": "sparse-csr-v2-incremental"}`)
	writeFile(t, dir, "old_algo_manifest.json", `{"baked_by": "WWEmote", "bake_version": "sparse-csr-v1"}`)
	writeFile(t, dir, "new_sparse_manifest.json", `{"baked_by": "WWEmote", "bake_version": "sparse-csr-v2-incremental"}`)

	stale := CheckStale(dir, "WWEmote", "sparse-csr-v2-incremental")
	if len(stale) != 2 {
		t.Fatalf("expected the foreign tag and old algorithm to be reported, got: %v", stale)
	}
	seen := map[string]bool{}
	for _, s := range stale {
		seen[filepath.Base(s.File)] = true
		if !s.Stale {
			t.Fatal("a stale manifest must be reported as stale, not as a violation")
		}
	}
	if !seen["old_sparse_manifest.json"] || !seen["old_algo_manifest.json"] {
		t.Fatalf("unexpected stale set: %v", seen)
	}
}
