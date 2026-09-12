package main

import (
	"context"
	"path/filepath"
	"testing"
)

func mustAnalyze(t testing.TB, session, args M) M {
	t.Helper()
	result, err := analyze(context.Background(), session, args)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func mustMaterialize(t testing.TB, state *parseState, now float64) M {
	t.Helper()
	result, err := state.materialize(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func mustBackground(t testing.TB, session M) map[string]M {
	t.Helper()
	result, err := backgroundServices(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func mustToolReport(t testing.TB, selected []M, args M, services map[string]M) M {
	t.Helper()
	result, err := toolReport(context.Background(), selected, args, services)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func mustChangeReport(t testing.TB, selected []M, args M) M {
	t.Helper()
	result, err := changeReport(context.Background(), selected, args)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
func testStore(t testing.TB, home string) *sourceStore {
	t.Helper()
	db, err := openDB(filepath.Join(t.TempDir(), "investigations.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.close)
	s := newStore(db, home)
	return s
}
