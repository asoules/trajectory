package main

import (
	"context"
	"math"
	"strings"
	"testing"
)

func reviewFixture() M {
	return M{"id": "s", "title": "Profile", "start": 1000, "end": 121000, "lastEvent": 121000, "status": "idle", "turns": []M{{"id": "t", "start": 1000, "end": 121000}}, "spans": []M{}, "diagnostics": M{}}
}
func work(id, label string, start, end float64) M {
	return M{"id": id, "turnId": "t", "name": "CommandExecution", "input": label, "label": label, "category": "shell", "start": start, "end": end, "durationMs": end - start, "status": "completed", "source": "explicit"}
}
func TestBackgroundRequiresStartupAndConcurrentEvidence(t *testing.T) {
	s := reviewFixture()
	service := work("server", "npm run dev", 1000, 361000)
	other := work("test", "npm test", 10000, 20000)
	edit := work("edit", "git diff", 30000, 32000)
	slow := work("slow", "npm run migrate", 1000, 121000)
	s["spans"] = []M{service, other, edit, slow}
	a := mustAnalyze(t, s, M{"top": 20})
	r := obj(a["review"])
	if num(r["backgroundCount"]) != 1 {
		t.Fatal(r)
	}
	for _, g := range maps(a["groups"]) {
		if g["label"] == "npm run dev" {
			t.Fatal("service ranked as work")
		}
	}
	if len(maps(r["time"])) == 0 || maps(r["time"])[0]["label"] != "npm run migrate" {
		t.Fatal(r)
	}
	toolGroups := maps(obj(r["tools"])["groups"])
	if len(toolGroups) != 1 || num(toolGroups[0]["calls"]) != 3 || num(obj(r["tools"])["backgroundServiceOperations"]) != 1 || !strings.Contains(str(obj(obj(r["tools"])["coverage"])["note"]), "1 likely background-service") {
		t.Fatal("background service leaked into tool ranking", toolGroups)
	}
	var sum float64
	for _, b := range maps(a["breakdown"]) {
		sum += num(b["ms"])
	}
	if math.Abs(sum-num(a["activeMs"])) > 2 {
		t.Fatal("category time must reconcile", sum, a["activeMs"])
	}
	s["spans"] = []M{service, other}
	if len(mustBackground(t, s)) != 0 {
		t.Fatal("insufficient concurrency must not classify service")
	}
	s["spans"] = []M{slow, other, edit}
	if len(mustBackground(t, s)) != 0 {
		t.Fatal("duration alone is not a service")
	}
}
func TestReviewSuppressesTrivialAndWrapperFindings(t *testing.T) {
	s := reviewFixture()
	s["spans"] = []M{work("a", "npm test", 1000, 4000), work("b", "npm test", 5000, 7000), M{"id": "wrap", "name": "exec", "category": "tools", "start": 1000, "end": 121000, "durationMs": 120000, "wrapper": true}}
	r := obj(mustAnalyze(t, s, M{"top": 12})["review"])
	// A five-second operation is at the threshold; six seconds across a
	// ninety-minute session is not material enough for the default shortlist.
	s["end"] = 5401000
	s["turns"] = []M{{"id": "t", "start": 1000, "end": 5401000}}
	r = obj(mustAnalyze(t, s, M{"top": 12})["review"])
	if len(maps(r["time"])) != 0 {
		t.Fatal(r)
	}
}
func TestResponseUsageIsDeduplicatedAttributableAndWindowed(t *testing.T) {
	p := newState()
	base := float64(1780000000000)
	add := func(kind string, payload M, at float64) {
		p.ingest(M{"type": kind, "payload": payload, "timestamp": iso(base + at)})
	}
	add("event_msg", M{"type": "task_started", "turn_id": "t"}, 0)
	u := M{"input_tokens": float64(1000), "cached_input_tokens": float64(800), "output_tokens": float64(100), "reasoning_output_tokens": float64(40), "total_tokens": float64(1100)}
	add("token_usage_record", M{"response_id": "r1", "turn_id": "t", "usage": u}, 1000)
	add("token_usage_record", M{"response_id": "r1", "turn_id": "t", "usage": u}, 1000)
	add("token_usage_record", M{"response_id": "r2", "turn_id": "t", "usage": u}, 2000)
	add("event_msg", M{"type": "task_complete", "turn_id": "t"}, 3000)
	s := mustMaterialize(t, p, base+3000)
	r := tokenReport(s, M{})
	if num(r["responses"]) != 2 || num(obj(r["usage"])["total_tokens"]) != 2200 || num(r["uncachedInput"]) != 400 {
		t.Fatal(r)
	}
	if len(maps(r["turns"])) != 1 || maps(r["turns"])[0]["turnId"] != "t" {
		t.Fatal(r)
	}
	window := tokenReport(s, M{"since": iso(base + 1000)})
	if num(window["responses"]) != 1 || num(obj(window["usage"])["output_tokens"]) != 100 {
		t.Fatal(window)
	}
	detail, err := querySession(context.Background(), s, "span", M{"id": "usage-r2"})
	if err != nil || num(obj(detail)["durationMs"]) != 0 || num(obj(obj(detail)["usage"])["reasoning_output_tokens"]) != 40 {
		t.Fatal(detail, err)
	}
	old := reviewFixture()
	old["tokens"] = u
	if tokenReport(old, M{})["source"] != "session totals only" || tokenReport(old, M{"since": iso(base)})["usage"] != nil {
		t.Fatal("legacy totals must not pretend to be windowed usage")
	}
}
func TestRecordedOutputSizeSurvivesDetailTruncation(t *testing.T) {
	p := newState()
	base := float64(1780000000000)
	p.ingest(M{"type": "response_item", "timestamp": iso(base), "payload": M{"type": "function_call", "call_id": "c", "name": "read", "arguments": "{}"}})
	text := ""
	for i := 0; i < 20000; i++ {
		text += "x"
	}
	p.ingest(M{"type": "response_item", "timestamp": iso(base + 1000), "payload": M{"type": "function_call_output", "call_id": "c", "output": text}})
	s := mustMaterialize(t, p, base+1000)
	v := maps(s["spans"])[0]
	if num(v["outputChars"]) != 20000 || len(str(v["output"])) != 16000 {
		t.Fatal(v["outputChars"], len(str(v["output"])))
	}
	r := obj(mustAnalyze(t, s, M{"top": 5})["review"])
	if len(maps(r["largeOutputs"])) != 1 {
		t.Fatal(r)
	}
}

