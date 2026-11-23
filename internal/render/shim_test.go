package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wwemote/internal/guard"
)

func TestSplitShimResource(t *testing.T) {
	cases := []struct {
		name       string
		full       string
		wantNS     string
		wantShort  string
		wantNeeded bool
	}{
		{
			name:      "space in mod folder needs shim",
			full:      `Resource\Mods\Lynae\Lynae skin mod\mod.ini\CustomShapeKeyValuesRW`,
			wantNS:    `Mods\Lynae\Lynae skin mod\mod.ini`,
			wantShort: `ResourceCustomShapeKeyValuesRW`, wantNeeded: true,
		},
		{
			name:      "space in position buffer needs shim",
			full:      `Resource\Mods\Lynae\Lynae skin mod\mod.ini\PositionBuffer`,
			wantNS:    `Mods\Lynae\Lynae skin mod\mod.ini`,
			wantShort: `ResourcePositionBuffer`, wantNeeded: true,
		},
		{
			name:      "bracket needs shim",
			full:      `Resource\Mods\Hsin\Mod[v2]\mod.ini\CustomShapeKeyValuesRW`,
			wantNS:    `Mods\Hsin\Mod[v2]\mod.ini`,
			wantShort: `ResourceCustomShapeKeyValuesRW`, wantNeeded: true,
		},
		{
			name:       "safe path untouched",
			full:       `Resource\Mods\Hsin\HsinMod-thicc\mod.ini\CustomShapeKeyValuesRW`,
			wantNeeded: false,
		},
		{
			name:      "namespace-declared res with space",
			full:      `Resource\Hsin Skin\CustomShapeKeyValuesRW`,
			wantNS:    `Hsin Skin`,
			wantShort: `ResourceCustomShapeKeyValuesRW`, wantNeeded: true,
		},
		{
			name:       "not a resource name",
			full:       `ResourceEpDummyU5`,
			wantNeeded: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ns, short, ok := splitShimResource(c.full)
			if ok != c.wantNeeded {
				t.Fatalf("needed = %v, want %v (ns=%q short=%q)", ok, c.wantNeeded, ns, short)
			}
			if !ok {
				return
			}
			if ns != c.wantNS {
				t.Errorf("namespace = %q, want %q", ns, c.wantNS)
			}
			if short != c.wantShort {
				t.Errorf("short = %q, want %q", short, c.wantShort)
			}
		})
	}
}

func TestShimBindU5AndCopySrc(t *testing.T) {
	s := newShimSet()
	safe := `Resource\Mods\Hsin\HsinMod-thicc\mod.ini\CustomShapeKeyValuesRW`
	if got := s.bindU5(safe); got != "cs-u5 = "+safe {
		t.Fatalf("safe bind rewritten: %q", got)
	}
	if s.hasShim() {
		t.Fatal("safe bind produced a shim")
	}

	spaced := `Resource\Mods\Lynae\Lynae skin mod\mod.ini\CustomShapeKeyValuesRW`
	bind := s.bindU5(spaced)
	if !strings.HasPrefix(bind, "run = CommandList\\Mods\\Lynae\\Lynae skin mod\\mod.ini\\WWEmoteShim") {
		t.Fatalf("bind not routed through shim: %q", bind)
	}
	if strings.Contains(bind, "cs-u5 = Resource\\Mods") {
		t.Fatalf("long path leaked into a value position: %q", bind)
	}

	dst := `ResourceEpPosedSrc1`
	cp := `Resource\Mods\Lynae\Lynae skin mod\mod.ini\PositionBuffer`
	copyLine := s.copySrc(dst, cp)
	if !strings.HasPrefix(copyLine, "run = CommandList\\Mods\\Lynae\\Lynae skin mod\\mod.ini\\WWEmoteShim") {
		t.Fatalf("copy not routed through shim: %q", copyLine)
	}
	if !s.hasShim() {
		t.Fatal("expected a shim to be collected")
	}
}

func TestShimFileShape(t *testing.T) {
	s := newShimSet()
	s.bindU5(`Resource\Mods\Lynae\Lynae skin mod\mod.ini\CustomShapeKeyValuesRW`)
	s.copySrc(`ResourceEpPosedSrc1`, `Resource\Mods\Lynae\Lynae skin mod\mod.ini\PositionBuffer`)

	files := s.files()
	if len(files) != 1 {
		t.Fatalf("got %d shim files, want 1", len(files))
	}
	f := files[0]
	if !strings.HasPrefix(f.Name, shimFilePrefix) || !strings.HasSuffix(f.Name, ".ini") {
		t.Fatalf("bad shim file name %q", f.Name)
	}
	// The namespace value must be one whole line: it carries spaces and must
	// not be split. 3DMigoto reads it verbatim.
	if !strings.Contains(f.Text, "namespace = Mods\\Lynae\\Lynae skin mod\\mod.ini\n") {
		t.Fatalf("namespace line missing or split:\n%s", f.Text)
	}
	// Short names, never full spaced paths, in value positions.
	for _, want := range []string{
		"cs-u5 = ResourceCustomShapeKeyValuesRW",
		"ResourceEpPosedSrc1 = copy ResourcePositionBuffer",
	} {
		if !strings.Contains(f.Text, want) {
			t.Errorf("shim missing %q:\n%s", want, f.Text)
		}
	}
	if strings.Contains(f.Text, "= copy Resource\\Mods") {
		t.Errorf("full spaced path leaked into a copy source:\n%s", f.Text)
	}
	// Comments must stay ASCII even though the namespace path is not.
	for _, ln := range strings.Split(f.Text, "\n") {
		tr := strings.TrimSpace(ln)
		if strings.HasPrefix(tr, ";") {
			for _, r := range tr {
				if r > 127 {
					t.Errorf("non-ASCII in comment: %q", tr)
					break
				}
			}
		}
	}
}

func TestShimPassesArtifactAudit(t *testing.T) {
	dir := t.TempDir()
	s := newShimSet()
	s.bindU5(`Resource\Mods\Lynae\Lynae skin mod\mod.ini\CustomShapeKeyValuesRW`)
	s.copySrc(`ResourceEpPosedSrc1`, `Resource\Mods\Lynae\Lynae skin mod\mod.ini\PositionBuffer`)
	for _, f := range s.files() {
		if err := os.WriteFile(filepath.Join(dir, f.Name), []byte(f.Text), 0o644); err != nil {
			t.Fatalf("write shim: %v", err)
		}
	}
	issues, files := guard.CheckDir(dir)
	if files != 1 {
		t.Fatalf("audit read %d files, want 1", files)
	}
	for _, issue := range issues {
		t.Errorf("shim failed artifact audit: %s", issue.String())
	}
}

func TestShimStableAcrossPasses(t *testing.T) {
	build := func() []struct{ Name, Text string } {
		s := newShimSet()
		s.bindU5(`Resource\Mods\Lynae\Lynae skin mod\mod.ini\CustomShapeKeyValuesRW`)
		return s.files()
	}
	a, b := build(), build()
	if len(a) != len(b) {
		t.Fatalf("passes disagree on count: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("shim not stable across renders:\n%s\n---\n%s", a[i].Text, b[i].Text)
		}
	}
}
