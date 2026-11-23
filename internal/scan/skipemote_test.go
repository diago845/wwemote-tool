package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeIni puts a readable ini at p; scanIni accepts it regardless of content.
func writeIni(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("; test ini\n[Constants]\nglobal $a = 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

// A folder ending in _skipemote must be pruned from the walk entirely: none
// of its ini files land in the index, and it is counted for the log line.
func TestBuildIndexScopeSkipsSkipEmoteFolder(t *testing.T) {
	root := t.TempDir()
	writeIni(t, filepath.Join(root, "normal", "1.ini"))
	writeIni(t, filepath.Join(root, "HsinMod_skipemote", "Nested", "2.ini"))
	writeIni(t, filepath.Join(root, "RoverMod_SKIPEMOTE", "3.ini"))
	writeIni(t, filepath.Join(root, "keep_skipemote_extra", "4.ini")) // suffix not at end

	idx, err := BuildIndexScope(root, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range idx.Order {
		if hasSkipEmoteFolder(rel) {
			t.Errorf("marked folder's ini was indexed: %s", rel)
		}
	}
	if len(idx.Recs) != 2 {
		t.Errorf("indexed %d inis, want 2 (normal + keep_skipemote_extra): %v", len(idx.Recs), idx.Order)
	}
	if idx.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2 (case-insensitive suffix folders)", idx.Skipped)
	}
	// Each pruned folder records its suffix-stripped base so the GUI can tell
	// a deliberate _skipemote opt-out from a dead DISABLED_ binding.
	wantBases := map[string]bool{"HsinMod": true, "RoverMod": true}
	if len(idx.SkippedBases) != 2 {
		t.Errorf("SkippedBases = %v, want 2 entries", idx.SkippedBases)
	}
	for _, b := range idx.SkippedBases {
		if !wantBases[b] {
			t.Errorf("SkippedBases has %q, want HsinMod or RoverMod", b)
		}
	}
}

// hasSkipEmoteFolder works on both slash styles and on a single dir name.
func TestHasSkipEmoteFolder(t *testing.T) {
	cases := map[string]bool{
		"RoverMod_skipemote":                       true,
		"RoverMod_SKIPEMOTE":                       true,
		`Hsin\HsinMod thicc_skipemote\Hsin Mod\1.ini`: true,
		"Hsin/HsinMod thicc_skipemote/Hsin Mod/1.ini": true,
		"HsinMod_skipemote_extra":                  false,
		"normal_mod":                               false,
	}
	for p, want := range cases {
		if got := hasSkipEmoteFolder(p); got != want {
			t.Errorf("hasSkipEmoteFolder(%q) = %v, want %v", p, got, want)
		}
	}
}

// A previously-configured set in a folder the user later marks must not be
// carried back by a scoped merge, otherwise a stale binding survives.
func TestOutsideLayerDropsSkipEmoteSets(t *testing.T) {
	prev := []byte(`{
  "sk_paths": [
    "Resource\\Mods\\Hsin\\HsinMod thicc\\Hsin Mod\\1.ini\\CustomShapeKeyValuesRW",
    "Resource\\Mods\\Hsin\\RoverMod_skipemote\\Rover.ini\\CustomShapeKeyValuesRW"
  ],
  "pvb_paths": [
    "Resource\\Mods\\Hsin\\HsinMod thicc\\Hsin Mod\\1.ini\\PositionBuffer",
    "Resource\\Mods\\Hsin\\RoverMod_skipemote\\Rover.ini\\PositionBuffer"
  ],
  "pose_exclude": ["Hsin/RoverMod_skipemote/Rover.ini"],
  "active": "Hsin/RoverMod_skipemote/Rover.ini",
  "mod_vc": {
    "Hsin/HsinMod thicc/Hsin Mod/1.ini": 103514,
    "Hsin/RoverMod_skipemote/Rover.ini": 90001
  },
  "mod_delta": {}
}
`)
	old, err := readPrevLayer(prev)
	if err != nil {
		t.Fatal(err)
	}
	kept := outsideLayer(old, "Hsin/other-folder")
	for rel := range kept.ModVC {
		if strings.Contains(strings.ToLower(rel), "skipemote") {
			t.Errorf("marked set survived merge: %s", rel)
		}
	}
	if len(kept.ModVC) != 1 {
		t.Errorf("kept %d sets, want 1: %v", len(kept.ModVC), kept.ModVC)
	}
	for _, r := range kept.PoseExclude {
		if strings.Contains(strings.ToLower(r), "skipemote") {
			t.Errorf("marked pose exclude survived merge: %s", r)
		}
	}
	if kept.Active != nil && strings.Contains(strings.ToLower(*kept.Active), "skipemote") {
		t.Errorf("marked active survived merge: %s", *kept.Active)
	}
}

// A folder renamed to _skipemote keeps its previous config under the OLD name
// (no suffix). Configuring the marked folder itself must drop that stale set,
// otherwise it is revived with a base mesh path that no longer exists and
// checkBaseMesh fails ("missing base mesh ... HsinMod thicc\...\Position.buf").
// Out-of-scope sets must still survive the merge.
func TestOutsideLayerDropsPreRenameBaseline(t *testing.T) {
	prev := []byte(`{
  "sk_paths": [
    "Resource\\Mods\\Hsin\\HsinMod thicc\\Hsin Mod\\1.ini\\CustomShapeKeyValuesRW",
    "Resource\\Mods\\RoverMod\\Rover.ini\\CustomShapeKeyValuesRW"
  ],
  "pvb_paths": [
    "Resource\\Mods\\Hsin\\HsinMod thicc\\Hsin Mod\\1.ini\\PositionBuffer",
    "Resource\\Mods\\RoverMod\\Rover.ini\\PositionBuffer"
  ],
  "pose_exclude": ["Hsin/HsinMod thicc/Hsin Mod/1.ini", "RoverMod/Rover.ini"],
  "active": "Hsin/HsinMod thicc/Hsin Mod/1.ini",
  "mod_vc": {
    "Hsin/HsinMod thicc/Hsin Mod/1.ini": 103514,
    "RoverMod/Rover.ini": 90001
  },
  "mod_delta": {}
}
`)
	old, err := readPrevLayer(prev)
	if err != nil {
		t.Fatal(err)
	}
	// Scope is the marked folder itself; the stale set uses the pre-rename name.
	kept := outsideLayer(old, "Hsin/HsinMod thicc_skipemote")
	if _, ok := kept.ModVC["Hsin/HsinMod thicc/Hsin Mod/1.ini"]; ok {
		t.Errorf("pre-rename stale set survived merge: %v", kept.ModVC)
	}
	if _, ok := kept.ModVC["RoverMod/Rover.ini"]; !ok {
		t.Errorf("out-of-scope set was wrongly dropped: %v", kept.ModVC)
	}
	for _, r := range kept.PoseExclude {
		if strings.HasPrefix(r, "Hsin/HsinMod thicc/") {
			t.Errorf("pre-rename pose_exclude survived: %s", r)
		}
	}
	if kept.Active != nil && strings.HasPrefix(*kept.Active, "Hsin/HsinMod thicc/") {
		t.Errorf("pre-rename active survived: %s", *kept.Active)
	}
}

// skipEmoteScopeBase must leave unmarked scopes byte-identical and strip only
// the trailing suffix (case-insensitive) from marked segments.
func TestSkipEmoteScopeBase(t *testing.T) {
	cases := map[string]string{
		"Hsin/HsinMod thicc_skipemote":          "Hsin/HsinMod thicc",
		"Hsin/HsinMod thicc_SKIPEMOTE/Hsin Mod": "Hsin/HsinMod thicc/Hsin Mod",
		"Hsin/HsinMod thicc":                    "Hsin/HsinMod thicc",
		"":                                   "",
		"RoverMod_skipemote_extra":           "RoverMod_skipemote_extra",
	}
	for in, want := range cases {
		if got := skipEmoteScopeBase(in); got != want {
			t.Errorf("skipEmoteScopeBase(%q) = %q, want %q", in, got, want)
		}
	}
}

// When the user configures a _skipemote folder itself and finds no installed
// mod, the old set under the pre-rename name must still be pruned even with
// clean-stale OFF: its base mesh path no longer exists and would fail render.
func TestCleanupScopeForSkipEmoteBaseline(t *testing.T) {
	prev := []byte(`{
  "sk_paths": ["Resource\\Mods\\Hsin\\HsinMod thicc\\Hsin Mod\\1.ini\\CustomShapeKeyValuesRW"],
  "pvb_paths": ["Resource\\Mods\\Hsin\\HsinMod thicc\\Hsin Mod\\1.ini\\PositionBuffer"],
  "mod_vc": {"Hsin/HsinMod thicc/Hsin Mod/1.ini": 103514},
  "mod_delta": {}
}
`)
	// Marked scope, clean-stale OFF -> still returns the unmarked baseline.
	if got := CleanupScopeFor("Hsin/HsinMod thicc_skipemote", prev, false); got != "Hsin/HsinMod thicc" {
		t.Errorf("CleanupScopeFor(marked, off) = %q, want %q", got, "Hsin/HsinMod thicc")
	}
	// Unmarked scope, clean-stale OFF -> nothing pruned.
	if got := CleanupScopeFor("Hsin/HsinMod thicc", prev, false); got != "" {
		t.Errorf("CleanupScopeFor(unmarked, off) = %q, want empty", got)
	}
	// Unmarked scope, clean-stale ON -> normal pruning still works.
	if got := CleanupScopeFor("Hsin/HsinMod thicc", prev, true); got != "Hsin/HsinMod thicc" {
		t.Errorf("CleanupScopeFor(unmarked, on) = %q, want %q", got, "Hsin/HsinMod thicc")
	}
	// Marked scope with no matching old set -> nothing pruned.
	if got := CleanupScopeFor("Hsin/HsinMod thicc_skipemote", []byte(`{"mod_vc":{}}`), false); got != "" {
		t.Errorf("CleanupScopeFor(marked, no old set) = %q, want empty", got)
	}
}
