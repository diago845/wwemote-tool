package bake

import (
	"os"
	"path/filepath"
	"testing"
)

func newBaseFixture(t *testing.T) (mods, out, src string) {
	t.Helper()
	d := t.TempDir()
	mods = filepath.Join(d, "mods")
	src = filepath.Join(mods, "Hsin", "HsinMod", "Meshes", "Position.buf")
	out = filepath.Join(mods, "WWEmote", "user")
	for _, p := range []string{filepath.Dir(src), out} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return mods, out, src
}

// An exact-length source is referenced in place: the manifest carries a path
// relative to the mods root and the user layer holds no file at all.
func TestPlaceBaseReferencesExactSource(t *testing.T) {
	mods, out, src := newBaseFixture(t)
	writeMesh(t, src, 120, 0)

	name, ref, err := placeBase(src, out, "ep_mod_base_10.buf", 120, mods)
	if err != nil {
		t.Fatal(err)
	}
	if name != "" {
		t.Fatalf("base_buf = %q, want empty: a referenced base owns no file", name)
	}
	if want := `Hsin\HsinMod\Meshes\Position.buf`; ref != want {
		t.Fatalf("base_ref = %q, want %q", ref, want)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("the user layer should hold nothing for a referenced base, found %d", len(entries))
	}
	// The ini resolves "..\..\<ref>" from its own directory.
	if _, err := os.Stat(filepath.Join(out, "..", "..", ref)); err != nil {
		t.Fatalf("the reference does not resolve back to the mesh: %v", err)
	}
}

// A source with trailing vertices cannot be referenced: the base has to be
// exactly vc*12 bytes and nothing in the ini declares the size.
func TestPlaceBaseCopiesLongerSource(t *testing.T) {
	mods, out, src := newBaseFixture(t)
	writeMesh(t, src, 120+72, 0)

	name, ref, err := placeBase(src, out, "ep_mod_base_10.buf", 120, mods)
	if err != nil {
		t.Fatal(err)
	}
	if name != "ep_mod_base_10.buf" || ref != "" {
		t.Fatalf("got (%q, %q), want the owned file name and no reference", name, ref)
	}
	st, err := os.Stat(filepath.Join(out, name))
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != 120 {
		t.Fatalf("base is %d B, want 120: the export tail leaked in", st.Size())
	}
}

// Upgrading from a build that hard-linked the mesh must remove the duplicate
// it left behind, otherwise the point of the change is lost.
func TestPlaceBaseDropsStaleCopy(t *testing.T) {
	mods, out, src := newBaseFixture(t)
	writeMesh(t, src, 120, 0)
	writeMesh(t, filepath.Join(out, "ep_mod_base_10.buf"), 120, 5)

	if _, _, err := placeBase(src, out, "ep_mod_base_10.buf", 120, mods); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"ep_mod_base_10.buf", "ep_mod_base_10.buf.old"} {
		if _, err := os.Stat(filepath.Join(out, p)); err == nil {
			t.Fatalf("%s is still there: the old copy must go", p)
		}
	}
}

// Re-running the same configuration twice must leave the same text and no
// leftovers, because the panel re-bakes on every configuration.
func TestPlaceBaseIsIdempotent(t *testing.T) {
	mods, out, src := newBaseFixture(t)
	writeMesh(t, src, 120, 0)

	_, first, err := placeBase(src, out, "ep_mod_base_10.buf", 120, mods)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_, again, err := placeBase(src, out, "ep_mod_base_10.buf", 120, mods)
		if err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		if again != first {
			t.Fatalf("pass %d: %q != %q", i, again, first)
		}
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("the user layer should hold nothing, found %d entries", len(entries))
	}
}

func TestPlaceBaseRejectsShortSource(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "Position.buf")
	out := filepath.Join(d, "user")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMesh(t, src, 60, 0)
	if _, _, err := placeBase(src, out, "ep_mod_base_10.buf", 120, filepath.Join(d, "mods")); err == nil {
		t.Fatal("a source too small for vc must be an error, not a partial base")
	}
}

// A mesh outside the mods tree has no reference to write, so it is still
// copied: the ini must never point somewhere the game cannot resolve.
func TestPlaceBaseCopiesSourceOutsideMods(t *testing.T) {
	mods, out, _ := newBaseFixture(t)
	src := filepath.Join(filepath.Dir(mods), "elsewhere", "Position.buf")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	writeMesh(t, src, 120, 0)

	name, ref, err := placeBase(src, out, "ep_mod_base_10.buf", 120, mods)
	if err != nil {
		t.Fatal(err)
	}
	if name != "ep_mod_base_10.buf" || ref != "" {
		t.Fatalf("got (%q, %q), want the owned file name and no reference", name, ref)
	}
	if st, err := os.Stat(filepath.Join(out, name)); err != nil || st.Size() != 120 {
		t.Fatalf("a source outside the mods tree must be copied: %v", err)
	}
}
