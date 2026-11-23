package main

import (
	"fmt"
	"net"
	"net/http"
	"os"

	"wwemote/internal/gui"
)

// runServe starts the API server without a window. It prints the listening
// address so an external driver (tests, diagnostics) can reach the exact same
// endpoints the GUI uses.
func runServe(args []string) int {
	addr := "127.0.0.1:0"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--addr":
			i++
			if i < len(args) {
				addr = args[i]
			}
		default:
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", args[i])
			return 2
		}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ listen: %v\n", err)
		return 1
	}
	fmt.Printf("serving http://%s/\n", ln.Addr().String())
	os.Stdout.Sync()
	srv := gui.NewServer(nil)
	if err := http.Serve(ln, srv.Handler()); err != nil {
		fmt.Fprintf(os.Stderr, "✗ serve: %v\n", err)
		return 1
	}
	return 0
}
