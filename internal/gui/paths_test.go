package gui

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveModsNestedFolders pins the rule that a mod collection or an
// individual mod folder inside Mods resolves back to the real Mods root. The
// address bar must accept those, not just the Mods folder itself.
func TestResolveModsNestedFolders(t *testing.T) {
	root := t.TempDir()
	wwmi := filepath.Join(root, "WWMI")
	mods := filepath.Join(wwmi, "Mods")
	coll := filepath.Join(mods, "Hsin")             // mod collection
	single := filepath.Join(mods, "MyMod")       // standalone mod folder
	deep := filepath.Join(coll, "HsinMod-thicc") // nested inside a collection
	for _, d := range []string{mods, coll, single, deep} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(wwmi, "d3dx.ini"), []byte("[Loader]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		in        string
		wantScope string // empty means "whole Mods tree"
	}{
		{"wwmi root", wwmi, ""},
		{"mods root", mods, ""},
		{"collection", coll, coll},
		{"standalone mod", single, single},
		{"deep nested mod", deep, deep},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotMods, gotWwmi, gotScope, err := resolveMods(tc.in)
			if err != nil {
				t.Fatalf("resolveMods(%q) failed: %v", tc.in, err)
			}
			if gotMods != mods {
				t.Errorf("mods root = %q, want %q", gotMods, mods)
			}
			if gotWwmi != wwmi {
				t.Errorf("wwmi = %q, want %q", gotWwmi, wwmi)
			}
			want := tc.wantScope
			if want == "" {
				want = mods // whole-tree form returns modsRoot as the scan root
			}
			if gotScope != want {
				t.Errorf("scan root = %q, want %q", gotScope, want)
			}
		})
	}
}

// TestResolveModsRejectsOutsideFolder makes sure the ancestor walk cannot
// swallow unrelated folders that merely sit under some other d3dx.ini.
func TestResolveModsRejectsOutsideFolder(t *testing.T) {
	root := t.TempDir()
	// A d3dx.ini with no Mods/ child: picking a sibling must be rejected.
	wwmi := filepath.Join(root, "WWMI")
	if err := os.MkdirAll(wwmi, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(wwmi, "d3dx.ini"), []byte(""), 0o644)
	other := filepath.Join(root, "Somewhere")
	os.MkdirAll(other, 0o755)
	if _, _, _, err := resolveMods(other); err == nil {
		t.Fatal("resolveMods should reject a folder outside a WWMI/Mods tree")
	}
}
