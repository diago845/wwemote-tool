package panel

import (
	"strings"
	"testing"
)

// A fragment collision must report the paths it hit and the full list, so a
// diagnostics bundle tells the author which mods clashed without needing the
// user's install. The old message only said "matches 3 pvb_paths".
func TestDeltaTargetsErrorListsHitsAndAll(t *testing.T) {
	c := &Char{
		Label: "heart",
		PvbPaths: []string{
			`Resource\Mods\heart\thicc\Hsin Mod\1.ini\PositionBuffer`,
			`Resource\Mods\heart\HsinMod\1.ini\PositionBuffer`,
			`Resource\Mods\other\mod.ini\PositionBuffer`,
		},
		ModDelta: map[string]*Delta{
			"1.ini": {VC: 103514, Sparse: "d.buf", Base: "b.buf"},
		},
		ModDeltaOrder: []string{"1.ini"},
		ModVC:         map[string]int{"heart/thicc": 103514},
	}
	_, err := c.DeltaTargets("")
	if err == nil {
		t.Fatal("expected a collision error")
	}
	msg := err.Error()
	for _, want := range []string{
		`fragment "1.ini" matches 2 of 3 pvb_paths`,
		`hits=[`, `all=[`, `thicc\\Hsin Mod\\1.ini`, `other\\mod.ini`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q:\n%s", want, msg)
		}
	}
}
