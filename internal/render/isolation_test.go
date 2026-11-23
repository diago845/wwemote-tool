package render

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wwemote/internal/panel"
)

func testChar() panel.Char {
	return panel.Char{
		Cid: 3, Name: "hsin", Label: "Hsin",
		AnchorHash: "aaaabbbb", BodyHash: "ccccdddd", Pose: true,
	}
}

func mustTable(c *panel.Char) *panel.Table {
	return &panel.Table{K: 1, Chars: []*panel.Char{c}}
}

func sectionBody(text, name string) string {
	start := strings.Index(text, "["+name+"]")
	if start < 0 {
		return ""
	}
	rest := text[start+len(name)+2:]
	if end := strings.Index(rest, "\n["); end >= 0 {
		return rest[:end]
	}
	return rest
}

func bindBody(section, sk string) string {
	start := strings.Index(section, "cs-u5 = "+sk+"\n")
	if start < 0 {
		return ""
	}
	rest := section[start:]
	// A table's block ends at the next top-level (four-space) binding. The
	// per-recipe rebinds are indented deeper, so they stay inside this block.
	if end := strings.Index(rest, "\n    cs-u5 = "); end >= 0 {
		return rest[:end]
	}
	return rest
}

// One set whose pose_exclude entry cannot be mapped to a real pvb path (a
// namespace finished mod: its pvb lives at Resource\<ns>\..., which pathOf
// deliberately does not treat as a bake target) used to fail blkRouteR and
// cost every other set its config. The render must now keep the good sets,
// emit their pose override, and report the dropped set in Gen.Skipped.
func TestRenderDropsUnmatchedPoseExcludeKeepsOthers(t *testing.T) {
	ch := testChar()
	c := &ch
	c.PvbPaths = []string{`Resource\Mods\Hsin\good\mod.ini\PositionBuffer`}
	c.SkPaths = []string{`Resource\Mods\Hsin\good\mod.ini\CustomShapeKeyValuesRW`}
	c.PoseExclude = []string{
		`Hsin/good/mod.ini`,               // maps to a real pvb path -> kept
		`Hsin/namespace_finished/mod.ini`, // no matching pvb path -> dropped
	}
	c.PoseExcludeVC = map[string]int{
		`Hsin/good/mod.ini`:               100,
		`Hsin/namespace_finished/mod.ini`: 200,
	}
	tbl := mustTable(c)
	g := New(tbl, t.TempDir(), "")
	mods, err := g.RenderMods()
	if err != nil {
		t.Fatalf("one unmatched pose_exclude must not abort the render, got: %v", err)
	}
	if !strings.Contains(mods, "hash = "+c.AnchorHash) {
		t.Fatal("render lost the pose override for the good set")
	}
	if len(g.Skipped) != 1 || !strings.Contains(g.Skipped[0], "namespace_finished") {
		t.Fatalf("expected the unmatched set in Skipped, got %v", g.Skipped)
	}
}

// Same isolation guarantee for pos_variants: an unmatched variant entry must
// drop only that entry, not abort the whole render.
func TestRenderDropsUnmatchedPosVariantKeepsOthers(t *testing.T) {
	ch := testChar()
	c := &ch
	c.PvbPaths = []string{`Resource\Mods\Hsin\good\mod.ini\PositionBuffer`}
	c.SkPaths = []string{`Resource\Mods\Hsin\good\mod.ini\CustomShapeKeyValuesRW`}
	c.PosVariants = map[string][]string{
		`Hsin/good/mod.ini`:              {`Resource\Mods\Hsin\good\mod.ini\PositionBuffer`, "base"},
		`Hsin/unmatched_variant/mod.ini`: {`Resource\Mods\gone\mod.ini\PositionBuffer`, "base"},
	}
	tbl := mustTable(c)
	g := New(tbl, t.TempDir(), "")
	mods, err := g.RenderMods()
	if err != nil {
		t.Fatalf("one unmatched pos_variant must not abort the render, got: %v", err)
	}
	if !strings.Contains(mods, "hash = "+c.AnchorHash) {
		t.Fatal("render lost the pose override")
	}
	found := false
	for _, s := range g.Skipped {
		if strings.Contains(s, "unmatched_variant") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the unmatched variant in Skipped, got %v", g.Skipped)
	}
}

