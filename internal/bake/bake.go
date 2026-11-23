// Package bake produces per-vertex shapekey deltas from a WWMI mod's own
// Meshes/ShapeKey*.buf triple. Output is "sparse-csr-v1":
//
//	[row CSR (vc+1) x u32][N x (key, dx, dy, dz) f32]
//
// sorted by (vertex, key). Three details keep it byte-identical to the Python
// reference: float32 accumulation at every step, a 0.0+v first term so -0.0
// becomes +0.0, and np.lexsort((key, vertex)) ordering.
package bake

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	KeysPerBatch = 127
	KeysTotal    = 162

	// Tolerance for trailing vertices the exporter adds past the declared
	// vertex count. Anything beyond this is not a tail but two generations of
	// data, and must be refused.
	MaxTailVerts = 64
)

// Manifest mirrors the JSON the reference implementation writes next to each
// delta buffer.
type Manifest struct {
	Format        string
	VC            int
	RowstartBytes int
	EntryStride   int
	TotalEntries  int
	EntriesBytes  int
	TotalBytes    int
	BaseBuf       string
	BaseRef       string            // mods-relative mesh path, when referenced in place
	ExtraRefs     map[string]string // variant base name -> mods-relative path
	BaseBytes     int64
	KeySemantics  string
	ModDir        string
	SourceFiles   [3][2]string // {logical name, absolute path}
	UnitWeight    bool
	BakedBy       string
	BakeVersion   string // algorithm tag; a mismatch forces a re-bake
	InputSig      string // hash of the inputs; a match allows a reuse
	Keys          [KeysTotal]KeyStat
}

// Metadata written into every manifest. It ships to the user's Mods folder,
// so it uses only stable machine-readable tags.
const (
	BakedByTag = "WWEmote"
	// BakeVersion identifies the delta encoding and the baking algorithm. Bump
	// it whenever the bytes bake.Bake writes could change, so manifests from an
	// older algorithm are recognised as stale and never reused as a cache hit.
	BakeVersion        = "sparse-csr-v2-incremental"
	keySemanticsSparse = "0..126=batch0 row k (mod user space); 127..161=batch1 row k-127"
	keySemanticsNative = "0..126=batch0 row k + batch1 row k (native aligned); 127..161=batch1 row k-127"
)

// KeyStat is one row of the per-key summary in the manifest.
type KeyStat struct {
	Entries      int
	NonzeroVerts int
	AbsMaxPos    float64
}

type tables struct {
	meshDir string
	vid     []uint32  // ShapeKeyVertexId.buf
	off     []float32 // ShapeKeyVertexOffset.buf, already decoded from f16
	b0      []uint32  // batch 0 boundary table, 128 entries
	b1      []uint32  // batch 1 boundary table, 128 entries
	n0      uint32    // b0[127]
	n1      uint32    // b1[127]
	base1   uint32    // first absolute index of batch 1
}

// Options carry the optional knobs the Python CLI exposes.
type Options struct {
	OutDir          string
	Name            string // output base name, without the _sparse.buf suffix
	Batch1EntryBase int    // -1 = read from the ini
	AlignNative     bool
	IniName         string // default "1.ini"
	ExtraPos        [][2]string
	// ModsRoot is the XXMI mods directory. When set, a base mesh is referenced
	// in place instead of written into OutDir (see placeBase).
	ModsRoot string
	// InputSig is the caller's hash of everything that determines the delta
	// bytes. It is stored in the manifest so a later run can reuse this bake
	// only when the inputs are unchanged. Empty is allowed for standalone bakes.
	InputSig string
	// Warn receives non-fatal notices (table vs ini disagreement, tail
	// truncation). Nil discards them.
	Warn func(string)
}

func f32(x float64) float32 { return float32(x) }

// halfToFloat32 decodes IEEE-754 binary16. Every half value is exactly
// representable in float32, so this is lossless.
func halfToFloat32(h uint16) float32 {
	sign := h >> 15
	exp := (h >> 10) & 0x1f
	mant := h & 0x3ff
	var f float64
	switch {
	case exp == 0:
		if mant == 0 {
			f = 0
		} else {
			f = math.Ldexp(float64(mant), -24)
		}
	case exp == 31:
		if mant == 0 {
			f = math.Inf(1)
		} else {
			f = math.NaN()
		}
	default:
		f = math.Ldexp(float64(1024+mant), int(exp)-25)
	}
	if sign == 1 {
		f = -f
	}
	return float32(f)
}

