package gui

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func get(t *testing.T, h http.Handler, path string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func post(t *testing.T, h http.Handler, path string, body any) (int, []byte) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func jmap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("not json: %v -- %s", err, b)
	}
	return v
}

// TestStaticEndpoints covers the read-only endpoints the GUI polls and the
// small write endpoints that must never fail the window.
func TestStaticEndpoints(t *testing.T) {
	srv := NewServer(nil)
	h := srv.Handler()

	if code, body := get(t, h, "/"); code != 200 || !strings.Contains(string(body), "<!DOCTYPE html>") {
		t.Fatalf("GET / = %d, want embedded HTML", code)
	}
	if code, body := get(t, h, "/api/health"); code != 200 || jmap(t, body)["ok"] != true {
		t.Fatalf("health = %d %s", code, body)
	}
	code, body := get(t, h, "/api/status")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	snap := jmap(t, body)
	for _, key := range []string{"state", "pct", "job", "message", "log", "phase_index",
		"last_mods", "root_ok", "phase_label", "update", "log_path", "elapsed"} {
		if _, ok := snap[key]; !ok {
			t.Errorf("status missing %q", key)
		}
	}
	if snap["state"] != "idle" {
		t.Errorf("initial state = %v, want idle", snap["state"])
	}

	if code, body := get(t, h, "/api/language"); code != 200 || jmap(t, body)["language"] == nil {
		t.Fatalf("GET language = %d %s", code, body)
	}
	if code, body := post(t, h, "/api/language", map[string]any{"language": "zh-CN"}); code != 200 || jmap(t, body)["language"] != "zh-CN" {
		t.Fatalf("POST language = %d %s", code, body)
	}
	// restore
	post(t, h, "/api/language", map[string]any{"language": "en"})

	if code, body := post(t, h, "/api/js-error", map[string]any{"msg": "test"}); code != 200 || jmap(t, body)["ok"] != true {
		t.Fatalf("js-error = %d %s", code, body)
	}
	if code, body := get(t, h, "/api/diag"); code != 200 || jmap(t, body)["ok"] != true {
		t.Fatalf("diag = %d %s", code, body)
	}
	if code, body := post(t, h, "/api/diag-bundle", map[string]any{}); code != 200 || jmap(t, body)["ok"] != true {
		t.Fatalf("diag-bundle = %d %s", code, body)
	}
}

// TestRejectBadPath: a bad Mods path is refused synchronously and does not
// occupy the run lock (the next valid request must still be accepted).
func TestRejectBadPath(t *testing.T) {
	srv := NewServer(nil)
	h := srv.Handler()
	_, body := post(t, h, "/api/start", map[string]any{"mods": "Z:\\definitely\\not\\a\\dir"})
	v := jmap(t, body)
	if v["ok"] != false || str(v, "error") == "" {
		t.Fatalf("expected refusal with a human error, got %s", body)
	}
	if srv.State().State != "idle" {
		t.Fatalf("rejected request changed state to %v", srv.State().State)
	}
}

// TestRunLock: while a job is marked running, a second /api/start is refused.
func TestRunLock(t *testing.T) {
	srv := NewServer(nil)
	srv.running = true
	defer func() { srv.running = false }()
	_, body := post(t, srv.Handler(), "/api/start", map[string]any{"mods": "C:\\anything"})
	if jmap(t, body)["ok"] != false {
		t.Fatalf("second job should be refused, got %s", body)
	}
}

// TestCancel sets the cancel channel without panicking when idle.
func TestCancel(t *testing.T) {
	srv := NewServer(nil)
	if code, body := post(t, srv.Handler(), "/api/cancel", map[string]any{}); code != 200 || jmap(t, body)["ok"] != true {
		t.Fatalf("cancel = %d %s", code, body)
	}
}

