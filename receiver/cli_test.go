package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestCLIOptionsPreserveEmptyValuesAndPositionals(t *testing.T) {
	args, positions, err := parseCLIArgs([]string{"review", "saved", "--turnId", "", "--category=", "--json", "--pretty=false", "--limit", "-1", "--question", "a quote ' and spaces"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(positions, []string{"review", "saved"}) || args["turnId"] != "" || args["category"] != "" || args["json"] != true || args["pretty"] != false || args["limit"] != "-1" || args["question"] != "a quote ' and spaces" {
		t.Fatal(args, positions)
	}
	for _, invalid := range [][]string{{"--session"}, {"--session", "--json"}, {"--typo", "x"}, {"--json=maybe"}} {
		if _, _, err := parseCLIArgs(invalid); err == nil {
			t.Fatalf("accepted invalid options %v", invalid)
		}
	}
}

func TestCLIHelpAndErrorsDoNotOpenDatabase(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("LOCALAPPDATA", "")
	for _, args := range [][]string{{"help"}, {"--help"}, {"review", "--help"}, {"investigation", "--help"}} {
		var output, diagnostics bytes.Buffer
		if err := run(args, strings.NewReader(""), &output, &diagnostics); err != nil || !strings.Contains(output.String(), "trajectory serve") || diagnostics.Len() != 0 {
			t.Fatalf("%v: %v; %s; %s", args, err, output.String(), diagnostics.String())
		}
	}
	for _, args := range [][]string{{"typo"}, {"review", "unexpected"}, {"investigation"}, {"investigation", "view"}, {"mcp", "--session", "demo"}} {
		if err := run(args, strings.NewReader(""), io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted invalid command %v", args)
		}
	}
}

type cliRoundTripper func(*http.Request) (*http.Response, error)

func (f cliRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCLIHTTPPreservesQueryAndPropagatesServerErrors(t *testing.T) {
	client, err := newCLIClient("http://127.0.0.1:4321")
	if err != nil {
		t.Fatal(err)
	}
	client.http.Transport = cliRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/investigation_review" || !r.URL.Query().Has("turnId") || r.URL.Query().Get("turnId") != "" || r.URL.Query().Get("id") != "saved + quoted" {
			t.Fatal(r.URL)
		}
		return &http.Response{StatusCode: 409, Body: io.NopCloser(strings.NewReader(`{"error":"This live view expired"}`))}, nil
	})
	if _, err := client.query("investigation_review", M{"id": "saved + quoted", "turnId": ""}); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatal(err)
	}
	for _, invalid := range []string{"file:///tmp/data", "http://", "http://user:secret@localhost:4318", "http://localhost:4318?x=1"} {
		if _, err := newCLIClient(invalid); err == nil {
			t.Fatalf("accepted invalid server URL %q", invalid)
		}
	}
}

func TestMCPRejectsMalformedRequestsAndInvalidToolArguments(t *testing.T) {
	client, _ := newCLIClient("")
	client.http.Transport = cliRoundTripper(func(r *http.Request) (*http.Response, error) {
		t.Fatal("invalid MCP call reached the backend", r.URL)
		return nil, fmt.Errorf("unexpected request")
	})
	input := strings.Join([]string{
		`not json`,
		`null`,
		`{"jsonrpc":"2.0","id":true,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":"unknown","method":"unknown"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":"null-args","method":"tools/call","params":{"name":"trajectory_review","arguments":null}}`,
		`{"jsonrpc":"2.0","id":"extra","method":"tools/call","params":{"name":"trajectory_review","arguments":{"session":"s","unexpected":true}}}`,
		`{"jsonrpc":"2.0","id":"bound","method":"tools/call","params":{"name":"trajectory_spans","arguments":{"session":"s","limit":201}}}`,
		`{"jsonrpc":"2.0","id":"enum","method":"tools/call","params":{"name":"trajectory_spans","arguments":{"session":"s","sort":"random"}}}`,
		`{"jsonrpc":"2.0","id":"type","method":"tools/call","params":{"name":"trajectory_span","arguments":{"session":"s","id":3}}}`,
		`{"jsonrpc":"2.0","id":"missing","method":"tools/call","params":{"name":"trajectory_span","arguments":{"session":"s"}}}`,
		`{"jsonrpc":"2.0","id":"missing-task","method":"tools/call","params":{"name":"trajectory_review","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":"write","method":"tools/call","params":{"name":"trajectory_finding","arguments":{}}}`,
	}, "\n")
	var output bytes.Buffer
	if err := serveMCP(client, strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 12 {
		t.Fatal(output.String())
	}
	for i, line := range lines {
		var response M
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatal(err)
		}
		if i < 4 {
			want := []float64{-32700, -32600, -32600, -32601}[i]
			if num(obj(response["error"])["code"]) != want {
				t.Fatal(response)
			}
		} else if obj(response["result"])["isError"] != true {
			t.Fatal(response)
		}
	}
}

