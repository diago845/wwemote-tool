package main

import (
	"strings"
	"testing"
)

// TestDragScriptContract pins the title-bar-only drag. Every failure mode here
// is silent: the window simply does not move, and nothing is logged.
//   - the listener must be on the header bar only (the user asked to shrink the
//     drag region to the title bar), and must not fall back to `document`,
//   - it must be capture-phase and check the left button,
//   - and it must bail out on controls, otherwise pressing a button drags the
//     window and the button never fires.
func TestDragScriptContract(t *testing.T) {
	if !strings.Contains(dragScript, "bar.addEventListener('mousedown'") {
		t.Error("drag listener is not bound on the header bar")
	}
	if strings.Contains(dragScript, "document.addEventListener('mousedown'") {
		t.Error("drag listener must NOT be bound on the whole document")
	}
	if !strings.Contains(dragScript, "querySelector('header.bar')") {
		t.Error("drag listener does not target the header bar")
	}
	if !strings.Contains(dragScript, ", true);") {
		t.Error("drag listener is not capture-phase")
	}
	if !strings.Contains(dragScript, "e.button !== 0") {
		t.Error("drag listener does not restrict itself to the left button")
	}
	if !strings.Contains(dragScript, "closest(BLOCK)") {
		t.Error("drag listener does not skip interactive elements")
	}
	for _, sel := range []string{"button", "input", "select", "textarea", "a", ".no-drag"} {
		if !strings.Contains(dragScript, sel) {
			t.Errorf("drag block list is missing %q", sel)
		}
	}
	if !strings.Contains(dragScript, "window.dragWindow") {
		t.Error("drag listener never calls the native bridge")
	}
	// Init() runs at document-created time, before the DOM is parsed. Querying
	// header.bar there returns null and the listener is silently never bound.
	// The script MUST defer binding until the DOM exists. Pin that contract.
	if !strings.Contains(dragScript, "DOMContentLoaded") && !strings.Contains(dragScript, "readyState") {
		t.Error("drag script binds at document-created time; header.bar does not exist yet - listener silently missing")
	}
	queryIdx := strings.Index(dragScript, "querySelector('header.bar')")
	attachIdx := strings.Index(dragScript, "function attach()")
	if queryIdx < 0 || attachIdx < 0 || queryIdx < attachIdx {
		t.Error("querySelector must be inside the deferred attach(), not at top level")
	}
}

// TestDragScriptDefersUntilDOM mirrors the real Init() timing: at script
// execution the DOM is empty, so a top-level querySelector would find nothing.
// It verifies the attach function only queries after DOMContentLoaded fires.
func TestDragScriptDefersUntilDOM(t *testing.T) {
	// The buggy shape was: `var bar = document.querySelector(...); if (!bar) return;`
	// at the top level. The fixed shape queries inside a function invoked later.
	if strings.Contains(dragScript, "var bar = document.querySelector('header.bar');\n\t\tif (!bar)") {
		t.Error("top-level query + early return detected: listener never attaches before DOM exists")
	}
	if !strings.Contains(dragScript, "document.addEventListener('DOMContentLoaded', attach)") {
		t.Error("no DOMContentLoaded deferred attach")
	}
}
