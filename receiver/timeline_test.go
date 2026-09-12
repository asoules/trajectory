package main

import (
	"context"
	"testing"
)

func TestTimelineTurnMetadataDoesNotDependOnPageOrFilter(t *testing.T) {
	s := reviewFixture()
	s["turns"] = []M{{"id": "t", "start": 1000, "end": 121000, "label": "First"}, {"id": "two", "start": 130000, "end": 150000, "label": "Second"}}
	a := work("a", "go test", 1000, 3000)
	b := work("b", "go build", 4000, 5000)
	u := M{"id": "usage", "turnId": "two", "category": "model", "start": 140000, "end": 140000, "durationMs": 0, "usage": M{"input_tokens": 100, "cached_input_tokens": 60, "output_tokens": 20}}
	s["spans"] = []M{a, b, u}
	raw, err := querySession(context.Background(), s, "trace", M{"turnId": "t", "limit": 1, "offset": 0})
	if err != nil {
		t.Fatal(err)
	}
	trace := obj(raw)
	if len(maps(trace["spans"])) != 1 || len(maps(trace["turns"])) != 2 {
		t.Fatal("navigation depends on span page")
	}
	if num(obj(maps(trace["turns"])[1]["usage"])["total_tokens"]) != 120 {
		t.Fatal("other turn usage missing")
	}
	if maps(trace["turns"])[0]["usage"] != nil {
		t.Fatal("invented zero usage")
	}
	if maps(s["turns"])[1]["usage"] != nil {
		t.Fatal("mutated source")
	}
	view := M{"mode": "waterfall", "turnId": "t", "collapsed": []string{"t"}, "expanded": []string{}}
	rows := visibleRows(trace, view)
	if len(rows) != 1 || rows[0]["id"] != "a" {
		t.Fatal("collapsed state affected timeline", rows)
	}
}

func TestTurnRequestRetainsTextBeyondNavigationLabel(t *testing.T) {
	p := newState()
	p.ingest(M{"type": "event_msg", "timestamp": iso(1000), "payload": M{"type": "task_started", "turn_id": "t"}})
	request := "A request with detail\nKeep this second line for the inspector."
	p.ingest(M{"type": "response_item", "timestamp": iso(2000), "payload": M{"type": "message", "role": "user", "content": []any{M{"type": "input_text", "text": "<in-app-browser-context>ambient</in-app-browser-context>\n## My request:\n" + request}}}})
	turn := maps(mustMaterialize(t, p, 3000)["turns"])[0]
	if turn["request"] != request {
		t.Fatal("request lost or wrapper retained", turn)
	}
}

func TestSnapshotRejectsEvidenceFromAnotherDisplayedTurn(t *testing.T) {
	s := reviewFixture()
	s["turns"] = []M{{"id": "t"}, {"id": "other"}}
	s["spans"] = []M{work("span", "go test", 1000, 2000)}
	if _, err := validateView(M{"turnId": "other", "selectedSpan": "span"}, s); err == nil {
		t.Fatal("accepted evidence from another turn")
	}
}

func TestSavedViewAcceptsPageScrollAndPreservesLegacyOrigin(t *testing.T) {
	s := reviewFixture()
	s["spans"] = []M{work("span", "go test", 1000, 2000)}
	current, err := validateView(M{"selectedSpan": "span", "scrollRoot": "page", "scrollTop": 420}, s)
	if err != nil || current["scrollRoot"] != "page" || num(current["scrollTop"]) != 420 {
		t.Fatal(current, err)
	}
	legacy, err := validateView(M{"selectedSpan": "span", "scrollTop": 99}, s)
	if err != nil || legacy["scrollRoot"] != "timeline" {
		t.Fatal(legacy, err)
	}
}
