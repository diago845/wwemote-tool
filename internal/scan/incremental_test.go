package scan

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"wwemote/internal/bake"
)

// The three tables here are only realistic enough for tablesSig: 256 uint32
// boundaries, an id table, and a half-float offset table. Reuse decisions hash
// these bytes without decoding them, so content fidelity is not needed.
func writeSigTables(t *testing.T, modDir string, seed byte) {
	t.Helper()
	meshes := filepath.Join(modDir, "Meshes")
	if err := os.MkdirAll(meshes, 0o755); err != nil {
		t.Fatal(err)
	}
	flat := make([]byte, 256*4)
	for i := 0; i < 128; i++ {
		binary.LittleEndian.PutUint32(flat[i*4:], uint32(i))
		binary.LittleEndian.PutUint32(flat[(128+i)*4:], uint32(i))
	}
	flat[0] = seed // make the bytes seed-dependent
	os.WriteFile(filepath.Join(meshes, "ShapeKeyOffset.buf"), flat, 0o644)

	vid := make([]byte, 8*4)
	for i := 0; i < 8; i++ {
		binary.LittleEndian.PutUint32(vid[i*4:], uint32(i))
	}
	os.WriteFile(filepath.Join(meshes, "ShapeKeyVertexId.buf"), vid, 0o644)
	os.WriteFile(filepath.Join(meshes, "ShapeKeyVertexOffset.buf"), make([]byte, 8*6*2), 0o644)
	os.WriteFile(filepath.Join(meshes, "Position.buf"), make([]byte, 10*12), 0o644)
}

func TestBucketSigCoversEachInput(t *testing.T) {
	modDir := t.TempDir()
	writeSigTables(t, modDir, 1)
	base := tablesSig(modDir)
	if base == "" {
		t.Fatal("tablesSig must read the fixture")
	}

	pos := [][3]string{{"Body", `Meshes\Position.buf`, ""}}
	sig := bucketSig(base, 10, modDir, pos)
	if sig == "" {
		t.Fatal("bucketSig must produce a signature")
	}

	checkChanged := func(name string, mutate func()) {
		t.Helper()
		mutate()
		got := bucketSig(tablesSig(modDir), 10, modDir, pos)
		if got == sig {
			t.Fatalf("%s changed but the signature did not move", name)
		}
	}

	// Table bytes.
	checkChanged("shape-key table", func() {
		p := filepath.Join(modDir, "Meshes", "ShapeKeyVertexId.buf")
		b, _ := os.ReadFile(p)
		b[0] ^= 0xff
		os.WriteFile(p, b, 0o644)
	})
	// Vertex count.
	if bucketSig(base, 11, modDir, pos) == sig {
		t.Fatal("vc changed but the signature did not move")
	}
	// Position.buf length.
	checkChanged("base mesh length", func() {
		os.WriteFile(filepath.Join(modDir, "Meshes", "Position.buf"), make([]byte, 11*12), 0o644)
	})
	// Variant list (a second body-shape entry).
	if bucketSig(base, 10, modDir, append(pos, [3]string{"Thicc", `Meshes\Position_thicc.buf`, ""})) == sig {
		t.Fatal("variant list changed but the signature did not move")
	}
	// Variant mesh length: add the file that PosVariants would have found and
	// change its size; row[1] is the mesh path relative to modDir.
	vp := filepath.Join(modDir, "Meshes", "Position_thicc.buf")
	os.WriteFile(vp, make([]byte, 10*12), 0o644)
	pos2 := append(pos, [3]string{"Thicc", `Meshes\Position_thicc.buf`, ""})
	sigWithVariant := bucketSig(base, 10, modDir, pos2)
	os.WriteFile(vp, make([]byte, 11*12), 0o644)
	if bucketSig(base, 10, modDir, pos2) == sigWithVariant {
		t.Fatal("variant mesh length changed but the signature did not move")
	}
}

func TestBucketSigEmptyWhenTablesUnreadable(t *testing.T) {
	modDir := t.TempDir() // no Meshes/
	if got := bucketSig(tablesSig(modDir), 10, modDir, nil); got != "" {
		t.Fatalf("unreadable tables must yield an empty signature, got %q", got)
	}
}

