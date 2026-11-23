package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wwemote/internal/panel"
)

// A fresh install or a wiped user/ dir leaves every author-layer character
// pose=true but with no scanner data. Rendering such a table used to trip the
// pose guard ("pose=true but no pvb_paths") and abort the whole config. The
// right behaviour is to skip those characters entirely, so no empty pose
// override hooks the real character's draw.
func TestRenderSkipsUnconfiguredCharacters(t *testing.T) {
	tbl := &panel.Table{
		K: 1,
		Chars: []*panel.Char{
			{
				Cid: 3, Name: "hsin", Label: "Hsin",
				AnchorHash: "aaaabbbb", BodyHash: "ccccdddd", Pose: true,
				// no user-layer data at all
			},
			{
				Cid: 4, Name: "hsinult", Label: "HsinUlt",
				AnchorHash: "ddddeeee", BodyHash: "ffff0000", Pose: true,
			},
		},
	}
	g := New(tbl, t.TempDir(), "")
	mods, err := g.RenderMods()
	if err != nil {
		t.Fatalf("unconfigured table must render without error, got: %v", err)
	}
	// The whole user layer may be near-empty, but it must not contain a pose
	// override keyed on either frozen anchor hash.
	for _, hash := range []string{"aaaabbbb", "ddddeeee"} {
		if strings.Contains(mods, "hash = "+hash) {
			t.Errorf("rendered an empty pose override for unconfigured hash %s", hash)
		}
	}
}

// A character that HAS scanner data but no pvb_paths is a real anomaly, not a
// fresh install. The guard must still fire so a broken config is not silently
// written.
func TestRenderStillRejectsPartialData(t *testing.T) {
	c := &panel.Char{
		Cid: 3, Name: "hsin", Label: "Hsin",
		AnchorHash: "aaaabbbb", BodyHash: "ccccdddd", Pose: true,
		SkPaths: []string{`Resource\Mods\Hsin\mod.ini\CustomShapeKeyValuesRW`},
		// PvbPaths deliberately empty -> HasUserData() is true, guard must fire
	}
	tbl := &panel.Table{K: 1, Chars: []*panel.Char{c}}
	g := New(tbl, t.TempDir(), "")
	_, err := g.RenderMods()
	if err == nil {
		t.Fatal("partial data (sk_paths present, pvb_paths empty) must still be rejected")
	}
	if !strings.Contains(err.Error(), "no pvb_paths") {
		t.Fatalf("expected the pvb_paths guard, got: %v", err)
	}
}

// ConfiguredOnly must not mutate the receiver: the scan phase needs every
// author-layer character to detect newly installed mods.
func TestConfiguredOnlyKeepsReceiver(t *testing.T) {
	unconfigured := &panel.Char{Cid: 3, Name: "hsin", Pose: true}
	configured := &panel.Char{
		Cid: 4, Name: "hsinult", Pose: true,
		PvbPaths: []string{`Resource\Mods\x\PositionBuffer`},
	}
	tbl := &panel.Table{K: 1, Chars: []*panel.Char{unconfigured, configured}}

	got := tbl.ConfiguredOnly()
	if len(got.Chars) != 1 || got.Chars[0] != configured {
		t.Fatalf("ConfiguredOnly = %d chars, want only the configured one", len(got.Chars))
	}
	if len(tbl.Chars) != 2 {
		t.Fatalf("ConfiguredOnly mutated the receiver: %d chars, want 2", len(tbl.Chars))
	}
}

// With no configured characters, WriteMods must succeed without writing an
// empty ini, and must remove a stale ini left by an earlier run (which would
// otherwise reference deltas that no longer exist). Zero bytes signals "skip".
func TestWriteModsSkipsWhenNothingConfigured(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "WWEmoteMods.ini")
	stale := "namespace = WWEmote\n\n[TextureOverrideEpPoseHsin]\nhash = aaaabbbb\n"
	if err := os.WriteFile(out, []byte(stale), 0o644); err != nil {
		t.Fatalf("seed stale ini: %v", err)
	}
	tbl := &panel.Table{K: 1, Chars: []*panel.Char{
		{Cid: 3, Name: "hsin", Pose: true, AnchorHash: "aaaabbbb"},
	}}
	g := New(tbl, dir, "")
	path, n, err := g.WriteMods(out, "")
	if err != nil {
		t.Fatalf("WriteMods with no configured chars must not fail, got: %v", err)
	}
	if n != 0 || path != "" {
		t.Fatalf("want (empty path, 0 bytes), got (%q, %d)", path, n)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("stale ini should have been removed, stat err = %v", err)
	}
}

// An orphan shim can outlive the ini that used it: the ini is deleted on an
// empty render, but include_recursive still loads every _wwemote_ns_*.ini and
// its namespace would point at a mod no longer configured. The empty branch
// must clear those shims too.
func TestWriteModsClearsOrphanShimsWhenNothingConfigured(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "WWEmoteMods.ini")
	shim := filepath.Join(dir, "_wwemote_ns_102b3170c061.ini")
	userFile := filepath.Join(dir, "user.ini")
	for p, body := range map[string]string{
		out:      "namespace = WWEmote\n",
		shim:     "namespace = Mods\\old\\mod.ini\n",
		userFile: "; user layer\n",
	} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}
	g := New(&panel.Table{K: 1}, dir, "")
	if _, n, err := g.WriteMods(out, ""); err != nil || n != 0 {
		t.Fatalf("empty render: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(shim); !os.IsNotExist(err) {
		t.Fatalf("orphan shim should have been removed, stat err = %v", err)
	}
	// user.ini is the user's own expression state, not a generated shim: it
	// must survive a config that happens to render nothing.
	if _, err := os.Stat(userFile); err != nil {
		t.Fatalf("user.ini should be left alone, stat err = %v", err)
	}
}
