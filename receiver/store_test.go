package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testThread = "11111111-1111-1111-1111-111111111111"

func rollout(t testing.TB, home, name, data string) string {
	t.Helper()
	path := filepath.Join(home, "sessions", name+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func appendRollout(t testing.TB, path, data string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(data); err != nil {
		t.Fatal(err)
	}
}
func metaRecord(id string) string {
	b, _ := json.Marshal(M{"type": "session_meta", "timestamp": "2026-09-01T00:00:00Z", "payload": M{"id": id, "cwd": "/café"}})
	return string(b) + "\n"
}

func TestOnDemandCatalogAndIncrementalReadsNeverPersist(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	record := metaRecord(testThread)
	cut := strings.Index(record, "é") + 1
	path := rollout(t, home, "rollout-"+testThread, record[:cut])
	rollout(t, home, "unopened", strings.Repeat("x", maxRecordBytes+1))
	s := testStore(t, home)
	list, err := s.query(ctx, "sessions", M{"limit": 20})
	if err != nil || num(obj(list)["total"]) != 2 {
		t.Fatal(list, err)
	}
	if len(s.cache) != 0 {
		t.Fatal("catalog parsed a rollout")
	}
	first, err := s.get(ctx, testThread)
	if err != nil || num(obj(first["diagnostics"])["records"]) != 0 {
		t.Fatal(first, err)
	}
	appendRollout(t, path, record[cut:])
	second, err := s.get(ctx, str(first["id"]))
	if err != nil || second["cwd"] != "/café" || num(obj(second["diagnostics"])["records"]) != 1 {
		t.Fatal(second, err)
	}
	s.scanned = time.Time{}
	_, err = s.query(ctx, "review", M{"session": first["id"]})
	if err != nil {
		t.Fatal(err)
	}
	if !s.scanned.IsZero() {
		t.Fatal("known task triggered discovery")
	}
	if len(s.cache) != 1 {
		t.Fatal("read unrelated task")
	}
	rows, _ := s.db.run("SELECT total_changes() AS n")
	if num(rows[0]["n"]) != 0 {
		t.Fatal("live analysis wrote to SQLite")
	}
	os.Mkdir(filepath.Join(home, "archived_sessions"), 0700)
	if err := os.Rename(path, filepath.Join(home, "archived_sessions", filepath.Base(path))); err != nil {
		t.Fatal(err)
	}
	archived, err := s.query(ctx, "sessions", M{"archived": "true"})
	if err != nil || num(obj(archived)["total"]) != 1 {
		t.Fatal(archived, err)
	}
}

func TestSourceReplacementTruncationAndEvictionRebuild(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	path := rollout(t, home, "rollout-"+testThread, metaRecord(testThread))
	s := testStore(t, home)
	first, err := s.get(ctx, testThread)
	if err != nil {
		t.Fatal(err)
	}
	id := str(first["id"])
	appendRollout(t, path, "{bad}\n")
	withBad, err := s.get(ctx, id)
	if err != nil || num(obj(withBad["diagnostics"])["malformed"]) != 1 {
		t.Fatal(withBad, err)
	}
	if err := os.WriteFile(path, []byte(metaRecord("new")), 0600); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := s.get(ctx, id)
	if err != nil || rebuilt["threadId"] != "new" || num(obj(rebuilt["diagnostics"])["malformed"]) != 0 {
		t.Fatal(rebuilt, err)
	}
	oldState := s.cache[id].state
	// Same-sized edits are invalidated using mtime, including a partial prefix.
	if err := os.WriteFile(path, []byte(metaRecord("two")), 0600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(time.Second)
	os.Chtimes(path, at, at)
	rebuilt, err = s.get(ctx, id)
	if err != nil || rebuilt["threadId"] != "two" || oldState == s.cache[id].state {
		t.Fatal(rebuilt, err)
	}
	replacement := path + ".new"
	os.WriteFile(replacement, []byte(metaRecord("replaced")), 0600)
	os.Rename(replacement, path)
	rebuilt, err = s.get(ctx, id)
	if err != nil || rebuilt["threadId"] != "replaced" {
		t.Fatal(rebuilt, err)
	}
	delete(s.cache, id)
	again, err := s.get(ctx, id)
	a, _ := json.Marshal(rebuilt)
	b, _ := json.Marshal(again)
	if err != nil || !bytes.Equal(a, b) {
		t.Fatal("eviction changed result", err)
	}
}

func TestExplicitTaskRequiredAndThreadResolvesNewest(t *testing.T) {
	home := t.TempDir()
	old := rollout(t, home, "old-"+testThread, metaRecord("old"))
	rollout(t, home, "new-"+testThread, metaRecord("new"))
	os.Chtimes(old, time.Unix(1, 0), time.Unix(1, 0))
	s := testStore(t, home)
	for _, id := range []string{"", "latest"} {
		if _, err := s.query(context.Background(), "review", M{"session": id}); err == nil {
			t.Fatal("implicit task accepted")
		}
	}
	m, err := s.get(context.Background(), testThread)
	if err != nil || m["threadId"] != "new" {
		t.Fatal(m, err)
	}
	pinned, err := s.get(context.Background(), hash(old, 16))
	if err != nil || pinned["threadId"] != "old" {
		t.Fatal(pinned, err)
	}
}

func TestContextCancellationAndConcurrentReads(t *testing.T) {
	home := t.TempDir()
	path := rollout(t, home, "rollout-"+testThread, metaRecord(testThread))
	s := testStore(t, home)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.query(ctx, "review", M{"session": testThread}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(s.cache) != 0 {
		t.Fatal("canceled request parsed data")
	}
	s.gate <- struct{}{}
	ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := s.query(ctx, "review", M{"session": testThread}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	<-s.gate
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.query(context.Background(), "review", M{"session": testThread}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	cached := s.cache[hash(path, 16)]
	if cached.state.Lines != 1 || cached.offset != int64(len(metaRecord(testThread))) {
		t.Fatal("duplicate parsing", cached.offset, cached.state.Lines)
	}
}

func TestBoundedRecordsAndLRUEviction(t *testing.T) {
	ctx := context.Background()
	if _, err := readRecord(ctx, bufio.NewReader(strings.NewReader(strings.Repeat("x", maxRecordBytes+1)))); err == nil {
		t.Fatal("unbounded source record")
	}
	s := testStore(t, t.TempDir())
	for i, key := range []string{"oldest", "middle", "newest", "current"} {
		state := newState()
		state.bytes = maxTaskBytes
		s.cache[key] = &cachedRollout{state: state, used: time.Unix(int64(i), 0)}
	}
	s.reserve("incoming", maxTaskBytes)
	if len(s.cache) != 3 || s.cache["oldest"] != nil || s.cache["newest"] == nil {
		t.Fatal("not LRU")
	}
}

func TestParsedTaskLimit(t *testing.T) {
	home := t.TempDir()
	path := rollout(t, home, "rollout-"+testThread, metaRecord(testThread))
	s := testStore(t, home)
	if _, err := s.get(context.Background(), testThread); err != nil {
		t.Fatal(err)
	}
	cached := s.cache[hash(path, 16)]
	// Model an already-full retained cache, then append an otherwise small call.
	cached.state.bytes = maxTaskBytes
	appendRollout(t, path, `{"type":"response_item","timestamp":"2026-09-01T00:00:01Z","payload":{"type":"function_call","call_id":"c","name":"read","arguments":"{}"}}`+"\n")
	if _, err := s.get(context.Background(), testThread); err == nil || !strings.Contains(err.Error(), "parsed-data limit") {
		t.Fatal(err)
	}
	if len(s.cache) != 0 {
		t.Fatal("oversized state retained")
	}
}

// Deterministic cancellation at a record boundary, without timing-dependent sleeps.
type steppedContext struct {
	context.Context
	steps int
	hook  func(int)
}

func (c *steppedContext) Err() error {
	c.steps--
	if c.hook != nil {
		c.hook(c.steps)
	}
	if c.steps <= 0 {
		return context.Canceled
	}
	return nil
}

func TestCanceledPrefixResumesWithoutDoubleCounting(t *testing.T) {
	home := t.TempDir()
	path := rollout(t, home, "rollout-"+testThread, strings.Repeat(metaRecord(testThread), 100))
	s := testStore(t, home)
	ctx := &steppedContext{Context: context.Background(), steps: 60}
	if _, err := s.get(ctx, testThread); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	cached := s.cache[hash(path, 16)]
	if cached == nil || cached.state.Lines == 0 || cached.state.Lines >= 100 {
		t.Fatal("no resumable prefix")
	}
	initial := cached.state.Lines
	m, err := s.get(context.Background(), testThread)
	if err != nil || num(obj(m["diagnostics"])["records"]) != 100 || cached.state.Lines != 100 {
		t.Fatal("bad resume", initial, m, err)
	}
}

func TestReadStopsAtInitialWatermark(t *testing.T) {
	home := t.TempDir()
	data := strings.Repeat(metaRecord(testThread), 20)
	path := rollout(t, home, "rollout-"+testThread, data)
	s := testStore(t, home)
	if err := s.discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := &steppedContext{Context: context.Background(), steps: 10000, hook: func(n int) {
		if n == 9990 {
			appendRollout(t, path, metaRecord("appended"))
		}
	}}
	m, err := s.get(ctx, testThread)
	if err != nil || num(obj(m["diagnostics"])["records"]) != 20 {
		t.Fatal(m, err)
	}
	m, err = s.get(context.Background(), testThread)
	if err != nil || num(obj(m["diagnostics"])["records"]) != 21 || m["threadId"] != "appended" {
		t.Fatal(m, err)
	}
}

func TestReviewRevisionSurvivesSourceDeletion(t *testing.T) {
	home := t.TempDir()
	data := metaRecord(testThread) + `{"type":"response_item","timestamp":"2026-09-01T00:00:01Z","payload":{"type":"function_call","name":"read","call_id":"c","arguments":"{}"}}` + "\n"
	path := rollout(t, home, "rollout-"+testThread, data)
	s := testStore(t, home)
	raw, err := s.query(context.Background(), "review", M{"session": testThread})
	if err != nil {
		t.Fatal(err)
	}
	revision := str(obj(raw)["revision"])
	if revision == "" {
		t.Fatal("agent review has no revision")
	}
	os.Remove(path)
	s.cache = map[string]*cachedRollout{}
	detail, err := s.query(context.Background(), "span", M{"revision": revision, "id": "c"})
	if err != nil || obj(detail)["input"] != "{}" {
		t.Fatal(detail, err)
	}
	saved, err := s.createInvestigation(M{"revision": revision, "view": M{"selectedSpan": "c"}})
	if err != nil {
		t.Fatal(err)
	}
	// A fresh store has no catalog, parser state, or live revisions.
	restarted := newStore(s.db, t.TempDir())
	evidence, err := restarted.query(context.Background(), "investigation_span", M{"id": saved["id"], "span": "c"})
	if err != nil || obj(evidence)["input"] != "{}" {
		t.Fatal(evidence, err)
	}
}

func TestMaterializationLeavesOpenFactsAndAccountingUnchanged(t *testing.T) {
	p := newState()
	p.ingest(M{"type": "event_msg", "timestamp": iso(1000), "payload": M{"type": "task_started", "turn_id": "t"}})
	p.ingest(M{"type": "response_item", "timestamp": iso(2000), "payload": M{"type": "function_call", "call_id": "c", "name": "read", "arguments": "{}"}})
	before, _ := json.Marshal(p)
	first := mustMaterialize(t, p, 3000)
	second := mustMaterialize(t, p, 4000)
	after, _ := json.Marshal(p)
	if !bytes.Equal(before, after) || p.Spans["c"]["end"] != nil || p.Turns["t"]["end"] != nil {
		t.Fatal("display mutated parser facts")
	}
	if num(maps(first["spans"])[0]["durationMs"]) != 1000 || num(maps(second["spans"])[0]["durationMs"]) != 2000 {
		t.Fatal("open horizon frozen")
	}
	p.ingest(M{"type": "response_item", "timestamp": iso(5000), "payload": M{"type": "function_call_output", "call_id": "c", "output": "done"}})
	completed := mustMaterialize(t, p, 6000)
	if num(maps(completed["spans"])[0]["durationMs"]) != 3000 {
		t.Fatal("completion lost")
	}
	stateBytes := p.bytes
	for i := 0; i < 100; i++ {
		p.ingest(M{"type": "response_item", "timestamp": iso(5000), "payload": M{"type": "function_call_output", "call_id": "c", "output": "done"}})
	}
	if p.bytes != stateBytes {
		t.Fatal("replacement accumulates retained bytes", p.bytes, stateBytes)
	}
}
