package gui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"wwemote/internal/modpath"
	"wwemote/internal/panel"
	"wwemote/internal/render"
	"wwemote/internal/scan"
)

// failedModNames lists the mod files a character claimed, for a fatal error's
// detail string. Both the text panel (err.Error()) and the Diagnostics snapshot
// (captureFailure) carry that string, so a failed run always names the mods it
// was working on instead of a bare wrapper. Sanitised before it reaches either
// sink: the paths here are relative to Mods/, never absolute machine paths.
func failedModNames(res *scan.ClaimResult, limit int) string {
	if res == nil || len(res.FoundKeys) == 0 {
		return ""
	}
	keys := res.FoundKeys
	suffix := ""
	if limit > 0 && len(keys) > limit {
		suffix = fmt.Sprintf(" (…and %d more)", len(keys)-limit)
		keys = keys[:limit]
	}
	return "\ninvolved mod files: " + strings.Join(keys, "; ") + suffix
}

// formatFailedText builds the summary-box row for per-set failures. The
// front-end localises the leading "N 套未配置" but keeps the mod file list
// verbatim, so a successful run with skipped sets is never reported as a
// clean success. The raw rows still go to WWEmote.log and Diagnostics.
func formatFailedText(failures []string) string {
	if len(failures) == 0 {
		return ""
	}
	return fmt.Sprintf("%d 套未配置 —— %s", len(failures), strings.Join(failures, " · "))
}

// formatOkText is the mirror of formatFailedText for the mods that DID get
// configured. Same "N 套 … —— item · item" shape so the front-end renders both
// as per-item rows; the lead word differs so the two read as "done" vs "failed".
func formatOkText(ok []string) string {
	if len(ok) == 0 {
		return ""
	}
	return fmt.Sprintf("%d 套已配置 —— %s", len(ok), strings.Join(ok, " · "))
}

// adaptedEntry is one configured character as reported in the success summary.
// fresh is how many of its deltas this run actually baked (0 = all cached).
type adaptedEntry struct {
	name  string
	label string
	sets  int
	fresh int
}

// formatAddedText builds the "newly baked this run" summary line. The Chinese
// wording is localised by the shared front-end (leading count + names + 套),
// so its shape must stay: "新增 N 套" and optional " —— label M 套 · ...".
func formatAddedText(entries []adaptedEntry) string {
	freshTotal := 0
	parts := make([]string, 0, len(entries))
	for _, a := range entries {
		if a.fresh > 0 {
			freshTotal += a.fresh
			parts = append(parts, fmt.Sprintf("%s %d 套", a.label, a.fresh))
		}
	}
	if freshTotal == 0 {
		return "新增 0 套"
	}
	return fmt.Sprintf("新增 %d 套 —— %s", freshTotal, strings.Join(parts, " · "))
}

// formatAdaptedText builds the "N 个角色已适配 —— label M 套 · ..." line.
// The wording is byte-locked to the old Python scanner: the shared front-end
// renderSummary() keys off "N 个角色已适配" and drops the whole card otherwise.
// Only entries with at least one EFFECTIVE set count as adapted. A
// finished-only character has zero working sets (reported as unsupported
// failures instead); listing it as "adapted, 0 sets" next to "this format not
// supported" is the contradiction the user saw on the pre-fix V3.8. An empty
// result is intentional -- the front-end falls through to its localised done
// string -- so never substitute a Chinese literal here.
func formatAdaptedText(entries []adaptedEntry) string {
	eff := make([]adaptedEntry, 0, len(entries))
	for _, a := range entries {
		if a.sets > 0 {
			eff = append(eff, a)
		}
	}
	if len(eff) == 0 {
		return ""
	}
	parts := make([]string, 0, len(eff))
	for _, a := range eff {
		parts = append(parts, fmt.Sprintf("%s %d 套", a.label, a.sets))
	}
	return fmt.Sprintf("%d 个角色已适配 —— %s", len(eff), strings.Join(parts, " · "))
}

// formatStagedText reports deltas written to user/ for characters excluded
// from this render because their mod is not loaded. Those sets are reserve
// data: still on disk, ready to take effect once the mod is enabled and the
// panel is configured again. Without this line an all-inactive run reads as
// "no work was done" even though the user layer gained buffers and configs.
// Count "sets" (data on disk), not "fresh" (baked this run): a cache-hit rerun
// bakes nothing yet still has the same reserve data waiting. The Chinese
// wording is localised by the front-end, so keep the shape
// "已生成 N 套储备数据 —— label M 套 · ...".
func formatStagedText(addedText string, entries []adaptedEntry, inactive map[string]string) string {
	// Only meaningful when NOTHING took effect this run. A partial run already
	// reports its live wins in "added"; a staged row there would double count.
	if addedText != "新增 0 套" {
		return ""
	}
	total := 0
	parts := make([]string, 0, len(entries))
	for _, a := range entries {
		if _, ok := inactive[a.name]; !ok || a.sets <= 0 {
			continue
		}
		total += a.sets
		parts = append(parts, fmt.Sprintf("%s %d 套", a.label, a.sets))
	}
	if total == 0 {
		return ""
	}
	return fmt.Sprintf("已生成 %d 套储备数据 —— %s", total, strings.Join(parts, " · "))
}

