// Package panel holds the character table and the user-layer mod data that
// together drive every generated ini.
//
// The author-layer half (ids, anchor hashes, recipes, layouts) is frozen at
// build time in data/chars.json. The user-layer half (which mods are installed,
// their vertex counts, which deltas were baked) is read from
// <panel>/user/<char>.mods.json at run time -- those files describe the user's
// own disk, so they can never be shipped.
package panel

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"wwemote/internal/modpath"
)

//go:embed data/chars.json
var charsJSON []byte

// Recipe is one (shapekey id, weight) term of an emote. The frozen table stores
// it as a two-element array -- ["key", weight] -- not as an object.
type Recipe [2]float64

// Key is the shapekey id the term drives.
func (r Recipe) Key() int { return int(r[0]) }

// Weight is the blend weight for that id.
func (r Recipe) Weight() float64 { return r[1] }

// Token is one emote: an icon plus the recipe that produces it.
type Token struct {
	Tid    int      `json:"tid"`
	Name   string   `json:"name"`
	Char   string   `json:"char"`
	Src    string   `json:"src"`
	Recipe []Recipe `json:"recipe"`
}

// PosSpec is one body-shape variant of a mod: the resource to write back and
// the base mesh baked for it.
type PosSpec struct {
	Path    string `json:"path"`
	Base    string `json:"base"`
	BaseRef string `json:"base_ref"`
}

// Delta is one baked vertex-space bucket.
type Delta struct {
	VC      int       `json:"vc"`
	Sparse  string    `json:"sparse"`
	Base    string    `json:"base"`
	BaseRef string    `json:"base_ref"`
	Pos     []PosSpec `json:"pos"`
}

// Char is one character: author-layer fields frozen in the binary, user-layer
// fields merged in from <panel>/user/<name>.mods.json.
type Char struct {
	Cid        int    `json:"cid"`
	Name       string `json:"name"`
	Label      string `json:"label"`
	AnchorHash string `json:"anchor_hash"`
	BodyHash   string `json:"body_hash"`
	Pose       bool   `json:"pose"`
	// Frozen marks a character whose support is kept in the source but not
	// shipped. Load drops it, so the generator, the scanner and the manifest
	// never see it -- freeing it again is a one-field edit, not a code change.
	Frozen bool `json:"frozen"`

	NativeScene struct {
		Gate []int `json:"gate"`
		VC   int   `json:"vc"`
	} `json:"native_scene"`
	NativeSparse      string                       `json:"native_sparse"`
	NativeMissingKeys []int                        `json:"native_missing_keys"`
	Layout            map[string]map[string]string `json:"layout"`
	Tokens            []Token                      `json:"tokens"`

	// user layer
	SkPaths       []string            `json:"sk_paths"`
	PvbPaths      []string            `json:"pvb_paths"`
	PoseExclude   []string            `json:"pose_exclude"`
	PoseExcludeVC map[string]int      `json:"pose_exclude_vc"`
	ModDelta      map[string]*Delta   `json:"mod_delta"`
	ModVC         map[string]int      `json:"mod_vc"`
	Active        *string             `json:"active"`
	PosVariants   map[string][]string `json:"pos_variants"`

	// ModDeltaOrder is the on-disk key order of mod_delta. It is kept because
	// the first entry of a shared vertex-count bucket decides the bucket's
	// label and (when only that one has body shapes) its shape table, so a
	// hash-map walk would make the output depend on Go's random iteration.
	ModDeltaOrder []string `json:"-"`
}

// Cap is the capitalised name used as a section suffix ("Hsin").
func (c *Char) Cap() string { return strings.ToUpper(c.Name[:1]) + c.Name[1:] }

// HasUserData reports whether the scanner ever produced a config for this
// character. All user-layer fields are absent until a scan claims a mod for
// it; a fresh install (or a wiped user/ directory) leaves every one empty.
// Rendering such a character would still emit its pose override -- keyed by
// the frozen AnchorHash -- with no write-back target, which hooks the real
// character's draw and runs the shape-key applier for nothing. Those
// characters must be skipped, not rendered with empty paths.
func (c *Char) HasUserData() bool {
	return len(c.SkPaths) > 0 || len(c.PvbPaths) > 0 ||
		len(c.PoseExclude) > 0 || len(c.PoseExcludeVC) > 0 ||
		len(c.ModDelta) > 0 || len(c.ModVC) > 0 ||
		c.Active != nil || len(c.PosVariants) > 0
}

