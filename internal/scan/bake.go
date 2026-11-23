package scan

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"wwemote/internal/bake"
	"wwemote/internal/modpath"
	"wwemote/internal/panel"
)

// userRootOverride is set by the caller to the deployed panel's user/ dir.
// When empty, the scan falls back to <modsRoot's parent>/Mods/... -- but a
// real run must always set it, since the author and deployed layouts differ.
var userRootOverride string

// SetUserRoot sets the destination for baked deltas and <char>.mods.json.
func SetUserRoot(p string) { userRootOverride = p }

// UserDeltaRoot returns the one writable persistence root.
func UserDeltaRoot(modsRoot string) string {
	if userRootOverride != "" {
		return userRootOverride
	}
	return filepath.Join(filepath.Dir(modsRoot), "user")
}

// pickFrag picks the unique path segment (or consecutive segment suffix) of
// rel that matches exactly one pvb path. Mirrors scan_mods._pick_frag().
func pickFrag(rel string, pvb []string) string {
	segs := pathSegs(rel)
	for i := len(segs) - 1; i >= 0; i-- {
		if countFragHits(segs[i], pvb) == 1 {
			return segs[i]
		}
	}
	for n := 2; n <= len(segs); n++ {
		cand := strings.Join(segs[len(segs)-n:], "/")
		if countFragHits(cand, pvb) == 1 {
			return cand
		}
	}
	return ""
}

