package render

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// modsTmpl is the whole user layer template. Its only job is to place the three
// blocks in the same order the character region of the full template uses, so
// that the section order of the output stays the same.
var modsTmpl = strings.Join([]string{
	"namespace = WWEmote",
	"",
	"[Constants]",
	"; @@BLOCK:ROUTE_R_STATE_CONSTS@@",
	"",
	"; @@BLOCK:VALUE_ROUTE@@",
	"",
	// This banner must match the full template word for word. It used to live
	// only there; once the emergency-key section was dropped it became the
	// first line of the user layer, and the two paths differed by one line.
	"; ---- CHARS: 5 BAKE ROUTE ----",
	"; @@BLOCK:ROUTE_R@@",
}, "\n")

const maxBlockDepth = 8

// render expands @@BLOCK:NAME@@ lines recursively.
//
// Indentation follows the placeholder line: a placeholder inside an `if` must
// indent its expansion, otherwise the if/endif pairing still balances but the
// content ends up outside the branch.
//
// The termination test is "nothing was expanded this round", not "no @@BLOCK:
// is left in the text": the reference template explains its placeholders in a
// leading comment, and testing for the marker never terminates.
func (g *Gen) render(tmpl string) (string, error) {
	blocks := map[string]func() ([]string, error){
		"ROUTE_R_STATE_CONSTS": g.blkRouteRStateConsts,
		"VALUE_ROUTE":          g.blkValueRoute,
		"ROUTE_R":              g.blkRouteR,
	}
	usedAll := map[string]bool{}
	for depth := 0; depth < maxBlockDepth; depth++ {
		var out []string
		used := map[string]bool{}
		for _, ln := range strings.Split(tmpl, "\n") {
			m := reBlockPlace.FindStringSubmatch(ln)
			if m == nil {
				out = append(out, ln)
				continue
			}
			indent, name := m[1], m[2]
			fn, ok := blocks[name]
			if !ok {
				return "", fmt.Errorf("unknown placeholder @@BLOCK:%s@@ in the template", name)
			}
			used[name] = true
			body, err := fn()
			if err != nil {
				return "", err
			}
			for _, bl := range body {
				if strings.TrimSpace(bl) == "" {
					out = append(out, "")
				} else {
					out = append(out, indent+bl)
				}
			}
		}
		tmpl = strings.Join(out, "\n")
		for k := range used {
			usedAll[k] = true
		}
		if len(used) == 0 {
			var missing []string
			for k := range blocks {
				if !usedAll[k] {
					missing = append(missing, k)
				}
			}
			if len(missing) > 0 {
				return "", fmt.Errorf("template is missing placeholders: %v", missing)
			}
			return tmpl, nil
		}
	}
	return "", fmt.Errorf("render: placeholders were still nested after %d levels -- "+
		"looks like a placeholder expanding itself", maxBlockDepth)
}

// sectionBlock is one section together with the comment block in front of it.
type sectionBlock struct {
	head   []string
	name   string // "" for the file header before the first section
	body   []string
	isHead bool
}

