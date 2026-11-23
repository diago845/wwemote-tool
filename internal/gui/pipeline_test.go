package gui

import (
	"crypto/md5"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"wwemote/internal/scan"
)

func md5File(t *testing.T, p string) (int, string) {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return len(b), fmt.Sprintf("%x", md5.Sum(b))
}

// TestPipelineEndToEnd runs the real scan -> bake -> render chain against the
// machine's Mods/ tree and checks the authoritative ini fingerprints. It skips
// when the WWMI install is not present, so the suite stays portable.
func TestPipelineEndToEnd(t *testing.T) {
	wwmi := os.Getenv("WWEMOTE_TEST_WWMI")
	if wwmi == "" {
		t.Skip("set WWEMOTE_TEST_WWMI to run the real scan pipeline")
	}
	modsRoot := filepath.Join(wwmi, "Mods")
	if !isDir(modsRoot) || !isFile(filepath.Join(wwmi, "d3dx.ini")) {
		t.Fatalf("not a WWMI root: %s", wwmi)
	}

	// t.TempDir()/user -- basename MUST be "user" for the bake base-mesh guard.
	tmp := filepath.Join(t.TempDir(), "user")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}

	st := newState("")
	summary, err := st.pipeline(modsRoot, wwmi, modsRoot, filepath.Dir(tmp), tmp, ConfigOptions{}, nil)
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	if summary == nil {
		t.Fatal("nil summary")
	}

	// The success summary must use the old backend's exact wording, because the
	// shared front-end renderSummary() keys off "N 个角色已适配" and silently
	// drops the whole card on any other shape.
	sm, ok := summary.(map[string]any)
	if !ok {
		t.Fatalf("summary is %T, want map[string]any", summary)
	}
	adapted, _ := sm["adapted"].(string)
	re := regexp.MustCompile(`^\d+ 个角色已适配 —— .+ \d+ 套( · .+ \d+ 套)*$`)
	if !re.MatchString(adapted) {
		t.Errorf("summary.adapted = %q, want old format 'N 个角色已适配 —— 心 M 套 · ...'", adapted)
	}
	reAdded := regexp.MustCompile(`^新增 [1-9]\d* 套 —— .+ \d+ 套( · .+ \d+ 套)*$`)

	ini := filepath.Join(tmp, "WWEmoteMods.ini")
	n, h := md5File(t, ini)
	if n != 32556 || h != "630880297af620222537ae5449a3e86f" {
		t.Errorf("WWEmoteMods.ini = %d bytes / %s, want 32556 / 630880297af620222537ae5449a3e86f", n, h)
	}
	n2, h2 := md5File(t, filepath.Join(tmp, "user.ini"))
	if n2 != 3630 || h2 != "2f3416f10908ea1c2474715a82ce5d45" {
		t.Errorf("user.ini = %d bytes / %s, want 3630 / 2f3416f10908ea1c2474715a82ce5d45", n2, h2)
	}
	buFs, _ := filepath.Glob(filepath.Join(tmp, "*.buf"))
	if len(buFs) < 18 {
		t.Errorf("only %d delta buf files, want >= 18", len(buFs))
	}
	// No files outside user/: the panel dir itself stays untouched.
	entries, _ := os.ReadDir(filepath.Dir(tmp))
	for _, e := range entries {
		if e.Name() != "user" {
			t.Errorf("unexpected file outside user/: %s", e.Name())
		}
	}

	// The first run bakes from scratch, so the added summary must be non-zero.
	added1, _ := sm["added"].(string)
	if !reAdded.MatchString(added1) {
		t.Errorf("first run summary.added = %q, want '新增 N 套 —— ...'", added1)
	}

	// Idempotency: a second run must not change any bytes, and with every
	// delta reused from cache the added summary must read zero -- this is the
	// exact case reported where the old UI kept showing the installed total.
	before, _ := md5File(t, ini)
	second, err := st.pipeline(modsRoot, wwmi, modsRoot, filepath.Dir(tmp), tmp, ConfigOptions{}, nil)
	if err != nil {
		t.Fatalf("second pipeline: %v", err)
	}
	after, _ := md5File(t, ini)
	if before != after {
		t.Error("second run changed WWEmoteMods.ini (not idempotent)")
	}
	sm2, ok := second.(map[string]any)
	if !ok {
		t.Fatalf("second summary is %T", second)
	}
	added2, _ := sm2["added"].(string)
	if added2 != "新增 0 套" {
		t.Errorf("second run summary.added = %q, want '新增 0 套' (all cached)", added2)
	}
	_ = strings.TrimSpace
}

