// Package scan reproduces the user-side path of _tools/scan_mods.py:
// read the exclude rules, index every ini under Mods/, claim a character's
// sets by anchor hash, and derive the user-layer data dict.
//
// It deliberately stops before baking and rendering -- those are separate
// packages with their own byte-for-byte golden tests.
package scan

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	AnchorSection   = "TextureOverrideShapeKeyOffsets"
	ResSK           = "CustomShapeKeyValuesRW"
	ResPVB          = "PositionBuffer"
	PathHeadMods    = `Resource\Mods\`
	unexcludeSep    = "_- "
	skipEmoteSuffix = "_skipemote"
)

// hasSkipEmoteFolder reports whether any segment of p ends in _skipemote.
// A folder a user marks this way keeps loading as a normal mod in the game
// (3DMigoto includes every ini under Mods/), but WWEmote must not configure
// it: the expression bindings are keyed by the folder chain in the resource
// path, so configuring would write bindings the user took out on purpose.
// Matching a segment rather than a whole path keeps the marker folder-scoped
// and case-insensitive.
func hasSkipEmoteFolder(p string) bool {
	for _, seg := range strings.FieldsFunc(p, func(r rune) bool { return r == '\\' || r == '/' }) {
		if strings.HasSuffix(strings.ToLower(seg), skipEmoteSuffix) {
			return true
		}
	}
	return false
}

// skipEmoteScopeBase returns scopeRel with the _skipemote suffix stripped from
// every segment. When the user configures a marked folder directly, the old
// mods.json still points at that same folder under its pre-rename (unmarked)
// name, whose base mesh no longer exists at that path. A scoped merge must treat
// that old name as in scope and drop the stale set; reviving it writes a
// filename 3DMigoto cannot open, and a missing base collapses every vertex.
// An unmarked scope is returned unchanged, so normal runs are unaffected.
func skipEmoteScopeBase(scopeRel string) string {
	if scopeRel == "" || !hasSkipEmoteFolder(scopeRel) {
		return scopeRel
	}
	segs := strings.FieldsFunc(scopeRel, func(r rune) bool { return r == '\\' || r == '/' })
	for i, seg := range segs {
		if strings.HasSuffix(strings.ToLower(seg), skipEmoteSuffix) {
			segs[i] = seg[:len(seg)-len(skipEmoteSuffix)]
		}
	}
	return strings.Join(segs, "/")
}

var (
	reRunSelfBake = regexp.MustCompile(`(?i)run[ \t]*=[ \t]*CustomShader\\WWMIv1\\ShapeKeyApplier`)
	reRunTarget   = regexp.MustCompile(`(?i)^run[ \t]*=[ \t]*(CommandList[^\s]+)`)
	reVB0         = regexp.MustCompile(`(?i)^vb0[ \t]*=[ \t]*(\S+)`)
	reVB6Null     = regexp.MustCompile(`(?im)^vb6[ \t]*=[ \t]*null[ \t]*$`)
	reResDecl     = regexp.MustCompile(`(?i)^hash[ \t]*=[ \t]*(\S+)`)
	reCVCAssign   = regexp.MustCompile(`(?m)^\$\\WWMIv1\\custom_vertex_count[ \t]*=[ \t]*(\S+)[ \t]*$`)
	reVCLiteral   = regexp.MustCompile(`^(\d+)$`)
	rePosbufPath  = regexp.MustCompile(`Resource\\Mods\\[^\s=]+\\PositionBuffer`)
	reVB0Assign   = regexp.MustCompile(`(?im)^\s*vb0\s*=\s*(.+?)\s*$`)
	reFilename    = regexp.MustCompile(`(?im)^\s*filename\s*=\s*(.+?)\s*$`)
	reHex8        = regexp.MustCompile(`^[0-9a-f]{8}$`)
)

// ReadExcludeGlobs mirrors read_exclude_globs(): parse exclude_recursive from
// d3dx.ini, splitting on commas without trying to strip inline comments.
func ReadExcludeGlobs(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, ln := range strings.Split(string(b), "\n") {
		s := strings.TrimSpace(strings.TrimSuffix(ln, "\r"))
		if strings.HasPrefix(s, ";") || !strings.HasPrefix(strings.ToLower(s), "exclude_recursive") {
			continue
		}
		_, val, ok := strings.Cut(s, "=")
		if !ok {
			continue
		}
		for _, part := range strings.Split(val, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out, nil
}

// globMatch implements fnmatch.fnmatchcase for the simple globs d3dx.ini uses.
func globMatch(part, glob string) bool {
	part = strings.ToLower(part)
	glob = strings.ToLower(glob)
	var b strings.Builder
	b.WriteByte('^')
	for _, r := range glob {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteByte('.')
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteByte('$')
	re, err := regexp.Compile(b.String())
	if err != nil {
		return false
	}
	return re.MatchString(part)
}

func hit(part string, globs []string) bool {
	for _, g := range globs {
		if globMatch(part, g) {
			return true
		}
	}
	return false
}

// unexclude strips the prefix declared by a matched glob from every path
// component, plus one separator character from unexcludeSep.
func unexclude(rel string, globs []string) string {
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		newPart := part
		for _, g := range globs {
			if !globMatch(part, g) {
				continue
			}
			star := strings.IndexByte(g, '*')
			pre := ""
			if star >= 0 {
				pre = g[:star]
			}
			if pre != "" && strings.HasPrefix(strings.ToLower(newPart), strings.ToLower(pre)) {
				newPart = newPart[len(pre):]
				if len(newPart) > 0 && strings.Contains(unexcludeSep, newPart[:1]) {
					newPart = newPart[1:]
				}
			}
		}
		parts[i] = newPart
	}
	return strings.Join(parts, "/")
}

// iniNamespace reads a file's preamble "namespace =" value. 3DMigoto applies it
// to the whole file: every section in it is renamed to "<prefix>\<value>\<name>",
// so a reference built from the ini's path stops resolving there. The preamble
// ends at the first section header.
func iniNamespace(text string) string {
	text = strings.TrimPrefix(text, "\ufeff")
	for _, raw := range strings.Split(text, "\n") {
		s := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if s == "" || strings.HasPrefix(s, ";") {
			continue
		}
		if strings.HasPrefix(s, "[") {
			return ""
		}
		k, v, ok := strings.Cut(s, "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), "namespace") {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func codeLines(text string) []string {
	var out []string
	for _, raw := range strings.Split(text, "\n") {
		s := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if s == "" || strings.HasPrefix(s, ";") {
			continue
		}
		out = append(out, s)
	}
	return out
}

type section struct {
	name string
	body []string
}

func sections(text string) []section {
	var out []section
	for _, raw := range strings.Split(text, "\n") {
		ln := strings.TrimSuffix(raw, "\r")
		s := strings.TrimSpace(ln)
		if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
			out = append(out, section{name: strings.TrimSpace(s[1 : len(s)-1])})
			continue
		}
		if len(out) > 0 {
			out[len(out)-1].body = append(out[len(out)-1].body, ln)
		}
	}
	return out
}

func sectionOf(text, name string) string {
	for _, s := range sections(text) {
		if strings.EqualFold(s.name, name) {
			return strings.Join(s.body, "\n")
		}
	}
	return ""
}

func declaredHashes(text, sectionName string) []string {
	var out []string
	for _, ln := range strings.Split(sectionOf(text, sectionName), "\n") {
		s := strings.TrimSpace(strings.TrimSuffix(ln, "\r"))
		if strings.HasPrefix(s, ";") {
			continue
		}
		key, val, ok := strings.Cut(s, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "hash") {
			v := strings.ToLower(strings.TrimSpace(val))
			if reHex8.MatchString(v) {
				out = append(out, v)
			}
		}
	}
	return out
}

func componentsHashes(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, sec := range sections(text) {
		if !strings.HasPrefix(strings.ToLower(sec.name), "textureoverridecomponent") {
			continue
		}
		for _, ln := range sec.body {
			s := strings.TrimSpace(strings.TrimSuffix(ln, "\r"))
			if strings.HasPrefix(s, ";") {
				continue
			}
			key, val, ok := strings.Cut(s, "=")
			if ok && strings.EqualFold(strings.TrimSpace(key), "hash") {
				v := strings.ToLower(strings.TrimSpace(val))
				if reHex8.MatchString(v) && !seen[v] {
					seen[v] = true
					out = append(out, v)
				}
			}
		}
	}
	return out
}

func declaredResNames(text string) map[string]string {
	out := map[string]string{}
	for _, sec := range sections(text) {
		if !strings.HasPrefix(strings.ToLower(sec.name), "resource") {
			continue
		}
		for _, ln := range sec.body {
			s := strings.TrimSpace(strings.TrimSuffix(ln, "\r"))
			if strings.HasPrefix(s, ";") {
				continue
			}
			if m := reResDecl.FindStringSubmatch(s); m != nil {
				out[strings.ToLower(sec.name)] = strings.TrimSpace(m[1])
				break
			}
		}
	}
	return out
}

// Rec is the feature record for one ini file. Fields the scanner does not
// produce stay at their zero value (never a guessed 0).
type Rec struct {
	SelfBake      bool
	VB0           string
	VB0Target     string
	VB6Null       bool
	Anchors       []string
	Bodies        []string
	RegsPosbuf    bool
	RegsSkpos     bool
	ResNames      map[string]string
	HasLoader     bool
	HasMultiplier bool
	PosbufPaths   []string
	PosbufFile    string
	CVCVals       []int
	// Namespace is the file's own "namespace =" preamble value, if any. A file
	// that declares one has every section renamed to "<prefix>\<namespaced>\
	// <name>", so a reference written from the ini's path no longer resolves.
	Namespace string

	// Filled by scanChar.
	RelDisk string
	RelUnex string
	Enabled bool
	Kind    string
}

// runTargets lists the CommandList section names invoked by a section body.
func runTargets(body string) []string {
	var out []string
	seen := map[string]bool{}
	for _, ln := range codeLines(body) {
		m := reRunTarget.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		name := m[1]
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

func resolveTarget(vb0 string, resNames map[string]string) string {
	v := strings.TrimSpace(vb0)
	if v == "" {
		return ""
	}
	if strings.Contains(v, "ShapeKeyedPosition") {
		return "ShapeKeyedPosition"
	}
	if strings.Contains(v, "PositionBuffer") {
		return "PositionBuffer"
	}
	lv := strings.ToLower(v)
	for sec, bare := range resNames {
		if strings.ToLower(bare) == lv {
			lo := strings.ToLower(sec)
			if strings.Contains(lo, "shapekeyedposition") {
				return "ShapeKeyedPosition"
			}
			if strings.Contains(lo, "positionbuffer") {
				return "PositionBuffer"
			}
		}
	}
	switch lv {
	case "resourcepositionbuffer", "positionbuffer":
		return "PositionBuffer"
	case "resourceshapekeyedposition", "shapekeyedposition":
		return "ShapeKeyedPosition"
	}
	return ""
}

func scanIni(path string) (*Rec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	code := strings.Join(codeLines(text), "\n")

	rec := &Rec{
		SelfBake:      reRunSelfBake.MatchString(code),
		VB6Null:       reVB6Null.MatchString(code),
		Anchors:       declaredHashes(text, AnchorSection),
		Bodies:        componentsHashes(text),
		ResNames:      declaredResNames(text),
		HasLoader:     strings.Contains(strings.ToLower(code), "shapekeyloader"),
		HasMultiplier: strings.Contains(strings.ToLower(code), "shapekeymultiplier"),
		Namespace:     iniNamespace(text),
	}
	names := map[string]bool{}
	for _, sec := range sections(text) {
		names[strings.ToLower(sec.name)] = true
	}
	rec.RegsPosbuf = names["resourcepositionbuffer"]
	rec.RegsSkpos = names["resourceshapekeyedposition"]

	if m := reFilename.FindStringSubmatch(sectionOf(text, "ResourcePositionBuffer")); m != nil {
		rec.PosbufFile = strings.TrimSpace(m[1])
	}
	over := sectionOf(text, "CommandListOverrideSharedResources")
	// vb0 is usually assigned directly in the override list. Newer WWMI mods
	// route it through a command list invoked from there, e.g.
	//   run = CommandListResolveShapeKeys
	// whose body sets vb0 inside if/else branches. Follow one level of `run`
	// targets so those conditional assignments are seen too. "ref" assignments
	// are excluded: they only snapshot the pre-override buffer for cleanup.
	for _, ln := range codeLines(over) {
		if m := reVB0.FindStringSubmatch(ln); m != nil {
			rec.VB0 = m[1] // last assignment wins
		}
	}
	for _, target := range runTargets(over) {
		body := sectionOf(text, target)
		for _, ln := range codeLines(body) {
			if m := reVB0.FindStringSubmatch(ln); m != nil && !strings.HasPrefix(strings.ToLower(m[1]), "ref") {
				rec.VB0 = m[1] // last assignment wins
			}
		}
	}
	rec.VB0Target = resolveTarget(rec.VB0, rec.ResNames)

	seenPaths := map[string]bool{}
	for _, m := range rePosbufPath.FindAllString(text, -1) {
		seenPaths[m] = true
	}
	for p := range seenPaths {
		rec.PosbufPaths = append(rec.PosbufPaths, p)
	}
	sort.Strings(rec.PosbufPaths)

	seenVals := map[int]bool{}
	for _, m := range reCVCAssign.FindAllStringSubmatch(code, -1) {
		rhs := strings.TrimSpace(m[1])
		if lit := reVCLiteral.FindStringSubmatch(rhs); lit != nil {
			var v int
			if _, err := fmtSscan(lit[1], &v); err == nil {
				seenVals[v] = true
			}
			continue
		}
		if strings.HasPrefix(rhs, "$") {
			re := regexp.MustCompile(`(?im)^global[ \t]+(?:persist[ \t]+)?` + regexp.QuoteMeta(rhs) + `[ \t]*=[ \t]*(\d+)`)
			if dm := re.FindStringSubmatch(text); dm != nil {
				var v int
				if _, err := fmtSscan(dm[1], &v); err == nil {
					seenVals[v] = true
				}
			}
		}
	}
	for v := range seenVals {
		rec.CVCVals = append(rec.CVCVals, v)
	}
	sort.Ints(rec.CVCVals)
	return rec, nil
}

// Index holds every ini under Mods/ plus the walk order (Python dict order).
type Index struct {
	Recs    map[string]*Rec
	Order   []string
	Skipped int // number of _skipemote folders pruned from the walk
	// SkippedBases holds each pruned folder's path relative to modsRoot with
	// the _skipemote suffix stripped. A configured character whose write-back
	// path no longer resolves but starts with one of these bases was skipped
	// on purpose by the user, not left pointing at a dead DISABLED_ mod.
	SkippedBases []string
}

// BuildIndex indexes every ini under modsRoot. Paths in the returned records
// are relative to modsRoot; that is the basis every 3DMigoto resource name in
// the generated ini relies on, so it must never follow a narrower scan scope.
// BuildIndex indexes every ini under modsRoot. Kept as the whole-tree form so
// the CLI and the golden tests stay unchanged.
func BuildIndex(modsRoot string) (*Index, error) {
	return BuildIndexScope(modsRoot, modsRoot)
}

// BuildIndexScope indexes only the ini files under scanRoot. scanRoot may be a
// subfolder of modsRoot (the user picked one mod folder). The relative path in
// each record is STILL measured against modsRoot, never scanRoot: 3DMigoto
// resolves Resource\Mods\... against the real Mods root, so narrowing the scan
// must never move that basis.
func BuildIndexScope(modsRoot, scanRoot string) (*Index, error) {
	idx := &Index{Recs: map[string]*Rec{}}
	err := filepath.WalkDir(scanRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// Prune a marked folder before descending, so no ini under it is
			// ever indexed. The marker is on the folder, not on any one ini.
			if hasSkipEmoteFolder(d.Name()) {
				idx.Skipped++
				if rel, e := filepath.Rel(modsRoot, p); e == nil {
					idx.SkippedBases = append(idx.SkippedBases, skipEmoteScopeBase(
						strings.ReplaceAll(rel, "\\", "/")))
				}
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".ini") {
			return nil
		}
		rel, err := filepath.Rel(modsRoot, p)
		if err != nil {
			return nil
		}
		rel = strings.ReplaceAll(rel, "\\", "/")
		rec, err := scanIni(p)
		if err != nil {
			return nil
		}
		if _, ok := idx.Recs[rel]; !ok {
			idx.Order = append(idx.Order, rel)
		}
		idx.Recs[rel] = rec
		return nil
	})
	return idx, err
}

// Kind values. Internal only: never serialised into mods.json and never shown
// to a user, so they are English like the rest of the parser. Anything that
// does reach a user goes through T("...", "...").
const (
	KindFinished   = "finished"     // vb0 already targets a baked mesh
	KindRaw        = "raw"          // vb0 targets the raw position buffer
	KindUnknown    = "unrecognized" // vb0 set, target not one we know
	KindRawSuspect = "raw?"         // no vb0, but a posbuf is referenced
	KindNoShapeKey = "no-shapekey"  // no vb0 and no posbuf
)

// classify returns the kind string used by userLayerOf. Only the kind is
// needed for the user-side path; reason/advice are report-only.
func classify(rec *Rec) string {
	if rec.VB0Target == "ShapeKeyedPosition" {
		return KindFinished
	}
	if rec.VB0Target == "PositionBuffer" {
		return KindRaw
	}
	if rec.VB0 != "" {
		return KindUnknown
	}
	if rec.RegsPosbuf {
		return KindRawSuspect
	}
	return KindNoShapeKey
}

// ClaimResult is the anchor-claim result for one character.
type ClaimResult struct {
	Found     map[string]*Rec // unprefixed relative path -> rec
	FoundKeys []string        // sorted keys
	NDeclared int
	NIr       int
	Dropped   []string
}

// ScanChar claims a character's sets by anchor hash, ignoring directory names.
func ScanChar(anchors, globs []string, idx *Index) *ClaimResult {
	want := map[string]bool{}
	for _, a := range anchors {
		want[strings.ToLower(a)] = true
	}
	res := &ClaimResult{Found: map[string]*Rec{}, NIr: len(idx.Recs)}
	for _, rel := range idx.Order {
		rec := idx.Recs[rel]
		hitAnchors := map[string]bool{}
		for _, a := range rec.Anchors {
			if want[strings.ToLower(a)] {
				hitAnchors[strings.ToLower(a)] = true
			}
		}
		if len(hitAnchors) == 0 {
			continue
		}
		res.NDeclared++
		// A space or ']' in the path used to drop the set here. It no longer
		// does: the render layer emits a namespace shim for such resources so
		// 3DMigoto can resolve a short name instead of the space-split path.
		// Exclusion globs still drop a set below.
		un := unexclude(rel, globs)
		if un == rel {
			blocked := false
			for _, part := range strings.Split(rel, "/") {
				if hit(part, globs) {
					blocked = true
					break
				}
			}
			if blocked {
				res.Dropped = append(res.Dropped, rel)
				continue
			}
		}
		// Copy: index records are shared across characters.
		r2 := *rec
		r2.RelDisk = rel
		r2.RelUnex = un
		r2.Enabled = un == rel
		r2.Kind = classify(&r2)
		if _, exists := res.Found[un]; !exists {
			res.Found[un] = &r2
		}
	}
	for k := range res.Found {
		res.FoundKeys = append(res.FoundKeys, k)
	}
	sort.Strings(res.FoundKeys)
	return res
}

func regexpMustCompile(pat string) *regexp.Regexp { return regexp.MustCompile(pat) }

func fmtSscan(s string, v *int) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, os.ErrInvalid
		}
		n = n*10 + int(r-'0')
	}
	*v = n
	return 1, nil
}
