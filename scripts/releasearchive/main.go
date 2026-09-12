package main

import (
	"archive/tar"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type archiveEntry struct {
	name string
	path string
	mode int64
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "releasearchive:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("releasearchive", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("output", "", "output .tar.gz path")
	epoch := flags.String("epoch", "", "source commit time as Unix seconds")
	binary := flags.String("binary", "bin/trajectory", "trajectory executable path")
	license := flags.String("license", "LICENSE", "license path")
	readme := flags.String("readme", "README.md", "README path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	if *output == "" || *epoch == "" {
		return fmt.Errorf("-output and -epoch are required")
	}
	seconds, err := strconv.ParseInt(*epoch, 10, 64)
	if err != nil || seconds < 0 {
		return fmt.Errorf("-epoch must be a non-negative Unix timestamp")
	}
	return writeArchive(*output, time.Unix(seconds, 0).UTC(), []archiveEntry{
		{name: "trajectory", path: *binary, mode: 0755},
		{name: "LICENSE", path: *license, mode: 0644},
		{name: "README.md", path: *readme, mode: 0644},
	})
}

func writeArchive(output string, timestamp time.Time, entries []archiveEntry) (err error) {
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(output), ".trajectory-archive-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		temporary.Close()
		if err != nil {
			os.Remove(temporaryPath)
		}
	}()

	gz, err := gzip.NewWriterLevel(temporary, gzip.BestCompression)
	if err != nil {
		return err
	}
	gz.Header.ModTime = timestamp
	gz.Header.OS = 255
	archive := tar.NewWriter(gz)
	for _, entry := range entries {
		info, statErr := os.Stat(entry.path)
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", entry.path)
		}
		header := &tar.Header{
			Name:     entry.name,
			Mode:     entry.mode,
			Size:     info.Size(),
			ModTime:  timestamp,
			Typeflag: tar.TypeReg,
			Uname:    "root",
			Gname:    "root",
			Format:   tar.FormatUSTAR,
		}
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		file, openErr := os.Open(entry.path)
		if openErr != nil {
			return openErr
		}
		_, copyErr := io.Copy(archive, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temporaryPath, 0644); err != nil {
		return err
	}
	return os.Rename(temporaryPath, output)
}