// Table is the assembled character table.
type Table struct {
	K     int     `json:"K"`
	Chars []*Char `json:"chars"`
}

// ConfiguredOnly returns a shallow copy of the table holding only characters
// with scanner-produced data. Author-layer entries that were never configured
// are dropped so the generator neither triggers its pose guard nor emits an
// empty pose override for them. The receiver and its chars are not modified.
func (t *Table) ConfiguredOnly() *Table {
	out := &Table{K: t.K, Chars: make([]*Char, 0, len(t.Chars))}
	for _, c := range t.Chars {
		if c.HasUserData() {
			out.Chars = append(out.Chars, c)
		}
	}
	return out
}

// Load reads the embedded author layer and merges each character's user-layer
// JSON when present.
//
// A missing <char>.mods.json is not an error: it means the scanner has not run
// yet, and every user-layer field degrades to its empty default -- which the
// generator renders as "no mod adapted", exactly like the reference tool.
func Load(userDir string) (*Table, error) {
	var t Table
	if err := json.Unmarshal(charsJSON, &t); err != nil {
		return nil, fmt.Errorf("chars.json: %w", err)
	}
	if t.K < 1 {
		t.K = 1
	}
	live := make([]*Char, 0, len(t.Chars))
	for _, c := range t.Chars {
		// Frozen characters stay in the source tree but are never shipped.
		// Dropping them here keeps the generator, the scanner, the manifest
		// and the asserts all agreeing with what the release actually has.
		if c.Frozen {
			continue
		}
		p := filepath.Join(userDir, c.Name+".mods.json")
		raw, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				live = append(live, c)
				continue
			}
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		if err := json.Unmarshal(raw, c); err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		c.ModDeltaOrder = keyOrder(raw, "mod_delta")
		live = append(live, c)
	}
	t.Chars = live
	return &t, nil
}

// keyOrder returns the keys of a top-level object field in the order they
// appear in the document. encoding/json gives no ordering guarantee for maps,
// and one part of the generator (which entry labels a shared bucket) depends
// on the file's own order -- so the order is read straight from the tokens.
func keyOrder(raw []byte, field string) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil
		}
		key, _ := kt.(string)
		if key != field {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil
			}
			continue
		}
		if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
			return nil
		}
		var out []string
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return out
			}
			k, _ := kt.(string)
			out = append(out, k)
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return out
			}
		}
		return out
	}
	return nil
}

