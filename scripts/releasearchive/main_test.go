package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestReleaseArchiveIsReproducibleAndNormalized(t *testing.T) {
	root := t.TempDir()
	paths := map[string]string{}
	for name, body := range map[string]string{
		"trajectory": "binary",
		"LICENSE":    "license",
		"README.md":  "readme",
	} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		paths[name] = path
	}

	epoch := "1789142400"
	first := filepath.Join(root, "first.tar.gz")
	second := filepath.Join(root, "second.tar.gz")
	args := func(output string) []string {
		return []string{"-output", output, "-epoch", epoch, "-binary", paths["trajectory"], "-license", paths["LICENSE"], "-readme", paths["README.md"]}
	}
	if err := run(args(first)); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if err := os.Chtimes(path, time.Now(), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(args(second)); err != nil {
		t.Fatal(err)
	}

	firstBytes, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("identical inputs produced different release archives")
	}

	gz, err := gzip.NewReader(bytes.NewReader(firstBytes))
	if err != nil {
		t.Fatal(err)
	}
	archive := tar.NewReader(gz)
	var names []string
	var modes []int64
	wantTime := time.Unix(1789142400, 0).UTC()
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
		modes = append(modes, header.Mode)
		if header.Uid != 0 || header.Gid != 0 || !header.ModTime.Equal(wantTime) {
			t.Fatalf("unnormalized header for %s: %#v", header.Name, header)
		}
	}
	if !reflect.DeepEqual(names, []string{"trajectory", "LICENSE", "README.md"}) {
		t.Fatalf("archive order %v", names)
	}
	if !reflect.DeepEqual(modes, []int64{0755, 0644, 0644}) {
		t.Fatalf("archive modes %v", modes)
	}
}
