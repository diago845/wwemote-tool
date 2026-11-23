package gui

import (
	"os"
	"path/filepath"
	"strings"
)

// ConfigOptions are the optional switches below the address bar. All default
// to false so an ordinary configure keeps today's behaviour: disabled mods
// stay configured and nothing under user/ is deleted.
type ConfigOptions struct {
	IgnoreDisabled bool // exclude DISABLED_ mods from this run
	CleanStale     bool // delete config files whose mod no longer exists
}

// cleanStaleFiles removes config products with no mod behind them, decided
// only from this run's outputs: a sparse pair is stale if its name is absent
// from keepSparse, a <char>.mods.json if its char is absent from keepChars.
// Basing it on the current scan handles deletes, renames and DISABLED toggles
// without trusting absolute paths in old manifests. Only files directly in
// userDir are touched; it returns the number removed.
func cleanStaleFiles(userDir string, keepSparse, keepChars map[string]bool) (int, []string) {
	removed := 0
	var names []string

	entries, err := os.ReadDir(userDir)
	if err != nil {
		return 0, nil
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, "_sparse_manifest.json") {
			base := strings.TrimSuffix(name, "_sparse_manifest.json")
			bufName := base + "_sparse.buf"
			if keepSparse[bufName] {
				continue
			}
			// Delete both members of the pair.
			if removeIfExists(filepath.Join(userDir, name)) {
				removed++
				names = append(names, name)
			}
			if removeIfExists(filepath.Join(userDir, bufName)) {
				removed++
				names = append(names, bufName)
			}
		} else if strings.HasSuffix(name, "_sparse.buf") {
			if keepSparse[name] {
				continue
			}
			if removeIfExists(filepath.Join(userDir, name)) {
				removed++
				names = append(names, name)
			}
		}
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".mods.json") {
			continue
		}
		char := strings.TrimSuffix(name, ".mods.json")
		if keepChars[char] {
			continue
		}
		if removeIfExists(filepath.Join(userDir, name)) {
			removed++
			names = append(names, name)
		}
	}

	return removed, names
}

// cleanStaleScoped is the scoped-run variant. It only removes sparse files (and
// their manifests) that were referenced by a config this run touched and are
// no longer referenced by any final config. Character json files outside the
// chosen folder are never considered, so they cannot be deleted by a folder-
// scoped configure.
func cleanStaleScoped(userDir string, candidates, keepSparse map[string]bool) (int, []string) {
	removed := 0
	var names []string
	entries, err := os.ReadDir(userDir)
	if err != nil {
		return 0, nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		var bufName string
		switch {
		case strings.HasSuffix(name, "_sparse_manifest.json"):
			bufName = strings.TrimSuffix(name, "_sparse_manifest.json") + "_sparse.buf"
		case strings.HasSuffix(name, "_sparse.buf"):
			bufName = name
		default:
			continue
		}
		if !candidates[bufName] || keepSparse[bufName] {
			continue
		}
		if removeIfExists(filepath.Join(userDir, name)) {
			removed++
			names = append(names, name)
		}
	}
	return removed, names
}

func removeIfExists(p string) bool {
	return os.Remove(p) == nil
}
