package scan

import "testing"

func fakeRec(kind string, ns string) *Rec {
	return &Rec{Kind: kind, Namespace: ns, CVCVals: []int{1000}}
}

// A character whose only set is a finished (author-baked) mod must produce an
// empty but successful delta. The renderer drives such a mod through the U5
// value route; treating the empty delta as a fatal error aborted the whole
// config for V3.8-style mods.
func TestAutoBakeAllFinishedIsNotFatal(t *testing.T) {
	rel := "heart/finished/mod.ini"
	ns := "nm_Finished"
	pvb := `Resource\` + ns + `\` + ResPVB
	layer := &Layer{
		ModVC:       map[string]int{rel: 1000},
		PvbPaths:    []string{pvb},
		PoseExclude: []string{pvb}, // namespace sets use the resource path as key
	}
	found := map[string]*Rec{rel: fakeRec(KindFinished, ns)}
	delta, _, _, _, err := AutoBake("heart", layer, t.TempDir(), found)
	if err != nil {
		t.Fatalf("all-finished must not be fatal: %v", err)
	}
	if len(delta) != 0 {
		t.Fatalf("all-finished delta = %d, want 0", len(delta))
	}
}

// Guard retained: if a usable raw set exists but nothing can be baked (its
// namespace is shared, so no unique target), that real failure must still be
// fatal instead of silently writing an empty layer.
func TestAutoBakeBadRawStillFatal(t *testing.T) {
	rel := "heart/a/Hsin/mod.ini"
	pvb := `Resource\Hsin\PositionBuffer`
	layer := &Layer{
		ModVC:    map[string]int{rel: 1000},
		PvbPaths: []string{pvb, pvb}, // shared ns -> not unique
		Active:   &rel,
	}
	found := map[string]*Rec{rel: fakeRec(KindRaw, "Hsin")}
	_, _, _, _, err := AutoBake("heart", layer, t.TempDir(), found)
	if err == nil {
		t.Fatal("a bad raw set must still fail loudly")
	}
}
