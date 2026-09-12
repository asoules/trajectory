package main

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestAPIContractAndSecurity(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "api.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.close()
	store := newStore(db, t.TempDir())
	if err := store.seedDemo(); err != nil {
		t.Fatal(err)
	}
	app := application(store)
	for _, tc := range []struct {
		path, method, host, origin string
		status                     int
	}{{"/api/trace?session=demo&category=tests&limit=1", "GET", "127.0.0.1:4318", "", 200}, {"/api/summary?session=demo", "GET", "localhost:4318", "http://localhost:4318", 200}, {"/api/spans?limit=-1", "GET", "127.0.0.1:4318", "", 400}, {"/api/spans?since=invalid", "GET", "127.0.0.1:4318", "", 400}, {"/api/summary", "POST", "127.0.0.1:4318", "", 405}, {"/api/summary", "GET", "evil.test:4318", "", 403}, {"/api/summary", "GET", "127.0.0.1:4318", "https://evil.test", 403}, {"/overview.js", "GET", "127.0.0.1:4318", "", 200}, {"/assets.go", "GET", "127.0.0.1:4318", "", 404}} {
		r := httptest.NewRequest(tc.method, "http://127.0.0.1:4318"+tc.path, nil)
		r.Host = tc.host
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d != %d %s", tc.path, w.Code, tc.status, w.Body)
		}
		if tc.path == "/api/trace?session=demo&category=tests&limit=1" {
			var m M
			json.Unmarshal(w.Body.Bytes(), &m)
			if num(m["total"]) != 3 || num(m["offset"]) != 2 || maps(m["spans"])[0]["id"] != "test-3" {
				t.Fatal(m)
			}
			if maps(m["spans"])[0]["output"] != nil {
				t.Fatal("unbounded detail in list")
			}
		}
	}
}