func TestServiceInferenceDoesNotHideForegroundArgumentsOrURLs(t *testing.T) {
	for _, cmd := range []string{"git clone --branch dev https://example.invalid/repo", "curl --port 4318 http://localhost", "./bin/migrate --port 5432"} {
		s := reviewFixture()
		v := work("long", cmd, 1000, 121000)
		v["output"] = "See http://localhost:4318 for documentation"
		s["spans"] = []M{v, work("a", "git diff", 10000, 11000), work("b", "git status", 20000, 22000)}
		if len(mustBackground(t, s)) != 0 {
			t.Fatal("misclassified", cmd)
		}
	}
}
func TestOneCostlyToolAndUnrelatedEditsRemainHonest(t *testing.T) {
	s := reviewFixture()
	v := work("tool", "", 1000, 90000)
	v["category"] = "tools"
	v["name"] = "imagegen"
	v["label"] = "imagegen"
	s["spans"] = []M{v}
	r := obj(mustAnalyze(t, s, M{"top": 5})["review"])
	if len(maps(r["time"])) != 1 {
		t.Fatal("costly single tool hidden", r)
	}
	edits := []M{}
	for i := 0; i < 3; i++ {
		e := work(str(i), "", float64(1000+i*10000), float64(10000+i*10000))
		e["category"] = "edit"
		e["label"] = "FileChange"
		edits = append(edits, e)
	}
	s["spans"] = edits
	r = obj(mustAnalyze(t, s, M{"top": 5})["review"])
	if len(maps(r["time"])) != 1 || maps(r["time"])[0]["title"] == "Repeated operation" {
		t.Fatal(r)
	}
}