// applyIgnoreDisabled drops DISABLED_ mods from a claim result and resyncs
// NDeclared with what is left. ScanChar counts every ini carrying the anchor,
// including the ones whose DISABLED_ prefix it stripped; leaving that count
// untouched made an all-disabled character look like a data anomaly even
// though ignoring those mods was exactly what the user asked for.
func applyIgnoreDisabled(res *scan.ClaimResult) {
	enabled := 0
	for k, rec := range res.Found {
		if rec.Enabled {
			enabled++
			continue
		}
		delete(res.Found, k)
	}
	// Count from the kept set rather than subtracting: several declarations
	// can collapse onto one unprefixed key, so "declared - removed" can stay
	// positive with nothing claimable left.
	res.NDeclared = enabled
	res.FoundKeys = res.FoundKeys[:0]
	for k := range res.Found {
		res.FoundKeys = append(res.FoundKeys, k)
	}
	sort.Strings(res.FoundKeys)
}

// pipeline runs the whole configuration in-process: scan the claimed sets,
// bake vertex deltas, write <user>/<char>.mods.json, then render the user-layer
// ini. Every produced file lands under <panel>/user; nothing touches the game
// directory or any other mod.
func (s *State) pipeline(modsRoot, wwmi, scanRoot, panelDir, userDir string, opts ConfigOptions, cancel <-chan struct{}) (any, error) {
	udir := userDir
	if udir == "" {
		udir = filepath.Join(panelDir, "user")
	}
	// A scoped run really narrows the scan: only ini files under scanRoot are
	// indexed. The path basis stays the real Mods root (BuildIndexScope), so
	// generated Resource\Mods\... paths never change. Because the index is
	// narrow, the per-character layer would lose sets outside the folder; those
	// are merged back from the previous mods.json instead. Stale cleanup is
	// limited to files belonging to characters this run touched.
	scoped := scanRoot != "" && scanRoot != modsRoot
	scopeRel := ""
	if scoped {
		if r, e := filepath.Rel(modsRoot, scanRoot); e == nil {
			scopeRel = filepath.ToSlash(r)
		}
	}

	// Pick up the previous user-layer files before anything is overwritten.
	// A scoped run reads them back so out-of-scope sets survive the rewrite;
	// a whole-tree run only needs them for the note field.
	prev := map[string][]byte{}
	if entries, e := os.ReadDir(udir); e == nil {
		for _, en := range entries {
			if !en.IsDir() && strings.HasSuffix(en.Name(), ".mods.json") {
				if raw, re := os.ReadFile(filepath.Join(udir, en.Name())); re == nil {
					prev[strings.TrimSuffix(en.Name(), ".mods.json")] = raw
				}
			}
		}
	}

	// ---- ① preflight (read-only) ----
	s.setPhase(0, 1, "pre", T("① 检查面板本体", "① Check the panel"))
	s.push(strings.Repeat("=", 66))
	s.push(T("WWEmote ① 一键配置 —— ", "WWEmote ① One-click setup -- ") + modsRoot)
	s.push(T("配置产出只写入面板自己的 user/ 目录；Mods/ 下别的 mod 一个字节不动。",
		"Output is written only to this panel's user/ folder; other mods under Mods/ are not touched."))
	if err := cancelled(cancel); err != nil {
		return nil, err
	}

	globs, err := scan.ReadExcludeGlobs(filepath.Join(wwmi, "d3dx.ini"))
	if err != nil {
		return nil, bad(T("读不出 d3dx.ini 里的排除规则", "Cannot read the exclusion rules in d3dx.ini"),
			T("重启 WWEmoteTool.exe 再点一次配置。", "Restart WWEmoteTool.exe and configure again."), err.Error())
	}
	if err := os.MkdirAll(udir, 0o755); err != nil {
		return nil, bad(T("写不进面板的 user 文件夹", "Cannot write to the panel's user folder"),
			T("重启 WWEmoteTool.exe 再点一次配置。", "Restart WWEmoteTool.exe and configure again."), err.Error())
	}
	tbl, err := panel.Load(udir)
	if err != nil {
		return nil, bad(T("角色数据读不出来", "Cannot read the character data"),
			adviceReport(), err.Error())
	}
	s.push(T("  面板本体：", "  Panel: ") + panelDir)
	s.push(fmt.Sprintf(T("  排除规则：%s（%d 条）", "  Exclusions: %s (%d rules)"), strings.Join(globs, ", "), len(globs)))
	if opts.IgnoreDisabled {
		s.push(T("  选项：已忽略 DISABLED_ 状态的 mod", "  Option: DISABLED_ mods are ignored"))
	}
	if opts.CleanStale {
		s.push(T("  选项：将删除失效配置文件", "  Option: stale config files will be removed"))
	}
	s.setPhase(0, 34, "pre", T("① 面板本体已就位", "① Panel is in place"))
	if err := cancelled(cancel); err != nil {
		return nil, err
	}

	// ---- ② scan + bake + render ----
	s.setPhase(1, 36, "run", T("② 扫描并写入 user 层", "② Scan and write the user layer"))
	idx, err := scan.BuildIndexScope(modsRoot, scanRoot)
	if err != nil {
		return nil, bad(T("读不了所选文件夹", "Cannot read the chosen folder"),
			T("确认路径没错、没被别的程序占用，再点一次配置。",
				"Check the path and that nothing else is using it, then configure again."), err.Error())
	}
	if scoped {
		s.push(T("  范围：仅扫描 ", "  Scope: scanning only ") + scanRoot)
		s.push(T("  范围外已配置的套保持原样；清理也只针对本范围。",
			"Sets configured outside this scope are kept; cleanup is limited to this scope."))
	}
	s.push(fmt.Sprintf(T("  已索引 %d 个 ini", "  Indexed %d ini files"), len(idx.Recs)))
	if idx.Skipped > 0 {
		s.push(fmt.Sprintf(T("  已跳过 %d 个带 _skipemote 后缀的文件夹（这些 mod 仍在游戏里生效，只是不配表情）",
			"  Skipped %d folder(s) ending in _skipemote (they still work in the game, only expression setup is skipped)"), idx.Skipped))
	}
	// An empty index is almost always the wrong folder. In a scoped run it can
	// also mean every mod in that folder was removed; with CleanStale the
	// caller still needs to prune old entries, so only full runs fail here.
	if len(idx.Recs) == 0 && !scoped {
		return nil, bad(T("这个 Mods 文件夹里一个 ini 都没有", "There is not a single ini in that Mods folder"),
			T("确认选的是 WWMI 下面的那个 Mods 文件夹。",
				"Make sure you picked the Mods folder under WWMI."),
			modsRoot)
	}
	scan.SetUserRoot(udir)

	// adapted holds one entry per configured character, in the exact shape the
	// Python scanner printed and the front-end renderSummary() expects:
	//   "2 个角色已适配 —— 心 6 套 · 露帕 4 套"
	// Only summary.adapted is rendered for the config job, so its wording must
	// stay byte-compatible with the old backend (the front-end is shared).
	adapted := []adaptedEntry{}
	// claimedByChar holds the mod files each character claimed this run, so a
	// later fatal (e.g. a base-mesh guard in WriteMods) can still name the mods
	// involved even though the per-character loop has moved on. failedSets holds
	// the per-set reasons that were only in the log before; they also need to
	// reach the summary box, otherwise the visible result shows only successes.
	claimedByChar := map[string][]string{}
	failedSets := []string{}
	// okByChar holds the mod files that actually got configured for each
	// character this run. The report used to show only the failures, so a
	// user could not tell WHICH mods were adopted without reading the log.
	// Filled per character, emitted only for characters that kept their
	// bindings (see `excluded` below).
	okByChar := map[string][]string{}
	progress := 36
	// keepSparse / keepChars drive stale cleanup: they hold exactly what this
	// run decided to keep, so anything else under user/ is by definition stale.
	// A scoped run builds them from ALL final configs instead, so a character
	// outside the folder is never mistaken for stale.
	keepSparse := map[string]bool{}
	keepChars := map[string]bool{}
	// Scoped stale candidates: sparse names referenced by the configs this run
	// rewrote or pruned. Only these may be removed; everything else is outside
	// the chosen folder and must be ignored.
	staleCandidates := map[string]bool{}
	touched := map[string]bool{}
	for _, c := range tbl.Chars {
		if err := cancelled(cancel); err != nil {
			return nil, err
		}
		res := scan.ScanChar([]string{c.AnchorHash}, globs, idx)
		if opts.IgnoreDisabled {
			applyIgnoreDisabled(res)
		}
		if len(res.FoundKeys) == 0 {
			if res.NDeclared > 0 {
				return nil, bad(
					fmt.Sprintf(T("%d 个 ini 声称是 %s 的 mod，但内容对不上",
						"%d ini files claim to be %s mods but do not match"), res.NDeclared, c.Name),
					adviceReport(),
					fmt.Sprintf("%s: %d ini files declared its anchor but none was claimed (data anomaly)", c.Name, res.NDeclared))
			}
			// The chosen folder may have had sets that were uninstalled since
			// the last run. Prune just those entries when stale cleanup was
			// requested; out-of-scope entries stay untouched. A _skipemote scope
			// also prunes the pre-rename baseline even without clean-stale: that
			// old set points at a base mesh path that no longer exists, which
			// would make the render pass fail with "missing base mesh".
			cleanScope := scan.CleanupScopeFor(scopeRel, prev[c.Name], opts.CleanStale)
			if scoped && cleanScope != "" {
				pruned, hasAny, pe := scan.PruneOutside(prev[c.Name], cleanScope)
				if pe != nil {
					return nil, bad(
						fmt.Sprintf(T("%s 的旧配置读不出来", "Cannot read the old config for %s"), c.Name),
						adviceReport(), pe.Error())
				}
				jp := filepath.Join(udir, c.Name+".mods.json")
				if hasAny {
					if e := writeFileAtomic(jp, scan.DumpModsJSON(pruned)); e != nil {
						return nil, bad(
							fmt.Sprintf(T("写不进 %s 的适配数据", "Cannot write the delta data for %s"), c.Name),
							adviceReport(), e.Error())
					}
					s.push(fmt.Sprintf(T("  -- %s：范围内 mod 已移除，已清理其配置", "  -- %s: scoped mods removed, their config pruned"), c.Name))
				} else if removeIfExists(jp) {
					s.push(fmt.Sprintf(T("  -- %s：范围内 mod 已全部移除，已删除配置", "  -- %s: all scoped mods removed, config deleted"), c.Name))
				}
				touched[c.Name] = true
				for name := range scan.SparseNames(prev[c.Name]) {
					staleCandidates[name] = true
				}
			} else {
				s.push(fmt.Sprintf(T("  -- %s：未安装，跳过", "  -- %s: not installed, skipped"), c.Name))
			}
			continue
		}
		// The index is already narrowed, so every claimed set is in scope.
		claimedByChar[c.Name] = append([]string(nil), res.FoundKeys...)
		needActive := c.NativeSparse != ""
		layer, errs := scan.UserLayer(res, modsRoot, needActive)
		if len(errs) > 0 {
			det := strings.Join(errs, "\n   ") + failedModNames(res, 20)
			return nil, bad(
				fmt.Sprintf(T("%s 的 mod 数据有问题，跳过生成", "%s mod data is inconsistent; skipped"), c.Name),
				adviceReport(), det)
		}
		// Per-set problems already dropped just those sets. Report them so the
		// user knows a mod was skipped, without losing every other good set.
		for _, sk := range layer.Skipped {
			line := c.Name + ": " + sk
			s.push("  ! " + line)
			failedSets = append(failedSets, line)
		}
		// Preserve the human note from the current file if present.
		jp := filepath.Join(udir, c.Name+".mods.json")
		if raw := prev[c.Name]; len(raw) > 0 {
			var old struct {
				Note []string `json:"_note"`
			}
			if json.Unmarshal(raw, &old) == nil {
				layer.Note = old.Note
			}
		}
		fresh := 0
		var bakeSkipped []string
		if needActive {
			_, _, skipped, baked, e := scan.AutoBake(c.Name, layer, modsRoot, res.Found)
			fresh = baked
			bakeSkipped = skipped
			if e != nil {
				// A failed bake is never about a DISABLED_ prefix: the scanner
				// claims those mods too. It means none of this character's sets
				// could produce a delta (unsupported mod format, missing Meshes/,
				// or inconsistent vertex counts). Point at the real cause and
				// name the mods, rather than sending the user after a prefix.
				return nil, bad(
					fmt.Sprintf(T("%s 的 mod 无法生成表情差量", "No usable delta could be baked for %s"), c.Name),
					adviceReport(),
					e.Error()+failedModNames(res, 20))
			}
			for _, sk := range skipped {
				line := c.Name + ": " + sk
				s.push("  ! " + line)
				failedSets = append(failedSets, line)
			}
			// Finished-format sets ship their own baked shape-key chain. They
			// cannot ride the delta route, so this character loses them. Only
			// say so when this CHARACTER uses the delta route (native sparse
			// present): a value-route character writes every set's table
			// directly and needs no notice.
			// Do NOT key this off len(layer.ModDelta) > 0. A character whose
			// sets are ALL finished produces an empty delta (allFinished is
			// deliberately non-fatal), so that guard skipped the one case that
			// most needs the warning: a lone finished mod silently reported
			// success.
			if needActive {
				var finishedRels []string
				for _, r := range res.FoundKeys {
					if rec := res.Found[r]; rec != nil && rec.Kind == scan.KindFinished {
						finishedRels = append(finishedRels, r)
					}
				}
				if len(finishedRels) > 0 {
					msg := fmt.Sprintf(T("%s 暂不支持对此格式的 mod 兼容",
						"%s: this mod format (finished shape keys) is not supported yet"),
						strings.Join(finishedRels, ", "))
					line := c.Name + ": " + msg
					s.push("  ! " + line)
					failedSets = append(failedSets, line)
				}
			}
		}
		// Mods this character actually got configured. Everything claimed minus
		// the ones reported above as failed: per-set drops from the user layer,
		// bake-time drops, and finished-format sets that cannot ride the delta
		// route. Without this list the report only ever names what went wrong.
		dropRel := map[string]bool{}
		for _, sk := range layer.Skipped {
			if i := strings.Index(sk, ": "); i > 0 {
				dropRel[sk[:i]] = true
			}
		}
		for _, sk := range bakeSkipped {
			if i := strings.Index(sk, ": "); i > 0 {
				dropRel[sk[:i]] = true
			}
		}
		var okRels []string
		rels := append([]string(nil), res.FoundKeys...)
		sort.Strings(rels)
		for _, r := range rels {
			rec := res.Found[r]
			if rec != nil && rec.Kind == scan.KindFinished {
				continue
			}
			if dropRel[r] {
				continue
			}
			okRels = append(okRels, c.Name+": "+r)
		}
		okByChar[c.Name] = okRels
		// A narrowed scan only sees this folder's sets. Merge the previous
		// file's out-of-scope entries back so the character keeps every set it
		// already had; freshly scanned sets take precedence.
		if scoped {
			merged, me := scan.MergeOutside(layer, prev[c.Name], scopeRel)
			if me != nil {
				return nil, bad(
					fmt.Sprintf(T("%s 的旧配置读不出来", "Cannot read the old config for %s"), c.Name),
					adviceReport(), me.Error())
			}
			layer = merged
		}
		body := scan.DumpModsJSON(layer)
		if e := writeFileAtomic(jp, body); e != nil {
			return nil, bad(
				fmt.Sprintf(T("写不进 %s 的适配数据", "Cannot write the delta data for %s"), c.Name),
				T("重启 WWEmoteTool.exe 再点一次配置。", "Restart WWEmoteTool.exe and configure again."),
				fmt.Sprintf("%s: %v", jp, e))
		}
		s.push(fmt.Sprintf(T("  OK %s: %d 套, %d 字节", "  OK %s: %d sets, %d bytes"), c.Name, len(layer.SkPaths), len(body)))
		// Count only sets that actually took effect (non-finished, not dropped),
		// not every sk path in the layer. A finished-only character has sk
		// paths but no working bindings; showing it as "adapted, N sets" while
		// also reporting "this format unsupported" is self-contradictory.
		adapted = append(adapted, adaptedEntry{name: c.Name, label: c.Label, sets: len(okRels), fresh: fresh})
		touched[c.Name] = true
		for name := range scan.SparseNames(prev[c.Name]) {
			staleCandidates[name] = true
		}
		keepChars[c.Name] = true
		for _, d := range layer.ModDelta {
			if d != nil && d.Sparse != "" {
				keepSparse[d.Sparse] = true
			}
		}
		progress += 12
		if progress > 90 {
			progress = 90
		}
		s.setPct(progress)
	}

	// Runs AFTER all mods.json are written so keepSparse/keepChars reflect the
	// final state of this run. A sparse that no set references any more, or a
	// character that has no installed mod, is by definition stale.
	if opts.CleanStale {
		var n int
		var gone []string
		if scoped {
			// Rebuild the keep set from every final config, including characters
			// outside the folder. Only candidate buffers from touched configs
			// may be removed, so an out-of-scope character is never cleaned.
			keepSparse = map[string]bool{}
			if entries, e := os.ReadDir(udir); e == nil {
				for _, en := range entries {
					if en.IsDir() || !strings.HasSuffix(en.Name(), ".mods.json") {
						continue
					}
					if raw, re := os.ReadFile(filepath.Join(udir, en.Name())); re == nil {
						for name := range scan.SparseNames(raw) {
							keepSparse[name] = true
						}
					}
				}
			}
			n, gone = cleanStaleScoped(udir, staleCandidates, keepSparse)
		} else {
			n, gone = cleanStaleFiles(udir, keepSparse, keepChars)
		}
		if n > 0 {
			s.push(fmt.Sprintf(T("  已删除 %d 个失效配置文件", "  Removed %d stale config files"), n))
			for _, g := range gone {
				s.push("    - " + g)
			}
		} else {
			s.push(T("  失效配置检查：无需删除", "  Stale check: nothing to remove"))
		}
	}

	// Rebuild from freshly written json; must not see the pre-scan table.
	fresh, err := panel.Load(udir)
	if err != nil {
		return nil, bad(T("刚写好的适配数据读不回来", "The delta data just written cannot be read back"),
			adviceReport(), err.Error())
	}
	// A DISABLED_ mod is configured under its unprefixed name, and a disabled
	// folder still resolves as a live write-back target (the prefix is the
	// game's load switch, not a reason to drop bindings). So such a character
	// gets full bindings and needs no reconfigure when the prefix is removed.
	// Only a genuinely renamed/removed mod has no target and is left out here.
	renderTable := &panel.Table{K: fresh.K, Chars: make([]*panel.Char, 0, len(fresh.Chars))}
	// name -> label. A character here may never have entered adapted (a stale
	// config whose mod is now marked), so keep the label at the point of
	// classification rather than recovering it from adapted later.
	inactiveLabels := map[string]string{}
	skippedLabels := map[string]string{}
	for _, c := range fresh.Chars {
		if len(c.PvbPaths) > 0 {
			switch classifyWriteback(modsRoot, c.PvbPaths, idx.SkippedBases) {
			case wbMarked:
				// The configured mod exists on disk under a _skipemote folder the
				// user renamed on purpose. That is an intentional opt-out, not a
				// failed load: report it neutrally and never as "inactive".
				s.push(fmt.Sprintf(T("  -- %s：mod 已标记 _skipemote，按要求不配置表情",
					"  -- %s: mod marked _skipemote, expression setup skipped as requested"), c.Label))
				skippedLabels[c.Name] = c.Label
				continue
			case wbDead:
				s.push(fmt.Sprintf(T("  -- %s：配置里的 mod 已改名或缺失，不生成表情绑定",
					"  -- %s: configured mod was renamed or is missing, skipping expression bindings"), c.Label))
				inactiveLabels[c.Name] = c.Label
				continue
			}
		}
		renderTable.Chars = append(renderTable.Chars, c)
	}
	// The real Mods root is passed to the renderer so a base_ref can be walked
	// back from the ini no matter how deep the panel folder is nested. A fixed
	// two-level prefix resolved every base inside the panel wrapper instead of
	// under Mods/, and looked like a missing mesh in every mod at once.
	g := render.New(renderTable, udir, "", modsRoot)
	out := filepath.Join(udir, "WWEmoteMods.ini")
	_, n, err := g.WriteMods(out, udir)
	// Per-set problems found while rendering (an unmatched pose_exclude, or a
	// bucket whose base/delta is unusable) were dropped by the renderer. Report
	// them so the user knows which sets were left out, without aborting the
	// whole config. s.push also writes the log, so these reach Diagnostics.
	for _, sk := range g.Skipped {
		s.push("  ! " + sk)
		failedSets = append(failedSets, sk)
	}
	if len(g.Skipped) > 0 {
		s.push(fmt.Sprintf(T("  -- %d 套未配置，失败 mod 见上方 ! 行。",
			"  -- %d set(s) not configured; see the ! lines above for the failed mods."), len(g.Skipped)))
	}
	if err != nil {
		// A render-time fatal after the per-character loop still carries the
		// skipped set names. Listing every claimed mod would hide which ones
		// actually failed, so g.Skipped is the primary evidence here.
		det := err.Error()
		if len(g.Skipped) > 0 {
			det += "\nfailed mod files:\n  " + strings.Join(g.Skipped, "\n  ")
		} else {
			var names []string
			for cn := range claimedByChar {
				names = append(names, claimedByChar[cn]...)
			}
			sort.Strings(names)
			if len(names) > 0 {
				if len(names) > 20 {
					det += fmt.Sprintf("\ninvolved mod files (%d total): %s …", len(names), strings.Join(names[:20], "; "))
				} else {
					det += "\ninvolved mod files: " + strings.Join(names, "; ")
				}
			}
		}
		return nil, bad(T("写不出发给游戏的配置文件", "Cannot write the config file the game reads"),
			adviceReport(), det)
	}
	if n == 0 {
		// No character had any scanner data (fresh install, wiped user/, or a
		// scope holding only skipped mods). Nothing to configure: the generator
		// removed any stale ini, so report the skip instead of a 0-byte write.
		s.push(T("  没有需要配置的 mod，已跳过（该文件夹的表情配置未改动）。",
			"  No mod to configure, skipped (expression setup left unchanged)."))
	} else {
		s.push(fmt.Sprintf(T("已写出 %s（%d 字节）", "Wrote %s (%d bytes)"), out, n))
	}
	s.setPhase(1, 95, "run", T("② 已写入 user 层", "② User layer written"))
	if err := cancelled(cancel); err != nil {
		return nil, err
	}

	// ---- ③ verify: only user/ may contain new files ----
	s.setPhase(2, 96, "verify", T("③ 复核改动范围", "③ Verify the change scope"))
	buFs, _ := filepath.Glob(filepath.Join(udir, "*.buf"))
	s.push(fmt.Sprintf(T("  OK  改动范围：只在 user/ 内（%d 份差量 buf + 2 个 ini）",
		"  OK  Scope: inside user/ only (%d delta buf + 2 ini)"), len(buFs)))
	s.setPhase(2, 99, "verify", T("③ 复核通过", "③ Verified"))

	// A character whose config points at an unloaded mod keeps its data in
	// mods.json but got no expression bindings. Report it separately and drop
	// it from adapted/added, so those lines describe what actually took effect
	// rather than what was merely written to disk.
	// Both kinds of skipped character must leave adapted/added. A _skipemote
	// opt-out and a dead write-back target both produce no bindings, so neither
	// may be reported as adapted. They differ only in how the UI labels them.
	excluded := make(map[string]bool, len(inactiveLabels)+len(skippedLabels))
	for n := range inactiveLabels {
		excluded[n] = true
	}
	for n := range skippedLabels {
		excluded[n] = true
	}
	effective, excludedEntries := splitAdapted(adapted, excluded)
	adapted = effective
	// Build the two report lines in scan order from the labels captured during
	// classification. A stale config marked _skipemote may never have entered
	// adapted, so recovering labels from adapted would drop it silently.
	var inactiveOrder, skippedOrder []string
	for _, c := range fresh.Chars {
		if _, ok := inactiveLabels[c.Name]; ok {
			inactiveOrder = append(inactiveOrder, c.Label)
		} else if _, ok := skippedLabels[c.Name]; ok {
			skippedOrder = append(skippedOrder, c.Label)
		}
	}

	// adaptedText mirrors the Python scanner's ⑥ summary line verbatim:
	//   "✓ 扫描完成：2 个角色已适配 —— 心 6 套 · 露帕 4 套"
	// The front-end is byte-identical to the old one and its renderSummary()
	// keys off "N 个角色已适配" (then localises the labels and 套). Keep that
	// exact wording or the whole summary silently disappears in non-CN locales.
	// Empty fallback (not "配置完成"): an empty adapted makes the front-end
	// renderSummary() fall through to its localised r.done string. A Chinese
	// literal here would leak into non-Chinese UIs as raw text.
	adaptedText := formatAdaptedText(adapted)
	// inactiveText/skippedText name the MOD as the subject, never the
	// character. "this character's mod failed to load" is very different from
	// "this character failed to load": other mods of the same character may
	// still be configured. The character names only identify the owner and go
	// in the trailing list. The reason lives in the front-end's localised
	// label so no Chinese leaks into non-CN clients.
	inactiveText := ""
	if len(inactiveOrder) > 0 {
		inactiveText = fmt.Sprintf("%d 个角色的 mod 未加载 —— %s", len(inactiveOrder), strings.Join(inactiveOrder, " · "))
	}
	// skippedText is neutral: the user deliberately marked these mods with
	// _skipemote. They are NOT a failure, so the UI must not say "inactive".
	skippedText := ""
	if len(skippedOrder) > 0 {
		skippedText = fmt.Sprintf("%d 个角色的 mod 按 _skipemote 跳过 —— %s", len(skippedOrder), strings.Join(skippedOrder, " · "))
	}
	// addedText reports what THIS run baked, not the installed total. Every
	// delta reused from cache contributes zero, so a no-change configure says
	// "新增 0 套". See formatAddedText for the front-end contract.
	addedText := formatAddedText(adapted)
	// When nothing took effect, still report what WAS written to user/.
	// Otherwise an all-DISABLED_ run reads as zero work despite real deltas.
	stagedText := formatStagedText(addedText, excludedEntries, inactiveLabels)

	// okText names every mod this run configured, same layout as the failure
	// list. Characters are visited in table order so the list is stable.
	var okSets []string
	for _, c := range fresh.Chars {
		if excluded[c.Name] {
			continue
		}
		okSets = append(okSets, okByChar[c.Name]...)
	}
	okText := formatOkText(okSets)

	return map[string]any{
		"adapted":     adaptedText,
		"inactive":    inactiveText,
		"skipped":     skippedText,
		"added":       addedText,
		"staged":      stagedText,
		"ok":          okText,
		"failed":      formatFailedText(failedSets),
		"deployed_to": udir,
		"n_files":     len(buFs) + 2,
	}, nil
}

