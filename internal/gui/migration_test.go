package gui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLegacyState(t *testing.T) {
	legacy := t.TempDir()
	newRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", legacy)
	t.Setenv("WWEMOTE_STATE_DIR", newRoot)

	old := filepath.Join(legacy, "WWEmote")
	os.MkdirAll(old, 0o755)
	os.WriteFile(filepath.Join(old, "language.json"), []byte(`{"language":"ja"}`), 0o644)
	os.WriteFile(filepath.Join(old, "mods.txt"), []byte("C:\\mods"), 0o644)
	os.WriteFile(filepath.Join(old, "WWEmote.log"), []byte("old log"), 0o644)
	os.WriteFile(filepath.Join(old, "diag_20260101_000000.txt"), []byte("old diag"), 0o644)

	MigrateLegacyState()

	// Settings moved to the new root.
	if got := savedLanguage(); got != "ja" {
		t.Fatalf("language after migrate = %q, want ja", got)
	}
	if b, err := os.ReadFile(filepath.Join(newRoot, "mods.txt")); err != nil || string(b) != "C:\\mods" {
		t.Fatalf("mods.txt after migrate = %q, %v", b, err)
	}
	// Log + diag share the same data folder as the settings.
	if b, err := os.ReadFile(filepath.Join(diagDir(), "WWEmote.log")); err != nil || string(b) != "old log" {
		t.Fatalf("log after migrate = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(diagDir(), "diag_20260101_000000.txt")); err != nil {
		t.Fatalf("diag report after migrate: %v", err)
	}
}

func TestMigrateNeverOverwrites(t *testing.T) {
	legacy := t.TempDir()
	newRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", legacy)
	t.Setenv("WWEMOTE_STATE_DIR", newRoot)

	old := filepath.Join(legacy, "WWEmote")
	os.MkdirAll(old, 0o755)
	os.WriteFile(filepath.Join(old, "language.json"), []byte(`{"language":"en"}`), 0o644)
	os.MkdirAll(newRoot, 0o755)
	os.WriteFile(filepath.Join(newRoot, "language.json"), []byte(`{"language":"ko"}`), 0o644)

	MigrateLegacyState()

	if got := savedLanguage(); got != "ko" {
		t.Fatalf("language = %q, want ko (new setting must win)", got)
	}
}

func TestStatePathsUseOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WWEMOTE_STATE_DIR", dir)
	if got := dataRoot(); got != dir {
		t.Fatalf("dataRoot = %q, want %q", got, dir)
	}
	want := dir
	if got := diagDir(); got != want {
		t.Fatalf("diagDir = %q, want %q", got, want)
	}
}