// TestNoVersionAdvertised locks the unreleased-build rule: neither the status
// payload nor the diagnostics report may advertise a version number.
func TestNoVersionAdvertised(t *testing.T) {
	for _, it := range collectDiag() {
		if it.Key == "app" && strings.ContainsAny(it.Value, "0123456789") {
			t.Errorf("diag app line advertises a version: %q", it.Value)
		}
	}
	// /api/status must not carry a version field either.
	raw, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "shell_version") {
		t.Error("status payload still exposes shell_version")
	}
}

// TestLanguageOptionsSelfLabelled locks the language selector: every option is
// written in its own language and never rewritten when the UI language changes.
// A native speaker looks for their own language in their own script.
func TestLanguageOptionsSelfLabelled(t *testing.T) {
	raw, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	want := []string{
		`<option value="en" selected>English</option>`,
		`<option value="ja">日本語</option>`,
		`<option value="ko">한국어</option>`,
		`<option value="zh-CN">简体中文</option>`,
		`<option value="zh-TW">繁體中文</option>`,
	}
	// The order is part of the UI spec: en, ja, ko, zh-CN, zh-TW (top down).
	last := -1
	for _, w := range want {
		i := strings.Index(s, w)
		if i < 0 {
			t.Errorf("language option is not self-labelled: %s", w)
			continue
		}
		if i <= last {
			t.Errorf("language option is out of order: %s", w)
		}
		last = i
	}
	// applyLanguage must not rewrite option labels.
	if strings.Contains(s, "options[i].textContent") || strings.Contains(s, "langOpts") {
		t.Error("applyLanguage still rewrites language option labels")
	}
}

// TestLocalisedTextNoChinese locks the rule that UI text pushed to the GUI is
// bilingual: a non-Chinese language must never receive Chinese log/error text.
func TestLocalisedTextNoChinese(t *testing.T) {
	t.Setenv("WWEMOTE_STATE_DIR", t.TempDir())
	prev := savedLanguage()
	defer saveLanguage(prev)

	if !saveLanguage("en") {
		t.Fatal("saveLanguage(en) failed")
	}
	if got := T("配置完成", "Setup complete"); got != "Setup complete" {
		t.Errorf("T() in en = %q, want 'Setup complete'", got)
	}
	if got := T("配置失败", "Setup failed"); got != "Setup failed" {
		t.Errorf("T() skeleton in en = %q", got)
	}
	if !saveLanguage("zh-CN") {
		t.Fatal("saveLanguage(zh-CN) failed")
	}
	if got := T("配置完成", "Setup complete"); got != "配置完成" {
		t.Errorf("T() in zh = %q, want Chinese", got)
	}
	// Every non-Chinese locale must render its own language, never fall back
	// to another locale's text or leak the Chinese argument.
	for _, tc := range []struct{ lang, want string }{
		{"ja", "設定に失敗しました"},
		{"ko", "구성 실패"},
		{"zh-TW", "設定失敗"},
	} {
		if !saveLanguage(tc.lang) {
			t.Fatalf("saveLanguage(%s) failed", tc.lang)
		}
		if got := T("配置失败", "Setup failed"); got != tc.want {
			t.Errorf("T() in %s = %q, want %q", tc.lang, got, tc.want)
		}
	}
	// Known phase line localises too (this is what trMessage shows as-is).
	if !saveLanguage("ja") {
		t.Fatal("saveLanguage(ja) failed")
	}
	if got := T("② 扫描并写入 user 层", "② Scan and write the user layer"); got != "② ユーザー層をスキャンして書き込み" {
		t.Errorf("phase T() in ja = %q", got)
	}
	// The retry advice must ask the user to restart the panel, never to shut
	// down the game. It also must not push the user toward Diagnostics: the
	// actionable fix for a locked user/ file is a fresh panel process.
	const adviceZh = "重启 WWEmoteTool.exe 再点一次配置。"
	const adviceEn = "Restart WWEmoteTool.exe and configure again."
	for _, tc := range []struct{ lang, want string }{
		{"zh-CN", adviceZh},
		{"ja", "WWEmoteTool.exe を再起動してから、もう一度設定してください。"},
		{"ko", "WWEmoteTool.exe를 다시 시작한 뒤 다시 구성하세요."},
		{"zh-TW", "重新啟動 WWEmoteTool.exe 再點一次設定。"},
		{"en", adviceEn},
	} {
		if !saveLanguage(tc.lang) {
			t.Fatalf("saveLanguage(%s) failed", tc.lang)
		}
		got := T(adviceZh, adviceEn)
		if got != tc.want {
			t.Errorf("retry advice in %s = %q, want %q", tc.lang, got, tc.want)
		}
		if strings.Contains(got, "ゲーム") || strings.Contains(got, "게임") ||
			strings.Contains(got, "Diagnostics") || strings.Contains(got, "游戏") {
			t.Errorf("retry advice in %s still mentions the game or Diagnostics: %q", tc.lang, got)
		}
	}
}