func TestGoReviewUsesServerPresentationWithoutReranking(t *testing.T) {
	report := obj(mustAnalyze(t, reviewFixture(), M{})["review"])
	p := obj(report["presentation"])
	item := M{"id": "server-choice", "title": "server-ranked title", "metric": "server metric", "detail": "server fact", "reason": "server reason", "action": "server action", "turnId": "server-turn", "evidence": []M{{"spanId": "server-evidence"}}}
	p["sections"] = []M{{"title": "Server section", "note": "server caveat", "items": []M{item}}}
	text, err := reviewText(report, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"server-ranked title", "server metric", "server fact", "server reason", "server action", "server caveat", "server-evidence", "server-turn"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
	delete(report, "presentation")
	if _, err := reviewText(report, false); err == nil {
		t.Fatal("silently reconstructed a different review from an old backend")
	}
}

func TestPresentationFormattingAndTokenEvidence(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  string
	}{{nil, "—"}, {0, "0"}, {999.96, "1K"}, {1234, "1.2K"}, {42000, "42K"}, {999999, "1M"}, {1250000000, "1.3B"}} {
		if got := formatCount(tc.value); got != tc.want {
			t.Fatalf("count %v: %q != %q", tc.value, got, tc.want)
		}
	}
	for _, tc := range []struct {
		ms   float64
		want string
	}{{0, "0ms"}, {999, "999ms"}, {1000, "1.0s"}, {1250, "1.3s"}, {1450, "1.4s"}, {8250, "8.3s"}, {9950, "9.9s"}, {10000, "10s"}, {90000, "1m 30s"}, {3600000, "1h 0m"}} {
		if got := formatDuration(tc.ms); got != tc.want {
			t.Fatalf("duration %v: %q != %q", tc.ms, got, tc.want)
		}
	}
	p := reviewPresentation(M{"tokens": M{"turns": []M{{"turnId": "t", "label": "Turn 1", "description": "Fix tests", "uncachedInput": 1000, "output": 200, "cachedInput": 3000, "peakInput": 5000, "responses": 2, "toolResultChars": 18000, "largestResultChars": 18000, "largestResultSpan": "out", "evidence": []M{{"spanId": "usage"}}}}}})
	item := maps(maps(p["sections"])[1]["items"])[0]
	if item["title"] != "Turn 1 · Fix tests" || item["metric"] != "1.2K tokens" || item["turnId"] != "t" || len(maps(item["evidence"])) != 2 || !strings.Contains(str(item["reason"]), "not a token attribution") {
		t.Fatal(item)
	}
}

func TestPresentationDisclosesBoundedEvidenceCounts(t *testing.T) {
	toolGroups := []M{}
	files := []M{}
	followUps := []M{}
	for n := range 5 {
		toolGroups = append(toolGroups, M{"name": "tool-" + str(n), "calls": 1, "recordedMs": 1, "failures": 0, "evidence": []string{}})
		files = append(files, M{"path": "/repo/" + str(n), "operations": 1, "failures": 0, "notCompleted": 0, "types": []string{"update"}, "typeCount": 1, "evidence": []string{}})
		if n < 3 {
			followUps = append(followUps, M{"id": "follow-" + str(n), "category": "tests", "label": "test-" + str(n), "calls": 1, "recordedMs": 1, "failures": 0, "evidence": []string{}, "changeEvidence": []string{}})
		}
	}
	presentation := reviewPresentation(M{
		"tokens":  M{},
		"tools":   M{"groups": toolGroups, "groupCount": 6, "coverage": M{"note": "Tool limits."}},
		"changes": M{"files": files, "fileCount": 7, "followUps": followUps, "followUpCount": 4, "coverage": M{"note": "Change limits."}},
	})
	sections := maps(presentation["sections"])
	if !strings.Contains(str(sections[2]["note"]), "Showing 5 of 6") || !strings.Contains(str(sections[3]["note"]), "Showing 5 of 7 native paths and 3 of 4") {
		t.Fatal(sections)
	}
}
