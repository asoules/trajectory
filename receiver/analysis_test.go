package main

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

func TestAnalysisParity(t *testing.T) {
	raw, _ := os.ReadFile("testdata/demo.json")
	var session M
	json.Unmarshal(raw, &session)
	raw, _ = os.ReadFile("testdata/demo-summary.json")
	var expected M
	json.Unmarshal(raw, &expected)
	actual := mustAnalyze(t, session, M{"top": 12})
	for _, key := range []string{"activeMs", "elapsedMs", "betweenTurnsMs", "spanCount", "turnCount"} {
		if num(actual[key]) != num(expected[key]) {
			t.Fatalf("%s: %v != %v", key, actual[key], expected[key])
		}
	}
	for _, key := range []string{"breakdown", "groups"} {
		want := map[string]M{}
		for _, v := range maps(expected[key]) {
			want[str(choose(v["id"], v["category"]))] = v
		}
		for _, v := range maps(actual[key]) {
			w := want[str(choose(v["id"], v["category"]))]
			if w == nil {
				t.Fatal("unexpected", v)
			}
			for _, field := range []string{"ms", "percent", "selfMs", "totalMs", "calls", "failures"} {
				if math.Abs(num(v[field])-num(w[field])) > .01 {
					t.Fatalf("%s/%s: %v != %v", key, field, v, w)
				}
			}
		}
	}
}
func TestParserNestingAndUsage(t *testing.T) {
	s := newState()
	base := float64(1770000000000)
	add := func(kind string, p M, ms float64) {
		s.ingest(M{"type": kind, "payload": p, "timestamp": iso(base + ms)})
	}
	add("session_meta", M{"id": "thread"}, 0)
	add("event_msg", M{"type": "task_started", "turn_id": "t", "started_at": base / 1000}, 0)
	add("response_item", M{"type": "custom_tool_call", "call_id": "outer", "name": "exec", "input": "await test()"}, 0)
	add("event_msg", M{"type": "item_completed", "turn_id": "t", "started_at_ms": base + 1000, "completed_at_ms": base + 9000, "item": M{"id": "test", "type": "CommandExecution", "command": []any{"/bin/zsh", "-lc", "npm test"}, "exit_code": float64(1)}}, 9000)
	add("response_item", M{"type": "custom_tool_call_output", "call_id": "outer", "output": "done"}, 10000)
	add("event_msg", M{"type": "task_complete", "turn_id": "t", "completed_at": (base + 10000) / 1000}, 10000)
	for i := 0; i < 2; i++ {
		add("token_usage_record", M{"response_id": "r", "usage": M{"total_tokens": float64(42)}}, 10000)
	}
	m := mustMaterialize(t, s, base+10000)
	a := mustAnalyze(t, m, M{"top": 12})
	if num(a["activeMs"]) != 10000 || num(obj(m["tokens"])["total_tokens"]) != 42 {
		t.Fatal(a)
	}
	for _, g := range maps(a["groups"]) {
		if g["category"] == "tools" && num(g["selfMs"]) != 2000 {
			t.Fatal(g)
		}
		if g["category"] == "tests" && num(g["failures"]) != 1 {
			t.Fatal(g)
		}
	}
	for _, v := range maps(m["spans"]) {
		if v["id"] == "test" && v["parentId"] != "outer" {
			t.Fatal(v)
		}
	}
}