// TestLocalizedTextFallsBack pins the rule that a message missing from the
// table degrades to a readable language instead of rendering empty.
func TestLocalizedTextFallsBack(t *testing.T) {
	t.Setenv("WWEMOTE_STATE_DIR", t.TempDir())
	prev := savedLanguage()
	defer saveLanguage(prev)

	saveLanguage("ja")
	if got := T("未翻译的新文案", "Brand new string"); got != "Brand new string" {
		t.Errorf("ja fallback = %q, want English", got)
	}
	saveLanguage("zh-TW")
	if got := T("未翻译的新文案", "Brand new string"); got != "未翻译的新文案" {
		t.Errorf("zh-TW fallback = %q, want Chinese", got)
	}
}

// TestErrorBoxTwoLineContract locks the shape the error box is built around:
// it only fits two lines of 11px text, so every failure carries exactly a
// conclusion and a next step, and the raw cause is log-only (Error()).
func TestErrorBoxTwoLineContract(t *testing.T) {
	t.Setenv("WWEMOTE_STATE_DIR", t.TempDir())
	prev := savedLanguage()
	defer saveLanguage(prev)

	for _, lang := range []string{"zh-CN", "en"} {
		if !saveLanguage(lang) {
			t.Fatalf("saveLanguage(%s) failed", lang)
		}

		// A real refusal must already be a two-line uiErr.
		_, _, _, err := resolveMods("   ")
		var ue *uiErr
		if !errors.As(err, &ue) {
			t.Fatalf("%s: empty path must yield *uiErr, got %T (%v)", lang, err, err)
		}
		if ue.what == "" || ue.how == "" {
			t.Errorf("%s: uiErr needs both lines, got %+v", lang, ue)
		}
		if strings.Contains(ue.what, "\n") {
			t.Errorf("%s: conclusion must be one line, got %q", lang, ue.what)
		}
		if ue.Error() != ue.det && ue.det != "" {
			t.Errorf("%s: Error() must return the log-only detail", lang)
		}

		// The assembled box renders as exactly two lines.
		box := T("配置失败", "Setup failed") + " —— " + ue.what
		if ue.how != "" {
			box += "\n" + ue.how
		}
		if n := strings.Count(box, "\n") + 1; n != 2 {
			t.Errorf("%s: box renders %d lines, want 2: %q", lang, n, box)
		}
		if lang != "zh-CN" && containsCJK(box) {
			t.Errorf("%s: box leaked Chinese: %q", lang, box)
		}

		// Sync refusals (before the run starts) use the same two-line shape but
		// no "setup failed" skeleton: they must never show the raw detail.
		sync := twoLine(err)
		if n := strings.Count(sync, "\n") + 1; n != 2 {
			t.Errorf("%s: sync refusal renders %d lines, want 2: %q", lang, n, sync)
		}
		if ue.det != "" && strings.Contains(sync, ue.det) {
			t.Errorf("%s: sync refusal leaked the raw detail: %q", lang, sync)
		}
		withDet := bad(T("结论", "conclusion"), T("动作", "next step"), "raw detail")
		if got := twoLine(withDet); strings.Contains(got, "raw detail") {
			t.Errorf("%s: sync refusal leaked the raw detail: %q", lang, got)
		}
		if lang != "zh-CN" && containsCJK(sync) {
			t.Errorf("%s: sync refusal leaked Chinese: %q", lang, sync)
		}
	}
}

