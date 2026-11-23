package gui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanStaleFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}

	// Present on disk.
	write("hsin.mods.json")
	write("hsin_mod_100_sparse.buf")
	write("hsin_mod_100_sparse_manifest.json")
	write("hsin_mod_200_sparse.buf")
	write("hsin_mod_200_sparse_manifest.json")
	write("oldchar.mods.json")       // char no longer installed
	write("gone_mod_999_sparse.buf") // orphan buf, no set references it
	write("gone_mod_999_sparse_manifest.json")
	write("unrelated.json") // must survive

	keepSparse := map[string]bool{"hsin_mod_100_sparse.buf": true}
	keepChars := map[string]bool{"hsin": true}

	n, gone := cleanStaleFiles(dir, keepSparse, keepChars)
	if n != 5 {
		t.Fatalf("removed %d files (%v), want 5", n, gone)
	}

	// Kept.
	for _, name := range []string{
		"hsin.mods.json", "hsin_mod_100_sparse.buf",
		"hsin_mod_100_sparse_manifest.json", "unrelated.json",
	} {
		if !exists(name) {
			t.Errorf("%s should have been kept", name)
		}
	}
	// Removed.
	for _, name := range []string{
		"hsin_mod_200_sparse.buf", "hsin_mod_200_sparse_manifest.json",
		"oldchar.mods.json", "gone_mod_999_sparse.buf",
		"gone_mod_999_sparse_manifest.json",
	} {
		if exists(name) {
			t.Errorf("%s should have been removed", name)
		}
	}
}

func TestCleanStaleNoopWhenEverythingKept(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hsin.mods.json"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "hsin_mod_100_sparse.buf"), []byte("x"), 0o644)

	n, _ := cleanStaleFiles(dir,
		map[string]bool{"hsin_mod_100_sparse.buf": true},
		map[string]bool{"hsin": true})
	if n != 0 {
		t.Fatalf("removed %d files with everything kept, want 0", n)
	}
}

// A baseline JSON that is not a sparse manifest must never be mistaken for a
// stale pair, even when it shares the _manifest.json suffix pattern loosely.
func TestCleanStaleIgnoresSubdirs(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "old_mod_1_sparse.buf")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	n, _ := cleanStaleFiles(dir, nil, nil)
	if n != 0 {
		t.Fatalf("removed %d files, want 0 (subdir must be ignored)", n)
	}
}