func TestChangeReportDistinguishesUnverifiedRecordedChanges(t *testing.T) {
	edit := work("edit", "", 1000, 2000)
	edit["category"] = "edit"
	edit["changes"] = []M{{"path": "/repo/a.go", "type": "update"}}
	report := mustChangeReport(t, []M{edit}, M{})
	coverage := obj(report["coverage"])
	if coverage["fileChanges"] != "observed" || coverage["subsequentChecks"] != "not observed" || len(maps(report["followUps"])) != 0 {
		t.Fatal(report)
	}
}

func TestNewReviewEvidenceUsesStrictSinceBoundary(t *testing.T) {
	edit := work("edit", "", 1000, 2000)
	edit["name"] = "FileChange"
	edit["category"] = "edit"
	edit["changes"] = []M{{"path": "/repo/a.go", "type": "update"}}
	args := M{"since": iso(2000)}
	if len(maps(mustToolReport(t, []M{edit}, args, nil)["groups"])) != 0 || len(maps(mustChangeReport(t, []M{edit}, args)["files"])) != 0 {
		t.Fatal("events ending at since must not be reported again")
	}
}

func TestChangeFollowUpIDsUseFullOperationIdentity(t *testing.T) {
	edit := work("edit", "", 1000, 2000)
	edit["category"] = "edit"
	edit["changes"] = []M{{"path": "/repo/a.go", "type": "update"}}
	checks := []M{edit}
	for _, key := range []string{"full-first", "full-second"} {
		check := work("check-"+key, "same clipped label", 3000, 4000)
		check["category"] = "tests"
		check["inputKey"] = key
		check["selfMs"] = float64(1000)
		checks = append(checks, check)
	}
	report := mustChangeReport(t, checks, M{})
	followUps := maps(report["followUps"])
	if len(followUps) != 2 || followUps[0]["id"] == followUps[1]["id"] || str(followUps[0]["id"]) > str(followUps[1]["id"]) {
		t.Fatal(followUps)
	}
}

func TestNewEvidenceCitationsCoverFailuresAndChangeTypes(t *testing.T) {
	completed := work("edit-completed", "", 1000, 2000)
	completed["name"] = "FileChange"
	completed["category"] = "edit"
	completed["selfMs"] = float64(1000)
	completed["changes"] = []M{{"path": "/repo/a.go", "type": "update"}}
	failedEdit := work("edit-failed", "", 2500, 2600)
	failedEdit["name"] = "FileChange"
	failedEdit["category"] = "edit"
	failedEdit["selfMs"] = float64(100)
	failedEdit["status"] = "failed"
	failedEdit["changes"] = []M{{"path": "/repo/a.go", "type": "add"}}
	declinedEdit := work("edit-declined", "", 2700, 2800)
	declinedEdit["name"] = "FileChange"
	declinedEdit["category"] = "edit"
	declinedEdit["selfMs"] = float64(50)
	declinedEdit["status"] = "declined"
	declinedEdit["changes"] = []M{{"path": "/repo/a.go", "type": "delete"}}
	selected := []M{completed, failedEdit, declinedEdit}
	for n := range 4 {
		check := work("check-"+str(n), "go test ./...", float64(3000+n*1000), float64(3900+n*1000))
		check["category"] = "tests"
		check["inputKey"] = "same"
		check["selfMs"] = float64(900 - n*100)
		if n == 3 {
			check["status"] = "failed"
		}
		selected = append(selected, check)
	}
	changes := mustChangeReport(t, selected, M{})
	file := maps(changes["files"])[0]
	if num(file["failures"]) != 1 || num(file["notCompleted"]) != 2 || len(stringValues(file["types"])) != 3 || !containsString(stringValues(file["evidence"]), "edit-failed") || !containsString(stringValues(file["evidence"]), "edit-declined") {
		t.Fatal(file)
	}
	followUp := maps(changes["followUps"])[0]
	if num(followUp["failures"]) != 1 || !containsString(stringValues(followUp["evidence"]), "check-3") || containsString(stringValues(followUp["changeEvidence"]), "edit-failed") {
		t.Fatal(followUp)
	}
	tools := mustToolReport(t, selected, M{}, nil)
	for _, group := range maps(tools["groups"]) {
		if group["name"] == "CommandExecution" && !containsString(stringValues(group["evidence"]), "check-3") {
			t.Fatal(group)
		}
	}
}

