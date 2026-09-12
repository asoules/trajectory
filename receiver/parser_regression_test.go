package main

import (
	"context"
	"strings"
	"testing"
)

func parserFixture() (*parseState, func(string, M, float64)) {
	p := newState()
	add := func(kind string, payload M, at float64) {
		p.ingest(M{"type": kind, "payload": payload, "timestamp": iso(1770000000000 + at)})
	}
	add("session_meta", M{"id": "fixture"}, 0)
	add("event_msg", M{"type": "task_started", "turn_id": "t", "started_at": float64(1770000000)}, 0)
	return p, add
}
func explicit(add func(string, M, float64), id, cmd string, start, end float64) {
	add("event_msg", M{"type": "item_completed", "turn_id": "t", "started_at_ms": 1770000000000 + start, "completed_at_ms": 1770000000000 + end, "item": M{"id": id, "type": "CommandExecution", "command": []any{"/bin/zsh", "-lc", cmd}, "exit_code": float64(0)}}, end)
}
func breakdownMS(summary M, category string) float64 {
	for _, row := range maps(summary["breakdown"]) {
		if row["category"] == category {
			return num(row["ms"])
		}
	}
	return 0
}

func TestParallelAndNestedWorkKeepsWallTimeDistinctFromSummedWork(t *testing.T) {
	p, add := parserFixture()
	explicit(add, "a", "npm test", 0, 10000)
	explicit(add, "b", "npm run build", 2000, 6000)
	add("event_msg", M{"type": "task_complete", "turn_id": "t", "completed_at": float64(1770000010)}, 10000)
	m := mustMaterialize(t, p, 1770000010000)
	summary := mustAnalyze(t, m, M{"top": 12})
	if num(summary["activeMs"]) != 10000 || breakdownMS(summary, "tests") != 8000 || breakdownMS(summary, "build") != 2000 {
		t.Fatal(summary)
	}
	if num(maps(summary["groups"])[0]["selfMs"]) != 10000 {
		t.Fatal("summed work was apportioned")
	}

	p, add = parserFixture()
	add("response_item", M{"type": "custom_tool_call", "name": "exec", "call_id": "outer", "input": "await test()"}, 0)
	explicit(add, "child", "npm test", 1000, 9000)
	add("response_item", M{"type": "custom_tool_call_output", "call_id": "outer", "output": "done"}, 10000)
	add("event_msg", M{"type": "item_completed", "turn_id": "t", "started_at_ms": float64(1770000000000), "completed_at_ms": float64(1770000010000), "item": M{"id": "independent", "type": "McpToolCall", "server": "external", "tool": "independent"}}, 10000)
	add("event_msg", M{"type": "task_complete", "turn_id": "t"}, 10000)
	summary = mustAnalyze(t, mustMaterialize(t, p, 1770000010000), M{"top": 12})
	if breakdownMS(summary, "tests") != 4000 || breakdownMS(summary, "tools") != 6000 {
		t.Fatal(summary)
	}
}

func TestHistoricalGapsAndIncrementalIntervals(t *testing.T) {
	p, add := parserFixture()
	explicit(add, "a", "pytest", 0, 10000)
	add("event_msg", M{"type": "task_complete", "turn_id": "t"}, 10000)
	add("event_msg", M{"type": "task_started", "turn_id": "t2"}, 100000)
	add("response_item", M{"type": "function_call", "call_id": "pending", "name": "wait", "arguments": "{}"}, 105000)
	m := mustMaterialize(t, p, 1770000400000)
	summary := mustAnalyze(t, m, M{"top": 12})
	if m["status"] != "stale" || num(summary["activeMs"]) != 15000 || num(summary["betweenTurnsMs"]) != 90000 {
		t.Fatal(summary)
	}
	window := mustAnalyze(t, m, M{"top": 12, "turnId": "t", "since": iso(1770000005000)})
	if num(window["activeMs"]) != 5000 || num(maps(window["groups"])[0]["selfMs"]) != 5000 || window["tokens"] != nil {
		t.Fatal(window)
	}
}

func TestExplicitSpanSupersedesTransportWithoutLosingFailure(t *testing.T) {
	p, add := parserFixture()
	add("response_item", M{"type": "function_call", "name": "exec_command", "call_id": "transport", "arguments": `{"cmd":"npm test"}`}, 0)
	explicit(add, "native", "npm test", 1000, 9000)
	add("response_item", M{"type": "function_call_output", "call_id": "transport", "output": "done"}, 10000)
	add("event_msg", M{"type": "task_complete", "turn_id": "t"}, 10000)
	m := mustMaterialize(t, p, 1770000010000)
	if len(maps(m["spans"])) != 1 || num(maps(m["spans"])[0]["durationMs"]) != 8000 {
		t.Fatal(m)
	}

	p, add = parserFixture()
	add("response_item", M{"type": "function_call", "name": "foo", "call_id": "call", "arguments": "{}"}, 0)
	add("event_msg", M{"type": "item_completed", "started_at_ms": float64(1770000000100), "completed_at_ms": float64(1770000001000), "item": M{"id": "call", "type": "McpToolCall", "server": "mcp", "tool": "foo", "status": "failed", "error": "denied"}}, 1000)
	add("response_item", M{"type": "function_call_output", "call_id": "call", "output": "error"}, 2000)
	span := maps(mustMaterialize(t, p, 1770000003000)["spans"])[0]
	if span["status"] != "failed" || num(span["durationMs"]) != 900 {
		t.Fatal(span)
	}
}

