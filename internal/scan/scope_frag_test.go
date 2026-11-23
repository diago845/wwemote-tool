package scan

import "testing"

// A narrowed scan picks the frag "1.ini" from its single in-scope pvb path,
// where that segment is unique. Once out-of-scope sets are merged back, three
// of the five pvb paths contain a "1.ini" segment. The merged delta keys must
// be unambiguous against the final full list, otherwise panel.DeltaTargets
// fails with "matches 3 pvb_paths (must be exactly 1)".
func TestMergeOutsideRepicksAmbiguousFrag(t *testing.T) {
	prev := []byte(`{
  "sk_paths": [
    "Resource\\Mods\\Hsin\\HsinMod thicc\\Hsin Mod\\1.ini\\CustomShapeKeyValuesRW",
    "Resource\\Mods\\Hsin\\HsinMod\\HsinMod\\1.ini\\CustomShapeKeyValuesRW",
    "Resource\\Mods\\Hsin\\Hsin_Unfinished\\1Mod\\1.ini\\CustomShapeKeyValuesRW",
    "Resource\\Mods\\Hsin\\tsqn-HsinMod\\tsqn-HsinMod\\mod.ini\\CustomShapeKeyValuesRW",
    "Resource\\Mods\\Hsin\\moonfox-v2-fixed\\normal\\mod.ini\\CustomShapeKeyValuesRW"
  ],
  "pvb_paths": [
    "Resource\\Mods\\Hsin\\HsinMod thicc\\Hsin Mod\\1.ini\\PositionBuffer",
    "Resource\\Mods\\Hsin\\HsinMod\\HsinMod\\1.ini\\PositionBuffer",
    "Resource\\Mods\\Hsin\\Hsin_Unfinished\\1Mod\\1.ini\\PositionBuffer",
    "Resource\\Mods\\Hsin\\tsqn-HsinMod\\tsqn-HsinMod\\mod.ini\\PositionBuffer",
    "Resource\\Mods\\Hsin\\moonfox-v2-fixed\\normal\\mod.ini\\PositionBuffer"
  ],
  "pose_exclude": [],
  "pose_exclude_vc": {},
  "active": "Hsin/HsinMod thicc/Hsin Mod/1.ini",
  "mod_vc": {
    "Hsin/HsinMod thicc/Hsin Mod/1.ini": 103514,
    "Hsin/HsinMod/HsinMod/1.ini": 103514,
    "Hsin/Hsin_Unfinished/1Mod/1.ini": 74963,
    "Hsin/tsqn-HsinMod/tsqn-HsinMod/mod.ini": 134850,
    "Hsin/moonfox-v2-fixed/normal/mod.ini": 437416
  },
  "mod_delta": {
    "Hsin Mod": {"vc": 103514, "sparse": "hsin_mod_103514_sparse.buf", "base": "ep_mod_base_103514.buf"},
    "HsinMod": {"vc": 103514, "sparse": "hsin_mod_103514_sparse.buf", "base": "ep_mod_base_103514.buf"},
    "1Mod": {"vc": 74963, "sparse": "hsin_mod_74963_sparse.buf", "base": "ep_mod_base_74963.buf"},
    "tsqn-HsinMod": {"vc": 134850, "sparse": "hsin_mod_134850_sparse.buf", "base": "ep_mod_base_134850.buf"},
    "normal": {"vc": 437416, "sparse": "hsin_mod_437416_sparse.buf", "base": "ep_mod_base_437416.buf"}
  }
}
`)
	active := "Hsin/HsinMod thicc/Hsin Mod/1.ini"
	fresh := &Layer{
		SkPaths:       []string{`Resource\Mods\Hsin\HsinMod thicc\Hsin Mod\1.ini\CustomShapeKeyValuesRW`},
		PvbPaths:      []string{`Resource\Mods\Hsin\HsinMod thicc\Hsin Mod\1.ini\PositionBuffer`},
		Active:        &active,
		ModVC:         map[string]int{active: 103514},
		ModDelta:      map[string]*Delta{"1.ini": {VC: 103514, Sparse: "hsin_mod_103514_sparse.buf", Base: "ep_mod_base_103514.buf"}},
		ModDeltaOrder: []string{"1.ini"},
	}

	merged, err := MergeOutside(fresh, prev, "Hsin/HsinMod thicc")
	if err != nil {
		t.Fatal(err)
	}

	// Every surviving delta key must name exactly one pvb path in the final
	// full list -- the same invariant panel.DeltaTargets checks.
	for frag := range merged.ModDelta {
		if n := countFragHits(frag, merged.PvbPaths); n != 1 {
			t.Errorf("merged frag %q matches %d pvb paths, want exactly 1", frag, n)
		}
	}
	if merged.ModDelta["1.ini"] != nil {
		t.Error("ambiguous frag \"1.ini\" survived the merge")
	}
	if merged.ModDelta["Hsin Mod"] == nil {
		t.Error("expected the scoped frag to be re-picked as \"Hsin Mod\"")
	}
	if len(merged.ModDeltaOrder) != len(merged.ModDelta) {
		t.Errorf("order has %d keys, delta has %d", len(merged.ModDeltaOrder), len(merged.ModDelta))
	}
}