func TestNewEvidenceAnalysisObservesCancellation(t *testing.T) {
	selected := []M{}
	for n := range 100 {
		edit := work("edit-"+str(n), "", float64(n*20), float64(n*20+10))
		edit["category"] = "edit"
		edit["changes"] = []M{{"path": "/repo/" + str(n), "type": "update"}}
		selected = append(selected, edit)
	}
	ctx := &steppedContext{Context: context.Background(), steps: 20}
	if _, err := changeReport(ctx, selected, M{}); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestOutputTextExcludesEncodedMedia(t *testing.T) {
	raw := M{"content": []any{M{"type": "text", "text": "useful"}, M{"type": "image", "data": "base64-is-not-text"}, M{"type": "input_text", "text": "result"}, M{"type": "input_image", "image_url": "data:image/png;base64,abcdef"}}}
	if outputText(raw) != "useful\nresult" {
		t.Fatal(outputText(raw))
	}
	if messageLabel("\n<in-app-browser-context>ambient</in-app-browser-context>\nFix the tests") != "Fix the tests" {
		t.Fatal("host wrapper leaked into turn label")
	}
}

func TestLargeResultsUseDeliveredTextNotRawProcessOutput(t *testing.T) {
	s := reviewFixture()
	raw := work("raw", "rg search", 1000, 2000)
	raw["outputChars"] = 1000000
	delivered := work("delivered", "", 1000, 3000)
	delivered["category"] = "tools"
	delivered["name"] = "exec"
	delivered["wrapper"] = true
	delivered["deliveredChars"] = 20000
	s["spans"] = []M{raw, delivered}
	r := obj(mustAnalyze(t, s, M{"top": 5})["review"])
	list := maps(r["largeOutputs"])
	if len(list) != 1 || list[0]["spanId"] != "delivered" || num(list[0]["characters"]) != 20000 {
		t.Fatal(r)
	}
	r = obj(mustAnalyze(t, s, M{"top": 5, "since": iso(3000)})["review"])
	if len(maps(r["largeOutputs"])) != 0 {
		t.Fatal("boundary message counted twice")
	}
}

func TestProductionBuildNeverBecomesService(t *testing.T) {
	s := reviewFixture()
	s["spans"] = []M{work("build", "npx vite build", 1000, 121000), work("a", "git status", 10000, 11000), work("b", "git diff", 20000, 21000)}
	if len(mustBackground(t, s)) != 0 {
		t.Fatal("production build excluded")
	}
	r := obj(mustAnalyze(t, s, M{"top": 5})["review"])
	if len(maps(r["time"])) != 1 {
		t.Fatal(r)
	}
}
func TestLateToolDeliveryAppearsInIncrementalReview(t *testing.T) {
	p := newState()
	base := float64(1780000000000)
	add := func(typ string, payload M, at float64) {
		p.ingest(M{"type": typ, "payload": payload, "timestamp": iso(base + at)})
	}
	add("event_msg", M{"type": "task_started", "turn_id": "t"}, 0)
	add("response_item", M{"type": "function_call", "call_id": "call", "name": "exec_command", "arguments": "{\"cmd\":\"rg search\"}"}, 0)
	add("event_msg", M{"type": "item_completed", "turn_id": "t", "started_at_ms": base, "completed_at_ms": base + 1000, "item": M{"id": "native", "type": "CommandExecution", "command": "rg search", "stdout": "native stdout"}}, 1000)
	text := ""
	for i := 0; i < 20000; i++ {
		text += "x"
	}
	add("response_item", M{"type": "function_call_output", "call_id": "call", "output": text}, 3000)
	add("event_msg", M{"type": "task_complete", "turn_id": "t"}, 4000)
	s := mustMaterialize(t, p, base+4000)
	r := obj(mustAnalyze(t, s, M{"top": 5, "since": iso(base + 2000)})["review"])
	out := maps(r["largeOutputs"])
	if len(out) != 1 || out[0]["spanId"] != "native" || num(out[0]["deliveredAt"]) != base+3000 {
		t.Fatal(r)
	}
}

func TestServiceStartupEvidenceSurvivesMissingLaterOutput(t *testing.T) {
	s := reviewFixture()
	s["turns"] = []M{{"id": "t", "start": 1000, "end": 121000}}
	first := work("first", "./bin/receiver -port 4321", 1000, 61000)
	first["output"] = "Receiver OTLP → http://127.0.0.1:4321"
	second := work("second", "./bin/receiver -port 4321", 62000, 300000)
	s["spans"] = []M{first, work("a", "git diff", 10000, 11000), work("b", "git status", 20000, 21000), second, work("c", "rg name", 70000, 71000), work("d", "go test", 80000, 90000)}
	services := mustBackground(t, s)
	if len(services) != 1 || services["second"]["startupSpanId"] != "first" {
		t.Fatal(services)
	}
	// A different invocation cannot inherit a service's startup announcement.
	second["input"] = "./bin/receiver -port 4321 --migrate"
	if mustBackground(t, s)["second"] != nil {
		t.Fatal("different arguments inherited classification")
	}
}

func TestForegroundScriptWithStartupAnnouncementRemainsWork(t *testing.T) {
	s := reviewFixture()
	v := work("integration", "./scripts/integration.sh", 1000, 100000)
	v["output"] = "Listening on http://127.0.0.1:9000\nRunning integration tests..."
	s["spans"] = []M{v, work("a", "git diff", 10000, 11000), work("b", "git status", 20000, 22000)}
	if len(mustBackground(t, s)) != 0 {
		t.Fatal("foreground tests hidden")
	}
}
func TestReceiverCommandUsesExactIdentityAndServeFlags(t *testing.T) {
	for _, cmd := range []string{"./bin/trajectory", "./bin/trajectory -demo -port 4321", "/repo/bin/trajectory --db=/tmp/test.sqlite", "./bin/trajectory serve", "/repo/bin/trajectory serve --port=4321"} {
		if !receiverCommand(cmd, "/repo", "/repo/bin/trajectory") {
			t.Fatal("missed receiver", cmd)
		}
	}
	for _, cmd := range []string{"./bin/other -port 4318", "./bin/trajectory --help", "./bin/trajectory migrate", "./bin/trajectory && npm test", "./bin/trajectory -port", "./bin/trajectory -db $(bad)", "./bin/trajectory review --session demo", "./bin/trajectory mcp", "./bin/trajectory serve --help", "./bin/trajectory serve serve"} {
		if receiverCommand(cmd, "/repo", "/repo/bin/trajectory") {
			t.Fatal("incorrect identity", cmd)
		}
	}
}

func TestReceiverIdentityRequiresInvocationDirectory(t *testing.T) {
	if receiverCommand("./bin/trajectory", "", "/repo/bin/trajectory") || receiverCommand("./bin/trajectory", "/other", "/repo/bin/trajectory") {
		t.Fatal("assumed invocation directory")
	}
	if !receiverCommand("./bin/trajectory", "file:///repo", "/repo/bin/trajectory") {
		t.Fatal("recorded file URI not resolved")
	}
	p := newState()
	p.ingest(M{"type": "session_meta", "timestamp": iso(1000), "payload": M{"cwd": "/repo"}})
	p.ingest(M{"type": "event_msg", "timestamp": iso(90000), "payload": M{"type": "item_completed", "started_at_ms": float64(1000), "completed_at_ms": float64(90000), "item": M{"id": "command", "type": "CommandExecution", "command": "./bin/trajectory", "cwd": "file:///other"}}})
	s := mustMaterialize(t, p, 100000)
	if maps(s["spans"])[0]["cwd"] != "file:///other" {
		t.Fatal("invocation directory lost")
	}
}
