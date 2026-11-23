package scan

import (
	"os"
	"path/filepath"
	"testing"
)

// The preamble is the only place 3DMigoto reads "namespace =" from, and only
// before the first section header.
func TestIniNamespace(t *testing.T) {
	cases := []struct{ name, text, want string }{
		{"plain", "namespace = HsinTmp\n\n[Constants]\n", "HsinTmp"},
		{"tight", "namespace=time\n[S]\n", "time"},
		{"after comment", "; WWMI BETA-5 INI\r\n; state\r\nnamespace = A\\B\r\n[S]\r\n", `A\B`},
		{"bom", "\ufeffnamespace = cx_Mod051\r\n[S]\r\n", "cx_Mod051"},
		{"condition first", "condition = $x\nnamespace = Ns\n[S]\n", "Ns"},
		{"after section", "[Constants]\nnamespace = Late\n", ""},
		{"absent", "; nothing\n[Constants]\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := iniNamespace(c.text); got != c.want {
				t.Fatalf("iniNamespace = %q, want %q", got, c.want)
			}
		})
	}
}

// A file that declares a namespace has its sections renamed, so the reference
// the panel writes must use the namespace instead of the ini's path.
func TestUserLayerUsesNamespaceWhenDeclared(t *testing.T) {
	mods := t.TempDir()
	dir := filepath.Join(mods, "Hsin", "TestMod")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ini := filepath.Join(dir, "1.ini")
	text := "namespace = HsinTmp\n\n" +
		"[Constants]\n" +
		"$\\WWMIv1\\custom_vertex_count = 10\n\n" +
		"[ResourcePositionBuffer]\n" +
		"filename = Meshes/Position.buf\n"
	if err := os.WriteFile(ini, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	rec, err := scanIni(ini)
	if err != nil {
		t.Fatal(err)
	}
	rec.RelDisk = "Hsin/TestMod/1.ini"
	rec.Kind = KindFinished
	rec.CVCVals = []int{10}
	rec.PosbufFile = "Meshes/Position.buf"

	rel := "Hsin/TestMod/1.ini"
	res := &ClaimResult{Found: map[string]*Rec{rel: rec}, FoundKeys: []string{rel}}
	layer, errs := UserLayer(res, mods, false)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(layer.SkPaths) != 1 || len(layer.PvbPaths) != 1 {
		t.Fatalf("want one path each, got %v / %v", layer.SkPaths, layer.PvbPaths)
	}
	if want := `Resource\HsinTmp\` + ResSK; layer.SkPaths[0] != want {
		t.Fatalf("sk path = %q, want %q", layer.SkPaths[0], want)
	}
	if want := `Resource\HsinTmp\` + ResPVB; layer.PvbPaths[0] != want {
		t.Fatalf("pvb path = %q, want %q", layer.PvbPaths[0], want)
	}
}

// Without a declaration the path form stays: it is the name the game derives
// from where the ini sits, and the fallback the change keeps.
func TestUserLayerFallsBackToPathForm(t *testing.T) {
	mods := t.TempDir()
	dir := filepath.Join(mods, "Hsin", "TestMod")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ini := filepath.Join(dir, "1.ini")
	text := "; WWMI BETA-5 INI\n\n" +
		"[Constants]\n" +
		"$\\WWMIv1\\custom_vertex_count = 10\n\n" +
		"[ResourcePositionBuffer]\n" +
		"filename = Meshes/Position.buf\n"
	if err := os.WriteFile(ini, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	rec, err := scanIni(ini)
	if err != nil {
		t.Fatal(err)
	}
	rec.RelDisk = "Hsin/TestMod/1.ini"
	rec.Kind = KindFinished
	rec.CVCVals = []int{10}
	rec.PosbufFile = "Meshes/Position.buf"

	rel := "Hsin/TestMod/1.ini"
	res := &ClaimResult{Found: map[string]*Rec{rel: rec}, FoundKeys: []string{rel}}
	layer, errs := UserLayer(res, mods, false)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if want := PathHeadMods + `Hsin\TestMod\1.ini\` + ResSK; layer.SkPaths[0] != want {
		t.Fatalf("sk path = %q, want %q", layer.SkPaths[0], want)
	}
}
