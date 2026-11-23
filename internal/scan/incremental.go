package scan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"wwemote/internal/bake"
	"wwemote/internal/modpath"
)

// cachedManifest holds the fields a reuse decision needs. bake.Manifest has no
// json tags (its writer is hand-rolled to match the Python dialect), so a
// tagged view is used to read the file back.
type cachedManifest struct {
	BakedBy      string            `json:"baked_by"`
	BakeVersion  string            `json:"bake_version"`
	InputSig     string            `json:"input_sig"`
	TotalBytes   int               `json:"total_bytes"`
	TotalEntries int               `json:"total_entries"`
	BaseBuf      string            `json:"base_buf"`
	BaseRef      string            `json:"base_ref"`
	ExtraRefs    map[string]string `json:"extra_refs"`
}

// loadCachedBake returns the previous manifest when it can safely be reused:
// same generator, same algorithm, identical input signature, and the sparse
// buffer on disk still has the byte count the manifest declares. Any miss
// returns false, and the caller falls back to a full bake.
func loadCachedBake(outDir, nameBase, sig string) (*cachedManifest, bool) {
	if sig == "" {
		return nil, false
	}
	raw, err := os.ReadFile(filepath.Join(outDir, nameBase+"_sparse_manifest.json"))
	if err != nil {
		return nil, false
	}
	var man cachedManifest
	if json.Unmarshal(raw, &man) != nil {
		return nil, false
	}
	if man.BakedBy != bake.BakedByTag || man.BakeVersion != bake.BakeVersion || man.InputSig != sig {
		return nil, false
	}
	st, err := os.Stat(filepath.Join(outDir, nameBase+"_sparse.buf"))
	if err != nil || st.Size() < 0 || int(st.Size()) != man.TotalBytes {
		return nil, false
	}
	return &man, true
}

// cachedBasePresent checks every base file a cached Delta would point at. A
// local base is a file in outDir; a referenced base is a mods-relative path
// that must still exist in the mod tree. A missing base invalidates the cache
// so the next run rewrites it.
func cachedBasePresent(outDir, modsRoot string, man *cachedManifest) bool {
	check := func(name, ref string) bool {
		if ref != "" {
			p := filepath.Join(modsRoot, filepath.FromSlash(strings.ReplaceAll(ref, "\\", "/")))
			// The manifest records the on-disk path at bake time, which may
			// carry a DISABLED_ marker that the user has since toggled. Treat
			// the marker as optional so enabling a mod does not invalidate a
			// cache that still points at its disabled-state path.
			return modpath.PresentOnDisk(p)
		}
		if name == "" {
			return false
		}
		p := filepath.Join(outDir, name)
		st, err := os.Stat(p)
		return err == nil && !st.IsDir()
	}
	if !check(man.BaseBuf, man.BaseRef) {
		return false
	}
	for name, ref := range man.ExtraRefs {
		if !check(name, ref) {
			return false
		}
	}
	return true
}
