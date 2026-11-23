// Package modpath normalises mod folder paths so that disabling a mod with a
// DISABLED_ prefix never invalidates a configuration. Config artifacts are
// written without the marker; validation adds it back when looking for the
// real file on disk. The game simply does not load a prefixed mod, so enabling
// one by deleting the marker needs no reconfigure and no runtime hook.
package modpath

import (
	"os"
	"path/filepath"
	"strings"
)

const disabledPrefix = "DISABLED_"

func hasDisabled(s string) bool {
	return len(s) >= len(disabledPrefix) && strings.EqualFold(s[:len(disabledPrefix)], disabledPrefix)
}

// StripDisabled removes a leading DISABLED_ marker from every path segment,
// preserving the input's separator style so golden output stays byte-identical.
func StripDisabled(rel string) string {
	sep := "/"
	if strings.Contains(rel, "\\") && !strings.Contains(rel, "/") {
		sep = "\\"
	}
	parts := strings.Split(rel, sep)
	for i, p := range parts {
		if hasDisabled(p) {
			parts[i] = p[len(disabledPrefix):]
		}
	}
	return strings.Join(parts, sep)
}

// ResolveOnDisk returns the path that actually exists for an unprefixed path.
// The first match is the path as given; otherwise each segment is retried with
// a DISABLED_ marker, because that is how a user disables a mod folder. A
// genuinely renamed or removed mod is never recovered, so a stale config is
// still reported instead of silently rendering against a missing base.
func ResolveOnDisk(path string) (string, bool) {
	path = filepath.FromSlash(path)
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		return path, true
	}
	sep := string(filepath.Separator)
	parts := strings.Split(path, sep)
	for i, p := range parts {
		if p == "" || hasDisabled(p) {
			continue
		}
		orig := parts[i]
		parts[i] = disabledPrefix + orig
		cand := strings.Join(parts, sep)
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand, true
		}
		parts[i] = orig
	}
	return "", false
}

// PresentOnDisk reports whether a path names an existing regular file,
// treating DISABLED_ markers as optional on any segment. Unlike ResolveOnDisk
// it also tolerates a path that already carries the marker while the file on
// disk has since been enabled (marker removed) -- the direction a user takes
// right after enabling a mod. It is intended for cache checks: toggling the
// marker must not make a cached base look missing. A genuinely renamed or
// removed file still reports false, so a stale cache is invalidated as before.
func PresentOnDisk(path string) bool {
	// Canonicalise to the unmarked path, then let ResolveOnDisk find the file
	// in either state: a direct hit means the mod is currently enabled, and a
	// prefix-restored hit means it is currently disabled. One lookup covers
	// both directions of a toggle.
	_, ok := ResolveOnDisk(StripDisabled(path))
	return ok
}
