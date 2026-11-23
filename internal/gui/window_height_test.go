package gui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowHeightMemory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WWEMOTE_STATE_DIR", dir)

	if got := SavedWindowHeight(); got != 0 {
		t.Fatalf("fresh install should have no remembered height, got %d", got)
	}

	SaveWindowHeight(520)
	if got := SavedWindowHeight(); got != 520 {
		t.Fatalf("remembered height = %d, want 520", got)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "winheight.txt"))
	if err != nil || string(raw) != "520" {
		t.Fatalf("winheight.txt = %q, err = %v", raw, err)
	}

	// Corrupted content must fall back, not crash or return a wild size.
	if err := os.WriteFile(filepath.Join(dir, "winheight.txt"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := SavedWindowHeight(); got != 0 {
		t.Fatalf("corrupt height should read as 0, got %d", got)
	}
}

func TestClampWindowHeight(t *testing.T) {
	cases := map[int]int{
		-50:   minWindowHeight,
		0:     minWindowHeight,
		319:   minWindowHeight,
		320:   minWindowHeight,
		400:   400,
		1000:  1000,
		1200:  maxWindowHeight,
		99999: maxWindowHeight,
	}
	for in, want := range cases {
		if got := ClampWindowHeight(in); got != want {
			t.Errorf("ClampWindowHeight(%d) = %d, want %d", in, got, want)
		}
	}
}