// A bucket whose base mesh is gone used to fail WriteMods after every other
// character had already been written, so the whole configure was reported as
// failed. The renderer must drop that one set, keep the good bucket, and name
// the mod file that could not be configured.
func TestRenderDropsMissingBaseKeepsOtherBucket(t *testing.T) {
	mods := t.TempDir()
	user := filepath.Join(mods, "WWEmote-v0.61", "WWEmote", "user")
	if err := os.MkdirAll(user, 0o755); err != nil {
		t.Fatal(err)
	}
	mesh := filepath.Join(mods, "Hsin", "good", "Meshes", "Position.buf")
	if err := os.MkdirAll(filepath.Dir(mesh), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mesh, make([]byte, 120), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(user, "good_sparse.buf"), make([]byte, 120), 0o644); err != nil {
		t.Fatal(err)
	}
	mf, _ := json.Marshal(map[string]any{
		"format": "sparse-csr-v1", "vc": 10, "total_bytes": 120,
	})
	if err := os.WriteFile(filepath.Join(user, "good_sparse_manifest.json"), mf, 0o644); err != nil {
		t.Fatal(err)
	}

	ch := testChar()
	c := &ch
	c.PvbPaths = []string{
		`Resource\Mods\Hsin\good\mod.ini\PositionBuffer`,
		`Resource\Mods\Hsin\bad\mod.ini\PositionBuffer`,
	}
	c.SkPaths = []string{
		`Resource\Mods\Hsin\good\mod.ini\CustomShapeKeyValuesRW`,
		`Resource\Mods\Hsin\bad\mod.ini\CustomShapeKeyValuesRW`,
	}
	c.ModVC = map[string]int{"Hsin/good/mod.ini": 10, "Hsin/bad/mod.ini": 11}
	c.ModDeltaOrder = []string{"good", "bad"}
	c.ModDelta = map[string]*panel.Delta{
		"good": {VC: 10, Sparse: "good_sparse.buf",
			BaseRef: `Hsin\good\Meshes\Position.buf`},
		"bad": {VC: 11, Sparse: "bad_sparse.buf",
			BaseRef: `Hsin\bad\Meshes\Position.buf`},
	}
	g := New(mustTable(c), user, "", mods)
	modsINI, err := g.RenderMods()
	if err != nil {
		t.Fatalf("one missing base must not abort the render: %v", err)
	}
	if !strings.Contains(modsINI, "$ep_bkt3 = 10") || strings.Contains(modsINI, "$ep_bkt3 = 11") {
		t.Fatal("good bucket lost or bad bucket still rendered")
	}
	found := false
	for _, s := range g.Skipped {
		if strings.Contains(s, "Hsin/bad/mod.ini") && strings.Contains(s, "set") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Skipped must name the failed mod file, got %v", g.Skipped)
	}
}

// A finished-only namespace mod (V3.8) has no sparse delta. Its pose_exclude
// key is the namespace resource path itself, which pathOf must resolve so the
// set is excluded from the bake write-back -- writing a finished mod's own
// baked mesh back would double-apply its shape keys and tear the model. The
// U5 value binding must still be emitted so the emotes work.
func TestRenderFinishedNamespaceNotWrittenBack(t *testing.T) {
	ch := testChar()
	c := &ch
	pvb := `Resource\nm_HsinMD1\PositionBuffer`
	c.PvbPaths = []string{pvb}
	c.SkPaths = []string{`Resource\nm_HsinMD1\CustomShapeKeyValuesRW`}
	c.PoseExclude = []string{pvb}
	c.PoseExcludeVC = map[string]int{pvb: 193205}
	tbl := mustTable(c)
	g := New(tbl, t.TempDir(), "")
	mods, err := g.RenderMods()
	if err != nil {
		t.Fatalf("finished-only render: %v", err)
	}
	if !strings.Contains(mods, "hash = "+c.AnchorHash) {
		t.Fatal("render lost the pose override for the finished set")
	}
	if !strings.Contains(mods, "cs-u5 = "+c.SkPaths[0]) {
		t.Fatal("render lost the U5 value binding for the finished set")
	}
	if n := strings.Count(mods, pvb+" = copy ResourceEpPosedRW"); n != 0 {
		t.Fatalf("finished pvb was written back %d times -- would double-apply shape keys", n)
	}
	if len(g.Skipped) != 0 {
		t.Fatalf("finished set should resolve cleanly, got Skipped=%v", g.Skipped)
	}
}

