package main

/*
#cgo CFLAGS: -DSQLITE_THREADSAFE=1 -DSQLITE_OMIT_LOAD_EXTENSION
#cgo linux LDFLAGS: -lm -ldl
#include "sqlite3.h"
#include <stdlib.h>
static int bind_text(sqlite3_stmt *s, int n, const char *v) {
  return sqlite3_bind_text(s, n, v, -1, SQLITE_TRANSIENT);
}
*/
import "C"
import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unsafe"
)

type database struct {
	mu sync.Mutex
	db *C.sqlite3
}

func openDB(path string) (*database, error) {
	// A read/write connection can checkpoint even when it only inspected the
	// schema. Validate existing files read-only so rejecting the old archive
	// cannot rewrite its database or remove its WAL.
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		probe, err := connectDB(path, C.SQLITE_OPEN_READONLY)
		if err != nil {
			return nil, err
		}
		err = probe.validateSchema()
		probe.close()
		if err != nil {
			return nil, err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	d, err := connectDB(path, C.SQLITE_OPEN_READWRITE)
	if err != nil {
		return nil, err
	}
	err = d.exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL;
 CREATE TABLE IF NOT EXISTS investigations(id TEXT PRIMARY KEY,created_at TEXT NOT NULL,question TEXT NOT NULL,session TEXT NOT NULL,view TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS findings(id TEXT PRIMARY KEY,investigation_id TEXT NOT NULL,created_at TEXT NOT NULL,data TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS findings_investigation ON findings(investigation_id,created_at);`)
	if err != nil {
		d.close()
		return nil, err
	}
	return d, nil
}

func connectDB(path string, flags C.int) (*database, error) {
	d := &database{}
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	if C.sqlite3_open_v2(p, &d.db, flags, nil) != C.SQLITE_OK {
		err := d.err()
		d.close()
		return nil, err
	}
	C.sqlite3_busy_timeout(d.db, 5000)
	return d, nil
}

func (d *database) validateSchema() error {
	rows, err := d.run("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	for _, row := range rows {
		if row["name"] != "investigations" && row["name"] != "findings" {
			return fmt.Errorf("incompatible database: use a new investigations database; legacy Trajectory data is not migrated")
		}
	}
	if len(rows) != 2 {
		return fmt.Errorf("incompatible investigations database: expected investigations and findings tables")
	}
	_, err = d.run("SELECT id,created_at,question,session,view FROM investigations LIMIT 0")
	if err == nil {
		_, err = d.run("SELECT id,investigation_id,created_at,data FROM findings LIMIT 0")
	}
	if err != nil {
		return fmt.Errorf("incompatible investigations database: %w", err)
	}
	return nil
}
func (d *database) err() error { return fmt.Errorf("sqlite: %s", C.GoString(C.sqlite3_errmsg(d.db))) }
func (d *database) close() {
	if d.db != nil {
		C.sqlite3_close(d.db)
	}
}
func (d *database) exec(sql string) error {
	p := C.CString(sql)
	defer C.free(unsafe.Pointer(p))
	if C.sqlite3_exec(d.db, p, nil, nil, nil) != C.SQLITE_OK {
		return d.err()
	}
	return nil
}

// Bound parameters are used for every value originating outside the program.
func (d *database) run(sql string, values ...string) ([]M, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.runLocked(sql, values...)
}
func (d *database) runLocked(sql string, values ...string) ([]M, error) {
	p := C.CString(sql)
	defer C.free(unsafe.Pointer(p))
	var s *C.sqlite3_stmt
	if C.sqlite3_prepare_v2(d.db, p, -1, &s, nil) != C.SQLITE_OK {
		return nil, d.err()
	}
	defer C.sqlite3_finalize(s)
	for i, v := range values {
		p := C.CString(v)
		rc := C.bind_text(s, C.int(i+1), p)
		C.free(unsafe.Pointer(p))
		if rc != C.SQLITE_OK {
			return nil, d.err()
		}
	}
	rows := []M{}
	for {
		rc := C.sqlite3_step(s)
		if rc == C.SQLITE_DONE {
			return rows, nil
		}
		if rc != C.SQLITE_ROW {
			return nil, d.err()
		}
		m := M{}
		for i := C.int(0); i < C.sqlite3_column_count(s); i++ {
			key := C.GoString(C.sqlite3_column_name(s, i))
			switch C.sqlite3_column_type(s, i) {
			case C.SQLITE_INTEGER:
				m[key] = int64(C.sqlite3_column_int64(s, i))
			case C.SQLITE_FLOAT:
				m[key] = float64(C.sqlite3_column_double(s, i))
			case C.SQLITE_NULL:
				m[key] = nil
			default:
				m[key] = C.GoString((*C.char)(unsafe.Pointer(C.sqlite3_column_text(s, i))))
			}
		}
		rows = append(rows, m)
	}
}
