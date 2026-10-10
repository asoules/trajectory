package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func claudeTranscript(t testing.TB, home, relative, data string) string {
	t.Helper()
	path := filepath.Join(home, "projects", "project", relative)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClaudeReviewNormalizesResponsesToolsAndTurns(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	fixture, err := os.ReadFile("testdata/claude.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	path := claudeTranscript(t, home, testThread+".jsonl", string(fixture))
	s := testStore(t, t.TempDir())
	s.claudeHome = home
	report, err := s.query(ctx, "review", M{"session": testThread})
	if err != nil {
		t.Fatal(err)
	}
	r := obj(report)
	u := obj(obj(r["tokens"])["usage"])
	// Five API responses, not seven assistant blocks. Input includes writes
	// and reads; only reads are subtracted to obtain uncached input.
	if r["title"] != "Config checks" || num(r["activeMs"]) != 21000 || num(obj(r["tokens"])["responses"]) != 5 || num(u["input_tokens"]) != 5743 || num(u["cached_input_tokens"]) != 5620 || num(u["cache_write_input_tokens"]) != 102 || num(u["output_tokens"]) != 115 || num(obj(r["tokens"])["uncachedInput"]) != 123 {
		t.Fatal(r)
	}
	trace, err := s.query(ctx, "trace", M{"revision": r["revision"]})
	if err != nil {
		t.Fatal(err)
	}
	tr := obj(trace)
	turns := maps(tr["turns"])
	meta := obj(tr["session"])
	if len(turns) != 2 || turns[0]["request"] != "Run checks and fix the config" || turns[1]["request"] != "Build now" || meta["source"] != "claude" || meta["threadId"] != testThread || meta["cwd"] != "/project" || meta["status"] != "idle" {
		t.Fatal(tr)
	}
	for _, want := range []struct {
		id, cat, status string
		ms, chars       float64
	}{{"bash-1", "tests", "failed", 7000, 6}, {"read-1", "tools", "completed", 1000, 4}, {"edit-1", "edit", "completed", 2000, 7}, {"build-1", "build", "completed", 6000, 5}} {
		detail, err := s.query(ctx, "span", M{"revision": r["revision"], "id": want.id})
		v := obj(detail)
		if err != nil || v["category"] != want.cat || v["status"] != want.status || v["source"] != "call-pair" || num(v["durationMs"]) != want.ms || num(v["deliveredChars"]) != want.chars {
			t.Fatal(want, v, err)
		}
	}
	filtered, err := s.query(ctx, "review", M{"session": testThread, "since": "2026-09-01T00:00:13Z"})
	if err != nil || num(obj(filtered)["activeMs"]) != 8000 || num(obj(obj(filtered)["tokens"])["responses"]) != 2 || num(obj(obj(obj(filtered)["tokens"])["usage"])["output_tokens"]) != 25 {
		t.Fatal(filtered, err)
	}
	// A delayed partial block cannot reopen an already completed response/turn.
	appendRollout(t, path, `{"type":"assistant","timestamp":"2026-09-01T00:00:13Z","message":{"id":"response-3","stop_reason":null,"usage":{"input_tokens":3,"output_tokens":1},"content":[{"type":"text","text":"partial"}]}}`+"\n")
	partial, err := s.query(ctx, "review", M{"session": testThread})
	if err != nil || num(obj(partial)["activeMs"]) != 21000 || num(obj(obj(obj(partial)["tokens"])["usage"])["output_tokens"]) != 115 {
		t.Fatal(partial, err)
	}
	// A replay after resume cannot add turns, tools, or usage to a live read.
	appendRollout(t, path, string(fixture))
	replayed, err := s.query(ctx, "review", M{"session": testThread})
	if err != nil || num(obj(replayed)["activeMs"]) != 21000 || num(obj(obj(replayed)["tokens"])["responses"]) != 5 || num(obj(obj(obj(replayed)["tokens"])["usage"])["output_tokens"]) != 115 {
		t.Fatal(replayed, err)
	}
}

func TestClaudeCatalogKeepsSourcesAndSubagentsSeparate(t *testing.T) {
	ctx := context.Background()
	codex, claude := t.TempDir(), t.TempDir()
	rollout(t, codex, "rollout-"+testThread, metaRecord(testThread))
	claudeTranscript(t, claude, testThread+".jsonl", strings.Repeat("x", maxRecordBytes+1))
	// Real subagent records use the same sessionId as their parent.
	child := `{"type":"user","uuid":"child-prompt","isSidechain":true,"agentId":"abc","sessionId":"` + testThread + `","timestamp":"2026-09-01T00:00:00Z","message":{"content":"Investigate"}}` + "\n" +
		`{"type":"assistant","isSidechain":true,"timestamp":"2026-09-01T00:00:01Z","message":{"id":"child-response","model":"claude-sonnet-4-5","stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":7},"content":[{"type":"text","text":"Done"}]}}` + "\n"
	claudeTranscript(t, claude, filepath.Join(testThread, "subagents", "agent-abc.jsonl"), child)
	claudeTranscript(t, claude, "unrelated.jsonl", "{bad}\n")
	s := testStore(t, codex)
	s.claudeHome = claude
	list, err := s.query(ctx, "sessions", M{"source": "claude"})
	if err != nil || num(obj(list)["total"]) != 2 || len(s.cache) != 0 {
		t.Fatal(list, err)
	}
	all, err := s.query(ctx, "sessions", M{"source": "all"})
	if err != nil || num(obj(all)["total"]) != 3 {
		t.Fatal(all, err)
	}
	alias := testThread + "/agent-abc"
	trace, err := s.query(ctx, "trace", M{"session": alias})
	meta := obj(obj(trace)["session"])
	if err != nil || meta["parentId"] != testThread || meta["agentId"] != "abc" || meta["threadId"] != alias || len(maps(obj(trace)["turns"])) != 1 || num(obj(obj(obj(trace)["summary"])["tokens"])["output_tokens"]) != 7 {
		t.Fatal(trace, err)
	}
	// An exact catalog ID is unambiguous even when providers share a UUID.
	for _, entry := range maps(obj(all)["sessions"]) {
		if entry["source"] == "rollout" {
			v, err := s.get(ctx, str(entry["id"]))
			if err != nil || v["source"] != "rollout" || v["cwd"] != "/café" {
				t.Fatal(v, err)
			}
		}
	}
}

func TestClaudeIncrementalReadCompletesPendingEvidence(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	start := `{"type":"user","uuid":"p","timestamp":"2026-09-01T00:00:00Z","message":{"content":"Run it"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-09-01T00:00:01Z","message":{"id":"m","stop_reason":null,"usage":{"input_tokens":2,"output_tokens":1},"content":[{"type":"tool_use","id":"c","name":"Bash","input":{"command":"go test ./..."}}]}}` + "\n"
	result := `{"type":"user","timestamp":"2026-09-01T00:00:07Z","message":{"content":[{"type":"tool_result","tool_use_id":"c","content":"café"}]}}` + "\n"
	cut := strings.Index(result, "é") + 1
	path := claudeTranscript(t, home, testThread+".jsonl", start+result[:cut])
	s := testStore(t, t.TempDir())
	s.claudeHome = home
	first, err := s.query(ctx, "summary", M{"session": testThread})
	if err != nil {
		t.Fatal(err)
	}
	revision := obj(first)["revision"]
	old, err := s.query(ctx, "span", M{"revision": revision, "id": "c"})
	if err != nil || obj(old)["status"] != "incomplete" || obj(old)["deliveredChars"] != nil {
		t.Fatal(old, err)
	}
	appendRollout(t, path, result[cut:]+`{"type":"assistant","timestamp":"2026-09-01T00:00:08Z","message":{"id":"m","stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":12},"content":[{"type":"text","text":"Done"}]}}`+"\n")
	updated, err := s.query(ctx, "summary", M{"session": testThread})
	if err != nil || num(obj(updated)["activeMs"]) != 8000 || num(obj(obj(updated)["tokens"])["output_tokens"]) != 12 {
		t.Fatal(updated, err)
	}
	detail, err := s.query(ctx, "span", M{"revision": obj(updated)["revision"], "id": "c"})
	if err != nil || obj(detail)["status"] != "completed" || obj(detail)["output"] != "café" || num(obj(detail)["durationMs"]) != 6000 {
		t.Fatal(detail, err)
	}
	frozen, err := s.query(ctx, "span", M{"revision": revision, "id": "c"})
	if err != nil || obj(frozen)["status"] != "incomplete" {
		t.Fatal(frozen, err)
	}
}

func TestClaudeMissingStopAndSyntheticContextDoNotChargeIdleGaps(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	records := []M{
		{"type": "user", "uuid": "p1", "timestamp": iso(1000), "message": M{"content": "First"}},
		{"type": "assistant", "timestamp": iso(2000), "message": M{"id": "a1", "content": []any{M{"type": "text", "text": "Done"}}}},
		{"type": "user", "uuid": "compact", "isCompactSummary": true, "timestamp": iso(3000), "message": M{"content": "Compacted context"}},
		{"type": "user", "uuid": "meta", "isMeta": true, "timestamp": iso(4000), "message": M{"content": "Hook context"}},
		{"type": "assistant", "isSidechain": true, "timestamp": iso(5000), "message": M{"id": "child", "usage": M{"input_tokens": 1000, "output_tokens": 1000}}},
		{"type": "user", "uuid": "p2", "timestamp": iso(60000), "message": M{"content": []any{M{"type": "image", "source": M{"data": "encoded"}}}}},
		{"type": "assistant", "timestamp": iso(61000), "message": M{"id": "a2", "model": "<synthetic>", "usage": M{"input_tokens": 0, "output_tokens": 0}, "stop_reason": "end_turn", "content": []any{M{"type": "text", "text": "Error"}}}},
		{"type": "queue-operation", "timestamp": iso(1000000)},
	}
	var data strings.Builder
	for _, record := range records {
		b, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		data.Write(b)
		data.WriteByte('\n')
	}
	claudeTranscript(t, home, testThread+".jsonl", data.String())
	s := testStore(t, t.TempDir())
	s.claudeHome = home
	v, err := s.query(ctx, "summary", M{"session": testThread})
	if err != nil || num(obj(v)["activeMs"]) != 2000 || num(obj(v)["turnCount"]) != 2 || obj(v)["tokens"] != nil || obj(v)["lastEvent"] != iso(61000) {
		t.Fatal(v, err)
	}
}