func readU32Array(path string) ([]uint32, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data)%4 != 0 {
		return nil, fmt.Errorf("%s: size %d is not a multiple of 4", path, len(data))
	}
	out := make([]uint32, len(data)/4)
	for i := range out {
		out[i] = binary.LittleEndian.Uint32(data[i*4:])
	}
	return out, nil
}

func readF16Array(path string) ([]float32, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data)%2 != 0 {
		return nil, fmt.Errorf("%s: size %d is not a multiple of 2", path, len(data))
	}
	out := make([]float32, len(data)/2)
	for i := range out {
		out[i] = halfToFloat32(binary.LittleEndian.Uint16(data[i*2:]))
	}
	return out, nil
}

func meshDirOf(modDir string) string {
	if strings.EqualFold(filepath.Base(filepath.Clean(modDir)), "meshes") {
		return modDir
	}
	return filepath.Join(modDir, "Meshes")
}

func batch1Base(modDir, iniName string, explicit int) (uint32, error) {
	if explicit >= 0 {
		return uint32(explicit), nil
	}
	p := filepath.Join(modDir, iniName)
	data, err := os.ReadFile(p)
	if err != nil {
		return 0, fmt.Errorf("cannot read %s (pass --batch1-entry-base): %w", p, err)
	}
	for _, ln := range strings.Split(string(data), "\n") {
		s := strings.TrimSpace(ln)
		if strings.HasPrefix(s, "global $shapekey_vertex_offset_batch1") {
			_, rhs, ok := strings.Cut(s, "=")
			if !ok {
				continue
			}
			v, err := strconv.ParseUint(strings.TrimSpace(rhs), 10, 32)
			if err != nil {
				return 0, fmt.Errorf("%s: bad $shapekey_vertex_offset_batch1: %w", p, err)
			}
			return uint32(v), nil
		}
	}
	return 0, fmt.Errorf("%s has no $shapekey_vertex_offset_batch1", p)
}

func loadTables(modDir string, vc int, opts Options, warn func(string)) (*tables, error) {
	mdir := meshDirOf(modDir)
	base1, err := batch1Base(modDir, opts.IniName, opts.Batch1EntryBase)
	if err != nil {
		return nil, err
	}

	flat, err := readU32Array(filepath.Join(mdir, "ShapeKeyOffset.buf"))
	if err != nil {
		return nil, err
	}
	if len(flat) != 256 {
		return nil, fmt.Errorf("%s: expected 256 uint32, got %d",
			filepath.Join(mdir, "ShapeKeyOffset.buf"), len(flat))
	}
	vid, err := readU32Array(filepath.Join(mdir, "ShapeKeyVertexId.buf"))
	if err != nil {
		return nil, err
	}
	off, err := readF16Array(filepath.Join(mdir, "ShapeKeyVertexOffset.buf"))
	if err != nil {
		return nil, err
	}

	b0 := append([]uint32(nil), flat[0:128]...)
	b1 := append([]uint32(nil), flat[128:256]...)
	n0, n1 := b0[127], b1[127]

	for i := 0; i < 127; i++ {
		if b0[i] > b0[i+1] {
			return nil, fmt.Errorf("batch0 boundary table is not monotonic at %d", i)
		}
		if b1[i] > b1[i+1] {
			return nil, fmt.Errorf("batch1 boundary table is not monotonic at %d", i)
		}
	}

	// The table is the fact; the ini is a description that can go stale.
	if n0 != base1 {
		if n0+n1 != uint32(len(vid)) {
			return nil, fmt.Errorf(
				"batch0 entries %d + batch1 %d = %d != id table length %d: "+
					"the table is not self-consistent, refusing to bake",
				n0, n1, n0+n1, len(vid))
		}
		if warn != nil {
			warn(fmt.Sprintf(
				"%s: ini batch1 base %d disagrees with table batch0 total %d "+
					"(%+d); table is self-consistent (%d+%d == %d) so the table wins",
				filepath.Base(filepath.Clean(modDir)), base1, n0,
				int64(n0)-int64(base1), n0, n1, len(vid)))
		}
		base1 = n0
	}
	if base1+n1 != uint32(len(vid)) {
		return nil, fmt.Errorf("batch1 base %d + %d != id table length %d: layout misunderstood",
			base1, n1, len(vid))
	}

	var tail int
	var tailMax uint32
	for _, v := range vid {
		if v >= uint32(vc) {
			tail++
			if v > tailMax {
				tailMax = v
			}
		}
	}
	if tail > 0 {
		over := int(tailMax) - vc + 1
		if over > MaxTailVerts {
			return nil, fmt.Errorf(
				"%d vertex ids >= vc(%d), max %d (%d past the end): "+
					"not an export tail but two generations of data, refusing to bake",
				tail, vc, tailMax, over)
		}
		if warn != nil {
			warn(fmt.Sprintf(
				"%s: %d entries reference vertex ids >= vc(%d), max %d (%d extra "+
					"at the tail); truncating like the base mesh, these entries are dropped",
				filepath.Base(filepath.Clean(modDir)), tail, vc, tailMax, over))
		}
	}
	if len(off) != 6*len(vid) {
		return nil, errors.New("offset table does not match id table entry count")
	}

	return &tables{meshDir: mdir, vid: vid, off: off, b0: b0, b1: b1,
		n0: n0, n1: n1, base1: base1}, nil
}

