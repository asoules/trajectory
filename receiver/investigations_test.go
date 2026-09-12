package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInvestigationPinsDisplayedRevisionAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite")
	db, e := openDB(path)
	if e != nil {
		t.Fatal(e)
	}
	store := newStore(db, t.TempDir())
	store.seedDemo()
	raw, e := store.query(context.Background(), "trace", M{"session": "demo", "limit": 5})
	if e != nil {
		t.Fatal(e)
	}
	trace := obj(raw)
	selected := maps(trace["spans"])[0]
	revision := str(trace["revision"])
	// Source advances after the human's view was rendered.
	var changed M
	json.Unmarshal([]byte(demoJSON), &changed)
	changed["end"] = num(changed["end"]) + 90000
	changed["spans"] = []M{}
	store.demo = changed
	saved, e := store.createInvestigation(M{"revision": revision, "question": "Is this repeated work?", "view": M{"selectedSpan": selected["id"], "offset": trace["offset"], "limit": 5, "collapsed": []any{"turn-1"}, "zoom": 2, "scrollTop": 43}})
	if e != nil {
		t.Fatal(e)
	}
	id := str(saved["id"])
	read, e := store.investigationQuery(context.Background(), "investigation_render", M{"id": id})
	if e != nil {
		t.Fatal(e)
	}
	r := obj(read)
	if num(obj(r["summary"])["spanCount"]) != 13 || r["asOf"] != saved["asOf"] {
		t.Fatal("snapshot changed", r)
	}
	if len(maps(obj(r["trace"])["viewRows"])) != 5 {
		t.Fatal("flat span rows not shared")
	}
	if num(obj(r["view"])["scrollTop"]) != 43 {
		t.Fatal("scroll not preserved")
	}
	view, e := store.investigationQuery(context.Background(), "investigation_view", M{"id": id})
	if e != nil {
		t.Fatal(e)
	}
	if len(maps(obj(obj(view)["rows"])["items"])) != 5 {
		t.Fatal("CLI rows differ")
	}
	finding, e := store.recordFinding(M{"investigation": id, "title": "Repeated test run", "observation": "Three invocations in the saved session.", "hypothesis": "Verification may be repeated.", "recommendation": "Check changed files before rerunning.", "evidence": []any{"test-1", "test-3"}})
	if e != nil {
		t.Fatal(e)
	}
	if len(maps(finding["evidence"])) != 2 {
		t.Fatal(finding)
	}
	if _, e = store.recordFinding(M{"investigation": id, "title": "Bad", "observation": "o", "hypothesis": "h", "recommendation": "r", "evidence": []any{"outside"}}); e == nil {
		t.Fatal("accepted outside evidence")
	}
	db.close()
	db, e = openDB(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.close()
	store = newStore(db, t.TempDir())
	read, e = store.investigationQuery(context.Background(), "investigation_render", M{"id": id, "span": "test-1"})
	if e != nil {
		t.Fatal(e)
	}
	r = obj(read)
	if obj(r["selectedSpan"])["id"] != "test-1" || len(maps(r["findings"])) != 1 {
		t.Fatal("evidence failed after restart", r)
	}
	if _, e = store.investigationQuery(context.Background(), "investigation_span", M{"id": id, "span": "test-1", "maxChars": 20}); e != nil {
		t.Fatal(e)
	}
}
func TestExpiredRevisionNeverSilentlyCapturesNewData(t *testing.T) {
	db, e := openDB(filepath.Join(t.TempDir(), "test.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.close()
	s := newStore(db, t.TempDir())
	var session M
	raw, _ := os.ReadFile("testdata/demo.json")
	json.Unmarshal(raw, &session)
	id, _ := s.remember(session)
	r := s.revisions.entries[id]
	r.created = time.Now().Add(-time.Hour)
	s.revisions.entries[id] = r
	if _, e := s.createInvestigation(M{"revision": id, "view": M{"selectedSpan": "test-1"}}); e == nil {
		t.Fatal("expired view accepted")
	}
}

func TestSavedReviewUsesSameTurnAndUsageAsBrowser(t *testing.T) {
	db, e := openDB(filepath.Join(t.TempDir(), "review.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.close()
	store := newStore(db, t.TempDir())
	session := reviewFixture()
	session["turns"] = []M{{"id": "t", "start": 1000, "end": 50000}, {"id": "u", "start": 51000, "end": 121000}}
	a := work("a", "npm test", 1000, 49000)
	b := work("b", "npm build", 51000, 120000)
	b["turnId"] = "u"
	use := work("usage", "", 52000, 52000)
	use["turnId"] = "u"
	use["category"] = "model"
	use["usage"] = M{"input_tokens": float64(10000), "cached_input_tokens": float64(8000), "output_tokens": float64(1000)}
	session["spans"] = []M{a, b, use}
	revision, e := store.remember(session)
	if e != nil {
		t.Fatal(e)
	}
	saved, e := store.createInvestigation(M{"revision": revision, "view": M{"mode": "review", "selectedSpan": "b", "turnId": "u"}})
	if e != nil {
		t.Fatal(e)
	}
	id := saved["id"]
	raw, e := store.investigationQuery(context.Background(), "investigation_review", M{"id": id})
	if e != nil {
		t.Fatal(e)
	}
	rendered, e := store.investigationQuery(context.Background(), "investigation_render", M{"id": id})
	if e != nil {
		t.Fatal(e)
	}
	one, _ := json.Marshal(raw)
	two, _ := json.Marshal(obj(obj(rendered)["summary"])["review"])
	if string(one) != string(two) {
		t.Fatalf("saved CLI review differs from browser\n%s\n%s", one, two)
	}
	all, e := store.investigationQuery(context.Background(), "investigation_review", M{"id": id, "turnId": ""})
	if e != nil {
		t.Fatal(e)
	}
	if num(obj(all)["activeMs"]) <= num(obj(raw)["activeMs"]) {
		t.Fatal("explicit clear must remove saved filter")
	}
}
