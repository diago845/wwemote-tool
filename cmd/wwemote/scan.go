package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"wwemote/internal/panel"
	"wwemote/internal/render"
	"wwemote/internal/scan"
)

func jsonUnmarshal(raw []byte, v any) bool { return json.Unmarshal(raw, v) == nil }

// runScan is the user-side one-shot: scan the Mods/ tree, bake deltas, write
// <user>/<char>.mods.json, then rebuild the user-layer ini. It produces only
// ini/json/buf files inside <user>; nothing is written under the game directory.
func runScan(args []string) int {
	var wwmi, userDir, version string
	var chars []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--mods":
			i++
			if i < len(args) {
				wwmi = args[i]
			}
		case "--user-dir":
			i++
			if i < len(args) {
				userDir = args[i]
			}
		case "--char":
			i++
			if i < len(args) {
				chars = append(chars, args[i])
			}
		case "--version":
			i++
			if i < len(args) {
				version = args[i]
			}
		default:
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", args[i])
			return 2
		}
	}
	if wwmi == "" || userDir == "" {
		fmt.Fprintf(os.Stderr, "--scan needs --mods <WWMI root> and --user-dir <panel/user>\n")
		return 2
	}
	modsRoot := filepath.Join(wwmi, "Mods")
	if st, err := os.Stat(modsRoot); err != nil || !st.IsDir() {
		fmt.Fprintf(os.Stderr, "✗ no Mods/ directory under %s\n", wwmi)
		return 1
	}
	globs, err := scan.ReadExcludeGlobs(filepath.Join(wwmi, "d3dx.ini"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		return 1
	}

	tbl, err := panel.Load(userDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		return 1
	}
	byName := map[string]*panel.Char{}
	for _, c := range tbl.Chars {
		byName[c.Name] = c
	}
	if len(chars) == 0 {
		for _, c := range tbl.Chars {
			chars = append(chars, c.Name)
		}
	}

	idx, err := scan.BuildIndex(modsRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		return 1
	}
	scan.SetUserRoot(userDir)

	for _, nm := range chars {
		c := byName[nm]
		if c == nil {
			fmt.Fprintf(os.Stderr, "✗ unknown character: %s\n", nm)
			return 1
		}
		res := scan.ScanChar([]string{c.AnchorHash}, globs, idx)
		if len(res.FoundKeys) == 0 {
			if res.NDeclared > 0 {
				fmt.Fprintf(os.Stderr, "✗ %s: %d ini declare its anchor but none were claimed (unclaimed, not 'not installed')\n", nm, res.NDeclared)
				return 1
			}
			fmt.Printf("-- %s: not installed, skipped\n", nm)
			continue
		}
		needActive := c.NativeSparse != ""
		layer, errs := scan.UserLayer(res, modsRoot, needActive)
		if len(errs) > 0 {
			fmt.Fprintf(os.Stderr, "✗ %s: refusing to generate:\n", nm)
			for _, e := range errs {
				fmt.Fprintf(os.Stderr, "   %s\n", e)
			}
			return 1
		}
		for _, sk := range layer.Skipped {
			fmt.Printf("  ! %s: %s\n", nm, sk)
		}
		// Preserve the human note from the current file, if any.
		old := filepath.Join(userDir, nm+".mods.json")
		if raw, err := os.ReadFile(old); err == nil {
			var prev struct {
				Note []string `json:"_note"`
			}
			if jsonUnmarshal(raw, &prev) {
				layer.Note = prev.Note
			}
		}
		if needActive {
			if _, _, skipped, _, err := scan.AutoBake(nm, layer, modsRoot, res.Found); err != nil {
				fmt.Fprintf(os.Stderr, "✗ %s: %v\n", nm, err)
				return 1
			} else {
				for _, s := range skipped {
					fmt.Printf("  ! %s: %s\n", nm, s)
				}
			}
		}
		body := scan.DumpModsJSON(layer)
		if err := os.WriteFile(old, body, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s: %v\n", nm, err)
			return 1
		}
		fmt.Printf("OK %s: %d sets, %d bytes\n", nm, len(layer.SkPaths), len(body))
	}

	// Rebuild the user-layer ini from the freshly written json. A new panel.Load
	// is required: it must not see the pre-scan in-memory table.
	fresh, err := panel.Load(userDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		return 1
	}
	g := render.New(fresh, userDir, version)
	out := filepath.Join(userDir, "WWEmoteMods.ini")
	path, n, err := g.WriteMods(out, userDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		return 1
	}
	fmt.Printf("Wrote %s (%d bytes)\n", path, n)
	return 0
}
