package main

import (
	"fmt"
	"os"

	"wwemote/internal/panel"
	"wwemote/internal/render"
)

const usage = `WWEmote configuration tool

  WWEmoteTool.exe
      Open the configuration window.

  WWEmoteTool.exe --scan --mods <WWMI root> --user-dir <panel>/user [--char <name>] [--version <version>]
      Scan the local Mods tree, bake deltas, write <user>/<character>.mods.json
      and rebuild the user-layer ini. All output stays under <panel>/user.

  WWEmoteTool.exe --mods-only --user-dir <panel>/user [--out <file>]
      Rebuild only the user-layer output (WWEmoteMods.ini + user.ini + delta
      *.buf) without opening a window. The scan step must have run first.

  WWEmoteTool.exe --serve [--addr 127.0.0.1:PORT]
      Start the local HTTP service only, print its address. For automation.
`

func main() {
	// This is a GUI binary: an unhandled panic kills the process with no
	// window, no console and no log line, which reads as "the exe is broken".
	// The message box at least tells the user something went wrong and what.
	defer func() {
		if r := recover(); r != nil {
			messageBox(fmt.Sprintf(
				"WWEmote hit an unexpected error and has to close.\n\n%v", r))
			os.Exit(3)
		}
	}()
	if len(os.Args) > 1 && os.Args[1] == "--scan" {
		os.Exit(runScan(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "--mods-only" {
		os.Exit(runModsOnly(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "--serve" {
		os.Exit(runServe(os.Args[2:]))
	}
	if len(os.Args) > 1 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	// Window mode only: the CLI modes above are explicitly driven (a second
	// --serve on another port is legitimate).
	if !acquireSingleInstance() {
		focusRunningWindow()
		os.Exit(0)
	}
	os.Exit(runWindow())
}

func runModsOnly(args []string) int {
	var userDir, out, version string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--user-dir":
			i++
			if i < len(args) {
				userDir = args[i]
			}
		case "--out":
			i++
			if i < len(args) {
				out = args[i]
			}
		case "--version":
			i++
			if i < len(args) {
				version = args[i]
			}
		default:
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", args[i])
			fmt.Fprint(os.Stderr, usage)
			return 2
		}
	}
	if userDir == "" {
		fmt.Fprintf(os.Stderr, "--mods-only needs --user-dir\n")
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	tbl, err := panel.Load(userDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		return 1
	}
	g := render.New(tbl, userDir, version)
	if out == "" {
		out = userDir + string(os.PathSeparator) + "WWEmoteMods.ini"
	}
	path, n, err := g.WriteMods(out, userDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		return 1
	}
	fmt.Printf("Wrote %s (%d bytes)\n", path, n)
	return 0
}