// splitAdapted separates characters that got no expression bindings from the
// ones that took effect. inactive keys name the skipped characters; order is
// preserved in both slices so the summary stays deterministic.
func splitAdapted(all []adaptedEntry, inactive map[string]bool) (effective, skipped []adaptedEntry) {
	effective = make([]adaptedEntry, 0, len(all))
	skipped = make([]adaptedEntry, 0, len(inactive))
	for _, a := range all {
		if inactive[a.name] {
			skipped = append(skipped, a)
		} else {
			effective = append(effective, a)
		}
	}
	return effective, skipped
}

type wbState int

const (
	wbNone   wbState = iota // no pvb path at all
	wbLive                  // at least one write-back target exists on disk
	wbMarked                // no live target, but a _skipemote folder explains it
	wbDead                  // no live target and no opt-out marker: a real failure
)

// writebackRel strips a scan pvb key down to the ini path relative to modsRoot.
// It returns "" when the key is malformed.
func writebackRel(p string) string {
	const head = "Resource/Mods/"
	rel := strings.ReplaceAll(p, "\\", "/")
	if len(rel) <= len(head) || !strings.EqualFold(rel[:len(head)], head) {
		return ""
	}
	rel = rel[len(head):]
	i := strings.LastIndex(rel, "/") // drop the PositionBuffer segment
	if i <= 0 {
		return ""
	}
	return rel[:i]
}

