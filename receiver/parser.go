package main

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strings"
)

var testRE = regexp.MustCompile(`(?:^|[;&|\n]\s*)\s*(?:(?:python[23]?\s+-m\s+|npx\s+)?(?:pytest|vitest|jest|playwright|ctest|unittest)\b|(?:npm|pnpm|yarn|bun|cargo|go)\s+(?:run\s+)?test\b|node\s+--test)`)
var buildRE = regexp.MustCompile(`(?:^|[;&|\n]\s*)\s*(?:(?:npm|pnpm|yarn|bun|cargo|go)\s+(?:run\s+)?build\b|tsc\b|webpack\b|vite build\b)`)
var wrapperRE = regexp.MustCompile(`^(functions\.)?(exec|wait)$`)
var exitRE = regexp.MustCompile(`Process exited with code\s+(-?\d+)`)

func category(name, command string) string {
	executable := strings.Split(command, "<<")[0]
	switch {
	case testRE.MatchString(executable):
		return "tests"
	case buildRE.MatchString(executable):
		return "build"
	case strings.Contains(name, "wait") || strings.Contains(name, "sleep"):
		return "wait"
	case strings.Contains(name, "Reasoning"):
		return "reasoning"
	case strings.Contains(name, "AgentMessage"):
		return "response"
	case strings.Contains(name, "Compaction"):
		return "compaction"
	case strings.Contains(name, "FileChange") || strings.Contains(name, "apply_patch"):
		return "edit"
	case command != "" || strings.Contains(name, "exec_command") || strings.Contains(name, "CommandExecution"):
		return "shell"
	}
	return "tools"
}

type parseState struct {
	bytes            int64
	Meta             M
	Spans, Turns     map[string]M
	Current          string
	First, Last      float64
	Lines, Malformed int
	Tokens           any
	Usage            map[string]M
	TurnLabels       map[string]string
	Title            string
}

