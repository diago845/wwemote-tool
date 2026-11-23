package modpath

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStripDisabled(t *testing.T) {
	// Backslash style (base_ref / mods.json) keeps backslashes.
	in := `Hsin\DISABLED_HsinMod thicc\Hsin Mod\Meshes\Position.buf`
	want := `Hsin\HsinMod thicc\Hsin Mod\Meshes\Position.buf`
	if got := StripDisabled(in); got != want {
		t.Fatalf("strip backslash = %q, want %q", got, want)
	}
	// Forward-slash style (pvb paths) keeps forward slashes.
	in2 := "Hsin/DISABLED_HsinMod/HsinMod/1.ini"
	want2 := "Hsin/HsinMod/HsinMod/1.ini"
	if got := StripDisabled(in2); got != want2 {
		t.Fatalf("strip slash = %q, want %q", got, want2)
	}
	// Every prefixed segment, case-insensitive; no marker is a no-op.
	if got := StripDisabled(`DISABLED_A\B\disabled_C`); got != `A\B\C` {
		t.Fatalf("strip multi = %q", got)
	}
	if got := StripDisabled(`Hsin\HsinMod thicc\1.ini`); got != `Hsin\HsinMod thicc\1.ini` {
		t.Fatalf("no-op changed = %q", got)
	}
}

func TestResolveOnDisk(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DISABLED_ restore uses platform separators")
	}
	root := t.TempDir()
	real := filepath.Join(root, "Hsin", "DISABLED_HsinMod thicc", "Hsin Mod", "1.ini")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Unprefixed logical path resolves back to the prefixed file on disk.
	logical := filepath.Join(root, "Hsin", "HsinMod thicc", "Hsin Mod", "1.ini")
	got, ok := ResolveOnDisk(logical)
	if !ok || got != real {
		t.Fatalf("restore = %q,%v want %q,true", got, ok, real)
	}
	// A genuinely renamed mod does not resolve (must still be reported dead).
	other := filepath.Join(root, "Hsin", "Renamed Mod", "Hsin Mod", "1.ini")
	if _, ok := ResolveOnDisk(other); ok {
		t.Fatal("renamed mod must not resolve")
	}
	// Direct hit needs no restore.
	if got, ok := ResolveOnDisk(real); !ok || got != real {
		t.Fatalf("direct = %q,%v", got, ok)
	}
	// A directory is not a valid file target.
	dir := filepath.Join(root, "Hsin", "HsinMod thicc", "Hsin Mod")
	if _, ok := ResolveOnDisk(dir); ok {
		t.Fatal("directory must not resolve as a file")
	}
}

func TestPresentOnDiskBothToggleDirections(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DISABLED_ toggle uses platform separators")
	}
	root := t.TempDir()
	unmarked := filepath.Join(root, "Hsin", "HsinMod thicc", "Hsin Mod", "Meshes", "Position.buf")
	if err := os.MkdirAll(filepath.Dir(unmarked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unmarked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	marked := filepath.Join(root, "Hsin", "DISABLED_HsinMod thicc", "Hsin Mod", "Meshes", "Position.buf")

	// Manifest recorded the DISABLED_ path (baked while disabled), but the user
	// has since enabled the mod: only the unmarked file is on disk.
	if !PresentOnDisk(marked) {
		t.Fatal("recorded DISABLED_ path must resolve to the now-enabled file")
	}
	// Manifest recorded the bare path (baked while enabled), mod now disabled.
	if !PresentOnDisk(unmarked) {
		t.Fatal("recorded bare path must resolve to the now-disabled file")
	}
	// A genuine rename breaks the equivalence and must invalidate the cache.
	renamed := filepath.Join(root, "Hsin", "Renamed Mod", "Hsin Mod", "Meshes", "Position.buf")
	if PresentOnDisk(renamed) {
		t.Fatal("a genuinely renamed mod must not resolve")
	}
}
