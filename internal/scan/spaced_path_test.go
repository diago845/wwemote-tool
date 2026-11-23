package scan

import (
	"os"
	"path/filepath"
	"testing"
)

// claimSpaced builds a real ini on disk so scanIni parses it exactly as it
// would in a run, then returns a record keyed by the given relative path.
func claimSpaced(t *testing.T, anchor, rel string) (*ClaimResult, []string) {
	t.Helper()
	dir := t.TempDir()
	text := "[TextureOverrideShapeKeyOffsets]\n" +
		"hash = " + anchor + "\n\n" +
		"[Constants]\n" +
		"$\\WWMIv1\\custom_vertex_count = 10\n"
	ini := filepath.Join(dir, "1.ini")
	if err := os.WriteFile(ini, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := scanIni(ini)
	if err != nil {
		t.Fatal(err)
	}
	idx := &Index{Recs: map[string]*Rec{rel: rec}, Order: []string{rel}}
	res := ScanChar([]string{anchor}, nil, idx)
	return res, res.FoundKeys
}

// A mod path with a space must now be claimed, not dropped. The render layer
// emits a namespace shim for it; dropping here would defeat that and silently
// leave the set unconfigured.
func TestScanCharClaimsSpacedPath(t *testing.T) {
	res, keys := claimSpaced(t, "abc12345", "Lynae/Lynae skin mod/mod.ini")
	if len(res.Dropped) != 0 {
		t.Fatalf("spaced path was dropped: %v", res.Dropped)
	}
	if len(keys) != 1 {
		t.Fatalf("spaced path not claimed: found %v", keys)
	}
}

func TestScanCharClaimsBracketPath(t *testing.T) {
	res, keys := claimSpaced(t, "def67890", "Hsin/Mod[v2]/mod.ini")
	if len(res.Dropped) != 0 {
		t.Fatalf("bracket path was dropped: %v", res.Dropped)
	}
	if len(keys) != 1 {
		t.Fatalf("bracket path not claimed: found %v", keys)
	}
}
