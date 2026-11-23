package scan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// golden is the shape of /tmp/scan_golden.json, exported by the reference
// Python scanner. mod_delta is deliberately absent: baking has its own test.
type golden struct {
	Globs []string `json:"globs"`
	Chars map[string]struct {
		Found []string `json:"found"`
		Data  struct {
			SkPaths       []string            `json:"sk_paths"`
			PvbPaths      []string            `json:"pvb_paths"`
			PoseExclude   []string            `json:"pose_exclude"`
			PoseExcludeVC map[string]int      `json:"pose_exclude_vc"`
			Active        *string             `json:"active"`
			ModVC         map[string]int      `json:"mod_vc"`
			PosVariants   map[string][]string `json:"pos_variants"`
		} `json:"data"`
		Errs  []string `json:"errs"`
		Cover struct {
			NIni      int `json:"n_ini"`
			NDeclared int `json:"n_declared"`
		} `json:"cover"`
	} `json:"chars"`
}

func TestGoldenScan(t *testing.T) {
	mods := os.Getenv("WWEMOTE_GOLDEN_MODS")
	goldenPath := os.Getenv("WWEMOTE_SCAN_GOLDEN")
	if mods == "" || goldenPath == "" {
		t.Skip("set WWEMOTE_GOLDEN_MODS and WWEMOTE_SCAN_GOLDEN to run the scan parity test")
	}
	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var g golden
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("parse golden: %v", err)
	}

	if filepath.Base(filepath.Clean(mods)) != "Mods" {
		mods = filepath.Join(mods, "Mods")
	}
	d3dx := filepath.Join(filepath.Dir(mods), "d3dx.ini")
	globs, err := ReadExcludeGlobs(d3dx)
	if err != nil {
		t.Fatalf("read exclude globs: %v", err)
	}
	if !reflect.DeepEqual(globs, g.Globs) {
		t.Fatalf("globs: got %v want %v", globs, g.Globs)
	}

	idx, err := BuildIndex(mods)
	if err != nil {
		t.Fatalf("build index: %v", err)
	}
	t.Logf("index size %d", len(idx.Recs))

	// Anchor hashes come from the frozen author layer.
	anchors := map[string]string{"hsin": "67b0a7f4", "hsinult": "4ce859c3"}
	needActive := map[string]bool{"hsin": true, "hsinult": true}

	names := make([]string, 0, len(g.Chars))
	for nm := range g.Chars {
		names = append(names, nm)
	}
	sort.Strings(names)
	for _, nm := range names {
		want := g.Chars[nm]
		t.Run(nm, func(t *testing.T) {
			res := ScanChar([]string{anchors[nm]}, globs, idx)
			if !reflect.DeepEqual(res.FoundKeys, want.Found) {
				t.Errorf("found paths differ:\n got  %q\n want %q", res.FoundKeys, want.Found)
			}
			if res.NDeclared != want.Cover.NDeclared {
				t.Errorf("n_declared: got %d want %d", res.NDeclared, want.Cover.NDeclared)
			}
			layer, errs := UserLayer(res, mods, needActive[nm])
			if len(errs) != len(want.Errs) {
				t.Errorf("err count: got %d (%v) want %d (%v)", len(errs), errs, len(want.Errs), want.Errs)
			}
			wd := want.Data
			checkStrSlice(t, "sk_paths", layer.SkPaths, wd.SkPaths)
			checkStrSlice(t, "pvb_paths", layer.PvbPaths, wd.PvbPaths)
			checkStrSlice(t, "pose_exclude", layer.PoseExclude, wd.PoseExclude)
			checkIntMap(t, "pose_exclude_vc", layer.PoseExcludeVC, wd.PoseExcludeVC)
			checkIntMap(t, "mod_vc", layer.ModVC, wd.ModVC)
			checkStrMap(t, "pos_variants", layer.PosVariants, wd.PosVariants)
			if !samePtr(layer.Active, wd.Active) {
				t.Errorf("active: got %v want %v", deref(layer.Active), deref(wd.Active))
			}
		})
	}
}

func samePtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func checkStrSlice(t *testing.T, name string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s differs:\n got  %q\n want %q", name, got, want)
	}
}

func checkIntMap(t *testing.T, name string, got, want map[string]int) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s differs:\n got  %v\n want %v", name, got, want)
	}
}

func checkStrMap(t *testing.T, name string, got, want map[string][]string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s differs:\n got  %v\n want %v", name, got, want)
	}
}
