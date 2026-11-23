package guard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeIni(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckDeployedCleanPanel(t *testing.T) {
	root := t.TempDir()
	writeIni(t, root, "WWEmote.ini", "[Constants]\nglobal $ep_show = 0\n")
	writeIni(t, root, "chars/Hsin/Hsin.ini", "[Constants]\nglobal $ep_chr = 3\n\n[ResourceEpTok000]\nfilename = hsin_01.png\n")
	writeIni(t, root, "chars/Hsinult/Hsinult.ini", "[Constants]\nglobal $ep_chr = 4\n\n[ResourceEpTok020]\nfilename = hsinult_01.png\n")
	// user/ holds config products and is skipped entirely.
	writeIni(t, root, "user/WWEmoteMods.ini", "namespace = WWEmote\n[Constants]\nx = 1\n")
	if got := CheckDeployed(root); len(got) != 0 {
		t.Fatalf("clean panel should pass, got: %v", got)
	}
}

func TestCheckDeployedCatchesBackupCopy(t *testing.T) {
	// A chars ini backup left inside Mods/WWEmote is loaded by
	// include_recursive, tearing Hsin and Hsinult with zero errors.
	root := t.TempDir()
	writeIni(t, root, "WWEmote.ini", "[Constants]\nglobal $ep_show = 0\n")
	live := "[Constants]\nglobal $ep_chr = 4\n\n[CommandListEpNativeHsinult]\nvb0 = ResourceEpNativeOut4\n\n[TextureOverrideEpNativeHsinult]\nhash = 9bb8b247\n"
	writeIni(t, root, "chars/Hsinult/Hsinult.ini", live)
	writeIni(t, root, "_deploy_backups/chars_20261122_1543_pre_suisui_delta/Hsinult/Hsinult.ini", live)

	issues := CheckDeployed(root)
	if len(issues) < 2 {
		t.Fatalf("expected allowlist + duplicate-section findings, got %d: %v", len(issues), issues)
	}
	var hasLeaf, hasDup bool
	for _, is := range issues {
		if strings.Contains(is.Why, "outside the panel allowlist") {
			hasLeaf = true
		}
		if strings.Contains(is.Why, "section [CommandListEpNativeHsinult] is defined in 2 ini files") {
			hasDup = true
		}
	}
	if !hasLeaf || !hasDup {
		t.Fatalf("missing signal (leaf=%v dup=%v): %v", hasLeaf, hasDup, issues)
	}
}

func TestCheckDeployedIgnoresThirdPartyHashReuse(t *testing.T) {
	// Only the panel folder is scanned; a sibling mod with the same mesh
	// hash is legitimate and must not be reported.
	root := t.TempDir()
	writeIni(t, root, "chars/Hsin/Hsin.ini", "[Constants]\nx = 1\n\n[TextureOverrideEpCharHsin]\nhash = 67b0a7f4\n")
	sibling := filepath.Join(filepath.Dir(root), "other-mod")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "mod.ini"), []byte("[TextureOverrideEpCharHsin]\nhash = 67b0a7f4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CheckDeployed(root); len(got) != 0 {
		t.Fatalf("sibling mod must not be reported: %v", got)
	}
}

func TestAllowedPanelIni(t *testing.T) {
	cases := map[string]bool{
		"WWEmote.ini":                   true,
		"chars/Hsin/Hsin.ini":           true,
		"chars/Suisui/Suisui.ini":       true,
		"chars/Hsin/HsinUlt.ini":        false,
		"_deploy_backups/Hsin.ini":      false,
		"chars/Hsin/native_delta/x.ini": false,
	}
	for rel, want := range cases {
		if got := AllowedPanelIni(rel); got != want {
			t.Errorf("AllowedPanelIni(%q) = %v, want %v", rel, got, want)
		}
	}
}