// LibIDsOf returns the shapekey ids this character's own emotes use.
//
// Per-character on purpose: a global set would make every character's file
// change whenever any other character gains an emote.
func (t *Table) LibIDsOf(cap string) []int {
	seen := map[int]bool{}
	for _, c := range t.Chars {
		if c.Cap() != cap {
			continue
		}
		for _, tk := range c.Tokens {
			for _, r := range tk.Recipe {
				seen[r.Key()] = true
			}
		}
	}
	out := make([]int, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// Tokens returns every token across the table, in declaration order.
func (t *Table) Tokens() []Token {
	var out []Token
	for _, c := range t.Chars {
		out = append(out, c.Tokens...)
	}
	return out
}

// Target is one resolved delta bucket.
type Target struct {
	Frag   string
	VC     int
	Sparse string
	Base   string
	Paths  []string
	Pos    []PosSpec
}

// DeltaSkip is one bucket the renderer leaves out. ModFile names the ini that
// owned the bucket, so a report can say which mod did not get expressions
// instead of listing every mod the character claimed.
type DeltaSkip struct {
	Frag    string
	ModFile string
	Reason  string
}

// DeltaTargets is the strict form used by offline checks and kept compatible
// with older callers: the first bad bucket is an error. The live renderer uses
// DeltaTargetsLenient so one bad mod cannot cost every other mod its config.
func (c *Char) DeltaTargets(baseRoot string) ([]Target, error) {
	out, skips, err := c.deltaTargets(baseRoot, "", false)
	if err != nil {
		return nil, err
	}
	if len(skips) > 0 {
		return nil, fmt.Errorf("%s", skips[0].Reason)
	}
	return out, nil
}

// DeltaTargetsLenient resolves the usable buckets and returns the unusable
// ones separately. baseRoot is the user/ directory; modsRoot lets prefix
// computation follow the real panel depth instead of assuming two levels.
func (c *Char) DeltaTargetsLenient(baseRoot, modsRoot string) ([]Target, []DeltaSkip, error) {
	return c.deltaTargets(baseRoot, modsRoot, true)
}

// deltaTargets resolves mod_delta into the buckets the generator emits,
// sorted by vertex count. The runtime selects a bucket purely by vertex count,
// so two mods with the same count necessarily share one bucket; the write-back
// paths are unioned so switching mods needs no re-run.
func (c *Char) deltaTargets(baseRoot, modsRoot string, lenient bool) ([]Target, []DeltaSkip, error) {
	if len(c.ModDelta) == 0 {
		return nil, nil, nil
	}
	knownVC := map[int]bool{}
	for _, v := range c.ModVC {
		knownVC[v] = true
	}
	prefix := baseRefPrefix(baseRoot, modsRoot)

	frags := c.ModDeltaOrder
	if len(frags) == 0 {
		frags = make([]string, 0, len(c.ModDelta))
		for f := range c.ModDelta {
			frags = append(frags, f)
		}
		sort.Strings(frags)
	}

	var out []Target
	var skips []DeltaSkip
	slot := map[int]int{}
	for _, frag := range frags {
		spec := c.ModDelta[frag]
		// Every rejection below is about one bucket. report() either records
		// that bucket and keeps going (render) or returns the same text as an
		// error (strict callers and diagnostics).
		report := func(reason string) bool {
			if !lenient {
				return true
			}
			skips = append(skips, DeltaSkip{
				Frag: frag, ModFile: modFilesOf(c, frag), Reason: reason,
			})
			return false
		}
		fail := func(reason string) bool {
			if report(reason) {
				return true
			}
			return false
		}
		if spec == nil || spec.VC <= 0 {
			if fail(fmt.Sprintf("%s: mod_delta[%q].vc must be positive", c.Label, frag)) {
				return nil, nil, fmt.Errorf("%s: mod_delta[%q].vc must be positive", c.Label, frag)
			}
			continue
		}
		var paths []string
		for _, p := range c.PvbPaths {
			if FragHit(frag, p) {
				paths = append(paths, p)
			}
		}
		if len(paths) != 1 {
			// Name the actual hits and the full list. A report reader must be
			// able to see which mods the fragment collided with and whether a
			// narrowed scan caused it, without reproducing the user's install.
			shown := paths
			if len(shown) > 8 {
				shown = shown[:8]
			}
			reason := fmt.Sprintf(
				"%s: mod_delta fragment %q matches %d of %d pvb_paths (must be exactly 1); "+
					"hits=%q; all=%q; 0 means the config is stale -- re-run the scan",
				c.Label, frag, len(paths), len(c.PvbPaths), shown, c.PvbPaths)
			if fail(reason) {
				return nil, nil, fmt.Errorf("%s", reason)
			}
			continue
		}
		if len(knownVC) > 0 && !knownVC[spec.VC] {
			reason := fmt.Sprintf(
				"%s: mod_delta[%q].vc = %d is not among mod_vc, so no mod would "+
					"ever select that bucket", c.Label, frag, spec.VC)
			if fail(reason) {
				return nil, nil, fmt.Errorf("%s", reason)
			}
			continue
		}
		if spec.Sparse == "" {
			reason := fmt.Sprintf("%s: mod_delta[%q] has no sparse field", c.Label, frag)
			if fail(reason) {
				return nil, nil, fmt.Errorf("%s", reason)
			}
			continue
		}
		base := baseMeshNameAt(spec.Base, spec.BaseRef, prefix)
		if base == "" {
			base = BaseNameOf(spec.VC)
		}
		if err := checkBaseMesh(baseRoot, base, spec.VC); err != nil {
			reason := fmt.Sprintf("%s: %v", c.Label, err)
			if fail(reason) {
				return nil, nil, fmt.Errorf("%s: %w", c.Label, err)
			}
			continue
		}
		if err := checkDelta(baseRoot, spec.Sparse, spec.VC); err != nil {
			reason := fmt.Sprintf("%s: %v", c.Label, err)
			if fail(reason) {
				return nil, nil, fmt.Errorf("%s: %w", c.Label, err)
			}
			continue
		}
		pos, err := checkPos(baseRoot, prefix, spec.Pos, spec.VC)
		if err != nil {
			reason := fmt.Sprintf("%s: %v", c.Label, err)
			if fail(reason) {
				return nil, nil, fmt.Errorf("%s: %w", c.Label, err)
			}
			continue
		}

		if i, ok := slot[spec.VC]; ok {
			if out[i].Sparse != spec.Sparse || out[i].Base != base {
				reason := fmt.Sprintf(
					"%s: %q and %q share vc = %d but name different delta files; "+
						"the runtime picks by vertex count, so one would misalign",
					c.Label, out[i].Frag, frag, spec.VC)
				if fail(reason) {
					return nil, nil, fmt.Errorf("%s", reason)
				}
				continue
			}
			for _, p := range paths {
				if !contains(out[i].Paths, p) {
					out[i].Paths = append(out[i].Paths, p)
				}
			}
			if len(pos) > len(out[i].Pos) {
				out[i].Pos = pos
			}
			continue
		}
		slot[spec.VC] = len(out)
		out = append(out, Target{
			Frag: frag, VC: spec.VC, Sparse: spec.Sparse, Base: base,
			Paths: paths, Pos: pos,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].VC < out[j].VC })
	return out, skips, nil
}

// modFilesOf names the ini files a fragment claims. mod_vc is the authority
// because its keys are the mod-relative paths the scanner recorded; pvb_paths
// are the fallback for older configs without that map.
func modFilesOf(c *Char, frag string) string {
	seen := map[string]bool{}
	for k := range c.ModVC {
		if FragHit(frag, k) {
			seen[filepath.ToSlash(k)] = true
		}
	}
	if len(seen) == 0 {
		for _, p := range c.PvbPaths {
			if FragHit(frag, p) {
				if rel := modFileFromPvb(p); rel != "" {
					seen[rel] = true
				}
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, "; ")
}

func modFileFromPvb(p string) string {
	s := strings.ReplaceAll(p, "\\", "/")
	const head = "Resource/Mods/"
	if len(s) <= len(head) || !strings.EqualFold(s[:len(head)], head) {
		return ""
	}
	s = s[len(head):]
	i := strings.LastIndex(s, "/")
	if i <= 0 {
		return ""
	}
	return s[:i]
}

// BaseNameOf is the single implementation of the base-mesh filename.
func BaseNameOf(vc int) string { return fmt.Sprintf("ep_mod_base_%d.buf", vc) }

// baseRefUp is the legacy prefix: the ini used to live exactly two levels
// below the mods root (<mods>\<panel>\user). A versioned wrapper changes that
// depth, so baseRefPrefix derives it from the real roots whenever available.
const baseRefUp = `..\..\`

// baseRefPrefix returns the ini-relative walk from baseRoot up to modsRoot.
// An empty or unusable root keeps the legacy two-level prefix so offline
// checks and golden files stay byte-identical.
func baseRefPrefix(baseRoot, modsRoot string) string {
	if modsRoot == "" || baseRoot == "" {
		return baseRefUp
	}
	rel, err := filepath.Rel(baseRoot, modsRoot)
	if err != nil {
		return baseRefUp
	}
	rel = filepath.ToSlash(rel)
	n := 0
	for rel == ".." || strings.HasPrefix(rel, "../") {
		n++
		if rel == ".." {
			rel = ""
			break
		}
		rel = rel[3:]
	}
	if n == 0 || rel != "" {
		return baseRefUp
	}
	return strings.Repeat(`..\`, n)
}

// baseMeshName is the legacy two-level form kept for direct callers and tests.
func baseMeshName(name, ref string) string {
	return baseMeshNameAt(name, ref, baseRefUp)
}

// baseMeshNameAt turns a manifest entry into the text the ini puts after
// "filename=": a reference into the mod tree walked back from the ini's own
// directory, or a file the panel owns.
func baseMeshNameAt(name, ref, prefix string) string {
	if ref != "" {
		return prefix + ref
	}
	return name
}

func checkBaseMesh(root, name string, vc int) error {
	// Config paths are shipped without DISABLED_, but a disabled mod still
	// lives under a prefixed folder on disk. Map the logical path back before
	// stat-ing; a genuinely renamed/removed mod never resolves and still fails.
	realPath, ok := modpath.ResolveOnDisk(filepath.Join(root, name))
	p := filepath.Join(root, name)
	if !ok {
		return fmt.Errorf("missing base mesh %s -- 3DMigoto silently skips a "+
			"missing filename, so the capture is empty and every vertex "+
			"collapses. Re-run the scan", p)
	}
	st, err := os.Stat(realPath)
	if err != nil {
		return fmt.Errorf("missing base mesh %s -- 3DMigoto silently skips a "+
			"missing filename, so the capture is empty and every vertex "+
			"collapses. Re-run the scan", p)
	}
	if want := int64(vc) * 12; st.Size() != want {
		return fmt.Errorf("base mesh %s is %d B (%d vertices) but vc=%d needs %d B; "+
			"the base must share the delta's vertex space. Re-run the scan",
			name, st.Size(), st.Size()/12, vc, want)
	}
	return nil
}

func checkDelta(root, sparse string, vc int) error {
	p := filepath.Join(root, sparse)
	st, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("delta file %s does not exist -- 3DMigoto silently "+
			"skips a missing filename, so every delta value becomes 0", p)
	}
	mfName := sparse
	if strings.HasSuffix(mfName, ".buf") {
		mfName = mfName[:len(mfName)-4] + "_manifest.json"
	} else {
		mfName += ".manifest.json"
	}
	raw, err := os.ReadFile(filepath.Join(root, mfName))
	if err != nil {
		return fmt.Errorf("delta manifest %s is missing -- re-run the bake", mfName)
	}
	var mf struct {
		Format     string `json:"format"`
		VC         int    `json:"vc"`
		TotalBytes int64  `json:"total_bytes"`
	}
	if err := json.Unmarshal(raw, &mf); err != nil {
		return fmt.Errorf("parse %s: %w", mfName, err)
	}
	if mf.Format != "sparse-csr-v1" {
		return fmt.Errorf("%s format=%q != sparse-csr-v1: baker and generator "+
			"versions do not match", mfName, mf.Format)
	}
	if mf.VC != vc {
		return fmt.Errorf("%s vc=%d != declared vc=%d: adding in the wrong "+
			"vertex space tears the mesh", mfName, mf.VC, vc)
	}
	if st.Size() != mf.TotalBytes {
		return fmt.Errorf("delta %s is %d bytes but the manifest says %d -- "+
			"re-bake", sparse, st.Size(), mf.TotalBytes)
	}
	return nil
}

func checkPos(root, prefix string, pos []PosSpec, vc int) ([]PosSpec, error) {
	var out []PosSpec
	for i, it := range pos {
		it.Base = baseMeshNameAt(it.Base, it.BaseRef, prefix)
		if it.Path == "" || it.Base == "" {
			return nil, fmt.Errorf("mod_delta pos[%d] lacks path or base", i)
		}
		if err := checkBaseMesh(root, it.Base, vc); err != nil {
			return nil, fmt.Errorf("pos[%d]: %w", i, err)
		}
		out = append(out, it)
	}
	return out, nil
}

// PosSlots expands a bucket's body shapes into (suffix, base, writeback).
//
// Slot 0 always exists with an empty suffix and a nil write-back, which means
// "use the bucket's union of paths". Single-shape mods therefore produce
// byte-identical output to a build that never had this concept.
func PosSlots(pos []PosSpec, base string) []PosSlot {
	if len(pos) == 0 {
		return []PosSlot{{"", base, ""}}
	}
	out := make([]PosSlot, 0, len(pos))
	for i, p := range pos {
		if i == 0 {
			out = append(out, PosSlot{"", p.Base, ""})
			continue
		}
		out = append(out, PosSlot{fmt.Sprintf("_%d", i), p.Base, p.Path})
	}
	return out
}

// PosSlot is one body shape's resources.
type PosSlot struct {
	Suffix    string
	Base      string
	Writeback string
}

// FragHit reports whether a mod_delta fragment names a contiguous run of
// segments in the ini path.
//
// Segment equality (not substring) is load-bearing: "HsinMod" must not match
// "tsqn-HsinMod", and a two-segment fragment is how two mods that both contain
// a directory called "HsinMod" are told apart.
func FragHit(frag, path string) bool {
	want := pathSegs(frag)
	if len(want) == 0 {
		return false
	}
	segs := pathSegs(path)
	if len(want) == 1 {
		for _, s := range segs {
			if s == want[0] {
				return true
			}
		}
		return false
	}
	for i := 0; i+len(want) <= len(segs); i++ {
		ok := true
		for j := range want {
			if segs[i+j] != want[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func pathSegs(s string) []string {
	s = strings.ReplaceAll(s, "\\", "/")
	var out []string
	for _, p := range strings.Split(s, "/") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
