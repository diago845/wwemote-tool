package bake

import (
	"os"
	"path/filepath"
	"testing"
)

func writeMesh(t *testing.T, p string, n int, seed byte) {
	t.Helper()
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i) + seed
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	ai, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	bi, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(ai, bi)
}

// The panel keeps no second copy of a mesh the mod tree already owns, so an
// exact-length source must end up as one file with two names.
func TestPlaceMeshLinksExactSource(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "Position.buf")
	dst := filepath.Join(d, "ep_mod_base_10.buf")
	writeMesh(t, src, 120, 0)
	if err := placeMesh(src, dst, 120); err != nil {
		t.Fatal(err)
	}
	if !sameFile(t, src, dst) {
		t.Fatal("exact-length source should be hard linked, not copied")
	}
}

// An export with trailing vertices cannot be linked: the link would carry the
// tail into the base mesh, and nothing in the ini declares the size.
func TestPlaceMeshCopiesLongerSource(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "Position.buf")
	dst := filepath.Join(d, "ep_mod_base_10.buf")
	writeMesh(t, src, 120+72, 0)
	if err := placeMesh(src, dst, 120); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != 120 {
		t.Fatalf("base is %d B, want 120: the export tail leaked in", st.Size())
	}
	if sameFile(t, src, dst) {
		t.Fatal("a source with a tail must be copied and truncated, never linked")
	}
}

func TestPlaceMeshRejectsShortSource(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "Position.buf")
	dst := filepath.Join(d, "ep_mod_base_10.buf")
	writeMesh(t, src, 60, 0)
	if err := placeMesh(src, dst, 120); err == nil {
		t.Fatal("a source too small for vc must be an error, not a partial base")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatal("no base should be written when the source is too short")
	}
}

// Upgrading from a version that copied the mesh must not leave the copy in
// place: the point of the change is that the duplicate disappears.
func TestPlaceMeshReplacesStaleCopy(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "Position.buf")
	dst := filepath.Join(d, "ep_mod_base_10.buf")
	writeMesh(t, src, 120, 0)
	writeMesh(t, dst, 120, 3)
	if sameFile(t, src, dst) {
		t.Fatal("precondition: the two files must start out independent")
	}
	if err := placeMesh(src, dst, 120); err != nil {
		t.Fatal(err)
	}
	if !sameFile(t, src, dst) {
		t.Fatal("an existing copy should be replaced by a link")
	}
}

func TestPlaceMeshIsIdempotent(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "Position.buf")
	dst := filepath.Join(d, "ep_mod_base_10.buf")
	writeMesh(t, src, 120, 0)
	for i := 0; i < 3; i++ {
		if err := placeMesh(src, dst, 120); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
	}
	if !sameFile(t, src, dst) {
		t.Fatal("repeated passes should keep the link")
	}
	if _, err := os.Stat(dst + ".old"); err == nil {
		t.Fatal("repeated passes must not leave .old files behind")
	}
}

// Reinstalling a mod drops the old file and writes a new one, so the stored
// link points at an inode nothing else references. The next run has to notice
// and link the current file.
func TestPlaceMeshRelinksAfterSourceReplaced(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "Position.buf")
	dst := filepath.Join(d, "ep_mod_base_10.buf")
	writeMesh(t, src, 120, 0)
	if err := placeMesh(src, dst, 120); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	writeMesh(t, src, 120, 7)
	if err := placeMesh(src, dst, 120); err != nil {
		t.Fatal(err)
	}
	if !sameFile(t, src, dst) {
		t.Fatal("a replaced source must be linked again, not left stale")
	}
	a, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("base must hold the current mesh after a reinstall")
	}
}
