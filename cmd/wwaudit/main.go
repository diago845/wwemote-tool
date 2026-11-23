// Command wwaudit is a build-time gate: it checks a directory of generated
// artifacts for non-ASCII prose in generated text and for manifests left by a
// different build. It exists only in the author's toolchain -- the shipped
// WWEmoteTool.exe carries no audit code -- and build_release.py drives it over the
// release package before the zip is signed off.
//
// Usage: wwaudit <dir> [--mods <deployed-panel-dir>]
// Exit codes: 0 clean, 1 violations, 2 bad invocation, 3 only stale files.
// With --mods the deployed panel folder is also checked for stray ini leaves
// and duplicate sections, which include_recursive would silently load.
package main

import (
	"fmt"
	"os"
	"strings"

	"wwemote/internal/bake"
	"wwemote/internal/guard"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	var dir, mods string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dir":
			i++
			if i < len(args) {
				dir = args[i]
			}
		case "--mods":
			i++
			if i < len(args) {
				mods = args[i]
			}
		default:
			if dir == "" && !strings.HasPrefix(args[i], "-") {
				dir = args[i]
				continue
			}
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", args[i])
			return 2
		}
	}
	if dir == "" {
		fmt.Fprintln(os.Stderr, "wwaudit needs a directory: wwaudit <panel-root>")
		return 2
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		fmt.Fprintf(os.Stderr, "not a directory: %s\n", dir)
		return 2
	}

	issues, files := guard.CheckDir(dir)
	issues = append(issues, guard.CheckStale(dir, bake.BakedByTag, bake.BakeVersion)...)
	violations, stale := 0, 0
	for _, is := range issues {
		if is.Stale {
			stale++
		} else {
			violations++
		}
		fmt.Println(is.String())
	}
	fmt.Printf("%s: %d ini/json scanned, %d violation(s), %d stale file(s)\n",
		dir, files, violations, stale)
	if mods != "" {
		deployed := guard.CheckDeployed(mods)
		for _, is := range deployed {
			fmt.Println(is.String())
		}
		fmt.Printf("%s: %d deployed-panel finding(s)\n", mods, len(deployed))
		violations += len(deployed)
	}
	switch {
	case violations > 0:
		return 1
	case stale > 0:
		return 3
	}
	return 0
}
