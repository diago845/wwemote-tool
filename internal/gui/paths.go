package gui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// T returns the UI string for the active language. Call sites pass the
// Chinese and English forms; localizedText() also resolves ja/ko/zh-TW from
// the shared table, so log/error/status text matches the language the user
// picked instead of falling back to English for every non-Chinese locale.
// State-machine contract strings (message / summary keys) are exempt -- the
// front-end renders those from its own LANG table.
func T(zhText, enText string) string {
	return localizedText(zhText, enText)
}

// resolveMods takes the folder the user picked and returns
// (modsRoot, wwmi, scanRoot). modsRoot is the real Mods folder and is the
// basis for every 3DMigoto resource path; it never changes with the pick.
// scanRoot is how deep this run scans: the whole Mods tree when the user
// picked the WWMI root or the Mods folder itself, otherwise just the picked
// subfolder (a mod collection or a single mod). Keeping the two separate is
// what makes "configure only this folder" possible without breaking paths.
func resolveMods(p string) (modsRoot, wwmi, scanRoot string, err error) {
	p = strings.TrimSpace(strings.Trim(p, `"`))
	if p == "" {
		return "", "", "", bad(T("没有填写 Mods 路径", "No Mods path given"),
			T("点 Browse 选到 WWMI 下面的那个 Mods 文件夹。", "Click Browse and pick the Mods folder under WWMI."), "")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", "", "", bad(T("这个路径无法解析", "That path cannot be resolved"),
			T("换一个文件夹再试。", "Try a different folder."), p+": "+err.Error())
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return "", "", "", bad(T("这个路径不存在，或者不是文件夹", "That path does not exist, or is not a folder"),
			T("点 Browse 重新选一次。", "Click Browse and pick it again."), abs)
	}
	// Case A: the user handed us the WWMI root (has Mods/ + d3dx.ini).
	if isDir(filepath.Join(abs, "Mods")) && isFile(filepath.Join(abs, "d3dx.ini")) {
		mr := filepath.Join(abs, "Mods")
		return mr, abs, mr, nil
	}

	// Case B: the Mods folder itself. WWMI is its parent; d3dx.ini must exist
	// there because the exclusion rules are read from it.
	modsRoot = abs
	wwmi = filepath.Dir(abs)
	if isFile(filepath.Join(wwmi, "d3dx.ini")) {
		return modsRoot, wwmi, modsRoot, nil
	}

	// Case C: a folder *inside* Mods (a mod collection or a single mod). Walk
	// up until we find the directory whose parent holds d3dx.ini -- that is the
	// real Mods root. This run scans only the folder the user picked, but every
	// resource path in the output is still relative to the real Mods root.
	// Bound the walk so a folder on a drive with no d3dx.ini fails the same way
	// it always did, instead of looping at the filesystem root.
	for depth, cur := 0, abs; depth < 32; depth++ {
		parent := filepath.Dir(cur)
		if parent == cur {
			break // reached the drive root
		}
		if isFile(filepath.Join(parent, "d3dx.ini")) {
			// parent is the WWMI root (it holds d3dx.ini); cur is the Mods root.
			return cur, parent, abs, nil
		}
		cur = parent
	}

	return "", "", "", bad(T("这个文件夹里没有 d3dx.ini", "There is no d3dx.ini in that folder"),
		T("请选 WWMI 根目录、它下面的 Mods 文件夹，或 Mods 里的某个 mod 文件夹。",
			"Pick the WWMI root, the Mods folder inside it, or a mod folder inside Mods."),
		filepath.Join(wwmi, "d3dx.ini")+
			T(" 不存在；排除规则（exclude_recursive）读不到 ⇒ 判不出哪些 mod 是停用的。正常布局：<WWMI>/d3dx.ini + <WWMI>/Mods/；你给的是 ",
				" is missing; the exclusion rules (exclude_recursive) cannot be read, so disabled mods cannot be told apart. Expected layout: <WWMI>/d3dx.ini + <WWMI>/Mods/; you gave ")+abs)
}

// autodetectMods guesses a default Mods folder for the input box only. A wrong
// guess is harmless (the user sees and edits it); it is never used as a gate.
func autodetectMods() string {
	for _, key := range []string{"WWMI_ROOT", "XXMI_ROOT"} {
		if v := os.Getenv(key); v != "" {
			if isDir(filepath.Join(v, "Mods")) {
				return filepath.Join(v, "Mods")
			}
			if isDir(v) {
				return v
			}
		}
	}
	for _, base := range []string{
		`C:\XXMI Launcher\WWMI`, `D:\XXMI Launcher\WWMI`, `E:\XXMI Launcher\WWMI`,
	} {
		if isDir(filepath.Join(base, "Mods")) {
			return filepath.Join(base, "Mods")
		}
	}
	return ""
}

// guardPanel checks the exe's own folder really is an unpacked panel. The Go
// build carries its author data (recipes, native sparse) inside the binary, so
// the only on-disk markers are the panel ini and the chars/Shaders folders.
func guardPanel() (string, error) {
	dir := panelDir()
	if isFile(filepath.Join(dir, "WWEmote.ini")) || isDir(filepath.Join(dir, "chars")) ||
		isDir(filepath.Join(dir, "Shaders")) {
		return dir, nil
	}
	return "", bad(T("找不到面板文件（WWEmote.ini / chars）",
		"Cannot find the panel files (WWEmote.ini / chars)"),
		T("把 WWEmoteTool.exe 放进解压出来的面板文件夹里，再运行它。",
			"Put WWEmoteTool.exe inside the unpacked panel folder, then run it."), dir)
}

func isDir(p string) bool  { st, err := os.Stat(p); return err == nil && st.IsDir() }
func isFile(p string) bool { st, err := os.Stat(p); return err == nil && !st.IsDir() }

// phaseLabels returns the same shape the Python backend sends. The front-end
// uses its own localised phases for config, so these are a fallback only.
func phaseLabels(zh bool) map[string]any {
	if zh {
		return map[string]any{
			"config": map[string]string{"pre": "① 检查面板本体", "run": "② 扫描并写入 user 层", "verify": "③ 复核改动范围"},
			"update": map[string]string{"pre": "① 定位面板（只读）", "run": "② 检查更新包", "verify": "③ 复核（未写任何文件）"},
		}
	}
	return map[string]any{
		"config": map[string]string{"pre": "① Check the panel", "run": "② Scan and write the user layer", "verify": "③ Verify the change scope"},
		"update": map[string]string{"pre": "① Locate the panel (read-only)", "run": "② Check the update package", "verify": "③ Verify (no file written)"},
	}
}

var errCancelled = errors.New("cancelled")
