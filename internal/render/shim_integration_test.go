package render

import (
	"strings"
	"testing"

	"wwemote/internal/panel"
)

// TestGenCollectsShimsEndToEnd drives the real RenderMods path with a char
// whose resource paths contain spaces, then checks that the shim files were
// collected and the main ini routed its references through them. It also
// proves a second render does not accumulate stale shims.
func TestGenCollectsShimsEndToEnd(t *testing.T) {
	spacedSk := `Resource\Mods\Lynae\Lynae skin mod\mod.ini\CustomShapeKeyValuesRW`
	spacedPvb := `Resource\Mods\Lynae\Lynae skin mod\mod.ini\PositionBuffer`
	c := &panel.Char{
		Cid: 1, Name: "hsin", Label: "Hsin",
		AnchorHash: "aaaabbbb", BodyHash: "ccccdddd", Pose: true,
		SkPaths:  []string{spacedSk},
		PvbPaths: []string{spacedPvb},
	}
	tbl := &panel.Table{K: 1, Chars: []*panel.Char{c}}
	g := New(tbl, t.TempDir(), "")

	mods, err := g.RenderMods()
	if err != nil {
		t.Fatalf("RenderMods: %v", err)
	}
	// The main ini must not carry the spaced path in any value position.
	if strings.Contains(mods, "cs-u5 = "+spacedSk) {
		t.Error("spaced sk path leaked into cs-u5 value")
	}
	if strings.Contains(mods, "copy "+spacedPvb) {
		t.Error("spaced pvb path leaked into copy source")
	}
	if !strings.Contains(mods, "run = CommandList\\Mods\\Lynae\\Lynae skin mod\\mod.ini\\WWEmoteShim") {
		t.Error("main ini does not route through a shim command list")
	}

	files := g.RenderShims()
	if len(files) != 1 {
		t.Fatalf("got %d shim files, want 1", len(files))
	}
	shim := files[0].Text
	if !strings.Contains(shim, "namespace = Mods\\Lynae\\Lynae skin mod\\mod.ini\n") {
		t.Errorf("shim namespace line missing:\n%s", shim)
	}
	if !strings.Contains(shim, "cs-u5 = ResourceCustomShapeKeyValuesRW") {
		t.Errorf("shim short bind missing:\n%s", shim)
	}
	if !strings.Contains(shim, "ResourceEpPosedSrc1 = copy ResourcePositionBuffer") {
		t.Errorf("shim short copy missing:\n%s", shim)
	}

	// A second render rebuilds the shim set from scratch rather than appending.
	if _, err := g.RenderMods(); err != nil {
		t.Fatalf("second RenderMods: %v", err)
	}
	if got := len(g.RenderShims()); got != 1 {
		t.Fatalf("second render collected %d shim files, want 1", got)
	}
}

// TestGenNoShimsForSafePaths pins the common case: with no breaking character
// the generator collects nothing and the output is unchanged.
func TestGenNoShimsForSafePaths(t *testing.T) {
	c := &panel.Char{
		Cid: 1, Name: "hsin", Label: "Hsin",
		AnchorHash: "aaaabbbb", BodyHash: "ccccdddd",
		SkPaths:  []string{`Resource\Mods\Hsin\HsinMod-thicc\mod.ini\CustomShapeKeyValuesRW`},
		PvbPaths: []string{`Resource\Mods\Hsin\HsinMod-thicc\mod.ini\PositionBuffer`},
	}
	g := New(&panel.Table{K: 1, Chars: []*panel.Char{c}}, t.TempDir(), "")
	mods, err := g.RenderMods()
	if err != nil {
		t.Fatalf("RenderMods: %v", err)
	}
	if !strings.Contains(mods, `cs-u5 = Resource\Mods\Hsin\HsinMod-thicc\mod.ini\CustomShapeKeyValuesRW`) {
		t.Error("safe path should be emitted verbatim")
	}
	if got := len(g.RenderShims()); got != 0 {
		t.Fatalf("safe paths produced %d shim files, want 0", got)
	}
}