// Two finished namespace sets (V3.8 vc=193205 and hsinnv vc=269832) are both
// claimed for the same character. Each needs to be skipped by the pose apply
// dispatch, using its own vertex count. The old vcOf required exactly one vc
// and dropped the whole dispatch, which cost every other set its pose apply.
func TestRenderTwoFinishedSetsEmitOrDispatch(t *testing.T) {
	ch := testChar()
	c := &ch
	pvbV38 := `Resource\nm_HsinMD1\PositionBuffer`
	pvbHsinnv := `Resource\Hsin\PositionBuffer`
	c.PvbPaths = []string{pvbV38, pvbHsinnv}
	c.SkPaths = []string{
		`Resource\nm_HsinMD1\CustomShapeKeyValuesRW`,
		`Resource\Hsin\CustomShapeKeyValuesRW`,
	}
	c.PoseExclude = []string{pvbV38, pvbHsinnv}
	c.PoseExcludeVC = map[string]int{pvbV38: 193205, pvbHsinnv: 269832}
	tbl := mustTable(c)
	g := New(tbl, t.TempDir(), "")
	mods, err := g.RenderMods()
	if err != nil {
		t.Fatalf("two finished sets must not abort render: %v", err)
	}
	// The skip dispatch must test BOTH vertex counts with ||.
	if !strings.Contains(mods, `custom_vertex_count == 193205 || $\WWMIv1\custom_vertex_count == 269832`) &&
		!strings.Contains(mods, `custom_vertex_count == 269832 || $\WWMIv1\custom_vertex_count == 193205`) {
		t.Fatal("expected an || dispatch over both finished vc's; dispatch was dropped or single")
	}
	// This build does not adapt finished sets: the panel gate must stay closed,
	// so neither finished vc may be published into ep_bkt3.
	for _, vc := range []int{193205, 269832} {
		if strings.Contains(mods, fmt.Sprintf("$ep_bkt3 = %d", vc)) {
			t.Fatalf("finished vc %d must NOT satisfy the panel gate in this build", vc)
		}
	}
	for _, s := range g.Skipped {
		if strings.Contains(s, "cannot pick a skip vertex count") {
			t.Fatalf("vcOf ambiguity regressed: %v", s)
		}
	}
}

// A raw delta and a finished skip can share the exact same pvb path when two
// mods declare the same namespace (a0e72 raw vc=75749, hsinnv finished
// vc=269832, both Resource\Hsin\PositionBuffer). The collision check must be
// by vertex count: only a delta whose vc equals a skipped set's vc is dropped.
// Otherwise the raw set's freshly baked delta would be thrown away.
func TestRenderRawDeltaSharesPathWithFinishedSkip(t *testing.T) {
	user := t.TempDir()
	// Prepare a valid sparse delta + base mesh so DeltaTargetsLenient keeps it.
	// The base mesh must be vc*12 bytes to share the delta's vertex space.
	base := "hsin_mod_75749"
	meshBytes := make([]byte, 75749*12)
	os.WriteFile(filepath.Join(user, base+"_base.buf"), meshBytes, 0o644)
	os.WriteFile(filepath.Join(user, "a0e72_sparse.buf"), make([]byte, 120), 0o644)
	os.WriteFile(filepath.Join(user, "a0e72_sparse_manifest.json"),
		[]byte(`{"format":"sparse-csr-v1","vc":75749,"total_bytes":120}`), 0o644)
	ch := testChar()
	c := &ch
	shared := `Resource\Hsin\PositionBuffer`
	c.PvbPaths = []string{shared}
	c.SkPaths = []string{`Resource\Hsin\CustomShapeKeyValuesRW`}
	c.ModVC = map[string]int{"heart/a0e72/mod.ini": 75749}
	// The finished set (vc 269832) opted out of baking.
	c.PoseExclude = []string{shared}
	c.PoseExcludeVC = map[string]int{shared: 269832}
	// The raw set (vc 75749) has a delta targeting the same resource path.
	c.ModDelta = map[string]*panel.Delta{
		"Hsin": {VC: 75749, Sparse: "a0e72_sparse.buf", Base: base + "_base.buf"},
	}
	tbl := mustTable(c)
	g := New(tbl, user, "")
	mods, err := g.RenderMods()
	if err != nil {
		t.Fatalf("render with shared path: %v", err)
	}
	// The raw delta's vertex count must appear in the ep_bkt dispatch, i.e.
	// its bake was NOT dropped just because the path is in pose_exclude.
	if !strings.Contains(mods, "$ep_bkt3 = 75749") {
		t.Fatalf("raw set vc=75749 was dropped by the path-only collision check; Skipped=%v", g.Skipped)
	}
	// This build does not adapt finished sets, so the shared table must not be
	// published into the panel gate and must not receive the recipe.
	if strings.Contains(mods, "$ep_bkt3 = 269832") {
		t.Fatal("finished skip vc must NOT satisfy the panel gate in this build")
	}
	shield := sectionBody(mods, "CommandListEpU5Shield3")
	if strings.Contains(shield, "run = CommandList\\WWMIv1\\SetShapeKey") {
		t.Fatal("shared shape-key table must not receive the recipe in this build")
	}
	for _, s := range g.Skipped {
		if strings.Contains(s, "also in pose_exclude; dropped") {
			t.Fatalf("raw delta was wrongly dropped: %v", s)
		}
	}
}

