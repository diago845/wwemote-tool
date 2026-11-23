package scan

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// prevLayer is a read-only view of an existing <char>.mods.json. It mirrors
// Layer's on-disk fields, including the delta writer's own tags.
type prevLayer struct {
	Note          []string            `json:"_note"`
	SkPaths       []string            `json:"sk_paths"`
	PvbPaths      []string            `json:"pvb_paths"`
	PoseExclude   []string            `json:"pose_exclude"`
	PoseExcludeVC map[string]int      `json:"pose_exclude_vc"`
	Active        *string             `json:"active"`
	ModVC         map[string]int      `json:"mod_vc"`
	PosVariants   map[string][]string `json:"pos_variants"`
	ModDelta      map[string]*Delta   `json:"mod_delta"`
}

func readPrevLayer(raw []byte) (*prevLayer, error) {
	var p prevLayer
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// InScopeRel reports whether a Mods-relative ini path lies inside the chosen
// folder. The normalized key stored in mods.json is measured against modsRoot,
// the same basis as scanRoot, so this comparison is valid for old set keys.
// The trailing slash matters: "心/HsinMod" must not swallow "心/HsinMod-thicc".
func InScopeRel(scopeRel, rel string) bool {
	if scopeRel == "" {
		return true
	}
	return strings.HasPrefix(rel, scopeRel+"/")
}

// layerSetPaths pairs each set key with its sk/pvb resource path by index.
// mod_vc covers every set in the same sorted order used by DumpModsJSON.
func layerSetPaths(l *prevLayer) map[string][2]string {
	out := map[string][2]string{}
	rels := make([]string, 0, len(l.ModVC))
	for rel := range l.ModVC {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for i, rel := range rels {
		var sk, pvb string
		if i < len(l.SkPaths) {
			sk = l.SkPaths[i]
		}
		if i < len(l.PvbPaths) {
			pvb = l.PvbPaths[i]
		}
		out[rel] = [2]string{sk, pvb}
	}
	return out
}

// outsideLayer keeps only sets whose key is outside scopeRel. Fresh in-scope
// data replaces them later; this is the part a scoped run must not lose.
func outsideLayer(old *prevLayer, scopeRel string) *Layer {
	out := &Layer{Note: append([]string(nil), old.Note...)}
	// A scope that is itself a _skipemote folder keeps its old sets under the
	// pre-rename name. Compare against the unmarked baseline as well, so those
	// stale sets are treated as in scope and dropped instead of revived with a
	// base mesh path that no longer exists.
	base := skipEmoteScopeBase(scopeRel)
	inScope := func(rel string) bool {
		return InScopeRel(scopeRel, rel) || (base != scopeRel && InScopeRel(base, rel))
	}
	sets := layerSetPaths(old)
	rels := make([]string, 0, len(sets))
	for rel := range sets {
		// A folder the user marked _skipemote is out for good: never carry its
		// old set back, otherwise a stale binding survives in mods.json.
		if hasSkipEmoteFolder(rel) {
			continue
		}
		if !inScope(rel) {
			rels = append(rels, rel)
		}
	}
	sort.Strings(rels)

	pe := map[string]bool{}
	for _, r := range old.PoseExclude {
		if !hasSkipEmoteFolder(r) && !inScope(r) {
			pe[r] = true
		}
	}
	for _, r := range rels {
		paths := sets[r]
		out.SkPaths = append(out.SkPaths, paths[0])
		out.PvbPaths = append(out.PvbPaths, paths[1])
		if v, ok := old.ModVC[r]; ok {
			if out.ModVC == nil {
				out.ModVC = map[string]int{}
			}
			out.ModVC[r] = v
		}
		if v, ok := old.PoseExcludeVC[r]; ok {
			if out.PoseExcludeVC == nil {
				out.PoseExcludeVC = map[string]int{}
			}
			out.PoseExcludeVC[r] = v
		}
		if v, ok := old.PosVariants[r]; ok {
			if out.PosVariants == nil {
				out.PosVariants = map[string][]string{}
			}
			out.PosVariants[r] = v
		}
	}
	for r := range pe {
		out.PoseExclude = append(out.PoseExclude, r)
	}
	sort.Strings(out.PoseExclude)
	if old.Active != nil && !hasSkipEmoteFolder(*old.Active) && !inScope(*old.Active) {
		s := *old.Active
		out.Active = &s
	}
	// A delta belongs to a set when its frag is picked from that set's pvb
	// path. Only carry deltas for sets we are actually keeping, so a removed
	// in-scope mod's sparse is not resurrected by the merge.
	for _, rel := range rels {
		frag := pickFrag(rel, old.PvbPaths)
		if frag == "" || old.ModDelta[frag] == nil {
			continue
		}
		if out.ModDelta == nil {
			out.ModDelta = map[string]*Delta{}
		}
		if _, exists := out.ModDelta[frag]; !exists {
			out.ModDelta[frag] = old.ModDelta[frag]
			out.ModDeltaOrder = append(out.ModDeltaOrder, frag)
		}
	}
	sort.Strings(out.ModDeltaOrder)
	return out
}

// MergeOutside builds the final layer for a scoped run: fresh (in-scope) data
// wins, out-of-scope sets are copied from the previous file byte-for-byte in
// content. prevRaw may be nil when the character had no config yet.
func MergeOutside(fresh *Layer, prevRaw []byte, scopeRel string) (*Layer, error) {
	if len(prevRaw) == 0 {
		return fresh, nil
	}
	old, err := readPrevLayer(prevRaw)
	if err != nil {
		return nil, err
	}
	keep := outsideLayer(old, scopeRel)
	out := combineLayers(fresh, keep)
	if err := reconcileFrags(out, fresh); err != nil {
		return nil, err
	}
	return out, nil
}

// reconcileFrags re-picks each freshly scanned delta's key against the final
// merged pvb list. pickFrag is only unique against the pvb set it was given;
// a scoped scan sees in-scope paths alone, so a short key like "1.ini" can be
// unique there yet match several paths once out-of-scope sets are merged back.
// panel.DeltaTargets validates against the full list, so the key must be
// re-picked against that same list. Keys already unique are left byte-identical.
func reconcileFrags(out, fresh *Layer) error {
	if len(fresh.ModDelta) == 0 || len(out.PvbPaths) == 0 {
		return nil
	}

	// Reverse-map each fresh delta back to the rel it was picked from. AutoBake
	// keys deltas by pickFrag(rel, fresh.PvbPaths); walk the same rels to recover
	// that mapping. A fresh frag that no rel claims is already a preserved key.
	owner := map[string]string{} // frag -> rel
	for rel := range fresh.ModVC {
		f := pickFrag(rel, fresh.PvbPaths)
		if f == "" || fresh.ModDelta[f] == nil {
			continue
		}
		owner[f] = rel
	}

	// Reserved keys are every key the merge will carry, so a re-pick cannot
	// collide with a preserved out-of-scope delta or another fresh delta.
	reserved := map[string]bool{}
	for k := range out.ModDelta {
		reserved[k] = true
	}

	renames := map[string]string{} // old frag -> new frag
	for oldFrag, rel := range owner {
		if countFragHits(oldFrag, out.PvbPaths) == 1 {
			continue
		}
		newFrag := pickFragKey(rel, out.PvbPaths, reserved, oldFrag)
		if newFrag == "" {
			return fmt.Errorf("%q: cannot find a fragment unique in the merged pvb list", rel)
		}
		renames[oldFrag] = newFrag
		reserved[newFrag] = true
	}
	if len(renames) == 0 {
		return nil
	}

	// Rebuild the delta map and its order with the new keys, keeping the
	// original sorted order by translating each entry in place.
	newDelta := map[string]*Delta{}
	newOrder := make([]string, 0, len(out.ModDeltaOrder))
	for _, k := range out.ModDeltaOrder {
		if nk, ok := renames[k]; ok {
			newDelta[nk] = out.ModDelta[k]
			newOrder = append(newOrder, nk)
			continue
		}
		newDelta[k] = out.ModDelta[k]
		newOrder = append(newOrder, k)
	}
	out.ModDelta = newDelta
	out.ModDeltaOrder = newOrder
	sort.Strings(out.ModDeltaOrder)
	return nil
}

// pickFragKey is pickFrag with a reserved-key guard: it returns the shortest
// suffix of rel that matches exactly one pvb path and is not in used, except
// for self (the key being renamed, which is freed at the same time).
func pickFragKey(rel string, pvb []string, used map[string]bool, self string) string {
	segs := pathSegs(rel)
	free := func(c string) bool { return c != "" && (!used[c] || c == self) }
	for i := len(segs) - 1; i >= 0; i-- {
		if countFragHits(segs[i], pvb) == 1 && free(segs[i]) {
			return segs[i]
		}
	}
	for n := 2; n <= len(segs); n++ {
		cand := strings.Join(segs[len(segs)-n:], "/")
		if countFragHits(cand, pvb) == 1 && free(cand) {
			return cand
		}
	}
	return ""
}

// PruneOutside is used by scoped stale cleanup when a mod in the selected
// folder was removed and no in-scope set is left to regenerate the character.
// It returns the layer with in-scope entries dropped and whether any set
// remains; the caller decides whether to rewrite or delete the file.
func PruneOutside(prevRaw []byte, scopeRel string) (*Layer, bool, error) {
	old, err := readPrevLayer(prevRaw)
	if err != nil {
		return nil, false, err
	}
	out := outsideLayer(old, scopeRel)
	return out, len(out.ModVC) > 0, nil
}

// HasScopeKey reports whether any configured set key lies inside scopeRel.
func HasScopeKey(prevRaw []byte, scopeRel string) bool {
	old, err := readPrevLayer(prevRaw)
	if err != nil || scopeRel == "" {
		return false
	}
	for rel := range old.ModVC {
		if InScopeRel(scopeRel, rel) {
			return true
		}
	}
	return false
}

// CleanupScopeFor returns the scope key whose stale sets should be pruned when
// a scoped scan finds no installed mod for a character. A _skipemote scope
// retires the mod under its pre-rename name as well: that old set points at a
// base mesh path that no longer exists, and leaving it in mods.json makes the
// render pass fail with "missing base mesh". So the unmarked baseline is
// pruned even when the user did not tick clean-stale. Returns "" when nothing
// should be pruned, in which case the caller leaves the config untouched.
func CleanupScopeFor(scopeRel string, prevRaw []byte, cleanStale bool) string {
	if scopeRel == "" {
		return ""
	}
	if hasSkipEmoteFolder(scopeRel) {
		if base := skipEmoteScopeBase(scopeRel); HasScopeKey(prevRaw, base) {
			return base
		}
	}
	if cleanStale && HasScopeKey(prevRaw, scopeRel) {
		return scopeRel
	}
	return ""
}

// SparseNames returns the sparse buffer names referenced by a mods.json.
func SparseNames(raw []byte) map[string]bool {
	old, err := readPrevLayer(raw)
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, d := range old.ModDelta {
		if d != nil && d.Sparse != "" {
			out[d.Sparse] = true
		}
	}
	return out
}

// combineLayers merges a fresh layer with a preserved outside layer. Fresh
// wins on key collisions; the result keeps DumpModsJSON's sorted order.
func combineLayers(fresh, keep *Layer) *Layer {
	out := &Layer{Note: append([]string(nil), fresh.Note...)}
	sets := map[string][2]string{}
	addSets := func(l *Layer) {
		rels := make([]string, 0, len(l.ModVC))
		for rel := range l.ModVC {
			rels = append(rels, rel)
		}
		sort.Strings(rels)
		for i, rel := range rels {
			var sk, pvb string
			if i < len(l.SkPaths) {
				sk = l.SkPaths[i]
			}
			if i < len(l.PvbPaths) {
				pvb = l.PvbPaths[i]
			}
			sets[rel] = [2]string{sk, pvb}
		}
	}
	addSets(fresh)
	// A set regenerated this run always wins over the stale copy on disk.
	freshRels := map[string]bool{}
	for rel := range fresh.ModVC {
		freshRels[rel] = true
	}
	keepRels := make([]string, 0, len(keep.ModVC))
	for rel := range keep.ModVC {
		keepRels = append(keepRels, rel)
	}
	sort.Strings(keepRels)
	for _, rel := range keepRels {
		if freshRels[rel] {
			continue
		}
		if v, ok := keep.ModVC[rel]; ok {
			if out.ModVC == nil {
				out.ModVC = map[string]int{}
			}
			out.ModVC[rel] = v
		}
		if v, ok := keep.PoseExcludeVC[rel]; ok {
			if out.PoseExcludeVC == nil {
				out.PoseExcludeVC = map[string]int{}
			}
			out.PoseExcludeVC[rel] = v
		}
		if v, ok := keep.PosVariants[rel]; ok {
			if out.PosVariants == nil {
				out.PosVariants = map[string][]string{}
			}
			out.PosVariants[rel] = v
		}
		sets[rel] = layerSetPathsOf(keep, rel)
	}

	for k, v := range fresh.ModVC {
		if out.ModVC == nil {
			out.ModVC = map[string]int{}
		}
		out.ModVC[k] = v
	}
	for k, v := range fresh.PoseExcludeVC {
		if out.PoseExcludeVC == nil {
			out.PoseExcludeVC = map[string]int{}
		}
		out.PoseExcludeVC[k] = v
	}
	for k, v := range fresh.PosVariants {
		if out.PosVariants == nil {
			out.PosVariants = map[string][]string{}
		}
		out.PosVariants[k] = v
	}

	rels := make([]string, 0, len(sets))
	for rel := range sets {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		out.SkPaths = append(out.SkPaths, sets[rel][0])
		out.PvbPaths = append(out.PvbPaths, sets[rel][1])
	}

	pe := map[string]bool{}
	for _, r := range fresh.PoseExclude {
		pe[r] = true
	}
	for _, r := range keep.PoseExclude {
		pe[r] = true
	}
	for r := range pe {
		out.PoseExclude = append(out.PoseExclude, r)
	}
	sort.Strings(out.PoseExclude)

	out.Active = fresh.Active
	if out.Active == nil {
		out.Active = keep.Active
	}

	out.ModDelta = map[string]*Delta{}
	for k, v := range fresh.ModDelta {
		out.ModDelta[k] = v
		out.ModDeltaOrder = append(out.ModDeltaOrder, k)
	}
	for _, k := range keep.ModDeltaOrder {
		if _, exists := out.ModDelta[k]; !exists && keep.ModDelta[k] != nil {
			out.ModDelta[k] = keep.ModDelta[k]
			out.ModDeltaOrder = append(out.ModDeltaOrder, k)
		}
	}
	sort.Strings(out.ModDeltaOrder)
	return out
}

// layerSetPathsOf returns the (sk, pvb) resource paths for one set key.
func layerSetPathsOf(l *Layer, rel string) [2]string {
	rels := make([]string, 0, len(l.ModVC))
	for r := range l.ModVC {
		rels = append(rels, r)
	}
	sort.Strings(rels)
	for i, r := range rels {
		if r != rel {
			continue
		}
		var sk, pvb string
		if i < len(l.SkPaths) {
			sk = l.SkPaths[i]
		}
		if i < len(l.PvbPaths) {
			pvb = l.PvbPaths[i]
		}
		return [2]string{sk, pvb}
	}
	return [2]string{}
}
