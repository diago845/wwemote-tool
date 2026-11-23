package panel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A base stored as a reference must be turned into the text the game resolves
// from the ini's own directory, which is always <mods>\<panel>\user.
func TestBaseMeshNameUsesReferencePrefix(t *testing.T) {
	if got, want := baseMeshName("", `Hsin\HsinMod\Meshes\Position.buf`), `..\..\Hsin\HsinMod\Meshes\Position.buf`; got != want {
		t.Fatalf("baseMeshName = %q, want %q", got, want)
	}
	if got, want := baseMeshName("ep_mod_base_10.buf", ""), "ep_mod_base_10.buf"; got != want {
		t.Fatalf("baseMeshName = %q, want %q", got, want)
	}
}

// The reference has to resolve two levels up from the user layer, and to the
// exact byte count the delta indexes. Both halves are what makes a wrong
// layout a configuration error instead of a silently skipped filename.
func TestCheckBaseMeshResolvesReference(t *testing.T) {
	mods := t.TempDir()
	user := filepath.Join(mods, "WWEmote", "user")
	mesh := filepath.Join(mods, "Hsin", "HsinMod", "Meshes", "Position.buf")
	for _, p := range []string{user, filepath.Dir(mesh)} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(mesh, make([]byte, 120), 0o644); err != nil {
		t.Fatal(err)
	}
	name := baseMeshName("", `Hsin\HsinMod\Meshes\Position.buf`)
	if err := checkBaseMesh(user, name, 10); err != nil {
		t.Fatalf("reference should resolve from the user layer: %v", err)
	}
	if err := checkBaseMesh(user, name, 11); err == nil {
		t.Fatal("a vertex-space mismatch must be reported, not accepted")
	}
	// A user layer that is not two levels below the mods root cannot resolve
	// the reference; that has to be an error, never a silent skip.
	scratch := filepath.Join(t.TempDir(), "user")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkBaseMesh(scratch, name, 10); err == nil {
		t.Fatal("a reference must not resolve from an unrelated directory")
	}
}

// A versioned panel wrapper puts user/ one level deeper than the legacy
// layout. The prefix written into filename= must follow that depth; otherwise
// the game resolves the base mesh inside the wrapper and every vertex collapses.
func TestBaseRefPrefixFollowsPanelDepth(t *testing.T) {
	mods := t.TempDir()
	user := filepath.Join(mods, "WWEmote-v0.61", "WWEmote", "user")
	if got, want := baseRefPrefix(user, mods), `..\..\..\`; got != want {
		t.Fatalf("nested panel prefix = %q, want %q", got, want)
	}
	if got, want := baseRefPrefix(filepath.Join(mods, "WWEmote", "user"), mods), baseRefUp; got != want {
		t.Fatalf("legacy panel prefix = %q, want %q", got, want)
	}
}

// One bucket whose base mesh is gone must not cost the other buckets their
// config. The skip names the ini that owned the bad bucket so the user can
// act on that mod instead of guessing from a pile of claimed files.
func TestDeltaTargetsLenientSkipsOnlyBadBucket(t *testing.T) {
	mods := t.TempDir()
	user := filepath.Join(mods, "WWEmote-v0.61", "WWEmote", "user")
	if err := os.MkdirAll(user, 0o755); err != nil {
		t.Fatal(err)
	}
	goodMesh := filepath.Join(mods, "Hsin", "good", "Meshes", "Position.buf")
	if err := os.MkdirAll(filepath.Dir(goodMesh), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goodMesh, make([]byte, 120), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"good_sparse.buf", "bad_sparse.buf"} {
		if err := os.WriteFile(filepath.Join(user, name), make([]byte, 120), 0o644); err != nil {
			t.Fatal(err)
		}
		mf, _ := json.Marshal(map[string]any{
			"format": "sparse-csr-v1", "vc": 10, "total_bytes": 120,
		})
		base := strings.TrimSuffix(name, ".buf")
		if err := os.WriteFile(filepath.Join(user, base+"_manifest.json"), mf, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := &Char{
		Label: "Hsin",
		PvbPaths: []string{
			`Resource\Mods\Hsin\good\mod.ini\PositionBuffer`,
			`Resource\Mods\Hsin\bad\mod.ini\PositionBuffer`,
		},
		ModVC:         map[string]int{"Hsin/good/mod.ini": 10, "Hsin/bad/mod.ini": 10},
		ModDeltaOrder: []string{"good", "bad"},
		ModDelta: map[string]*Delta{
			"good": {VC: 10, Sparse: "good_sparse.buf",
				BaseRef: `Hsin\good\Meshes\Position.buf`},
			// The mesh under Hsin/bad/Meshes is deliberately absent.
			"bad": {VC: 10, Sparse: "bad_sparse.buf",
				BaseRef: `Hsin\bad\Meshes\Position.buf`},
		},
	}
	targets, skips, err := c.DeltaTargetsLenient(user, mods)
	if err != nil {
		t.Fatalf("one missing base must not be fatal: %v", err)
	}
	if len(targets) != 1 || targets[0].Frag != "good" {
		t.Fatalf("good bucket was lost: %+v", targets)
	}
	if !strings.Contains(targets[0].Base, `..\..\..\Hsin\good`) {
		t.Fatalf("base prefix ignored panel depth: %q", targets[0].Base)
	}
	if len(skips) != 1 || skips[0].Frag != "bad" ||
		!strings.Contains(skips[0].ModFile, "Hsin/bad/mod.ini") {
		t.Fatalf("skip must name the bad mod file, got %+v", skips)
	}
}