func newState() *parseState {
	return &parseState{bytes: 1024, Meta: M{}, Spans: map[string]M{}, Turns: map[string]M{}, First: math.MaxFloat64, Usage: map[string]M{}, TurnLabels: map[string]string{}}
}
func (s *parseState) ingest(r M) {
	// Only changed entries are charged; accounting never scans the whole task.
	before := s.scalarBytes()
	defer func() { s.bytes += s.scalarBytes() - before }()
	s.Lines++
	p := obj(r["payload"])
	ts := timestamp(r["timestamp"])
	if math.IsNaN(ts) {
		return
	}
	s.First = math.Min(s.First, ts)
	s.Last = math.Max(s.Last, ts)
	if r["type"] == "session_meta" {
		s.Meta = M{"id": choose(p["id"], p["session_id"]), "cwd": p["cwd"], "version": p["cli_version"], "source": p["source"], "parentId": obj(obj(obj(p["source"])["subagent"])["thread_spawn"])["parent_thread_id"]}
		return
	}
	turn := str(choose(p["turn_id"], obj(p["internal_chat_message_metadata_passthrough"])["turn_id"], s.Current))
	if r["type"] == "turn_context" {
		s.Meta["model"] = p["model"]
		if str(p["turn_id"]) != "" {
			s.Current = str(p["turn_id"])
		}
	}
	if r["type"] == "event_msg" {
		switch p["type"] {
		case "task_started":
			s.Current = str(choose(p["turn_id"], "turn-"+str(s.Lines)))
			s.put(s.Turns, s.Current, M{"id": s.Current, "start": turnTimestamp(p["started_at"], ts), "end": nil, "status": "open"})
		case "task_complete", "turn_aborted":
			if old := s.Turns[turn]; old != nil {
				t := clone(old)
				t["end"] = turnTimestamp(p["completed_at"], ts)
				t["status"] = "completed"
				if p["type"] == "turn_aborted" {
					t["status"] = "aborted"
				}
				s.put(s.Turns, turn, t)
			}
		case "token_count":
			if v := obj(p["info"])["total_token_usage"]; v != nil {
				s.Tokens = v
			}
		}
	}
	if r["type"] == "token_usage_record" && p["usage"] != nil {
		id := str(choose(p["response_id"], "usage-"+str(s.Lines)))
		s.put(s.Usage, id, M{"id": id, "turnId": turn, "at": ts, "usage": obj(p["usage"])})
	}
	if r["type"] == "response_item" {
		switch p["type"] {
		case "message":
			if p["role"] == "user" {
				parts := []string{}
				for _, c := range arr(p["content"]) {
					parts = append(parts, str(obj(c)["text"]))
				}
				txt := messageText(strings.Join(parts, "\n"))
				if txt != "" {
					if s.Title == "" {
						s.Title = messageLabel(txt)
					}
					if s.TurnLabels[turn] == "" {
						s.label(turn, txt)
					}
				}
			}
		case "function_call", "custom_tool_call":
			id := str(choose(p["call_id"], p["id"]))
			input := clip(choose(p["arguments"], p["input"]), 16000)
			var args M
			json.Unmarshal([]byte(input), &args)
			name := str(p["name"])
			if str(p["namespace"]) != "" {
				name = str(p["namespace"]) + "." + name
			}
			command := str(choose(args["cmd"], args["command"]))
			s.put(s.Spans, id, M{"id": id, "turnId": turn, "name": name, "label": clip(choose(command, name), 160), "category": category(name, command), "start": ts, "end": nil, "source": "call-pair", "inputKey": hash(str(choose(p["arguments"], p["input"])), 32), "input": input, "cwd": choose(args["workdir"], args["cwd"]), "wrapper": wrapperRE.MatchString(name)})
		case "function_call_output", "custom_tool_call_output":
			if old := s.Spans[str(p["call_id"])]; old != nil {
				span := clone(old)
				span["deliveredChars"] = len([]rune(outputText(p["output"])))
				span["deliveredAt"] = ts
				if span["source"] != "explicit" {
					span["end"] = ts
					output := clip(outputText(p["output"]), 16000)
					if a, ok := p["output"].([]any); ok {
						parts := []string{}
						for _, v := range a {
							parts = append(parts, str(obj(v)["text"]))
						}
						output = strings.Join(parts, "\n")
					}
					var parsed M
					json.Unmarshal([]byte(output), &parsed)
					code := parsed["exit_code"]
					if match := exitRE.FindStringSubmatch(output); len(match) > 1 {
						code = num(match[1])
					}
					span["exitCode"] = code
					span["status"] = "completed"
					if (code != nil && num(code) != 0) || parsed["isError"] == true {
						span["status"] = "failed"
					}
				}
				if str(span["output"]) == "" {
					span["output"] = clip(outputText(p["output"]), 16000)
					span["outputChars"] = len([]rune(outputText(p["output"])))
				}
				s.put(s.Spans, str(p["call_id"]), span)
			}
		}
	}
	if r["type"] == "event_msg" && p["type"] == "item_completed" {
		i := obj(p["item"])
		start, end := timestamp(p["started_at_ms"]), timestamp(p["completed_at_ms"])
		if math.IsNaN(start) || math.IsNaN(end) || end < start || i["type"] == "UserMessage" || i["type"] == "FunctionCallOutput" {
			return
		}
		command := str(i["command"])
		if a, ok := i["command"].([]any); ok {
			parts := []string{}
			if len(a) >= 3 && strings.HasSuffix(str(a[0]), "sh") {
				a = a[2:]
			}
			for _, x := range a {
				parts = append(parts, str(x))
			}
			command = strings.Join(parts, " ")
		}
		name := str(choose(i["kind"], i["type"], "Unknown"))
		if i["type"] == "McpToolCall" {
			name = str(i["server"]) + "." + str(i["tool"])
		}
		id := str(choose(i["id"], "item-"+str(s.Lines)))
		prior := s.Spans[id]
		catName := str(i["type"])
		if catName == "Extension" {
			catName = name
		}
		status := choose(i["status"], "completed")
		if (i["exit_code"] != nil && num(i["exit_code"]) != 0) || i["status"] == "failed" || i["error"] != nil || obj(i["result"])["isError"] == true {
			status = "failed"
		}
		inputSource := choose(command, i["arguments"], prior["input"])
		input := clip(inputSource, 16000)
		changes := []M{}
		if i["type"] == "FileChange" {
			paths := []string{}
			for path := range obj(i["changes"]) {
				paths = append(paths, path)
			}
			sort.Strings(paths)
			lines := []string{}
			for _, path := range paths {
				value := obj(obj(i["changes"])[path])
				changeType := str(value["type"])
				change := M{"path": path, "type": changeType}
				line := strings.TrimSpace(changeType + " " + path)
				if movePath := str(value["move_path"]); movePath != "" {
					change["movePath"] = movePath
					line += " -> " + movePath
				}
				changes = append(changes, change)
				lines = append(lines, line)
			}
			if input == "" {
				inputSource = strings.Join(lines, "\n")
				input = clip(inputSource, 16000)
			}
		}
		span := M{"id": id, "turnId": turn, "name": name, "label": clip(choose(command, name), 160), "category": category(catName, command), "start": start, "end": end, "source": "explicit", "status": status, "inputKey": hash(str(inputSource), 32), "input": input, "output": clip(outputText(choose(i["aggregated_output"], i["stdout"], i["result"], i["error"])), 16000), "exitCode": i["exit_code"], "agentId": i["agent_thread_id"], "processId": i["process_id"], "cwd": choose(i["cwd"], i["workdir"], prior["cwd"])}
		if len(changes) > 0 {
			span["changes"] = changes
		}
		if command == "" && i["arguments"] == nil && prior["inputKey"] != nil {
			span["inputKey"] = prior["inputKey"]
		}
		if prior["deliveredChars"] != nil {
			span["deliveredChars"] = prior["deliveredChars"]
			span["deliveredAt"] = prior["deliveredAt"]
		}
		if out := choose(i["aggregated_output"], i["stdout"], i["result"], i["error"]); out != nil {
			span["outputChars"] = len([]rune(outputText(out)))
		}
		s.put(s.Spans, id, span)
	}
}