// splitOutputs cuts the rendered ini into the main file, one file per character
// and the user layer.
//
// Sections are moved together with their leading comment block. The `[Constants]`
// section is the only one that has to be split line by line, because it holds
// the declarations of all three destinations at once.
func (g *Gen) splitOutputs(ini string) (core string, chars map[string]string, mods string, err error) {
	var blocks []sectionBlock
	var pend []string
	cur := ""
	haveCur := false
	var body []string

	for _, ln := range strings.Split(ini, "\n") {
		m := reSectionHead.FindStringSubmatch(ln)
		if m != nil {
			if !haveCur {
				blocks = append(blocks, sectionBlock{head: append([]string(nil), pend...), isHead: true})
				// The header must be cleared out, or it would be treated as the
				// leading comment of the first section.
				pend = nil
			} else {
				// The trailing run of comments and blank lines belongs to the
				// next section: the generators write a section's banner before
				// its header, and some banners carry data-dependent text.
				var tail []string
				for len(body) > 0 {
					last := body[len(body)-1]
					if strings.TrimSpace(last) == "" ||
						strings.HasPrefix(strings.TrimLeftFunc(last, unicode.IsSpace), ";") {
						tail = append([]string{last}, tail...)
						body = body[:len(body)-1]
						continue
					}
					break
				}
				blocks = append(blocks, sectionBlock{
					head: append([]string(nil), pend...), name: cur, body: body})
				pend = tail
			}
			cur = m[1]
			haveCur = true
			body = nil
			continue
		}
		if !haveCur {
			pend = append(pend, ln)
		} else {
			body = append(body, ln)
		}
	}
	if haveCur {
		blocks = append(blocks, sectionBlock{
			head: append([]string(nil), pend...), name: cur, body: body})
	}

	// `namespace =` is a per-file declaration: it applies only to the file it
	// appears in. Without it the host derives a namespace from the file path,
	// so cross-file reads silently return 0.
	nsLine := ""
	for _, b := range blocks {
		if !b.isHead {
			continue
		}
		for _, ln := range b.head {
			if reNamespace.MatchString(ln) {
				nsLine = ln
				break
			}
		}
		break
	}
	if nsLine == "" {
		return "", nil, "", fmt.Errorf("splitOutputs: no `namespace =` in the main " +
			"file header -- the character files would get no namespace and " +
			"cross-file access would silently fail")
	}

	var coreLines []string
	charLines := map[string][]string{}
	var charOrder []string
	var modsLines []string

	for _, b := range blocks {
		if b.isHead {
			coreLines = append(coreLines, injectSignature(b.head)...)
			continue
		}
		if b.name == "Constants" {
			coreSeg, charSeg, charSeq, modsSeg, err := g.splitConstantsBody(b.body)
			if err != nil {
				return "", nil, "", err
			}
			hasReal := false
			for _, x := range coreSeg {
				s := strings.TrimSpace(x)
				if s != "" && !strings.HasPrefix(s, ";") {
					hasReal = true
					break
				}
			}
			coreLines = append(coreLines, b.head...)
			if hasReal {
				coreLines = append(coreLines, "["+b.name+"]")
			}
			coreLines = append(coreLines, coreSeg...)
			for _, cap := range charSeq {
				seg := charSeg[cap]
				if len(seg) == 0 {
					continue
				}
				if _, ok := charLines[cap]; !ok {
					charOrder = append(charOrder, cap)
				}
				charLines[cap] = append(charLines[cap], "", "; ---- CONSTANTS: THIS CHAR ----", "[Constants]")
				charLines[cap] = append(charLines[cap], seg...)
			}
			if len(modsSeg) > 0 {
				modsLines = append(modsLines, "", "; ---- CONSTANTS: USER LAYER ----", "[Constants]")
				modsLines = append(modsLines, modsSeg...)
			}
			continue
		}
		o := g.outputFileOf(b.name)
		switch o.Kind {
		case OUT_MODS:
			modsLines = append(modsLines, b.head...)
			modsLines = append(modsLines, "["+b.name+"]")
			modsLines = append(modsLines, b.body...)
		case OUT_CHAR:
			if _, ok := charLines[o.Cap]; !ok {
				charOrder = append(charOrder, o.Cap)
			}
			charLines[o.Cap] = append(charLines[o.Cap], b.head...)
			charLines[o.Cap] = append(charLines[o.Cap], "["+b.name+"]")
			charLines[o.Cap] = append(charLines[o.Cap], b.body...)
		default:
			coreLines = append(coreLines, b.head...)
			coreLines = append(coreLines, "["+b.name+"]")
			coreLines = append(coreLines, b.body...)
		}
	}

	chars = map[string]string{}
	for _, cap := range charOrder {
		v := append(charHeader(cap, nsLine), charLines[cap]...)
		stripped, err := stripINIComments(strings.Join(v, "\n"))
		if err != nil {
			return "", nil, "", err
		}
		chars[cap] = tidy(stripped)
	}
	if len(modsLines) > 0 {
		v := append(modsHeader(nsLine), modsLines...)
		stripped, err := stripINIComments(strings.Join(v, "\n"))
		if err != nil {
			return "", nil, "", err
		}
		mods = tidy(stripped)
	}
	stripped, err := stripINIComments(strings.Join(coreLines, "\n"))
	if err != nil {
		return "", nil, "", err
	}
	core = tidy(stripped)
	return core, chars, mods, nil
}