func TestLegacyExitAndCommandClassification(t *testing.T) {
	p, add := parserFixture()
	add("response_item", M{"type": "function_call", "name": "exec_command", "call_id": "c", "arguments": `{"cmd":"pytest"}`}, 0)
	add("response_item", M{"type": "function_call_output", "call_id": "c", "output": "Process exited with code 1\nOutput: failed"}, 2000)
	span := maps(mustMaterialize(t, p, 1770000003000)["spans"])[0]
	if span["status"] != "failed" || num(span["exitCode"]) != 1 {
		t.Fatal(span)
	}
	for _, cmd := range []string{"cat > file <<'EOF'\nnpm test\nEOF", `echo "npm test"`} {
		if category("CommandExecution", cmd) != "shell" {
			t.Fatal(cmd)
		}
	}
	for _, cmd := range []string{"cd app && npm test", "python3 -m pytest -q"} {
		if category("CommandExecution", cmd) != "tests" {
			t.Fatal(cmd)
		}
	}
}

func TestAggregateIdentityUsesMoreThanDisplayLabel(t *testing.T) {
	p, add := parserFixture()
	prefix := "npm test " + strings.Repeat("x", 170)
	explicit(add, "a", prefix+" first", 0, 1000)
	explicit(add, "b", prefix+" second", 1000, 2000)
	add("event_msg", M{"type": "task_complete", "turn_id": "t"}, 2000)
	groups := maps(mustAnalyze(t, mustMaterialize(t, p, 1770000003000), M{"top": 12})["groups"])
	if len(groups) != 2 || groups[0]["label"] != groups[1]["label"] || groups[0]["id"] == groups[1]["id"] {
		t.Fatal(groups)
	}
}

func TestFileChangeIdentityUsesFullPathListBeforeDisplayTruncation(t *testing.T) {
	p, add := parserFixture()
	prefix := "/repo/" + strings.Repeat("x", 17000)
	for n, suffix := range []string{"first", "second"} {
		add("event_msg", M{
			"type":            "item_completed",
			"turn_id":         "t",
			"started_at_ms":   float64(1770000000000 + n*1000),
			"completed_at_ms": float64(1770000000500 + n*1000),
			"item": M{
				"id":      "edit-" + suffix,
				"type":    "FileChange",
				"changes": M{prefix + suffix: M{"type": "update", "content": suffix}},
			},
		}, float64(500+n*1000))
	}
	spans := maps(mustMaterialize(t, p, 1770000003000)["spans"])
	if len(spans) != 2 || spans[0]["input"] != spans[1]["input"] || spans[0]["inputKey"] == spans[1]["inputKey"] {
		t.Fatal("FileChange display inputs must truncate equally while full-input fingerprints remain distinct")
	}
}

func TestFileChangeDetailsDoNotChangeTimeReviewSemantics(t *testing.T) {
	p, add := parserFixture()
	for n := range 3 {
		start := n * 2500
		add("event_msg", M{
			"type":            "item_completed",
			"turn_id":         "t",
			"started_at_ms":   float64(1770000000000 + start),
			"completed_at_ms": float64(1770000002500 + start),
			"item": M{
				"id":      "edit-" + str(n),
				"type":    "FileChange",
				"changes": M{"/repo/file-" + str(n): M{"type": "update"}},
			},
		}, float64(start+2500))
	}
	add("event_msg", M{"type": "task_complete", "turn_id": "t"}, 7500)
	review := obj(mustAnalyze(t, mustMaterialize(t, p, 1770000007500), M{"top": 12})["review"])
	items := maps(review["time"])
	if len(items) != 1 || items[0]["title"] != "Recorded activity" || num(items[0]["calls"]) != 3 {
		t.Fatal(review)
	}
}

func TestCancellationDuringMaterializationAndAnalysis(t *testing.T) {
	p, add := parserFixture()
	for i := 0; i < 100; i++ {
		explicit(add, str(i), "npm test", float64(i), float64(i+10))
	}
	ctx := &steppedContext{Context: context.Background(), steps: 20}
	if _, err := p.materialize(ctx, 1770000003000); err != context.Canceled {
		t.Fatal(err)
	}
	m := mustMaterialize(t, p, 1770000003000)
	ctx = &steppedContext{Context: context.Background(), steps: 20}
	if _, err := analyze(ctx, m, M{"top": 12}); err != context.Canceled {
		t.Fatal(err)
	}
}