// A character can have raw delta sets and finished sets at the same time. The
// raw sets keep the baked delta route; the finished sets need their own shape
// key value tables written directly. The value route must not write the recipe
// into a raw set's table, which would apply the same expression twice.
func TestRenderMixedRawDeltaWritesOnlyFinishedShapeKeys(t *testing.T) {
	user := t.TempDir()
	base := "hsin_mod_75749"
	os.WriteFile(filepath.Join(user, base+"_base.buf"), make([]byte, 75749*12), 0o644)
	os.WriteFile(filepath.Join(user, "a0e72_sparse.buf"), make([]byte, 120), 0o644)
	os.WriteFile(filepath.Join(user, "a0e72_sparse_manifest.json"),
		[]byte(`{"format":"sparse-csr-v1","vc":75749,"total_bytes":120}`), 0o644)

	ch := testChar()
	c := &ch
	rawPVB := `Resource\HsinRaw\PositionBuffer`
	rawSK := `Resource\HsinRaw\CustomShapeKeyValuesRW`
	v38PVB := `Resource\nm_HsinMD1\PositionBuffer`
	v38SK := `Resource\nm_HsinMD1\CustomShapeKeyValuesRW`
	hsinnvPVB := `Resource\Hsin\PositionBuffer`
	hsinnvSK := `Resource\Hsin\CustomShapeKeyValuesRW`
	c.PvbPaths = []string{rawPVB, v38PVB, hsinnvPVB}
	c.SkPaths = []string{rawSK, v38SK, hsinnvSK}
	c.PoseExclude = []string{v38PVB, hsinnvPVB}
	c.PoseExcludeVC = map[string]int{v38PVB: 193205, hsinnvPVB: 269832}
	c.ModVC = map[string]int{"heart/a0e72/mod.ini": 75749}
	c.ModDelta = map[string]*panel.Delta{
		"HsinRaw": {VC: 75749, Sparse: "a0e72_sparse.buf", Base: base + "_base.buf"},
	}

	tbl := mustTable(c)
	g := New(tbl, user, "")
	mods, err := g.RenderMods()
	if err != nil {
		t.Fatalf("mixed raw/finished render: %v", err)
	}
	for _, vc := range []int{75749} {
		if !strings.Contains(mods, fmt.Sprintf("$ep_bkt3 = %d", vc)) {
			t.Fatalf("raw delta vc %d missing from runtime gate", vc)
		}
	}
	// This build does not adapt finished sets, so their vc's must never reach
	// the panel gate.
	for _, vc := range []int{193205, 269832} {
		if strings.Contains(mods, fmt.Sprintf("$ep_bkt3 = %d", vc)) {
			t.Fatalf("finished vc %d must NOT satisfy the panel gate in this build", vc)
		}
	}
	shieldCL := sectionBody(mods, "CommandListEpU5Shield3")
	if !strings.Contains(shieldCL, "cs-u5 = "+rawSK) {
		t.Fatal("raw shape-key table missing from the shield binding")
	}
	if got := bindBody(shieldCL, rawSK); strings.Contains(got, "SetShapeKey") {
		t.Fatalf("raw shape-key table got the value route; expression would be applied twice: %q", got)
	}
	// Finished tables are shielded but never written in this build.
	for _, sk := range []string{v38SK, hsinnvSK} {
		got := bindBody(shieldCL, sk)
		if strings.Contains(got, "SetShapeKey") {
			t.Fatalf("finished shape-key table %q must NOT get the value route in this build: %q", sk, got)
		}
	}
	if n := strings.Count(shieldCL, "run = CommandList\\WWMIv1\\SetShapeKey"); n != 0 {
		t.Fatalf("expected no SetShapeKey calls at all, got %d", n)
	}
	for _, s := range g.Skipped {
		if strings.Contains(s, "cannot pick a skip vertex count") {
			t.Fatalf("finished skip dispatch regressed: %v", s)
		}
	}
}