func writeCachedManifest(t *testing.T, dir, nameBase string, man cachedManifest) {
	t.Helper()
	raw, _ := json.Marshal(man)
	if err := os.WriteFile(filepath.Join(dir, nameBase+"_sparse_manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, nameBase+"_sparse.buf"), make([]byte, man.TotalBytes), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadCachedBakeRejectsMismatches(t *testing.T) {
	dir := t.TempDir()
	name := "hsin_mod_10"
	good := cachedManifest{
		BakedBy:      bake.BakedByTag,
		BakeVersion:  bake.BakeVersion,
		InputSig:     "abc123",
		TotalBytes:   64,
		TotalEntries: 3,
		BaseBuf:      "ep_mod_base_10.buf",
	}
	writeCachedManifest(t, dir, name, good)
	os.WriteFile(filepath.Join(dir, "ep_mod_base_10.buf"), make([]byte, 120), 0o644)
	modsRoot := filepath.Dir(dir)

	if _, ok := loadCachedBake(dir, name, "abc123"); !ok {
		t.Fatal("a matching cache must be reusable")
	}
	if _, ok := loadCachedBake(dir, name, "different"); ok {
		t.Fatal("a different input signature must not be reused")
	}
	if _, ok := loadCachedBake(dir, name, ""); ok {
		t.Fatal("an empty signature must never be reused")
	}

	// Wrong algorithm version: bump BakeVersion.
	bad := good
	bad.BakeVersion = "an-old-algorithm"
	writeCachedManifest(t, dir, name, bad)
	if _, ok := loadCachedBake(dir, name, "abc123"); ok {
		t.Fatal("a manifest from another algorithm must not be reused")
	}

	// Truncated sparse buffer: byte count no longer matches.
	writeCachedManifest(t, dir, name, good)
	os.WriteFile(filepath.Join(dir, name+"_sparse.buf"), make([]byte, 32), 0o644)
	if _, ok := loadCachedBake(dir, name, "abc123"); ok {
		t.Fatal("a sparse buffer whose size does not match must not be reused")
	}

	// Missing base: the cache would point at a base that no longer exists.
	writeCachedManifest(t, dir, name, good)
	os.Remove(filepath.Join(dir, "ep_mod_base_10.buf"))
	man, ok := loadCachedBake(dir, name, "abc123")
	if !ok {
		t.Fatal("manifest itself is intact and should load")
	}
	if cachedBasePresent(dir, modsRoot, man) {
		t.Fatal("a missing base must invalidate the cache")
	}
}

// The manifest stores the on-disk path at bake time. When a mod was baked while
// disabled, that ref carries DISABLED_'s marker; enabling the mod removes the
// marker from the folder but not from the stored ref. A prefix toggle must not
// invalidate the cache -- otherwise every enable re-bakes and reports "new".
func TestCachedBasePresentToleratesDisabledToggle(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DISABLED_ toggle uses platform separators")
	}
	modsRoot := t.TempDir()
	outDir := t.TempDir()
	// Disk state: mod currently enabled (no marker).
	realBase := filepath.Join(modsRoot, "Hsin", "HsinMod thicc", "Hsin Mod", "Meshes", "Position.buf")
	if err := os.MkdirAll(filepath.Dir(realBase), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realBase, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Manifest recorded the disabled-state path (with marker).
	man := &cachedManifest{
		BaseRef: `Hsin\DISABLED_HsinMod thicc\Hsin Mod\Meshes\Position.buf`,
	}
	if !cachedBasePresent(outDir, modsRoot, man) {
		t.Fatal("a DISABLED_ prefix toggle must not invalidate the cached base")
	}

	// A genuine rename must still invalidate.
	man.BaseRef = `Hsin\Renamed Mod\Hsin Mod\Meshes\Position.buf`
	if cachedBasePresent(outDir, modsRoot, man) {
		t.Fatal("a genuinely renamed base must invalidate the cache")
	}
}
