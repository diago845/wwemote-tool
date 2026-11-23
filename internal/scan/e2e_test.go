package scan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// noteGolden is the shape of a reference <char>.mods.json. It lets the test
// reuse the real _note lines without copying them into Go source.
func readNote(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read note source: %v", err)
	}
	var v struct {
		Note []string `json:"_note"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parse note source: %v", err)
	}
	return v.Note
}

// TestGoldenModsJSON runs the whole user-side scan -> bake -> serialise chain
// in a temp user root and compares the resulting <char>.mods.json to the
// reference file byte for byte.
func TestGoldenModsJSON(t *testing.T) {
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

	cases := []struct {
		name   string
		anchor string
		need   bool
	}{
		{"hsin", "67b0a7f4", true},
		{"hsinult", "4ce859c3", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := filepath.Join(t.TempDir(), "user")
			if err := os.MkdirAll(tmp, 0o755); err != nil {
				t.Fatal(err)
			}
			SetUserRoot(tmp)

			res := ScanChar([]string{tc.anchor}, globs, idx)
			layer, errs := UserLayer(res, mods, tc.need)
			if len(errs) > 0 {
				t.Fatalf("layer errors: %v", errs)
			}
			if _, _, _, _, err := AutoBake(tc.name, layer, mods, res.Found); err != nil {
				t.Fatalf("autobake: %v", err)
			}
			layer.Note = readNote(t, filepath.Join(refUser, tc.name+".mods.json"))
			got := string(DumpModsJSON(layer))

			refPath := filepath.Join(refUser, tc.name+".mods.json")
			wantRaw, err := os.ReadFile(refPath)
			if err != nil {
				t.Fatalf("read reference: %v", err)
			}
			want := string(wantRaw)
			if got == want {
				t.Logf("byte-identical: %d bytes", len(got))
				return
			}

			// Parse both and compare the structural parts to localise the diff.
			var jg, jw map[string]json.RawMessage
			if e := json.Unmarshal([]byte(got), &jg); e != nil {
				t.Fatalf("parse got: %v", e)
			}
			if e := json.Unmarshal([]byte(want), &jw); e != nil {
				t.Fatalf("parse want: %v", e)
			}
			keys := []string{"sk_paths", "pvb_paths", "pose_exclude", "pose_exclude_vc", "active", "mod_vc", "mod_delta"}
			for _, k := range keys {
				if !reflect.DeepEqual(string(jg[k]), string(jw[k])) {
					t.Errorf("%s differs:\n got  %s\n want %s", k, truncate(string(jg[k])), truncate(string(jw[k])))
				}
			}
			// First line-level difference, for byte-level formatting issues.
			gl, wl := split(got), split(want)
			for i := 0; i < len(gl) || i < len(wl); i++ {
				var a, b string
				if i < len(gl) {
					a = gl[i]
				}
				if i < len(wl) {
					b = wl[i]
				}
				if a != b {
					t.Errorf("line %d:\n got  %q\n want %q", i+1, a, b)
					break
				}
			}
			t.Errorf("not byte-identical: got %d bytes, want %d bytes", len(got), len(want))
		})
	}
}

func split(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func truncate(s string) string {
	if len(s) > 500 {
		return s[:500] + "..."
	}
	return s
}