// splitConstantsBody attributes each `[Constants]` line to one destination.
//
// Comment lines follow the declaration below them, so a character file does not
// lose its explanation and the main file does not keep comments about variables
// it no longer declares.
func (g *Gen) splitConstantsBody(body []string) (core []string,
	chars map[string][]string, order []string, mods []string, err error) {
	chars = map[string][]string{}
	common := commonVars()
	var pend []string
	for _, ln := range body {
		m := reGlobalDecl.FindStringSubmatch(ln)
		if m == nil {
			if strings.HasPrefix(strings.TrimLeftFunc(ln, unicode.IsSpace), ";") {
				pend = append(pend, ln)
			} else {
				core = append(core, pend...)
				pend = nil
				core = append(core, ln)
			}
			continue
		}
		v := m[1]
		cap := ""
		toMods := false
		if !common[v] {
			if modsVar(v) {
				toMods = true
			} else {
				cap = g.roleVarOf(v)
			}
		}
		switch {
		case toMods:
			mods = append(mods, pend...)
			mods = append(mods, ln)
		case cap != "":
			if _, ok := chars[cap]; !ok {
				order = append(order, cap)
			}
			chars[cap] = append(chars[cap], pend...)
			chars[cap] = append(chars[cap], ln)
		default:
			core = append(core, pend...)
			core = append(core, ln)
		}
		pend = nil
	}
	core = append(core, pend...)
	return core, chars, order, mods, nil
}

func signatureValue(role string) string {
	if role == "core" {
		return signatureRoot
	}
	return signatureRoot + "/" + role
}

func signatureLine(role string) string {
	return "; " + signatureKey + " = " + signatureValue(role)
}

func versionLine(version string) string {
	if version == "" {
		version = "unknown"
	}
	return "; " + versionKey + " = " + version
}

// injectSignature puts the machine-readable name card right after the first
// line of the header, so a renamed panel directory is still recognisable.
func injectSignature(head []string) []string {
	var lines []string
	for _, ln := range head {
		if strings.Contains(ln, signatureKey) || strings.Contains(ln, versionKey) {
			continue
		}
		lines = append(lines, ln)
	}
	if len(lines) == 0 {
		return []string{signatureLine("core"), versionLine(""), ""}
	}
	out := []string{lines[0], signatureLine("core"), versionLine("")}
	return append(out, lines[1:]...)
}

func charHeader(cap, nsLine string) []string {
	return []string{
		"; ============================================================",
		"",
		nsLine,
		"",
	}
}

func modsHeader(nsLine string) []string {
	return []string{
		"; ============================================================",
		signatureLine("mods"),
		"; ============================================================",
		"",
		nsLine,
		"",
	}
}

// stripINIComments drops whole-line comments, keeping the name card, the
// version line and the structural banners that negative tests anchor on.
func stripINIComments(text string) (string, error) {
	var out []string
	for _, ln := range strings.Split(text, "\n") {
		l := strings.TrimLeftFunc(ln, unicode.IsSpace)
		if strings.HasPrefix(l, ";") {
			if strings.Contains(ln, signatureKey) || strings.Contains(ln, versionKey) ||
				strings.HasPrefix(l, "; ---- ") {
				out = append(out, strings.TrimRightFunc(ln, unicode.IsSpace))
			}
			continue
		}
		out = append(out, strings.TrimRightFunc(ln, unicode.IsSpace))
	}
	res := strings.Join(out, "\n")
	if err := AssertASCIIComments(res, "the split ini"); err != nil {
		return "", err
	}
	return res, nil
}