func pathSegs(p string) []string {
	var out []string
	for _, s := range strings.Split(strings.ReplaceAll(p, "\\", "/"), "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func countFragHits(frag string, paths []string) int {
	n := 0
	for _, p := range paths {
		if panel.FragHit(frag, p) {
			n++
		}
	}
	return n
}

// pickFragNS picks the frag for one set. A mod declaring `namespace = X`
// stores its resources at Resource\X\..., so none of the ini path segments
// appear in its pvb path; X is the only segment that can select it. The path
// form must NOT be tried first: a trailing "mod.ini" can match some other
// set's path-form pvb uniquely and bind this set to the wrong mesh.
// Two sets sharing one ns both match X, leaving no unique frag -- the caller
// drops the set rather than picking one arbitrarily.
func pickFragNS(rel, ns string, pvb []string) string {
	if ns != "" {
		if countFragHits(ns, pvb) == 1 {
			return ns
		}
		return ""
	}
	return pickFrag(rel, pvb)
}

// tablesSig is the md5 of the three shapekey tables, used to decide whether
// two sets with the same vertex count may share one delta.
func tablesSig(modDir string) string {
	mdir := modDir
	if strings.ToLower(filepath.Base(filepath.Clean(modDir))) != "meshes" {
		mdir = filepath.Join(modDir, "Meshes")
	}
	h := md5.New()
	for _, nm := range []string{"ShapeKeyOffset.buf", "ShapeKeyVertexId.buf", "ShapeKeyVertexOffset.buf"} {
		p := filepath.Join(mdir, nm)
		b, err := os.ReadFile(p)
		if err != nil {
			return ""
		}
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// contentSig fingerprints a set's whole geometry payload: vertex count, the
// three shape-key tables and the base mesh. Two folders that produce the same
// fingerprint are the same mod installed twice -- collapsing them is safe and
// must not cost the user the character. A different fingerprint means two
// genuinely different mods competing for one bake target, which stays fatal.
func contentSig(modDir string, vc int) string {
	mdir := modDir
	if strings.ToLower(filepath.Base(filepath.Clean(modDir))) != "meshes" {
		mdir = filepath.Join(modDir, "Meshes")
	}
	h := md5.New()
	fmt.Fprintf(h, "vc=%d\n", vc)
	for _, nm := range []string{"ShapeKeyOffset.buf", "ShapeKeyVertexId.buf",
		"ShapeKeyVertexOffset.buf", "Position.buf"} {
		h.Write([]byte(nm))
		h.Write([]byte{0})
		b, err := os.ReadFile(filepath.Join(mdir, nm))
		if err != nil {
			h.Write([]byte("missing"))
			continue
		}
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// bucketSig is the reuse key for one vc bucket: the three shape-key tables
// (tableSig), the vertex count, and the ordered body-shape variants with each
// mesh length. Mesh content and the ini batch1 value are excluded (bases are
// referenced in place; the table wins over a disagreeing ini value), but a
// mesh length matters because placeBase references only an exact vc*12 mesh.
// An empty tableSig means the tables could not be read; returning "" forces a
// bake instead of hiding the failure.
func bucketSig(tableSig string, vc int, modDir string, pos [][3]string) string {
	if tableSig == "" {
		return ""
	}
	h := md5.New()
	h.Write([]byte(tableSig))
	h.Write([]byte{0})
	fmt.Fprintf(h, "vc=%d", vc)
	h.Write([]byte{0})
	mdir := modDir
	if strings.ToLower(filepath.Base(filepath.Clean(mdir))) != "meshes" {
		mdir = filepath.Join(mdir, "Meshes")
	}
	if st, err := os.Stat(filepath.Join(mdir, "Position.buf")); err == nil {
		fmt.Fprintf(h, "poslen=%d", st.Size())
	}
	h.Write([]byte{0})
	for _, row := range pos {
		for _, col := range row {
			h.Write([]byte(col))
			h.Write([]byte{0})
		}
		// row[1] is the variant mesh path from the ini, relative to modDir.
		// Its length decides reference-vs-copy in placeBase.
		if row[1] != "" {
			vp := filepath.Join(append([]string{modDir}, strings.Split(strings.ReplaceAll(row[1], "\\", "/"), "/")...)...)
			if st, err := os.Stat(vp); err == nil {
				fmt.Fprintf(h, "vlen=%d", st.Size())
			} else {
				h.Write([]byte("vmissing"))
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// AutoBake bakes one delta per installed set. Mirrors scan_mods._auto_bake().
// On success out holds frag -> Delta in first-seen (sorted rel) order.
// fresh is the number of sets whose delta was (re)baked this run: a cache hit
// or a shared bucket contributes zero, a real bake contributes one. A full
// cache hit returns zero for the whole character.
func AutoBake(name string, layer *Layer, modsRoot string, found map[string]*Rec) (map[string]*Delta, []string, []string, int, error) {
	out := map[string]*Delta{}
	order := []string{}
	byVC := map[string]string{}      // vc -> representative frag
	byVCSig := map[string]string{}   // vc -> representative table signature
	bucketFresh := map[string]bool{} // vc -> representative bucket baked this run
	fresh := 0
	var notes []string

	vcm := layer.ModVC
	// pose_exclude keys are resource paths for namespace sets and ini-relative
	// paths otherwise (see layer.peKey). Rebuild the excluded set of rels by
	// matching each key back to the rel whose resource path it names, so a
	// finished namespace mod is still skipped by the bake loop.
	excl := map[string]bool{}
	for _, k := range layer.PoseExclude {
		if rec, ok := found[k]; ok && rec != nil {
			excl[k] = true
			continue
		}
		// A pose_exclude key may name a namespace resource shared by a raw and
		// a finished set (both declare namespace=Hsin). It is the finished set
		// that opted out of baking; a raw set sharing the namespace is still a
		// bake target. Do not break on the first match -- map order would then
		// randomly exclude the raw one and bake the finished one instead.
		for rel, rec := range found {
			if rec != nil && rec.Namespace != "" && k == `Resource\`+rec.Namespace+`\`+ResPVB {
				if rec.Kind == KindFinished {
					excl[rel] = true
				}
			}
		}
	}
	rels := make([]string, 0, len(vcm))
	for r := range vcm {
		rels = append(rels, r)
	}
	sortStrings(rels)
	active := ""
	if layer.Active != nil {
		active = *layer.Active
	}

	var skipped []string
	// fragSig/fragRel remember what is already bound to each bake target.
	// A second set with an identical geometry payload is the same mod
	// installed twice; a different payload is a real conflict.
	fragSig := map[string]string{}
	fragRel := map[string]string{}
	for _, rel := range rels {
		if excl[rel] {
			continue
		}
		vc := vcm[rel]
		rec := found[rel]
		ns := ""
		if rec != nil {
			ns = rec.Namespace
		}
		frag := pickFragNS(rel, ns, layer.PvbPaths)
		if frag == "" {
			why := fmt.Sprintf("no fragment of %q uniquely matches pvb_paths", rel)
			if ns != "" {
				why = fmt.Sprintf("namespace %q of %q is shared by several sets, so no unique bake target exists", ns, rel)
			}
			if rel == active {
				return nil, nil, nil, 0, fmt.Errorf("%s", why)
			}
			skipped = append(skipped, rel+": "+why)
			continue
		}
		relDisk := rel
		if rec != nil && rec.RelDisk != "" {
			relDisk = rec.RelDisk
		}
		ini := filepath.Join(append([]string{modsRoot}, strings.Split(relDisk, "/")...)...)
		modDir := filepath.Dir(ini)
		if st, err := os.Stat(filepath.Join(modDir, "Meshes")); err != nil || !st.IsDir() {
			skipped = append(skipped, rel+": no Meshes/ in mod folder")
			continue
		}
		// Two sets can resolve to one bake target when they declare the same
		// namespace (or, for path-form sets, the same resource). Deciding by
		// content rather than by path keeps a copied install from failing the
		// whole character: the same mod installed twice has an identical
		// geometry payload, so keeping the first copy loses nothing.
		csig := contentSig(modDir, vc)
		if prevSig, exists := fragSig[frag]; exists {
			if prevSig == csig {
				skipped = append(skipped, fmt.Sprintf(
					"%s: same mod as %q (installed twice), kept the first copy",
					rel, fragRel[frag]))
				continue
			}
			return nil, nil, nil, 0, fmt.Errorf("two sets share the fragment %q", frag)
		}
		fragSig[frag] = csig
		fragRel[frag] = rel
		sig := tablesSig(modDir)
		myPos := PosVariants(ini, rel, vc, modDir)
		vcKey := fmt.Sprintf("%d", vc)
		if refFrag, exists := byVC[vcKey]; exists {
			refSig := byVCSig[vcKey]
			if sig == "" || refSig == "" || sig != refSig {
				return nil, nil, nil, 0, fmt.Errorf("%q and %q share vc=%d but have different shape-key tables", rel, refFrag, vc)
			}
			shared := *out[refFrag]
			if len(myPos) > 1 {
				shared.Pos = make([]PosSpec, 0, len(myPos))
				for _, row := range myPos {
					shared.Pos = append(shared.Pos, PosSpec{Path: row[0], Base: row[1]})
				}
			} else {
				shared.Pos = nil
			}
			out[frag] = &shared
			order = append(order, frag)
			if bucketFresh[vcKey] {
				fresh++
			}
			notes = append(notes, fmt.Sprintf("%s: same vc and topology as %s => sharing one delta", name, refFrag))
			continue
		}

		nameBase := strings.ToLower(name) + fmt.Sprintf("_mod_%d", vc)
		outDir := UserDeltaRoot(modsRoot)
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return nil, nil, nil, 0, err
		}
		defaultBase := baseNameOf(vc, "")
		isig := bucketSig(sig, vc, modDir, myPos)

		// baseBuf/baseRef/extraRefs are what the Delta assembly needs. A cache
		// hit reads them from the old manifest; a miss bakes and takes them
		// from the fresh one. Assembly below is shared so the two paths cannot
		// drift apart.
		var baseBuf, baseRef string
		var extraRefs map[string]string
		if cached, ok := loadCachedBake(outDir, nameBase, isig); ok && cachedBasePresent(outDir, modsRoot, cached) {
			baseBuf, baseRef = cached.BaseBuf, cached.BaseRef
			extraRefs = cached.ExtraRefs
			bucketFresh[vcKey] = false
			notes = append(notes, fmt.Sprintf("%s: reuse cached %s (sig %s)",
				name, filepath.Base(bufName(outDir, nameBase)), sigPrefix(isig)))
		} else {
			mroot := filepath.Join(modDir, "Meshes")
			extra := [][2]string{}
			for _, row := range myPos {
				if row[1] == defaultBase {
					continue
				}
				abs := filepath.Join(append([]string{modDir}, strings.Split(strings.ReplaceAll(row[2], "\\", "/"), "/")...)...)
				relp, err := filepath.Rel(mroot, abs)
				if err != nil {
					continue
				}
				extra = append(extra, [2]string{filepath.ToSlash(relp), row[1]})
			}
			opts := bake.Options{
				OutDir:   outDir,
				Name:     nameBase,
				IniName:  filepath.Base(rel),
				ExtraPos: extra,
				ModsRoot: modsRoot,
				InputSig: isig,
			}
			man, err := bake.Bake(modDir, vc, opts)
			if err != nil {
				skipped = append(skipped, fmt.Sprintf("%s: bake failed: %v", filepath.Base(rel), err))
				continue
			}
			baseBuf, baseRef = man.BaseBuf, man.BaseRef
			extraRefs = man.ExtraRefs
			bucketFresh[vcKey] = true
			fresh++
			notes = append(notes, fmt.Sprintf("%s: baked %s -> %s (%d entries, %d B)",
				name, filepath.Base(rel), fmt.Sprintf("%s_sparse.buf", nameBase), man.TotalEntries, man.TotalBytes))
		}
		byVC[vcKey] = frag
		byVCSig[vcKey] = sig
		// Ship config paths without DISABLED_ so enabling a mod by deleting
		// the prefix needs no reconfigure. The manifest keeps the real disk
		// path for cache checks; only the config-layer copy is stripped.
		shipRef := modpath.StripDisabled(baseRef)
		d := &Delta{VC: vc, Sparse: filepath.Base(bufName(outDir, nameBase)), Base: baseBuf, BaseRef: shipRef}
		if len(myPos) > 1 {
			d.Pos = make([]PosSpec, 0, len(myPos))
			for _, row := range myPos {
				base, ref := row[1], extraRefs[row[1]]
				if row[1] == defaultBase {
					// Slot 0 is the bucket's own base, so it carries whatever
					// placeBase decided for it -- a reference, or a file.
					base, ref = baseBuf, baseRef
				}
				d.Pos = append(d.Pos, PosSpec{Path: row[0], Base: base, BaseRef: modpath.StripDisabled(ref)})
			}
		}
		out[frag] = d
		order = append(order, frag)
	}

	if len(out) == 0 {
		// A finished (already baked by the mod author) set needs no sparse
		// delta: the mod applies its own shape keys and the panel only binds
		// values through the U5 route. When every claimed set is finished the
		// empty delta is the correct result, not a fatal error -- otherwise a
		// single self-contained mod would abort the whole configuration.
		// Keep the guard when there were usable raw sets that all failed for
		// real reasons (bad frag, missing Meshes/, bake errors): those are in
		// skipped, and writing an empty layer would hide them.
		allFinished := len(vcm) > 0 && len(excl) == len(vcm)
		if !allFinished {
			return nil, nil, skipped, 0, fmt.Errorf("no bakeable set on this machine (%d entries in mod_vc)", len(vcm))
		}
	}
	layer.ModDelta = out
	layer.ModDeltaOrder = order
	return out, order, skipped, fresh, nil
}

func bufName(outDir, name string) string { return filepath.Join(outDir, name+"_sparse.buf") }

func sigPrefix(sig string) string {
	if len(sig) > 8 {
		return sig[:8]
	}
	return sig
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