// classifyWriteback explains why a character's configured write-back targets
// are or are not live. A target whose ini exists is wbLive. When none exists,
// a path under a folder the user marked _skipemote (recorded by the scan as a
// suffix-stripped base) is wbMarked: the user deliberately opted out, and a
// "mod not loaded" warning would mislead. Anything else is wbDead.
func classifyWriteback(modsRoot string, pvbPaths []string, skippedBases []string) wbState {
	if len(pvbPaths) == 0 {
		return wbNone
	}
	dead := false
	for _, p := range pvbPaths {
		rel := writebackRel(p)
		if rel == "" {
			continue
		}
		// A DISABLED_ mod is configured under its unprefixed name, but the
		// folder on disk still carries the marker until the user enables it.
		// Resolve that back so a disabled mod is a live target: disabling is
		// the game's load switch, not a reason to drop the bindings.
		if _, ok := modpath.ResolveOnDisk(filepath.Join(modsRoot, filepath.FromSlash(rel))); ok {
			return wbLive
		}
		dead = true
	}
	if !dead {
		return wbNone
	}
	for _, base := range skippedBases {
		bsegs := strings.Split(base, "/")
		if len(bsegs) < 2 {
			continue
		}
		for _, p := range pvbPaths {
			rel := writebackRel(p)
			rsegs := strings.Split(rel, "/")
			if len(rsegs) < 3 {
				continue
			}
			// seg 0 is the character folder, seg 1 the mod folder. A marked
			// mod is stored under its unmarked name; the DISABLED_ copy that
			// produced the config may shorten it ("HsinMod" vs "HsinMod
			// thicc"), so accept either name being a prefix of the other.
			if !strings.EqualFold(bsegs[0], rsegs[0]) {
				continue
			}
			bm, rm := strings.ToLower(bsegs[1]), strings.ToLower(rsegs[1])
			if strings.HasPrefix(bm, rm) || strings.HasPrefix(rm, bm) {
				return wbMarked
			}
		}
	}
	return wbDead
}

// liveWriteback is the boolean form kept for the scanner tests and any caller
// that only needs "is there a live target". It treats a marked mod as not live
// because no bindings are generated for it either.
func liveWriteback(modsRoot string, pvbPaths []string, skippedBases ...string) bool {
	return classifyWriteback(modsRoot, pvbPaths, skippedBases) == wbLive
}

// inScopeRel reports whether a disk-relative ini path lies inside the chosen
// folder. An empty scopeRel means "whole Mods tree" (everything is in scope).
// The trailing slash matters: "心/HsinMod" must not match "心/HsinMod-thicc".
func inScopeRel(scopeRel, diskRel string) bool {
	if scopeRel == "" {
		return true
	}
	return strings.HasPrefix(diskRel, scopeRel+"/")
}

func cancelled(ch <-chan struct{}) error {
	select {
	case <-ch:
		return errCancelled
	default:
		return nil
	}
}
