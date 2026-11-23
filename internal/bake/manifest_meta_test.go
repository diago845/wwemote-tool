package bake

import (
	"strings"
	"testing"
)

// The manifest is written into the user's Mods folder, next to the delta it
// describes. Its metadata must stay stable across builds, because a cache hit
// depends on the tags matching what the writer emits.
func TestManifestMetadataCarriesNoBuildTraces(t *testing.T) {
	values := map[string]string{
		"bakedBy":       BakedByTag,
		"keySemantics":  keySemanticsSparse,
		"nativeVariant": keySemanticsNative,
		"format":        "sparse-csr-v1",
	}
	for name, v := range values {
		if strings.TrimSpace(v) == "" {
			t.Errorf("%s is empty", name)
		}
		for _, r := range v {
			if r >= 0x4e00 && r <= 0x9fff {
				t.Errorf("%s = %q contains CJK", name, v)
				break
			}
		}
		low := strings.ToLower(v)
		for _, bad := range []string{".py", "python", "pywebview", "bake_mod"} {
			if strings.Contains(low, bad) {
				t.Errorf("%s = %q mentions a build tool (%q)", name, v, bad)
			}
		}
	}

	body := string((&Manifest{
		Format:       "sparse-csr-v1",
		KeySemantics: keySemanticsSparse,
		BakedBy:      BakedByTag,
	}).JSON())
	for _, bad := range []string{"\u6279", ".py", "python"} {
		if strings.Contains(body, bad) {
			t.Errorf("serialised manifest contains %q:\n%s", bad, body)
		}
	}
}