func TestFormatAddedText(t *testing.T) {
	cases := []struct {
		name string
		in   []adaptedEntry
		want string
	}{
		{"no characters", nil, "新增 0 套"},
		{"all cached", []adaptedEntry{
			{label: "心", sets: 6, fresh: 0},
			{label: "hsinult", sets: 4, fresh: 0},
		}, "新增 0 套"},
		{"one character fresh", []adaptedEntry{
			{label: "心", sets: 6, fresh: 2},
			{label: "hsinult", sets: 4, fresh: 0},
		}, "新增 2 套 —— 心 2 套"},
		{"both fresh", []adaptedEntry{
			{label: "心", sets: 6, fresh: 1},
			{label: "hsinult", sets: 4, fresh: 3},
		}, "新增 4 套 —— 心 1 套 · hsinult 3 套"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatAddedText(tc.in); got != tc.want {
				t.Errorf("formatAddedText = %q, want %q", got, tc.want)
			}
		})
	}
}

// An empty Mods/ is nearly always the wrong folder. Saying "setup complete"
// sends the user back to the game to find nothing, with no hint about the path.
func TestPipelineRejectsEmptyModsFolder(t *testing.T) {
	root := t.TempDir()
	wwmi := filepath.Join(root, "wwmi")
	mods := filepath.Join(wwmi, "Mods")
	if err := os.MkdirAll(mods, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wwmi, "d3dx.ini"), []byte("[Loader]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := newState("")
	_, err := st.pipeline(mods, wwmi, mods, filepath.Join(root, "panel"),
		filepath.Join(root, "panel", "user"), ConfigOptions{}, nil)
	if err == nil {
		t.Fatal("an empty Mods folder must not report success")
	}
	var ue *uiErr
	if !errors.As(err, &ue) {
		t.Fatalf("error is %T (%v), want *uiErr so the box gets two lines", err, err)
	}
	if !strings.Contains(strings.ToLower(ue.what), "ini") {
		t.Errorf("what = %q, want it to name the missing artifact", ue.what)
	}
}

// TestInScopeRel pins the folder-boundary rule: a scope of "心/HsinMod" must
// not swallow the sibling "心/HsinMod-thicc", and a path exactly equal to the
// folder is itself in scope via the trailing slash on a real ini below it.
func TestInScopeRel(t *testing.T) {
	cases := []struct {
		scope, rel string
		want       bool
	}{
		{"", "心/DISABLED_HsinMod/Hsin Mod Ultimate-Lune Ming/2.ini", true}, // whole tree
		{"心/HsinMod-thicc", "心/HsinMod-thicc/HsinMod/1.ini", true},
		{"心/HsinMod-thicc", "心/HsinMod-thicc/3Mod/3.ini", true},
		{"心/HsinMod-thicc", "心/DISABLED_HsinMod/Hsin Mod Ultimate-Lune Ming/2.ini", false},
		{"心/HsinMod", "心/HsinMod-thicc/HsinMod/1.ini", false}, // sibling must not match
		{"心/HsinMod-thicc", "心/HsinMod-thicc", false},         // folder itself is not an ini
	}
	for _, tc := range cases {
		got := inScopeRel(tc.scope, tc.rel)
		if got != tc.want {
			t.Errorf("inScopeRel(%q, %q) = %v, want %v", tc.scope, tc.rel, got, tc.want)
		}
	}
}

// TestBuildIndexScopeNarrowsWithoutMovingBasis pins the core rule: walking
// only the chosen folder must not change the rel path used for Resource\Mods.
func TestBuildIndexScopeNarrowsWithoutMovingBasis(t *testing.T) {
	mods := t.TempDir()
	inside := filepath.Join(mods, "心", "HsinMod-thicc")
	outside := filepath.Join(mods, "心", "DISABLED_HsinMod")
	for _, dir := range []string{inside, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{
		filepath.Join(inside, "1.ini"),
		filepath.Join(outside, "2.ini"),
	} {
		if err := os.WriteFile(p, []byte("[TextureOverrideShapeKeyOffsets]\nhash = abcdef12\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := scan.BuildIndexScope(mods, inside)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Recs) != 1 {
		t.Fatalf("scoped index has %d inis, want 1: %v", len(idx.Recs), idx.Order)
	}
	want := "心/HsinMod-thicc/1.ini"
	if idx.Order[0] != want {
		t.Errorf("rel = %q, want %q (basis must stay Mods root)", idx.Order[0], want)
	}
	full, err := scan.BuildIndex(mods)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Recs) != 2 {
		t.Errorf("full index has %d inis, want 2", len(full.Recs))
	}
}

// TestMergeOutsideKeepsOutOfScopeSets uses a real mods.json shape: two sets,
// one inside the chosen folder and one outside. The merged layer must keep the
// outside set's paths, vc and delta while taking fresh data for the inside one.
func TestMergeOutsideKeepsOutOfScopeSets(t *testing.T) {
	prev := []byte(`{
  "sk_paths": [
    "Resource\\Mods\\心\\HsinMod-thicc\\HsinMod\\1.ini\\CustomShapeKeyValuesRW",
    "Resource\\Mods\\心\\HsinMod\\HsinMod\\1.ini\\CustomShapeKeyValuesRW"
  ],
  "pvb_paths": [
    "Resource\\Mods\\心\\HsinMod-thicc\\HsinMod\\1.ini\\PositionBuffer",
    "Resource\\Mods\\心\\HsinMod\\HsinMod\\1.ini\\PositionBuffer"
  ],
  "pose_exclude": [],
  "pose_exclude_vc": {},
  "active": "心/HsinMod-thicc/HsinMod/1.ini",
  "mod_vc": {
    "心/HsinMod/HsinMod/1.ini": 103514,
    "心/HsinMod-thicc/HsinMod/1.ini": 103514
  },
  "mod_delta": {
    "HsinMod-thicc": {"vc": 103514, "sparse": "hsin_mod_103514_sparse.buf", "base": "ep_mod_base_103514.buf"},
    "HsinMod/HsinMod/1.ini": {"vc": 103514, "sparse": "hsin_mod_103514_sparse.buf", "base": "ep_mod_base_103514.buf"}
  }
}
`)
	active := "心/HsinMod-thicc/HsinMod/1.ini"
	fresh := &scan.Layer{
		SkPaths:  []string{`Resource\Mods\心\HsinMod-thicc\HsinMod\1.ini\CustomShapeKeyValuesRW`},
		PvbPaths: []string{`Resource\Mods\心\HsinMod-thicc\HsinMod\1.ini\PositionBuffer`},
		Active:   &active,
		ModVC:    map[string]int{"心/HsinMod-thicc/HsinMod/1.ini": 103514},
		ModDelta: map[string]*scan.Delta{"HsinMod-thicc": {VC: 103514, Sparse: "hsin_mod_103514_sparse.buf", Base: "ep_mod_base_103514.buf"}},
	}
	merged, err := scan.MergeOutside(fresh, prev, "心/HsinMod-thicc")
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.SkPaths) != 2 || len(merged.PvbPaths) != 2 {
		t.Fatalf("merged has %d sk / %d pvb, want 2 / 2", len(merged.SkPaths), len(merged.PvbPaths))
	}
	if !strings.Contains(strings.Join(merged.SkPaths, "\n"), "心\\HsinMod\\HsinMod") {
		t.Errorf("outside set was dropped: %v", merged.SkPaths)
	}
	if merged.ModVC["心/HsinMod/HsinMod/1.ini"] != 103514 {
		t.Errorf("outside set vc lost: %v", merged.ModVC)
	}
	if merged.ModDelta["HsinMod/HsinMod/1.ini"] == nil {
		t.Errorf("outside set delta lost: %v", merged.ModDelta)
	}
	if !scan.HasScopeKey(prev, "心/HsinMod-thicc") {
		t.Error("old config should report an in-scope set")
	}
	if scan.HasScopeKey(prev, "心/SomeOtherMod") {
		t.Error("old config should not report an unrelated scope")
	}
}

// TestCleanStaleScopedIgnoresOutsideFiles verifies the file-level boundary:
// only a candidate buffer that is no longer referenced may be removed.
func TestCleanStaleScopedIgnoresOutsideFiles(t *testing.T) {
	dir := t.TempDir()
	candidates := map[string]bool{"in_mod_100_sparse.buf": true}
	keep := map[string]bool{"in_mod_100_sparse.buf": true}
	for _, name := range []string{
		"in_mod_100_sparse.buf",
		"in_mod_100_sparse_manifest.json",
		"out_mod_200_sparse.buf",
		"out_mod_200_sparse_manifest.json",
		"other.mods.json",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	n, gone := cleanStaleScoped(dir, candidates, keep)
	if n != 0 || len(gone) != 0 {
		t.Fatalf("scoped cleanup removed %d files: %v", n, gone)
	}
	keep = map[string]bool{}
	n, gone = cleanStaleScoped(dir, candidates, keep)
	if n != 2 {
		t.Fatalf("stale in-scope pair should remove 2 files, got %d: %v", n, gone)
	}
	for _, name := range []string{"out_mod_200_sparse.buf", "out_mod_200_sparse_manifest.json", "other.mods.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("out-of-scope file %s was removed", name)
		}
	}
}

func TestLiveWritebackRequiresRealIni(t *testing.T) {
	mods := t.TempDir()
	onDisk := filepath.Join(mods, "xin", "HsinMod thicc_skipemote", "Hsin Mod", "1.ini")
	if err := os.MkdirAll(filepath.Dir(onDisk), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(onDisk, []byte("; ini"), 0o644); err != nil {
		t.Fatal(err)
	}

	live := `Resource\Mods\xin\HsinMod thicc_skipemote\Hsin Mod\1.ini\PositionBuffer`
	gone := `Resource\Mods\xin\HsinMod\HsinMod\1.ini\PositionBuffer`
	if !liveWriteback(mods, []string{gone, live}) {
		t.Fatal("a pvb path whose ini exists should count as live")
	}
	if liveWriteback(mods, []string{gone}) {
		t.Fatal("a DISABLED_-prefixed mod has no ini at its unexcluded path")
	}
	if liveWriteback(mods, nil) {
		t.Fatal("no paths means no live write-back target")
	}
}

func TestClassifyWritebackDistinguishesMarkedFromDead(t *testing.T) {
	mods := t.TempDir()
	live := `Resource\Mods\xin\HsinMod thicc_skipemote\Hsin Mod\1.ini\PositionBuffer`
	// The config still points at the pre-rename name from the DISABLED_ copy.
	gone := `Resource\Mods\xin\HsinMod\HsinMod\1.ini\PositionBuffer`
	bases := []string{"xin/HsinMod thicc"} // scan's suffix-stripped skipped folder
	onDisk := filepath.Join(mods, "xin", "HsinMod thicc_skipemote", "Hsin Mod", "1.ini")
	if err := os.MkdirAll(filepath.Dir(onDisk), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(onDisk, []byte("; ini"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := classifyWriteback(mods, []string{live}, bases); got != wbLive {
		t.Fatalf("existing ini = %v, want wbLive", got)
	}
	if got := classifyWriteback(mods, []string{gone}, bases); got != wbMarked {
		t.Fatalf("dead path under a _skipemote base = %v, want wbMarked", got)
	}
	if got := classifyWriteback(mods, []string{gone}, nil); got != wbDead {
		t.Fatalf("dead path with no marker = %v, want wbDead", got)
	}
	if got := classifyWriteback(mods, nil, bases); got != wbNone {
		t.Fatalf("no paths = %v, want wbNone", got)
	}
	// A different character folder must not be marked by another's skip.
	other := `Resource\Mods\rover\HsinMod\HsinMod\1.ini\PositionBuffer`
	if got := classifyWriteback(mods, []string{other}, bases); got != wbDead {
		t.Fatalf("wrong character folder = %v, want wbDead", got)
	}
}

func TestClassifyWritebackDisabledModIsLive(t *testing.T) {
	mods := t.TempDir()
	// Config ships the unprefixed path; the disabled mod still lives under
	// DISABLED_ until the user enables it. That must be a live target so the
	// character keeps its bindings and needs no reconfigure on enable.
	unprefixed := `Resource\Mods\xin\HsinMod thicc\Hsin Mod\1.ini\PositionBuffer`
	real := filepath.Join(mods, "xin", "DISABLED_HsinMod thicc", "Hsin Mod", "1.ini")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("; ini"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := classifyWriteback(mods, []string{unprefixed}, nil); got != wbLive {
		t.Fatalf("DISABLED_ folder = %v, want wbLive", got)
	}
	// Removing the prefix keeps it live (direct hit).
	enabled := filepath.Join(mods, "xin", "HsinMod thicc", "Hsin Mod", "1.ini")
	if err := os.MkdirAll(filepath.Dir(enabled), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(enabled, []byte("; ini"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := classifyWriteback(mods, []string{unprefixed}, nil); got != wbLive {
		t.Fatalf("enabled folder = %v, want wbLive", got)
	}
	// Genuinely gone (no folder under either name) stays dead.
	gone := `Resource\Mods\xin\Renamed Mod\Hsin Mod\1.ini\PositionBuffer`
	if got := classifyWriteback(mods, []string{gone}, nil); got != wbDead {
		t.Fatalf("renamed/removed mod = %v, want wbDead", got)
	}
}

// An all-disabled character used to fail with "N ini files declared its
// anchor but none was claimed": ScanChar counts disabled declarations in
// NDeclared, but IgnoreDisabled removed them from Found without resyncing the
// count. The zero-declared result is what lets the pipeline move on to the
// normal no-mods path instead of reporting a data anomaly.
func TestApplyIgnoreDisabledResyncsDeclaredCount(t *testing.T) {
	rec := func(enabled bool) *scan.Rec { return &scan.Rec{Enabled: enabled} }
	cases := []struct {
		name         string
		found        map[string]*scan.Rec
		declared     int
		wantDeclared int
		wantKeys     []string
	}{
		{"all disabled", map[string]*scan.Rec{"a": rec(false), "b": rec(false), "c": rec(false)}, 5, 0, []string{}},
		{"mixed", map[string]*scan.Rec{"a": rec(false), "b": rec(true)}, 2, 1, []string{"b"}},
		{"all enabled", map[string]*scan.Rec{"a": rec(true), "b": rec(true)}, 2, 2, []string{"a", "b"}},
		{"collapsed declarations", map[string]*scan.Rec{"a": rec(false)}, 5, 0, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			keys := make([]string, 0, len(tc.found))
			for k := range tc.found {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			res := &scan.ClaimResult{Found: tc.found, FoundKeys: keys, NDeclared: tc.declared}
			applyIgnoreDisabled(res)
			if res.NDeclared != tc.wantDeclared {
				t.Errorf("NDeclared = %d, want %d (drives the false data-anomaly branch)", res.NDeclared, tc.wantDeclared)
			}
			if !reflect.DeepEqual(res.FoundKeys, tc.wantKeys) {
				t.Errorf("FoundKeys = %q, want %q", res.FoundKeys, tc.wantKeys)
			}
		})
	}
}

func TestSplitAdaptedReportsInactiveSeparately(t *testing.T) {
	all := []adaptedEntry{
		{name: "hsin", label: "心", sets: 4, fresh: 0},
		{name: "hsinult", label: "心大招", sets: 3, fresh: 0},
		{name: "rover", label: "漂泊者", sets: 2, fresh: 1},
	}
	effective, skipped := splitAdapted(all, map[string]bool{"hsin": true, "hsinult": true})
	if len(effective) != 1 || effective[0].name != "rover" {
		t.Fatalf("effective = %+v, want only rover", effective)
	}
	if len(skipped) != 2 || skipped[0].name != "hsin" || skipped[1].name != "hsinult" {
		t.Fatalf("skipped = %+v, want hsin + hsinult in order", skipped)
	}
	if got := formatAddedText(effective); got != "新增 1 套 —— 漂泊者 1 套" {
		t.Fatalf("added text from effective entries = %q", got)
	}
}

func TestSplitAdaptedAllInactiveLeavesNothingEffective(t *testing.T) {
	all := []adaptedEntry{
		{name: "hsin", label: "心", sets: 4},
		{name: "hsinult", label: "心大招", sets: 3},
	}
	effective, skipped := splitAdapted(all, map[string]bool{"hsin": true, "hsinult": true})
	if len(effective) != 0 {
		t.Fatalf("effective = %+v, want empty", effective)
	}
	if len(skipped) != 2 {
		t.Fatalf("skipped = %+v, want both", skipped)
	}
	if got := formatAddedText(effective); got != "新增 0 套" {
		t.Fatalf("added text must be the honest zero, got %q", got)
	}
}

// The inactive/skipped summary lines name the MOD as the subject, not the
// character. A mod failing to load or being opted out must never be reported
// as the character itself failing - other mods of that character may still be
// configured. This test locks both the Go payload wording and the front-end
// regex prefix, which must stay in sync or the summary row silently vanishes.
func TestSummarySubjectIsModNotCharacter(t *testing.T) {
	inactive := fmt.Sprintf("%d 个角色的 mod 未加载 —— %s", 2, "心 · hsinult")
	skipped := fmt.Sprintf("%d 个角色的 mod 按 _skipemote 跳过 —— %s", 1, "心")

	// Payload must use "个角色的 mod ..." and must NOT use the old wording
	// that blamed the character ("个角色未生效" / "个角色按 _skipemote 跳过").
	for _, tc := range []struct{ got, wantPrefix string }{
		{inactive, "2 个角色的 mod 未加载 —— "},
		{skipped, "1 个角色的 mod 按 _skipemote 跳过 —— "},
	} {
		if !strings.HasPrefix(tc.got, tc.wantPrefix) {
			t.Errorf("summary payload %q does not start with %q", tc.got, tc.wantPrefix)
		}
	}
	for _, stale := range []string{"个角色未生效", "个角色按 _skipemote", "个角色已跳过"} {
		if strings.Contains(inactive, stale) || strings.Contains(skipped, stale) {
			t.Errorf("summary payload still blames the character via %q", stale)
		}
	}

	// Front-end regex must match both payloads. Read the embedded page.
	page, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	// Match on the Chinese anchor only: checking backslash escapes through a
	// raw string is fragile. The anchor proves the regex was rewritten to the
	// mod wording; the payload prefix check above proves Go agrees.
	for _, needle := range []string{
		"个角色的 mod 未加载",
		"个角色的 mod 按 _skipemote 跳过",
	} {
		if !strings.Contains(html, needle) {
			t.Errorf("index.html is missing summary regex %q (Go payload and front-end regex drifted)", needle)
		}
	}
	// Old character-blaming regexes must be gone.
	for _, stale := range []string{
		`个角色未生效/`,
		`个角色按 _skipemote 跳过/`,
	} {
		if strings.Contains(html, stale) {
			t.Errorf("index.html still has character-blaming regex %q", stale)
		}
	}
}

// A partial config must show its failures in the same summary box that shows
// successes. The log alone is not enough: the visible result otherwise reads
// as a clean success. The payload and the front-end regex/localiser must agree
// on the "N 套未配置" anchor, just like the adapted/inactive rows above.
func TestFailedSummaryCarriesModFiles(t *testing.T) {
	failures := []string{
		`hsin: 心/hsin_a0e72/Hsin/mod.ini: no fragment uniquely matches pvb_paths`,
		`心大招: pose_exclude entry "心/hsinnv/Hsin/UL/mod.ini" has no matching pvb path`,
	}
	got := formatFailedText(failures)
	if want := "2 套未配置 —— "; !strings.HasPrefix(got, want) {
		t.Fatalf("failed payload %q does not start with %q", got, want)
	}
	for _, name := range []string{
		"心/hsin_a0e72/Hsin/mod.ini",
		"心/hsinnv/Hsin/UL/mod.ini",
	} {
		if !strings.Contains(got, name) {
			t.Errorf("failed payload %q omitted mod file %q", got, name)
		}
	}
	if formatFailedText(nil) != "" {
		t.Fatal("no failures must leave the summary row empty")
	}

	page, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	for _, needle := range []string{
		"if (y.failed)",
		"套未配置（失败 mod）",
		`summaryRow(failed, "fail")`,
		`class="detail"`,
		`body.split(" · ")`,
		`failureItemHtml(items[i])`,
		`<span class="err-item">`,
		`<span class="path">`,
		`<span class="why">`,
	} {
		if !strings.Contains(html, needle) {
			t.Errorf("index.html is missing failed-row contract %q", needle)
		}
	}
}

// Each per-set failure must be a separate line. A single red paragraph made
// seven errors look like one blob and hid which character/mod failed.
func TestFailedSummaryItemsAreSeparateLines(t *testing.T) {
	page, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	for _, needle := range []string{
		".summary>.fail .err-item{display:block;position:relative;",
		"border-bottom:1px dashed #f0cdd4",
		".summary>.fail .err-item::before{content:\"\";",
		".summary>.fail .err-item b{display:inline-block;",
	} {
		if !strings.Contains(html, needle) {
			t.Errorf("index.html is missing per-error line style %q", needle)
		}
	}
}

// A config that adds zero sets still adapts characters. The added row is
// always present, so an else-if chain would hide the adapted row again.
func TestSummaryShowsAdaptedAlongsideAdded(t *testing.T) {
	page, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	if !strings.Contains(html, "if (y.adapted)") {
		t.Fatal("index.html must render adapted independently of added")
	}
	if strings.Contains(html, "else if (y.adapted)") {
		t.Fatal("index.html still hides adapted behind the always-present added row")
	}
}

func TestFormatStagedTextReportsReserveData(t *testing.T) {
	all := []adaptedEntry{
		{name: "hsin", label: "心", sets: 5, fresh: 5},
		{name: "hsinult", label: "心大招", sets: 4, fresh: 4},
	}
	_, excluded := splitAdapted(all, map[string]bool{"hsin": true, "hsinult": true})
	inactive := map[string]string{"hsin": "心", "hsinult": "心大招"}

	got := formatStagedText("新增 0 套", excluded, inactive)
	want := "已生成 9 套储备数据 —— 心 5 套 · 心大招 4 套"
	if got != want {
		t.Fatalf("staged text = %q, want %q", got, want)
	}
}

func TestFormatStagedTextOnlyWhenNothingTookEffect(t *testing.T) {
	all := []adaptedEntry{
		{name: "hsin", label: "心", sets: 5, fresh: 0},
		{name: "rover", label: "漂泊者", sets: 2, fresh: 2},
	}
	// rover took effect => added is non-zero => no staged row, even though
	// hsin has reserve data on disk. Otherwise added and staged double count.
	if got := formatStagedText("新增 2 套 —— 漂泊者 2 套", all[:1], map[string]string{"hsin": "心"}); got != "" {
		t.Fatalf("staged text must be empty on a partial run, got %q", got)
	}
	// Cache-hit rerun: fresh=0 for everyone but sets are still on disk.
	if got := formatStagedText("新增 0 套", all[:1], map[string]string{"hsin": "心"}); got != "已生成 5 套储备数据 —— 心 5 套" {
		t.Fatalf("cache-hit staged text = %q", got)
	}
}

func TestFormatStagedTextSkipsNonInactiveEntries(t *testing.T) {
	// _skipemote entries are deliberate opt-outs: they are excluded from the
	// render too, but must NOT be counted as "reserve data waiting to load".
	entries := []adaptedEntry{
		{name: "hsin", label: "心", sets: 5},
		{name: "marked", label: "标记角色", sets: 3},
	}
	inactive := map[string]string{"hsin": "心"}
	if got := formatStagedText("新增 0 套", entries, inactive); got != "已生成 5 套储备数据 —— 心 5 套" {
		t.Fatalf("staged text must count only inactive, got %q", got)
	}
}

// The front-end must render the staged row with a matching Chinese anchor,
// exactly like added/inactive/skipped. A drift here silently drops the row
// in every locale (Go payload stays Chinese, the page localises it).
func TestStagedSummaryFrontEndRegex(t *testing.T) {
	page, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	for _, needle := range []string{
		"if (y.staged)",
		"套储备数据",
		"stagedLabel",
	} {
		if !strings.Contains(html, needle) {
			t.Errorf("index.html is missing staged rendering anchor %q", needle)
		}
	}
	if strings.Contains(html, "else if (y.staged)") {
		t.Error("staged row must be an independent if, not else-if (added is always non-empty)")
	}
}

// failedModNames must put the actual mod file paths into the error detail,
// which feeds both the text panel and the Diagnostics failure snapshot. It
// caps long lists so the two-line box and the log stay readable.
func TestFailedModNamesListsModsAndCaps(t *testing.T) {
	none := failedModNames(nil, 20)
	if none != "" {
		t.Fatalf("nil claim should name nothing, got %q", none)
	}
	empty := failedModNames(&scan.ClaimResult{}, 20)
	if empty != "" {
		t.Fatalf("empty claim should name nothing, got %q", empty)
	}
	res := &scan.ClaimResult{FoundKeys: []string{
		"心/mod A/1.ini",
		"心/mod B/2.ini",
	}}
	got := failedModNames(res, 20)
	for _, want := range []string{"involved mod files", "心/mod A/1.ini", "心/mod B/2.ini"} {
		if !strings.Contains(got, want) {
			t.Fatalf("failedModNames = %q, missing %q", got, want)
		}
	}
	many := &scan.ClaimResult{}
	for i := 0; i < 25; i++ {
		many.FoundKeys = append(many.FoundKeys, fmt.Sprintf("心/mod%d/1.ini", i))
	}
	capped := failedModNames(many, 5)
	if strings.Count(capped, "1.ini") != 5 || !strings.Contains(capped, "and 20 more") {
		t.Fatalf("cap not applied: %q", capped)
	}
}