// Derive display rows from copies. Cached parser facts retain open ends and
// explicit provenance so later records can complete or supersede them.
func (s *parseState) materialize(ctx context.Context, now float64) (M, error) {
	status := "idle"
	for _, t := range s.Turns {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if t["end"] == nil {
			status = "stale"
			if now-s.Last < 120000 {
				status = "live"
			}
			break
		}
	}
	horizon := s.Last
	if status == "live" {
		horizon = math.Max(now, s.Last)
	}
	turns := ordered(s.Turns)
	turnMap := map[string]M{}
	for _, t := range turns {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		t["request"] = s.TurnLabels[str(t["id"])]
		t["label"] = messageLabel(str(t["request"]))
		if t["end"] == nil {
			t["end"] = horizon
		}
		turnMap[str(t["id"])] = t
	}
	spans := ordered(s.Spans)
	// Usage has a recorded completion timestamp, not a measured request duration.
	// Point events make exact response usage addressable through the same evidence
	// interface without inventing latency or distributing tokens over tool calls.
	for id, r := range s.Usage {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		spans = append(spans, M{"id": "usage-" + id, "responseId": id, "turnId": r["turnId"], "name": "Model response", "label": "Model response", "category": "model", "start": r["at"], "end": r["at"], "source": "usage", "status": "completed", "usage": r["usage"]})
	}
	for _, v := range spans {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if v["end"] == nil {
			end := horizon
			if t := turnMap[str(v["turnId"])]; t != nil {
				end = math.Min(num(t["end"]), horizon)
			}
			v["end"] = end
			v["status"] = "incomplete"
			if status == "live" {
				v["status"] = "running"
			}
		}
		v["parentId"] = nil
		v["depth"] = 0
	}
	duplicate := map[string]bool{}
	byTurn := map[string][]M{}
	for _, v := range spans {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if v["source"] == "call-pair" {
			k := str(v["turnId"])
			byTurn[k] = append(byTurn[k], v)
		}
	}
	for _, v := range spans {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if v["source"] != "explicit" {
			continue
		}
		var parent M
		for _, w := range byTurn[str(v["turnId"])] {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if w["source"] == "call-pair" && w["turnId"] == v["turnId"] && num(w["start"]) <= num(v["start"]) && num(w["end"]) >= num(v["end"]) {
				if parent == nil || num(w["end"])-num(w["start"]) < num(parent["end"])-num(parent["start"]) {
					parent = w
				}
			}
		}
		if parent == nil {
			continue
		}
		if parent["wrapper"] == true {
			v["parentId"] = parent["id"]
			v["depth"] = 1
		} else if strings.HasSuffix(str(parent["name"]), "exec_command") || strings.HasSuffix(str(parent["name"]), str(v["name"])) {
			duplicate[str(parent["id"])] = true
			if str(v["output"]) == "" {
				v["output"] = parent["output"]
				v["outputChars"] = parent["outputChars"]
			}
			if parent["deliveredChars"] != nil {
				v["deliveredChars"] = parent["deliveredChars"]
				v["deliveredAt"] = parent["deliveredAt"]
			}
		}
	}
	selected := []M{}
	unStart, unEnd := math.MaxFloat64, 0.0
	explicit, inferred := 0, 0
	for _, v := range spans {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if duplicate[str(v["id"])] {
			continue
		}
		v["durationMs"] = math.Max(0, num(v["end"])-num(v["start"]))
		if turnMap[str(v["turnId"])] == nil {
			v["turnId"] = "unscoped"
			unStart = math.Min(unStart, num(v["start"]))
			unEnd = math.Max(unEnd, num(v["end"]))
		}
		selected = append(selected, v)
		if v["source"] == "explicit" {
			explicit++
		} else {
			inferred++
		}
	}
	if unStart != math.MaxFloat64 {
		turns = append(turns, M{"id": "unscoped", "start": unStart, "end": unEnd, "status": "inferred"})
	}
	sort.SliceStable(selected, func(i, j int) bool {
		a, b := selected[i], selected[j]
		if num(a["start"]) == num(b["start"]) {
			return num(a["durationMs"]) > num(b["durationMs"])
		}
		return num(a["start"]) < num(b["start"])
	})
	tokens := s.Tokens
	if len(s.Usage) > 0 {
		totals := M{}
		for _, r := range s.Usage {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			for k, v := range obj(r["usage"]) {
				if _, ok := v.(float64); ok {
					totals[k] = num(totals[k]) + num(v)
				}
			}
		}
		tokens = totals
	}
	start := s.First
	if start == math.MaxFloat64 {
		start = 0
	}
	for _, t := range turns {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		start = math.Min(start, num(t["start"]))
	}
	result := clone(s.Meta)
	for k, v := range (M{"title": s.Title, "status": status, "start": start, "end": horizon, "lastEvent": s.Last, "turns": turns, "spans": selected, "tokens": tokens, "diagnostics": M{"records": s.Lines, "malformed": s.Malformed, "explicitSpans": explicit, "inferredSpans": inferred}}) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result[k] = v
	}
	return result, ctx.Err()
}
