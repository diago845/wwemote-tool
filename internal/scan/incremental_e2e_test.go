package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIncrementalReuseRoundTrip drives the real scan -> bake -> serialise chain
// twice on the installed mods and checks the incremental guarantees:
//   - a second run with unchanged inputs reuses every delta and produces
//     byte-identical <char>.mods.json;
//   - bumping BakeVersion is not something we can do at runtime here, so that
//     path is covered by TestLoadCachedBakeRejectsMismatches and the guard test;
//   - removing a sparse buffer forces only that bucket to re-bake.
//
// Env-gated like TestGoldenModsJSON: it needs the real mods tree.
func TestIncrementalReuseRoundTrip(t *testing.T) {
	mods := os.Getenv("WWEMOTE_GOLDEN_MODS")
	refUser := os.Getenv("WWEMOTE_REF_MODS")
	if mods == "" || refUser == "" {
		t.Skip("set WWEMOTE_GOLDEN_MODS and WWEMOTE_REF_MODS")
	}
	if filepath.Base(filepath.Clean(mods)) != "Mods" {
		mods = filepath.Join(mods, "Mods")
	}
	d3dx := filepath.Join(filepath.Dir(mods), "d3dx.ini")
	globs, err := ReadExcludeGlobs(d3dx)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := BuildIndex(mods)
	if err != nil {
		t.Fatal(err)
	}

	runOnce := func(userDir string, anchor string, need bool) (string, int) {
		t.Helper()
		SetUserRoot(userDir)
		res := ScanChar([]string{anchor}, globs, idx)
		layer, errs := UserLayer(res, mods, need)
		if len(errs) > 0 {
			t.Fatalf("layer errors: %v", errs)
		}
		layer.Note = readNote(t, filepath.Join(refUser, anchorName(anchor)+".mods.json"))
		_, _, _, fresh, err := AutoBake(anchorName(anchor), layer, mods, res.Found)
		if err != nil {
			t.Fatalf("autobake: %v", err)
		}
		return string(DumpModsJSON(layer)), fresh
	}

	// Stamp every sparse buffer with a sentinel mtime. A run that reuses a
	// delta leaves the file untouched; a run that re-bakes rewrites it and the
	// mtime moves. This cross-checks AutoBake's fresh count against the files
	// actually rewritten on disk.
	stamp := func(userDir string, tm time.Time) {
		t.Helper()
		entries, _ := os.ReadDir(userDir)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), "_sparse.buf") {
				if err := os.Chtimes(filepath.Join(userDir, e.Name()), tm, tm); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	fresh := func(userDir string, sentinel time.Time) (freshN, total int) {
		t.Helper()
		entries, _ := os.ReadDir(userDir)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), "_sparse.buf") {
				total++
				st, _ := os.Stat(filepath.Join(userDir, e.Name()))
				if st.ModTime().After(sentinel) {
					freshN++
				}
			}
		}
		return
	}

	for _, tc := range []struct {
		anchor string
		need   bool
	}{{"67b0a7f4", true}, {"4ce859c3", true}} {
		t.Run(anchorName(tc.anchor), func(t *testing.T) {
			userDir := filepath.Join(t.TempDir(), "user")
			if err := os.MkdirAll(userDir, 0o755); err != nil {
				t.Fatal(err)
			}

			first, freshFirst := runOnce(userDir, tc.anchor, tc.need)
			if freshFirst == 0 {
				t.Fatal("first run reported zero freshly baked sets, want > 0")
			}
			sentinel := time.Unix(1_500_000_000, 0) // 2017, well before any real write
			stamp(userDir, sentinel)
			freshN, total := fresh(userDir, sentinel)
			if total == 0 || freshN != 0 {
				t.Fatalf("stamp failed: %d/%d fresh", freshN, total)
			}

			second, freshSecond := runOnce(userDir, tc.anchor, tc.need)
			if first != second {
				t.Fatalf("incremental run changed <char>.mods.json:\n first %d B, second %d B", len(first), len(second))
			}
			if freshSecond != 0 {
				t.Fatalf("second run reported %d freshly baked sets, want 0 (all cached)", freshSecond)
			}
			freshN, total = fresh(userDir, sentinel)
			if freshN != 0 {
				t.Fatalf("second run re-baked %d/%d deltas instead of reusing them", freshN, total)
			}
			t.Logf("byte-identical: %d B, all %d deltas reused on second run (fresh=%d)", len(second), total, freshSecond)

			// Delete one sparse buffer: only that bucket re-bakes, and the
			// serialised result stays identical.
			os.Chtimes(filepath.Join(userDir, "ep_mod_base_10.buf"), sentinel, sentinel) // harmless if absent
			entries, _ := os.ReadDir(userDir)
			var victim string
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), "_sparse.buf") {
					victim = e.Name()
					break
				}
			}
			if victim == "" {
				t.Fatal("no sparse buffer to remove")
			}
			os.Remove(filepath.Join(userDir, victim))
			third, freshThird := runOnce(userDir, tc.anchor, tc.need)
			if first != third {
				t.Fatal("re-baking one bucket changed <char>.mods.json")
			}
			freshN, _ = fresh(userDir, sentinel)
			if freshN < 1 {
				t.Fatalf("no bucket was re-baked after removing %s", victim)
			}
			if freshN > 1 {
				t.Fatalf("removing one bucket re-baked %d deltas; only one should change", freshN)
			}
			// fresh counts SETS whose delta was regenerated; freshN counts
			// FILES rewritten. Two sets can share one vc bucket, so fresh is
			// allowed to be larger, but never smaller than the bucket count.
			if freshThird < freshN || freshThird == 0 {
				t.Fatalf("AutoBake reported %d fresh sets but %d buckets were re-baked", freshThird, freshN)
			}
			t.Logf("removed %s: %d bucket re-baked, %d sets reported fresh, json unchanged", victim, freshN, freshThird)
		})
	}
}

// anchorName maps the two known anchors to their character names. Kept local
// to the integration test so it does not become a production assumption.
func anchorName(anchor string) string {
	switch anchor {
	case "67b0a7f4":
		return "hsin"
	case "4ce859c3":
		return "hsinult"
	}
	return "unknown"
}
