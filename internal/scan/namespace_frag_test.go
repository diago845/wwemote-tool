package scan

import "testing"

// A mod declaring `namespace = X` stores its resources at Resource\X\..., so
// none of the ini path segments appear in its pvb path. The namespace itself
// must then serve as the frag, otherwise the set cannot be baked at all.
func TestPickFragNSFallsBackToNamespace(t *testing.T) {
	pvb := []string{
		`Resource\Mods\heart\plain\mod.ini\PositionBuffer`,
		`Resource\cx_Mod066\PositionBuffer`,
	}
	rel := "heart/swan_glimpse/mod.ini"
	// The path form would wrongly bind this set to the other set's "mod.ini".
	if got := pickFrag(rel, pvb); got != "mod.ini" {
		t.Fatalf("path-only pickFrag = %q, want mod.ini (the trap)", got)
	}
	if got := pickFragNS(rel, "cx_Mod066", pvb); got != "cx_Mod066" {
		t.Fatalf("pickFragNS = %q, want cx_Mod066", got)
	}
}

// When two sets declare the same namespace, 3DMigoto merges their resources
// into one. There is no safe bake target, so the frag must stay empty and the
// caller drops the set instead of picking one arbitrarily.
func TestPickFragNSRejectsSharedNamespace(t *testing.T) {
	pvb := []string{
		`Resource\Hsin\PositionBuffer`,
		`Resource\Hsin\PositionBuffer`,
	}
	if got := pickFragNS("heart/a/Hsin/mod.ini", "Hsin", pvb); got != "" {
		t.Fatalf("pickFragNS for shared ns = %q, want empty", got)
	}
}

// A plain set must keep its old path-based frag even when the layer also
// contains namespace-form paths: the fallback is strictly additive.
func TestPickFragNSPreservesPathFrag(t *testing.T) {
	pvb := []string{
		`Resource\Mods\heart\HsinMod\1.ini\PositionBuffer`,
		`Resource\nm_HsinMD1\PositionBuffer`,
	}
	if got := pickFragNS("heart/HsinMod/1.ini", "", pvb); got != "1.ini" {
		t.Fatalf("pickFragNS = %q, want 1.ini (shortest unique path segment)", got)
	}
}

// Two sets declaring the same namespace resolve to the same resource path.
// The user layer must write that path once, otherwise the render layer's
// FragHit sees 2 hits and drops the raw set that needs a delta.
func TestAppendUniqueDedupNamespacePaths(t *testing.T) {
	seen := map[string]bool{}
	var pvb []string
	pvb = appendUnique(pvb, seen, `Resource\Hsin\PositionBuffer`) // a0e72 raw
	pvb = appendUnique(pvb, seen, `Resource\Hsin\PositionBuffer`) // hsinnv finished
	pvb = appendUnique(pvb, seen, `Resource\nm_HsinMD1\PositionBuffer`)
	if len(pvb) != 2 {
		t.Fatalf("pvb len = %d, want 2 after dedup: %v", len(pvb), pvb)
	}
	if got := pickFragNS("heart/a0e72/Hsin/mod.ini", "Hsin", pvb); got != "Hsin" {
		t.Fatalf("raw set frag = %q, want Hsin (unique after dedup)", got)
	}
}
