package render

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"wwemote/internal/panel"
)

// TestGoldenModsINI requires the user-layer file this package generates to be
// byte-identical to the one the reference tool produced on the same machine.
//
// Point WWEMOTE_GOLDEN_USER at the panel's user/ directory (the one holding
// <char>.mods.json and the *.buf deltas) and WWEMOTE_GOLDEN_MODS_INI at the
// reference WWEmoteMods.ini to enable it.
func TestGoldenModsINI(t *testing.T) {
	userDir := os.Getenv("WWEMOTE_GOLDEN_USER")
	wantPath := os.Getenv("WWEMOTE_GOLDEN_MODS_INI")
	if userDir == "" || wantPath == "" {
		t.Skip("set WWEMOTE_GOLDEN_USER and WWEMOTE_GOLDEN_MODS_INI to run the parity test")
	}
	version := os.Getenv("WWEMOTE_VERSION")

	tbl, err := panel.Load(userDir)
	if err != nil {
		t.Fatalf("load panel data: %v", err)
	}
	g := New(tbl, userDir, version)
	got, err := g.RenderMods()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read reference: %v", err)
	}
	if got == string(want) {
		return
	}
	// Report the first divergent line: a byte offset alone is useless when the
	// difference is one shifted line.
	wl := splitLines(string(want))
	gl := splitLines(got)
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, x string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			x = gl[i]
		}
		if w != x {
			lo := i - 3
			if lo < 0 {
				lo = 0
			}
			t.Fatalf("first difference at line %d\n  want %q\n  got  %q\n"+
				"  want md5 %s (%d bytes)\n  got  md5 %s (%d bytes)",
				i+1, w, x, md5Hex(string(want)), len(want), md5Hex(got), len(got))
		}
	}
	t.Fatalf("length differs: want %d bytes, got %d", len(want), len(got))
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, cur)
	return out
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

var _ = filepath.Join

// TestGoldenUserINI requires the declared delta sections to match the reference
// byte for byte. Point WWEMOTE_GOLDEN_USER_INI at the reference user.ini.
func TestGoldenUserINI(t *testing.T) {
	userDir := os.Getenv("WWEMOTE_GOLDEN_USER")
	wantPath := os.Getenv("WWEMOTE_GOLDEN_USER_INI")
	if userDir == "" || wantPath == "" {
		t.Skip("set WWEMOTE_GOLDEN_USER and WWEMOTE_GOLDEN_USER_INI to run the parity test")
	}
	tbl, err := panel.Load(userDir)
	if err != nil {
		t.Fatalf("load panel data: %v", err)
	}
	got, err := New(tbl, userDir, "").RenderUserDelta()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read reference: %v", err)
	}
	if got != string(want) {
		wl, gl := splitLines(string(want)), splitLines(got)
		for i := 0; i < len(wl) || i < len(gl); i++ {
			var w, x string
			if i < len(wl) {
				w = wl[i]
			}
			if i < len(gl) {
				x = gl[i]
			}
			if w != x {
				t.Fatalf("first difference at line %d\n  want %q\n  got  %q\n"+
					"  want md5 %s (%d bytes)\n  got  md5 %s (%d bytes)",
					i+1, w, x, md5Hex(string(want)), len(want), md5Hex(got), len(got))
			}
		}
	}
}
