// Package guard keeps generated artifacts free of Chinese prose. Artifacts
// reach users who never see this repository, so the rule is checked in two
// places that fail for different reasons: CheckSource (from go test) covers
// the generator code, and CheckDir/CheckStale (from --audit) covers files on
// disk, including those left by an older build. Both share one rune test.
package guard

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Issue is one finding. Stale marks a file that is clean but was produced by a
// different build: shipping it would keep an old tag in front of the user.
type Issue struct {
	File  string
	Line  int
	Text  string
	Why   string
	Stale bool
}

func (i Issue) String() string {
	kind := "violation"
	if i.Stale {
		kind = "stale"
	}
	if i.Line > 0 {
		return fmt.Sprintf("%s: %s:%d  %s", kind, i.File, i.Line, i.Why)
	}
	return fmt.Sprintf("%s: %s  %s", kind, i.File, i.Why)
}

// ViolatingRune reports the first rune a generated artifact may not contain:
// Han characters, CJK punctuation, full-width forms, the middle dot and the
// typographic dashes, ellipsis and quotes that Chinese prose uses. Latin-1
// accents are not flagged -- the shipped UI is not English-only.
func ViolatingRune(s string) (rune, bool) {
	for _, r := range s {
		switch {
		case r < 0x80:
		case unicode.Is(unicode.Han, r):
			return r, true
		case r >= 0x4e00 && r <= 0x9fff:
			return r, true
		case r >= 0x3000 && r <= 0x303f:
			return r, true
		case r >= 0xff00 && r <= 0xffef:
			return r, true
		case r >= 0x2018 && r <= 0x201d:
			return r, true
		case r == 0x2014, r == 0x2026, r == 0x00b7, r == 0x30fb:
			return r, true
		}
	}
	return 0, false
}

// CheckSource walks roots for Go files and reports every non-comment line that
// carries a violating rune. Plain comments are left alone: the compiler drops
// them, so they cannot reach a user. Test files are skipped because they hold
// the negative fixtures this package is verified with.
func CheckSource(roots ...string) []Issue {
	var out []Issue
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", "release", "_diag_run":
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			inBlock := false
			for i, ln := range strings.Split(string(src), "\n") {
				t := strings.TrimSpace(ln)
				if inBlock {
					if idx := strings.Index(t, "*/"); idx >= 0 {
						inBlock = false
						t = strings.TrimSpace(t[idx+2:])
					} else {
						continue
					}
				}
				if strings.HasPrefix(t, "/*") {
					if !strings.Contains(t, "*/") {
						inBlock = true
					}
					continue
				}
				if t == "" || strings.HasPrefix(t, "//") {
					continue
				}
				if r, ok := ViolatingRune(ln); ok {
					out = append(out, Issue{
						File: p,
						Line: i + 1,
						Text: t,
						Why:  fmt.Sprintf("%U outside a comment would be embedded in the exe and may reach an artifact", r),
					})
				}
			}
			return nil
		})
	}
	sortIssues(out)
	return out
}

// CheckDir audits every ini and json under dir. files reports how many were
// read, so a caller can tell "clean" from "found nothing to check".
func CheckDir(dir string) (issues []Issue, files int) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".ini", ".json":
		default:
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		files++
		if strings.EqualFold(filepath.Ext(p), ".ini") {
			issues = append(issues, checkINI(p, string(raw))...)
		} else {
			issues = append(issues, checkJSON(p, string(raw))...)
		}
		return nil
	})
	sortIssues(issues)
	return issues, files
}

// CheckStale reports bake manifests that are not from the current build. A
// manifest is stale when its generator tag differs, or when it carries no
// bake_version matching the current algorithm. The version check is what makes
// an algorithm change force a re-bake: baked_by alone stays the same across
// versions, so without it old deltas would be reused as if still valid.
func CheckStale(dir, tag, version string) []Issue {
	var out []Issue
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), "_manifest.json") {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		var man struct {
			BakedBy     string `json:"baked_by"`
			BakeVersion string `json:"bake_version"`
		}
		if json.Unmarshal(raw, &man) != nil {
			return nil
		}
		if man.BakedBy == tag && man.BakeVersion == version {
			return nil
		}
		why := fmt.Sprintf("baked_by = %q, bake_version = %q; expected %q / %q: re-scan to refresh",
			man.BakedBy, man.BakeVersion, tag, version)
		out = append(out, Issue{File: p, Stale: true, Why: why})
		return nil
	})
	sortIssues(out)
	return out
}

// checkINI treats a comment line as prose and everything else as code that may
// only carry Chinese inside a path from the user's own mod tree.
func checkINI(path, text string) []Issue {
	var out []Issue
	for i, ln := range strings.Split(text, "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		code, comment := ln, ""
		if idx := strings.Index(ln, ";"); idx >= 0 {
			code, comment = ln[:idx], ln[idx:]
		}
		if r, ok := ViolatingRune(comment); ok {
			out = append(out, Issue{File: path, Line: i + 1, Text: strings.TrimSpace(ln),
				Why: fmt.Sprintf("comment carries %U: comments are generated text, keep them ASCII", r)})
			continue
		}
		if r, ok := ViolatingRune(code); ok && !strings.ContainsAny(code, `/\`) {
			out = append(out, Issue{File: path, Line: i + 1, Text: strings.TrimSpace(ln),
				Why: fmt.Sprintf("%U in a value that is not a path from the user's mod tree", r)})
		}
	}
	return out
}

var quotedString = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

// checkJSON allows Chinese only in strings that came from the user's mod tree:
// a path, or a name that appears inside one of the paths in the same file. Set
// labels are exactly that case -- the label is the folder name.
func checkJSON(path, text string) []Issue {
	locs := quotedString.FindAllStringIndex(text, -1)
	vals := make([]string, 0, len(locs))
	lines := make([]int, 0, len(locs))
	for _, loc := range locs {
		vals = append(vals, unescapeLiteral(text[loc[0]+1:loc[1]-1]))
		lines = append(lines, 1+strings.Count(text[:loc[0]], "\n"))
	}
	paths := make([]string, 0, len(vals))
	for _, v := range vals {
		if strings.ContainsAny(v, `/\`) {
			paths = append(paths, v)
		}
	}
	var out []Issue
	for i, v := range vals {
		r, ok := ViolatingRune(v)
		if !ok || isUserData(v, paths) {
			continue
		}
		out = append(out, Issue{File: path, Line: lines[i], Text: v,
			Why: fmt.Sprintf("%U in a generated string: only paths and set names from the user's mod tree may be non-ASCII", r)})
	}
	rest := quotedString.ReplaceAllString(text, "")
	if r, ok := ViolatingRune(rest); ok {
		off := strings.Index(rest, string(r))
		out = append(out, Issue{File: path, Line: 1 + strings.Count(rest[:off], "\n"), Text: string(r),
			Why: fmt.Sprintf("%U outside a string literal", r)})
	}
	return out
}

func isUserData(v string, paths []string) bool {
	if strings.ContainsAny(v, `/\`) {
		return true
	}
	for _, p := range paths {
		if strings.Contains(p, v) {
			return true
		}
	}
	return false
}

func unescapeLiteral(s string) string {
	s = strings.ReplaceAll(s, `\\`, "\x00")
	s = strings.ReplaceAll(s, `\"`, `"`)
	return strings.ReplaceAll(s, "\x00", `\`)
}

func sortIssues(is []Issue) {
	sort.SliceStable(is, func(a, b int) bool {
		if is[a].File != is[b].File {
			return is[a].File < is[b].File
		}
		return is[a].Line < is[b].Line
	})
}
