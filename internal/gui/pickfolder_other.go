//go:build !windows

package gui

import "fmt"

func pickFolderNative(title, initial string) (string, error) {
	return "", fmt.Errorf("folder picker requires Windows")
}

func openFolder(dir string) {}