func containsCJK(s string) bool {
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}

// TestFrontEndHsinultContract ensures the shared front-end maps the "心大招"
// label to the language-neutral "hsinult", and does so BEFORE the bare 心
// replacement (otherwise a non-Chinese UI shows "Hsin大招" with Chinese).
func TestFrontEndHsinultContract(t *testing.T) {
	raw, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	ult := strings.Index(s, `replace(/心大招/g, "hsinult")`)
	bare := strings.Index(s, `replace(/心/g, "Hsin")`)
	if ult < 0 {
		t.Fatal("front-end missing 心大招 -> hsinult mapping")
	}
	if bare < 0 {
		t.Fatal("front-end missing 心 -> Hsin mapping")
	}
	if ult > bare {
		t.Errorf("hsinult replacement (pos %d) must run before bare 心 (pos %d)", ult, bare)
	}
}

// TestLastReason locks the old-style error cause extraction: the red line
// shows the real ✗ reason from the log, never a bare generic message.
func TestLastReason(t *testing.T) {
	st := newState("")
	if got := st.lastReason(); got != "" {
		t.Fatalf("empty log reason = %q, want empty", got)
	}
	st.push("  普通日志")
	st.push("  ⇒ 某处提示")
	st.push("✗ 挑不出唯一片段")
	if got := st.lastReason(); got != " —— 挑不出唯一片段" {
		t.Fatalf("reason = %q, want ' —— 挑不出唯一片段'", got)
	}
	// A later ★/⇒ must NOT override the last ✗ line.
	st.push("★ 另起一行")
	if got := st.lastReason(); got != " —— 挑不出唯一片段" {
		t.Fatalf("reason after ★ = %q, want the ✗ line", got)
	}
	// With no ✗ line, fall back to ★/⇒ (old backend behaviour).
	st2 := newState("")
	st2.push("⇒ 目标文件不存在")
	if got := st2.lastReason(); got != " —— ⇒ 目标文件不存在" {
		t.Fatalf("fallback reason = %q", got)
	}
}

// TestFirstRunEmptyModsBox: the address box opens empty on a true first run,
// and refills from the last successful folder afterwards. The status API
// carries the remembered path only if it still exists, so a stale path can
// never come back as a prefilled failure.
func TestFirstRunEmptyModsBox(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WWEMOTE_STATE_DIR", dir)

	if got := savedMods(); got != "" {
		t.Fatalf("first run last_mods = %q, want empty", got)
	}
	// A remembered path that no longer exists is still dropped from the
	// store, so a stale value can never linger in the status payload.
	gone := filepath.Join(dir, "gone")
	saveMods(gone)
	if got := savedMods(); got != "" {
		t.Fatalf("stale last_mods = %q, want empty", got)
	}
	// A real folder round-trips.
	mods := filepath.Join(dir, "Mods")
	if err := os.MkdirAll(mods, 0o755); err != nil {
		t.Fatal(err)
	}
	saveMods(mods)
	if got := savedMods(); got != mods {
		t.Fatalf("last_mods = %q, want %q", got, mods)
	}
	// The status payload carries the remembered folder, and the old guessed
	// field is gone for good.
	_, body := get(t, NewServer(nil).Handler(), "/api/status")
	snap := jmap(t, body)
	if snap["last_mods"] != mods {
		t.Errorf("status last_mods = %v, want %q", snap["last_mods"], mods)
	}
	if _, ok := snap["default_mods"]; ok {
		t.Error("status still carries the guessed default_mods field")
	}
	// Front-end contract: prefill only from last_mods, and only on the FIRST
	// poll (guarded by `primed`), so typing is never overwritten mid-edit.
	raw, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "addr.value = s.last_mods") {
		t.Error("front-end does not prefill from s.last_mods")
	}
	if strings.Contains(string(raw), "default_mods") {
		t.Error("front-end still reads default_mods")
	}
	firstPoll := strings.Index(string(raw), "function poll()")
	prefill := strings.Index(string(raw), "addr.value = s.last_mods")
	if firstPoll < 0 || prefill < firstPoll || prefill > firstPoll+900 {
		t.Error("last_mods prefill must live inside the first-poll guard")
	}
}

