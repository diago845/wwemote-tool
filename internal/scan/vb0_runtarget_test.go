package scan

import (
	"os"
	"path/filepath"
	"testing"
)

// A newer WWMI mod assigns vb0 inside a command list invoked from the
// override section, not directly in the override section. It must classify
// as a normal raw set, not raw? (degraded).
func TestVB0ViaRunTargetIsRaw(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "mod.ini")
	ini := `[CommandListOverrideSharedResources]
ResourceBypassVB0 = ref vb0
run = CommandListResolveShapeKeys

[CommandListCleanupSharedResources]
vb0 = ref ResourceBypassVB0

[CommandListResolveShapeKeys]
if $external_shape_active == 1
    vb0 = ResourceExternalShapeKeyedPosition
else
    vb0 = ResourcePositionBuffer
endif

[ResourcePositionBuffer]
type = Buffer
filename = Meshes/Position.buf

[TextureOverrideX]
hash = 67b0a7f4
`
	if err := os.WriteFile(f, []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := scanIni(f)
	if err != nil {
		t.Fatal(err)
	}
	if rec.VB0 != "ResourcePositionBuffer" {
		t.Fatalf("VB0 = %q, want ResourcePositionBuffer", rec.VB0)
	}
	if got := classify(rec); got != KindRaw {
		t.Fatalf("classify = %q, want %q", got, KindRaw)
	}
}

// The old direct-assignment form must stay unchanged.
func TestVB0DirectStillRaw(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "mod.ini")
	ini := `[CommandListOverrideSharedResources]
vb0 = ResourcePositionBuffer

[ResourcePositionBuffer]
filename = Meshes/Position.buf
`
	if err := os.WriteFile(f, []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := scanIni(f)
	if err != nil {
		t.Fatal(err)
	}
	if got := classify(rec); got != KindRaw {
		t.Fatalf("classify = %q, want %q", got, KindRaw)
	}
}
