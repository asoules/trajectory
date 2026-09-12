package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Resolve defaults only after flags so an explicit -db works even without a home.
func databasePath(explicit string, demo bool) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	var directory string
	switch runtime.GOOS {
	case "darwin":
		// macOS uses Application Support, independent of XDG environment variables.
	case "windows":
		directory = os.Getenv("LOCALAPPDATA")
	default:
		directory = os.Getenv("XDG_DATA_HOME")
	}
	if !filepath.IsAbs(directory) {
		home, err := os.UserHomeDir()
		if err != nil || !filepath.IsAbs(home) {
			return "", fmt.Errorf("cannot determine user data directory; provide an explicit -db path")
		}
		switch runtime.GOOS {
		case "darwin":
			directory = filepath.Join(home, "Library", "Application Support")
		case "windows":
			directory = filepath.Join(home, "AppData", "Local")
		default:
			directory = filepath.Join(home, ".local", "share")
		}
	}
	name := "investigations.sqlite"
	if demo {
		name = "demo-investigations.sqlite"
	}
	return filepath.Join(directory, "trajectory", name), nil
}