// TestTopNoteCopy pins the one-line intro above the input. It tells the user
// they can either pick the mods folder or drag one into the address bar, so
// every language has to carry that meaning -- and the old "coexist / 兼容"
// wording must not survive anywhere.
func TestTopNoteCopy(t *testing.T) {
	raw, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	want := map[string][]string{
		"zh-CN": {"本地mods", "拖动文件夹", "地址栏"},
		"zh-TW": {"本地mods", "拖曳資料夾", "網址列"},
		"en":    {"drag a folder", "address bar"},
		"ja":    {"フォルダー", "アドレスバー", "ドラッグ"},
		"ko":    {"mods 폴더", "주소 표시줄", "끌어다"},
	}
	re := regexp.MustCompile(`topnote:"([^"]*)"`)
	found := 0
	for _, line := range strings.Split(s, "\n") {
		for lang, needles := range want {
			if !strings.Contains(line, `"`+lang+`": {`) {
				continue
			}
			m := re.FindStringSubmatch(line)
			if m == nil {
				t.Errorf("%s: no topnote", lang)
				continue
			}
			found++
			body := m[1]
			for _, n := range needles {
				if !strings.Contains(body, n) {
					t.Errorf("%s topnote %q missing %q", lang, body, n)
				}
			}
			if strings.Contains(body, "冲突") || strings.Contains(body, "衝突") ||
				strings.Contains(body, "conflict") || strings.Contains(body, "競合") ||
				strings.Contains(body, "충돌") {
				t.Errorf("%s topnote still talks about conflicts: %q", lang, body)
			}
		}
	}
	if found != len(want) {
		t.Errorf("found %d localised topnote lines, want %d", found, len(want))
	}
}

// TestNoAuthorPathInUI: no author-local path may appear in the page. One did
// (as the zh placeholder), and it reads as "the box is already filled in".
func TestNoAuthorPathInUI(t *testing.T) {
	raw, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if i := strings.Index(string(raw), "XXMI Launcher"); i >= 0 {
		line := strings.Count(string(raw[:i]), "\n") + 1
		t.Errorf("index.html:%d still hardcodes the author's XXMI path", line)
	}
}

// TestDataRoots verifies settings, logs and diagnostics all live in the one
// data folder inside the panel: no writes outside the WWEmote directory.
func TestDataRoots(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WWEMOTE_STATE_DIR", dir)
	if !filepath.IsAbs(dataRoot()) {
		t.Fatalf("dataRoot not absolute: %s", dataRoot())
	}
	if got := diagDir(); got != dir {
		t.Fatalf("diagDir = %q, want %q", got, dir)
	}
	if got := languagePath(); filepath.Dir(got) != dir {
		t.Fatalf("languagePath = %q, want it in %q", got, dir)
	}
	if got := modsPath(); filepath.Dir(got) != dir {
		t.Fatalf("modsPath = %q, want it in %q", got, dir)
	}
	// language file must actually round-trip.
	code := "ja"
	if !saveLanguage(code) {
		t.Fatal("saveLanguage returned false")
	}
	if got := savedLanguage(); got != code {
		t.Fatalf("savedLanguage = %s, want %s", got, code)
	}
}