func (t *tables) keyEntries(k int, alignNative bool) ([]uint32, error) {
	var e []uint32
	switch {
	case k >= 0 && k <= 126:
		lo, hi := t.b0[k], t.b0[k+1]
		for x := lo; x < hi; x++ {
			e = append(e, x)
		}
		if alignNative {
			lo1 := t.b1[k]
			hi1 := t.n1
			if k < 126 {
				hi1 = t.b1[k+1]
			}
			for x := lo1; x < hi1; x++ {
				e = append(e, x+t.base1)
			}
		}
	case k >= 127 && k <= 253:
		kk := k - KeysPerBatch
		lo := t.b1[kk]
		hi := t.n1
		if kk < 126 {
			hi = t.b1[kk+1]
		}
		for x := lo; x < hi; x++ {
			e = append(e, x+t.base1)
		}
	default:
		return nil, fmt.Errorf("key %d out of the mod's supported range (0..253)", k)
	}
	lim := t.n0 + t.n1
	out := e[:0]
	for _, x := range e {
		if x < lim {
			out = append(out, x)
		}
	}
	return out, nil
}

type vertexAcc struct {
	d [3]float32
}

// Bake produces <out>/<name>_sparse.buf plus a manifest, and refreshes the
// base mesh copies that live beside it.
func Bake(modDir string, vc int, opts Options) (*Manifest, error) {
	if opts.IniName == "" {
		opts.IniName = "1.ini"
	}
	if opts.Batch1EntryBase == 0 {
		opts.Batch1EntryBase = -1
	}
	warn := opts.Warn
	if warn == nil {
		warn = func(string) {}
	}

	tab, err := loadTables(modDir, vc, opts, warn)
	if err != nil {
		return nil, err
	}

	var vidsAll []uint32
	var keysAll []uint32
	var dAll [][3]float32
	var perKey [KeysTotal]KeyStat

	for k := 0; k < KeysTotal; k++ {
		e, err := tab.keyEntries(k, opts.AlignNative)
		if err != nil {
			return nil, err
		}
		if len(e) == 0 {
			continue
		}
		acc := make(map[uint32]*vertexAcc, len(e))
		for _, ei := range e {
			v := tab.vid[ei]
			j := int(ei) * 6
			a := acc[v]
			if a == nil {
				// The leading 0.0 is deliberate: it turns a -0.0 first term
				// into +0.0, which is numerically equal but differs in bytes.
				acc[v] = &vertexAcc{d: [3]float32{
					f32(0.0 + float64(tab.off[j])),
					f32(0.0 + float64(tab.off[j+1])),
					f32(0.0 + float64(tab.off[j+2])),
				}}
			} else {
				a.d[0] = f32(float64(a.d[0]) + float64(tab.off[j]))
				a.d[1] = f32(float64(a.d[1]) + float64(tab.off[j+1]))
				a.d[2] = f32(float64(a.d[2]) + float64(tab.off[j+2]))
			}
		}

		type pair struct {
			v uint32
			d [3]float32
		}
		var nz []pair
		for v, a := range acc {
			if (a.d[0] != 0 || a.d[1] != 0 || a.d[2] != 0) && int(v) < vc {
				nz = append(nz, pair{v, a.d})
			}
		}
		if len(nz) == 0 {
			continue
		}
		sort.Slice(nz, func(i, j int) bool { return nz[i].v < nz[j].v })
		var absMax float64
		for _, p := range nz {
			vidsAll = append(vidsAll, p.v)
			keysAll = append(keysAll, uint32(k))
			dAll = append(dAll, p.d)
			for _, c := range p.d {
				if m := math.Abs(float64(c)); m > absMax {
					absMax = m
				}
			}
		}
		perKey[k] = KeyStat{Entries: len(nz), NonzeroVerts: len(nz), AbsMaxPos: absMax}
	}

	if len(vidsAll) == 0 {
		return nil, errors.New("all 162 keys are zero: wrong mod directory or key semantics, refusing to output")
	}

	order := make([]int, len(vidsAll))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if vidsAll[a] != vidsAll[b] {
			return vidsAll[a] < vidsAll[b]
		}
		return keysAll[a] < keysAll[b]
	})

	N := len(order)
	counts := make([]uint32, vc)
	for _, v := range vidsAll {
		counts[v]++
	}
	rowstart := make([]uint32, vc+1)
	var acc uint32
	for i := 0; i < vc; i++ {
		rowstart[i] = acc
		acc += counts[i]
	}
	rowstart[vc] = acc
	if int(acc) != N {
		return nil, fmt.Errorf("CSR row end %d != entry count %d", acc, N)
	}

	base := opts.Name
	bufPath := base + "_sparse.buf"
	if opts.OutDir != "" {
		if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
			return nil, err
		}
		bufPath = filepath.Join(opts.OutDir, filepath.Base(opts.Name)) + "_sparse.buf"
	}

	rowBytes := make([]byte, (vc+1)*4)
	for i, v := range rowstart {
		binary.LittleEndian.PutUint32(rowBytes[i*4:], v)
	}
	entBytes := make([]byte, N*16)
	for n, i := range order {
		o := n * 16
		binary.LittleEndian.PutUint32(entBytes[o:], math.Float32bits(float32(keysAll[i])))
		binary.LittleEndian.PutUint32(entBytes[o+4:], math.Float32bits(dAll[i][0]))
		binary.LittleEndian.PutUint32(entBytes[o+8:], math.Float32bits(dAll[i][1]))
		binary.LittleEndian.PutUint32(entBytes[o+12:], math.Float32bits(dAll[i][2]))
	}

	if err := os.MkdirAll(filepath.Dir(bufPath), 0o755); err != nil && filepath.Dir(bufPath) != "." {
		return nil, err
	}
	// Never write the delta in place: the game reads this file directly, and a
	// truncated one makes it crash rather than fall back. Temp file first, so
	// the path either holds the previous bake or the complete new one.
	bufTmp := bufPath + ".tmp"
	if err := os.WriteFile(bufTmp, append(rowBytes, entBytes...), 0o644); err != nil {
		return nil, err
	}
	totalBytes := (vc+1)*4 + N*16
	if st, err := os.Stat(bufTmp); err != nil || int(st.Size()) != totalBytes {
		os.Remove(bufTmp)
		return nil, fmt.Errorf("byte-count interlock failed: wrote %v, expected %d", st, totalBytes)
	}
	if err := os.Rename(bufTmp, bufPath); err != nil {
		os.Remove(bufTmp)
		return nil, err
	}

	man := &Manifest{
		Format:        "sparse-csr-v1",
		VC:            vc,
		RowstartBytes: (vc + 1) * 4,
		EntryStride:   4,
		TotalEntries:  N,
		EntriesBytes:  N * 16,
		TotalBytes:    totalBytes,
		KeySemantics:  keySemanticsSparse,
		ModDir:        absOrSelf(modDir),
		UnitWeight:    true,
		BakedBy:       BakedByTag,
		BakeVersion:   BakeVersion,
		InputSig:      opts.InputSig,
		Keys:          perKey,
	}
	if opts.AlignNative {
		man.KeySemantics = keySemanticsNative
	}
	for i, n := range []string{"ShapeKeyOffset.buf", "ShapeKeyVertexId.buf", "ShapeKeyVertexOffset.buf"} {
		man.SourceFiles[i] = [2]string{n, filepath.Join(tab.meshDir, n)}
	}

	// Base mesh: must come from the same mod as the delta, because both define
	// the vertex space the shader indexes.
	if opts.OutDir != "" {
		switch strings.ToLower(filepath.Base(filepath.Clean(opts.OutDir))) {
		case "native_delta", "user":
			src := filepath.Join(tab.meshDir, "Position.buf")
			wantB := int64(vc) * 12
			name, ref, err := placeBase(src, opts.OutDir, fmt.Sprintf("ep_mod_base_%d.buf", vc), wantB, opts.ModsRoot)
			if err != nil {
				return nil, err
			}
			man.BaseBuf = name
			man.BaseRef = ref
			man.BaseBytes = wantB

			for _, ep := range opts.ExtraPos {
				bsrc := filepath.Join(tab.meshDir, filepath.FromSlash(strings.ReplaceAll(ep[0], "\\", "/")))
				_, bref, err := placeBase(bsrc, opts.OutDir, ep[1], wantB, opts.ModsRoot)
				if err != nil {
					return nil, err
				}
				if bref != "" {
					if man.ExtraRefs == nil {
						man.ExtraRefs = map[string]string{}
					}
					man.ExtraRefs[ep[1]] = bref
				}
			}
		}
	}

	mfPath := bufPath[:len(bufPath)-len("_sparse.buf")] + "_sparse_manifest.json"
	if err := writeAtomic(mfPath, man.JSON()); err != nil {
		return nil, err
	}
	return man, nil
}

