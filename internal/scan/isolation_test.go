package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Build a claim result with two good raw sets and one degraded set. The
// degraded set must be dropped from the layer while the two good ones remain
// and the call must NOT return a fatal error.
func TestUserLayerDropsBadSetKeepsGood(t *testing.T) {
	root := t.TempDir()
	mk := func(rel, kind string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		var body string
		switch kind {
		case "raw":
			body = "[CommandListOverrideSharedResources]\nvb0 = ResourcePositionBuffer\n\n[TextureOverrideX]\nhash = 67b0a7f4\n$\\WWMIv1\\custom_vertex_count = 50000\n\n[ResourcePositionBuffer]\nfilename = Meshes/Position.buf\n"
		case "degraded":
			body = "[CommandListOverrideSharedResources]\nvb0 = ResourceSomethingWeird\n"
		}
		os.WriteFile(p, []byte(body), 0o644)
	}
	mk("good1/mod.ini", "raw")
	mk("good2/mod.ini", "raw")
	mk("bad/mod.ini", "degraded")

	idx, err := BuildIndexScope(root, root)
	if err != nil {
		t.Fatal(err)
	}
	// No anchor needed: claim all three directly.
	res := &ClaimResult{Found: map[string]*Rec{}, FoundKeys: []string{}}
	for _, rel := range idx.Order {
		rec := idx.Recs[rel]
		r2 := *rec
		r2.RelDisk = rel
		r2.Kind = classify(&r2)
		res.Found[rel] = &r2
		res.FoundKeys = append(res.FoundKeys, rel)
	}
	if got := res.Found["bad/mod.ini"].Kind; got != KindUnknown {
		t.Fatalf("bad set kind = %q, want %q", got, KindUnknown)
	}

	layer, errs := UserLayer(res, root, false)
	if len(errs) != 0 {
		t.Fatalf("one degraded set must not be fatal, got errs=%v", errs)
	}
	if len(layer.ModVC) != 2 {
		t.Fatalf("want 2 good sets in mod_vc, got %v", layer.ModVC)
	}
	if _, ok := layer.ModVC["bad/mod.ini"]; ok {
		t.Fatal("degraded set leaked into mod_vc")
	}
	if len(layer.Skipped) != 1 || !strings.Contains(layer.Skipped[0], "bad/mod.ini") {
		t.Fatalf("want one skip reason naming bad set, got %v", layer.Skipped)
	}
	// Skipped must never be serialised into the on-disk config.
	if strings.Contains(string(DumpModsJSON(layer)), "Skipped") || strings.Contains(string(DumpModsJSON(layer)), "skipped") {
		t.Fatal("Skipped leaked into mods.json")
	}
}

// A set whose mesh vertex count is below the declared custom_vertex_count is
// a per-set data error too: drop just that set, keep the good one.
func TestUserLayerDropsTooFewVertices(t *testing.T) {
	root := t.TempDir()
	mkRaw := func(rel, vc string) string {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		body := "[CommandListOverrideSharedResources]\nvb0 = ResourcePositionBuffer\n\n[TextureOverrideX]\nhash = 67b0a7f4\n$\\WWMIv1\\custom_vertex_count = " + vc + "\n\n[ResourcePositionBuffer]\nfilename = Meshes/Position.buf\n"
		os.WriteFile(p, []byte(body), 0o644)
		return p
	}
	good := mkRaw("good/mod.ini", "100")
	bad := mkRaw("bad/mod.ini", "50000")
	// Good mesh: 100 vertices = 1200 bytes. Bad mesh: 1 vertex = 12 bytes.
	os.MkdirAll(filepath.Join(filepath.Dir(good), "Meshes"), 0o755)
	os.WriteFile(filepath.Join(filepath.Dir(good), "Meshes", "Position.buf"), make([]byte, 1200), 0o644)
	os.MkdirAll(filepath.Join(filepath.Dir(bad), "Meshes"), 0o755)
	os.WriteFile(filepath.Join(filepath.Dir(bad), "Meshes", "Position.buf"), make([]byte, 12), 0o644)

	idx, err := BuildIndexScope(root, root)
	if err != nil {
		t.Fatal(err)
	}
	res := &ClaimResult{Found: map[string]*Rec{}, FoundKeys: []string{}}
	for _, rel := range idx.Order {
		r2 := *idx.Recs[rel]
		r2.RelDisk = rel
		r2.Kind = classify(&r2)
		res.Found[rel] = &r2
		res.FoundKeys = append(res.FoundKeys, rel)
	}

	layer, errs := UserLayer(res, root, false)
	if len(errs) != 0 {
		t.Fatalf("too-few-vertices is per-set, must not be fatal, got %v", errs)
	}
	if len(layer.ModVC) != 1 {
		t.Fatalf("want 1 good set, got %v", layer.ModVC)
	}
	if _, ok := layer.ModVC["bad/mod.ini"]; ok {
		t.Fatal("bad mesh set leaked into mod_vc")
	}
	if len(layer.Skipped) != 1 || !strings.Contains(layer.Skipped[0], "too few vertices") {
		t.Fatalf("want a too-few-vertices skip reason, got %v", layer.Skipped)
	}
}

// When every set is bad and a baked delta is required, the caller must still
// get a fatal error so it never writes an empty delta config.
func TestUserLayerAllBadNeedActiveIsFatal(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "bad", "mod.ini")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("[CommandListOverrideSharedResources]\nvb0 = ResourceWeird\n"), 0o644)

	idx, err := BuildIndexScope(root, root)
	if err != nil {
		t.Fatal(err)
	}
	res := &ClaimResult{Found: map[string]*Rec{}, FoundKeys: []string{}}
	for _, rel := range idx.Order {
		r2 := *idx.Recs[rel]
		r2.RelDisk = rel
		r2.Kind = classify(&r2)
		res.Found[rel] = &r2
		res.FoundKeys = append(res.FoundKeys, rel)
	}

	_, errs := UserLayer(res, root, true)
	if len(errs) == 0 {
		t.Fatal("all sets bad + needActive must be fatal (no empty delta write)")
	}
}
