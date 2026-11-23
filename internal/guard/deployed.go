package guard

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DeployedRule documents the invariant CheckDeployed enforces. A deployed
// panel folder lives under Mods/, and d3dx.ini has include_recursive = Mods,
// so EVERY .ini under that folder is parsed by 3DMigoto -- including backups,
// staging dirs and copies left by a previous deploy. A stale copy defines the
// same sections as the live one; whichever is parsed later silently wins, and
// the model tears with no error anywhere. Two independent signals are checked:
//
//  1. an .ini outside the small allowlist (backup / temp / stray leaf);
//  2. the same non-Constants section appearing in two different files.
//
// Signal 2 also catches a stray copy that happens to use an allowlisted path
// segment, so the two rules are deliberately overlapping, not exclusive.
//
// Only the panel folder is scanned, never all of Mods/: third-party mods
// legitimately use the same mesh hashes (4705b82b, 4ce859c3, ...).
func CheckDeployed(root string) []Issue {
	var out []Issue
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return append(out, Issue{File: root, Why: "panel root is not a directory"})
	}

	files := 0
	owners := map[string]map[string]bool{} // section -> file -> true
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "user":
				// user/ holds config products; they are not panel definitions.
				return fs.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), ".ini") {
			return nil
		}
		files++
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			rel = p
		}
		rel = filepath.ToSlash(rel)
		if !AllowedPanelIni(rel) {
			out = append(out, Issue{File: rel,
				Why: "ini outside the panel allowlist is still loaded by include_recursive = Mods"})
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		for _, ln := range strings.Split(string(data), "\n") {
			s := strings.TrimSpace(ln)
			if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") || len(s) < 3 {
				continue
			}
			section := strings.TrimSpace(s[1 : len(s)-1])
			// Constants is deliberately repeated in every shipped ini; a
			// hash line is deliberately repeated within one section.
			if section == "" || strings.EqualFold(section, "Constants") {
				continue
			}
			if owners[section] == nil {
				owners[section] = map[string]bool{}
			}
			owners[section][rel] = true
		}
		return nil
	})

	sections := make([]string, 0, len(owners))
	for s := range owners {
		sections = append(sections, s)
	}
	sort.Strings(sections)
	for _, section := range sections {
		where := make([]string, 0, len(owners[section]))
		for f := range owners[section] {
			where = append(where, f)
		}
		sort.Strings(where)
		if len(where) < 2 {
			continue
		}
		out = append(out, Issue{File: where[0],
			Why: fmt.Sprintf("section [%s] is defined in %d ini files: %s",
				section, len(where), strings.Join(where, ", "))})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Why < out[j].Why
	})
	return out
}

// AllowedPanelIni is the allowlist of ini paths a shipped panel folder owns:
// WWEmote.ini plus chars/<Char>/<Char>.ini. user/ is excluded from the walk.
func AllowedPanelIni(rel string) bool {
	rel = filepath.ToSlash(rel)
	if rel == "WWEmote.ini" {
		return true
	}
	parts := strings.Split(rel, "/")
	if len(parts) == 3 && parts[0] == "chars" {
		base := strings.TrimSuffix(parts[2], ".ini")
		return parts[1] == base && strings.HasSuffix(parts[2], ".ini")
	}
	return false
}