// writeAtomic replaces path through path+".tmp". Every artifact this package
// emits is read by the game, so a half-written file is worse than a stale one.
func writeAtomic(path string, data []byte) error {
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

func absOrSelf(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}

func fileSize(p string) (int64, error) {
	st, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// errLinkUnavailable means the filesystem refused a hard link. Every caller
// treats it as "fall back to a copy", never as a failure: a mod tree on
// another volume, or on a filesystem without links, is a supported layout.
var errLinkUnavailable = errors.New("hard link unavailable")

// placeBase decides how the generated ini reaches the mash the delta indexes:
// a reference into the mod tree, or a file of our own.
//
// An exact-length source is referenced in place: no second copy, mod updates
// need no re-bake, and the manifest carries no unverifiable size. The ref is
// relative to the mods root so the manifest is location-independent; panel.Load
// turns it into the "..\..\" form resolved from <mods>\<panel>\user. Longer
// sources get a truncated copy, and so do sources outside the mods tree.
func placeBase(src, outDir, name string, wantB int64, modsRoot string) (bufName, ref string, err error) {
	got, err := fileSize(src)
	if err != nil {
		return "", "", fmt.Errorf("%s: the capture source is missing, every vertex would collapse: %w", src, err)
	}
	if got < wantB {
		return "", "", fmt.Errorf("%s is %d B (%d verts) but needs %d B: vertex space too small",
			src, got, got/12, wantB)
	}
	if got == wantB && modsRoot != "" {
		if rel, ok := modRef(modsRoot, src); ok {
			dropStaleFile(filepath.Join(outDir, name))
			return "", rel, nil
		}
	}
	if err := placeMesh(src, filepath.Join(outDir, name), wantB); err != nil {
		return "", "", err
	}
	return name, "", nil
}

// modRef renders src as a path relative to the mods root. ok is false when src
// is not below it (another volume, or a mesh kept outside the mod tree), where
// no reference exists and a copy is the only option.
func modRef(modsRoot, src string) (string, bool) {
	absMods, err := filepath.Abs(modsRoot)
	if err != nil {
		return "", false
	}
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(absMods, absSrc)
	if err != nil || rel == "" || rel == "." || filepath.IsAbs(rel) {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return strings.ReplaceAll(rel, "/", `\`), true
}

// dropStaleFile removes the copy an earlier build left behind now that the ini
// points at the mod tree instead. A game holding the file open turns a delete
// into a sharing violation, so it is renamed aside first and every error is
// ignored: a leftover costs disk space, a half-done cleanup must not fail the
// bake.
func dropStaleFile(p string) {
	if _, err := os.Stat(p); err != nil {
		return
	}
	old := p + ".old"
	_ = os.Remove(old)
	if err := os.Rename(p, old); err != nil {
		return
	}
	_ = os.Remove(old)
}

// placeMesh makes dst hold exactly wantB bytes of src. An exact-length source
// is hard-linked (no duplicate in user/, refresh costs one directory entry);
// trailing vertices, a different volume, or no link support fall back to
// copyMesh. Only exact-length sources are linked: a longer file would give the
// game a base mesh bigger than vc, a size the ini declares nowhere.
func placeMesh(src, dst string, wantB int64) error {
	got, err := fileSize(src)
	if err != nil {
		return fmt.Errorf("%s: the capture source is missing, every vertex would collapse: %w", src, err)
	}
	if got < wantB {
		return fmt.Errorf("%s is %d B (%d verts) but needs %d B: vertex space too small",
			src, got, got/12, wantB)
	}
	if got == wantB {
		linked, err := linkMesh(src, dst)
		if linked {
			return nil
		}
		if err != nil && !errors.Is(err, errLinkUnavailable) {
			return err
		}
	}
	return copyMesh(src, dst, wantB)
}

// linkMesh points dst at the same file as src and reports whether it did.
// The old file is renamed aside for the same reason copyMesh does it: a game
// holding dst open turns an overwrite into a sharing violation, while a
// rename always succeeds.
func linkMesh(src, dst string) (bool, error) {
	si, err := os.Stat(src)
	if err != nil {
		return false, nil
	}
	if di, err := os.Stat(dst); err == nil && os.SameFile(si, di) {
		return true, nil
	}
	old := ""
	if _, err := os.Stat(dst); err == nil {
		old = dst + ".old"
		_ = os.Remove(old)
		if err := os.Rename(dst, old); err != nil {
			return false, fmt.Errorf("cannot move the old %s aside (%v): restart WWEmoteTool.exe and configure again",
				filepath.Base(dst), err)
		}
	}
	if err := os.Link(src, dst); err != nil {
		if old != "" {
			_ = os.Rename(old, dst)
		}
		return false, errLinkUnavailable
	}
	if old != "" {
		_ = os.Remove(old)
	}
	return true, nil
}

// copyMesh makes dst exactly wantB bytes of src. Overwriting a file that a
// running game holds open fails with a sharing violation on Windows, while
// renaming it away always works, so the old copy is moved aside first.
func copyMesh(src, dst string, wantB int64) error {
	got, err := fileSize(src)
	if err != nil {
		return fmt.Errorf("%s: the capture source is missing, every vertex would collapse: %w", src, err)
	}
	if got < wantB {
		return fmt.Errorf("%s is %d B (%d verts) but needs %d B: vertex space too small",
			src, got, got/12, wantB)
	}
	// A link already satisfies this; comparing the two files byte by byte
	// would read the whole mesh twice to learn the same thing.
	if si, err := os.Stat(src); err == nil {
		if di, err := os.Stat(dst); err == nil && os.SameFile(si, di) {
			return nil
		}
	}
	if same, _ := samePrefix(src, dst, wantB); same {
		return nil
	}

	old := ""
	if _, err := os.Stat(dst); err == nil {
		old = dst + ".old"
		_ = os.Remove(old)
		if err := os.Rename(dst, old); err != nil {
			return fmt.Errorf("cannot move the old %s aside (%v): restart WWEmoteTool.exe and configure again",
				filepath.Base(dst), err)
		}
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	// Write the copy under a temp name first. os.Create(dst) would truncate the
	// live file, and a killed run would leave the game a half-written mesh.
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	// Truncating is the common case for exports that carry a few extra
	// vertices past the declared count.
	n, err := io.CopyN(out, in, wantB)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if n != wantB {
		os.Remove(tmp)
		return fmt.Errorf("%s only yielded %d of %d B: the file changed while reading", src, n, wantB)
	}
	// Move the new copy into place before dropping the backup: if this rename
	// fails the old file must still be there.
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	if old != "" {
		_ = os.Remove(old)
	}
	return nil
}

func samePrefix(a, b string, n int64) (bool, error) {
	st, err := os.Stat(b)
	if err != nil || st.Size() != n {
		return false, err
	}
	ba, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	bb, err := os.ReadFile(b)
	if err != nil {
		return false, err
	}
	if int64(len(ba)) < n || int64(len(bb)) < n {
		return false, nil
	}
	return equalBytes(ba[:n], bb[:n]), nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