// AssertASCIIComments fails when a shipped comment contains CJK text.
//
// Only the comments that survive stripping reach a user's disk, and those must
// be short English: the game and most editors will read them as GBK.
func AssertASCIIComments(text, where string) error {
	var bad []string
	for _, ln := range strings.Split(text, "\n") {
		if !strings.HasPrefix(strings.TrimLeftFunc(ln, unicode.IsSpace), ";") {
			continue
		}
		for _, r := range ln {
			if r >= 0x4e00 && r <= 0x9fff {
				bad = append(bad, ln)
				break
			}
		}
	}
	if len(bad) == 0 {
		return nil
	}
	shown := bad
	if len(shown) > 6 {
		shown = shown[:6]
	}
	var b strings.Builder
	for _, s := range shown {
		if len(s) > 96 {
			s = s[:96]
		}
		b.WriteString("  " + s + "\n")
	}
	return fmt.Errorf("%s: %d comment line(s) are not ASCII -- shipped comments "+
		"must be short English (ASCII):\n%s", where, len(bad), b.String())
}

// tidy collapses runs of blank lines, trims trailing ones and ends with a
// single newline.
func tidy(text string) string {
	var out []string
	for _, ln := range strings.Split(text, "\n") {
		if strings.TrimSpace(ln) == "" && len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			continue
		}
		out = append(out, strings.TrimRightFunc(ln, unicode.IsSpace))
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n") + "\n"
}

// RenderMods returns the complete text of WWEmoteMods.ini without writing it.
func (g *Gen) RenderMods() (string, error) {
	g.shims = newShimSet()
	g.Skipped = nil
	g.prepared = false
	ini, err := g.render(modsTmpl)
	if err != nil {
		return "", err
	}
	_, _, mods, err := g.splitOutputs(ini)
	if err != nil {
		return "", err
	}
	return mods, nil
}

// RenderShims returns the namespace shim files collected by the last
// RenderMods call. Each is written under the user layer directory. The set is
// empty when every mod path was already safe, so ordinary runs write nothing
// extra. Callers must clear stale shim files from earlier runs themselves.
func (g *Gen) RenderShims() []struct{ Name, Text string } {
	if g.shims == nil {
		return nil
	}
	return g.shims.files()
}

