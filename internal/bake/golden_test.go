package bake

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoldenParity re-bakes every delta the deployed panel currently ships and
// requires the buffer bytes to match the reference output exactly.
//
// Point WWEMOTE_GOLDEN_MODS at the XXMI mods root and WWEMOTE_GOLDEN_USER at
// the panel's user/ directory to enable it.
func TestGoldenParity(t *testing.T) {
	modsRoot := os.Getenv("WWEMOTE_GOLDEN_MODS")
	userDir := os.Getenv("WWEMOTE_GOLDEN_USER")
	if modsRoot == "" || userDir == "" {
		t.Skip("set WWEMOTE_GOLDEN_MODS and WWEMOTE_GOLDEN_USER to run parity tests")
	}

	docs := loadAllModsJSON(t, userDir)
	out := t.TempDir()

	for _, doc := range docs {
		doc := doc
		for _, rel := range doc.sortedInis() {
			rel := rel
			vc := doc.ModVC[rel]
			t.Run(doc.Char+"/"+rel, func(t *testing.T) {
				ini := resolveIni(modsRoot, rel)
				if ini == "" {
					t.Skipf("ini not on disk: %s", rel)
				}
				modDir := filepath.Dir(ini)
				name := doc.Char + "_mod_" + itoa(vc)

				_, err := Bake(modDir, vc, Options{
					OutDir:  out,
					Name:    name,
					IniName: filepath.Base(ini),
				})
				if err != nil {
					t.Fatalf("bake: %v", err)
				}

				gotPath := filepath.Join(out, name+"_sparse.buf")
				wantPath := filepath.Join(userDir, name+"_sparse.buf")
				if _, err := os.Stat(wantPath); err != nil {
					t.Skipf("no golden delta for vc %d", vc)
				}
				want := md5File(t, wantPath)
				got := md5File(t, gotPath)
				if got != want {
					t.Fatalf("delta mismatch for %s\n  want %s\n  got  %s", name, want, got)
				}
			})
		}
	}
}

type modsDoc struct {
	Char   string         `json:"-"`
	ModVC  map[string]int `json:"mod_vc"`
	Active string         `json:"active"`
}

// resolveIni maps a mods.json relative path (which drops any DISABLED_ prefix)
// back to a file that exists on disk. The prefix can sit on any directory in
// the chain, so the lookup compares paths with the prefix stripped.
func resolveIni(modsRoot, rel string) string {
	want := strings.ToLower(strings.ReplaceAll(rel, "/", "\\"))
	var found string
	filepath.WalkDir(modsRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || found != "" {
			return nil
		}
		sub, err := filepath.Rel(modsRoot, p)
		if err != nil {
			return nil
		}
		got := strings.ReplaceAll(strings.ToLower(strings.ReplaceAll(sub, "/", "\\")), "disabled_", "")
		if got == want {
			found = p
		}
		return nil
	})
	return found
}

func (d modsDoc) sortedInis() []string {
	keys := make([]string, 0, len(d.ModVC))
	for k := range d.ModVC {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

func loadAllModsJSON(t *testing.T, userDir string) []modsDoc {
	t.Helper()
	entries, err := os.ReadDir(userDir)
	if err != nil {
		t.Fatalf("read user dir: %v", err)
	}
	var out []modsDoc
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".mods.json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(userDir, n))
		if err != nil {
			t.Fatalf("read %s: %v", n, err)
		}
		var doc modsDoc
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse %s: %v", n, err)
		}
		doc.Char = strings.TrimSuffix(n, ".mods.json")
		if len(doc.ModVC) > 0 {
			out = append(out, doc)
		}
	}
	if len(out) == 0 {
		t.Skip("no mods.json with mod_vc found")
	}
	return out
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func md5File(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}
