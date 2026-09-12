package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDatabaseDefaultsPersistOutsideWorkingDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("LOCALAPPDATA", "")
	directory := filepath.Join(home, ".local", "share", "trajectory")
	if runtime.GOOS == "darwin" {
		directory = filepath.Join(home, "Library", "Application Support", "trajectory")
	} else if runtime.GOOS == "windows" {
		directory = filepath.Join(home, "AppData", "Local", "trajectory")
	}
	for _, demo := range []bool{false, true} {
		name := "investigations.sqlite"
		if demo {
			name = "demo-investigations.sqlite"
		}
		path, err := databasePath("", demo)
		if err != nil || path != filepath.Join(directory, name) {
			t.Fatalf("demo=%v: %q, %v", demo, path, err)
		}
		// Resolving a default must not create directories or a database.
		if _, err := os.Stat(directory); !os.IsNotExist(err) {
			t.Fatalf("resolution touched the data directory: %v", err)
		}
	}
}

func TestDatabaseDirectoryOverrides(t *testing.T) {
	home, override := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", override)
	t.Setenv("LOCALAPPDATA", override)
	path, err := databasePath("", false)
	want := filepath.Join(override, "trajectory", "investigations.sqlite")
	if runtime.GOOS == "darwin" {
		want = filepath.Join(home, "Library", "Application Support", "trajectory", "investigations.sqlite")
	}
	if err != nil || path != want {
		t.Fatalf("%q, %v; want %q", path, err, want)
	}
	t.Setenv("XDG_DATA_HOME", "relative")
	t.Setenv("LOCALAPPDATA", "relative")
	path, err = databasePath("", false)
	if err != nil || !filepath.IsAbs(path) || filepath.Dir(filepath.Dir(path)) == override {
		t.Fatalf("relative environment directory must fall back to home: %q, %v", path, err)
	}
}

func TestExplicitDatabasePathWorksWithoutHomeIncludingDemo(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("LOCALAPPDATA", "")
	for _, demo := range []bool{false, true} {
		for _, explicit := range []string{".trajectory/investigations.sqlite", filepath.Join(t.TempDir(), "saved.sqlite")} {
			path, err := databasePath(explicit, demo)
			if err != nil || path != explicit {
				t.Fatalf("explicit path changed: %q, %v", path, err)
			}
		}
	}
	if _, err := databasePath("", false); err == nil {
		t.Fatal("missing home must require an explicit path")
	}
}