// RenderUserDelta returns the declaration section for the user-layer data
// directory, or "" when no character needs a delta.
//
// An empty result is legal (no mods, or no adapted character) and the caller
// must not create the file: an empty ini makes "present" and "absent"
// indistinguishable.
func (g *Gen) RenderUserDelta() (string, error) {
	chars, err := g.chars()
	if err != nil {
		return "", err
	}
	out := []string{
		"; ------------------------------------------------------------",
		"; WWEmote user data - generated, do not edit",
		fmt.Sprintf(";   %-26s per-vertex shapekey deltas", userDeltaININame),
		";   <char>_mod_<vc>_sparse.buf   data files used by them",
		fmt.Sprintf(";   %-26s user-layer main file (same dir)", modsININame),
		"; Re-run the config tool after switching to another mod set:",
		"; the vertex space changes, so old data would tear the mesh.",
		"; ------------------------------------------------------------",
		"",
	}
	n := 0
	for _, c := range chars {
		// One section per bucket, named with the vertex count: the delta tables
		// of different mod sets live in different vertex spaces and must not
		// overwrite each other. chars() has already filtered unusable buckets.
		for _, t := range g.deltaTargets(c) {
			out = append(out, fmt.Sprintf("[%s%d_%d]", userDeltaSection, c.Cid, t.VC))
			frag := t.Frag
			if !isASCII(frag) {
				frag = "<non-ascii fragment>"
			}
			out = append(out, fmt.Sprintf("; %s: sparse delta, set = '%s', vc = %d",
				strings.ToUpper(c.Name), frag, t.VC))
			out = append(out, ";   layout = [row CSR (VC+1) x u32][N x (key, dx, dy, dz) f32 x 4]")
			out = append(out, ";   entries sorted by (vertex, key) -> one span per vertex")
			out = append(out, ";   bytes = manifest.total_bytes, vc = manifest.vc")
			out = append(out, "type = Buffer")
			out = append(out, "format = DXGI_FORMAT_R32_FLOAT")
			out = append(out, "stride = 4")
			out = append(out, "filename = "+t.Sparse)
			out = append(out, "")
			n++
		}
	}
	if n == 0 {
		return "", nil
	}
	res := strings.Join(out, "\n")
	// This file does not go through stripINIComments, so the ASCII rule needs
	// its own checkpoint here or its header would be unsupervised.
	if err := AssertASCIIComments(res, userDeltaININame); err != nil {
		return "", err
	}
	return res, nil
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

// DeltaBufs lists every baked delta file the user layer needs, as
// (bare filename, absolute source path).
//
// The names are read from each character's own mods.json rather than kept in a
// second list here: a copy would rot the moment a character is added or a bucket
// is re-baked.
func (g *Gen) DeltaBufs() ([]struct{ Name, Src string }, error) {
	type item struct{ Name, Src string }
	var out []item
	seen := map[string]bool{}
	chars, err := g.chars()
	if err != nil {
		return nil, err
	}
	for _, c := range chars {
		names := []string{}
		for _, t := range g.deltaTargets(c) {
			names = append(names, t.Sparse, t.Base)
			for _, ps := range t.Pos {
				if ps.Base != "" {
					names = append(names, ps.Base)
				}
			}
		}
		for _, fn := range names {
			// A name with a separator is a reference into the mod tree, not a
			// file this tool owns: the user layer already reaches it, and
			// "copying" it would resolve back onto itself.
			if fn == "" || seen[fn] || strings.ContainsAny(fn, `/\`) {
				continue
			}
			seen[fn] = true
			out = append(out, item{fn, filepath.Join(g.UserRoot, fn)})
		}
	}
	res := make([]struct{ Name, Src string }, len(out))
	for i, it := range out {
		res[i] = struct{ Name, Src string }{it.Name, it.Src}
	}
	return res, nil
}

// WriteMods renders and writes the user layer, returning the path and the byte
// count actually written.
//
// When userDeltaDir is non-empty the delta declaration and every delta buffer
// are written there as well. Writing only the ini would leave `cs-t8` pointing
// at a resource whose filename does not exist, and 3DMigoto skips that silently.
func (g *Gen) WriteMods(out string, userDeltaDir string) (string, int, error) {
	text, err := g.RenderMods()
	if err != nil {
		return "", 0, err
	}
	if out == "" {
		out = filepath.Join(modsINISubdir, modsININame)
	}
	if text == "" {
		// No character has any scanner data: a fresh install, a wiped user/
		// dir, or a scope that contained only skipped mods. There is nothing
		// to write. A stale ini from an earlier run would point at deltas that
		// are no longer configured, so remove it rather than leave a broken
		// config behind. Zero bytes is the caller's "nothing written" signal.
		if err := os.Remove(out); err != nil && !os.IsNotExist(err) {
			return "", 0, err
		}
		// Shims are collected by RenderMods; an empty render collected none, so
		// any left on disk belong to a previous run. include_recursive loads
		// them anyway and their namespace points at a mod this config no longer
		// touches. Clear them here, alongside the stale main ini.
		if err := writeShims(filepath.Dir(out), nil); err != nil {
			return "", 0, err
		}
		return "", 0, nil
	}
	if d := filepath.Dir(out); d != "" && d != "." {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", 0, err
		}
	}
	if err := WriteIfChanged(out, text); err != nil {
		return "", 0, err
	}
	// Namespace shims live beside the main ini so include_recursive picks them
	// up. Clear shims from earlier runs first (a mod may have been renamed),
	// then write the ones this pass collected. WriteIfChanged keeps a no-change
	// run from touching the disk.
	if err := writeShims(filepath.Dir(out), g.RenderShims()); err != nil {
		return "", 0, err
	}
	if userDeltaDir != "" {
		shim, err := g.RenderUserDelta()
		if err != nil {
			return "", 0, err
		}
		if shim != "" {
			if err := os.MkdirAll(userDeltaDir, 0o755); err != nil {
				return "", 0, err
			}
			if err := WriteIfChanged(filepath.Join(userDeltaDir, userDeltaININame), shim); err != nil {
				return "", 0, err
			}
			if err := g.CopyDeltaBufs(userDeltaDir); err != nil {
				return "", 0, err
			}
		}
	}
	return out, len(text), nil
}

// writeShims removes shim files left by earlier runs and writes the current
// set. Only files matching the generated prefix are touched, so nothing the
// user placed in the directory is removed.
func writeShims(dir string, files []struct{ Name, Text string }) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, f := range files {
		keep[f.Name] = true
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), shimFilePrefix) {
			continue
		}
		if strings.HasSuffix(e.Name(), ".tmp") || keep[e.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	for _, f := range files {
		if err := WriteIfChanged(filepath.Join(dir, f.Name), f.Text); err != nil {
			return err
		}
	}
	return nil
}

// WriteIfChanged writes text only when the bytes differ, so that running the
// same configuration twice touches nothing on disk.
//
// The write itself goes through a temp file plus rename. A plain WriteFile
// truncates first, so closing the window (or killing the process) mid-write
// leaves a half-written ini that the game then parses -- the failure shows up
// as "no expressions at all" with nothing in any log. Rename is atomic on the
// same volume, so the file is always either the old version or the new one.
func WriteIfChanged(path, text string) error {
	data := []byte(text)
	if old, err := os.ReadFile(path); err == nil && string(old) == string(data) {
		return nil
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

// CopyDeltaBufs copies the baked delta files into the user directory, skipping
// the ones that are already identical.
func (g *Gen) CopyDeltaBufs(destDir string) error {
	items, err := g.DeltaBufs()
	if err != nil {
		return err
	}
	for _, it := range items {
		if _, err := os.Stat(it.Src); err != nil {
			return fmt.Errorf("delta file missing: %s\n  => %q declares it should "+
				"ship, but it is not on disk. Re-bake it", it.Src, it.Name)
		}
		dp := filepath.Join(destDir, it.Name)
		if same, err := sameFile(it.Src, dp); err == nil && same {
			continue
		}
		if err := copyFile(it.Src, dp); err != nil {
			return err
		}
	}
	return nil
}

func sameFile(a, b string) (bool, error) {
	st, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	sa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	if st.Size() != sa.Size() {
		return false, nil
	}
	ha, err := md5Of(a)
	if err != nil {
		return false, err
	}
	hb, err := md5Of(b)
	if err != nil {
		return false, err
	}
	return ha == hb, nil
}

func md5Of(path string) ([16]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return [16]byte{}, err
	}
	return md5.Sum(data), nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	// Same rule as every other artifact: the destination only ever holds a
	// complete file. Build the copy under a temp name, then move it into place.
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	// Windows refuses to overwrite a file that is open, so the old target is
	// renamed out of the way first; rename does not need write permission.
	old := ""
	if _, err := os.Stat(dst); err == nil {
		old = dst + ".old"
		_ = os.Remove(old)
		if err := os.Rename(dst, old); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	if old != "" {
		_ = os.Remove(old)
	}
	return nil
}
