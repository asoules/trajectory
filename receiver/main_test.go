package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTelemetryRemovedAndHealthDoesNotWrite(t *testing.T) {
	s := testStore(t, t.TempDir())
	app := application(s)
	for _, path := range []string{"/v1/logs", "/v1/traces", "/api/telemetry"} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("POST", "http://127.0.0.1:4318"+path, strings.NewReader(`{"anything":true}`)))
		if w.Code != 404 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:4318/healthz", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	rows, err := s.db.run("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil || len(rows) != 2 || rows[0]["name"] != "findings" || rows[1]["name"] != "investigations" {
		t.Fatal(rows, err)
	}
	rows, _ = s.db.run("SELECT total_changes() AS n")
	if num(rows[0]["n"]) != 0 {
		t.Fatal("HTTP reads wrote to SQLite")
	}
	if len(s.entries) != 0 || len(s.cache) != 0 || !s.scanned.IsZero() {
		t.Fatal("health or telemetry requests inspected rollouts")
	}
}

func TestLegacyDatabaseRejectedWithoutMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.exec("CREATE TABLE batches(payload TEXT); INSERT INTO batches VALUES('keep me');"); err != nil {
		t.Fatal(err)
	}
	db.close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := openDB(path)
	if err == nil {
		reopened.close()
		t.Fatal("opened legacy database")
	}
	if !strings.Contains(err.Error(), "incompatible database") {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("legacy file was changed", err)
	}
}

func TestRejectUncheckpointedLegacyWAL(t *testing.T) {
	if path := os.Getenv("TRAJECTORY_TEST_LEGACY_WAL"); path != "" {
		db, err := openDB(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.exec("CREATE TABLE batches(payload TEXT); INSERT INTO batches VALUES('preserve WAL data');"); err != nil {
			t.Fatal(err)
		}
		os.Exit(0) // Deliberately leave committed data in the WAL, as after a crash.
	}
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	cmd := exec.Command(os.Args[0], "-test.run=^TestRejectUncheckpointedLegacyWAL$")
	cmd.Env = append(os.Environ(), "TRAJECTORY_TEST_LEGACY_WAL="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wal, err := os.ReadFile(path + "-wal")
	if err != nil || len(wal) == 0 {
		t.Fatal("fixture has no WAL", err)
	}
	db, err := openDB(path)
	if err == nil {
		db.close()
		t.Fatal("legacy WAL accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejection checkpointed the database", err)
	}
	afterWAL, err := os.ReadFile(path + "-wal")
	if err != nil || !bytes.Equal(wal, afterWAL) {
		t.Fatal("rejection changed/deleted the WAL", err)
	}
	// Read-only SQLite may update SHM coordination bytes; it must not delete it.
	if _, err := os.Stat(path + "-shm"); err != nil {
		t.Fatal(err)
	}
}
