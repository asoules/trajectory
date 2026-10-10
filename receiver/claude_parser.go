package main

import (
	"encoding/json"
	"math"
	"strings"
)

// Claude transcripts split an API message into blocks with the same message.id
// and repeated/cumulative usage. Usage is replaced by response ID, never added
// per block. Tool result user messages do not start a new human turn.
func (s *parseState) ingestClaude(r M) {
	before := s.scalarBytes()
	defer func() { s.bytes += s.scalarBytes() - before }()
	s.Lines++
	switch r["type"] {
	case "custom-title", "ai-title":
		if r["type"] == "custom-title" || s.Meta["customTitle"] == nil {
			s.Meta["title"] = clip(choose(r["customTitle"], r["aiTitle"]), 100)
			if r["type"] == "custom-title" {
				s.Meta["customTitle"] = true
			}
		}
		return
	case "user", "assistant", "system":
	default:
		return
	}
	// Inline sidechains in older files are separate work, not the parent's
	// foreground activity. Dedicated agent files are marked during discovery.
	if r["isSidechain"] == true && s.Meta["agentId"] == nil {
		return
	}
	ts := timestamp(r["timestamp"])
	if math.IsNaN(ts) {
		return
	}
	if r["type"] == "system" && r["subtype"] != "turn_duration" && r["subtype"] != "stop_hook_summary" && r["subtype"] != "compact_boundary" {
		return
	}
	for _, key := range []string{"cwd", "version"} {
		if r[key] != nil {
			s.Meta[key] = clip(r[key], 4000)
		}
	}
	if r["sessionId"] != nil {
		s.Meta["id"] = clip(r["sessionId"], 160)
	}
	s.First, s.Last = math.Min(s.First, ts), math.Max(s.Last, ts)
	m := obj(r["message"])
	blocks := arr(m["content"])
	if r["type"] == "user" {
		hasResults := false
		for _, raw := range blocks {
			b := obj(raw)
			if b["type"] == "tool_result" {
				hasResults = true
				s.claudeResult(b, r, ts)
			}
		}
		if !hasResults && r["isMeta"] != true && r["isCompactSummary"] != true {
			id := str(choose(r["uuid"], "turn-"+str(s.Lines)))
			if s.Turns[id] != nil {
				return // replay of a previously recorded prompt
			}
			if old := s.Turns[s.Current]; old != nil && old["end"] == nil {
				t := clone(old)
				t["end"], t["status"] = choose(t["lastEvent"], t["start"]), "inferred"
				s.put(s.Turns, s.Current, t)
			}
			s.Current = id
			s.put(s.Turns, id, M{"id": id, "start": ts, "end": nil, "lastEvent": ts, "status": "open"})
			text := clip(strings.TrimSpace(outputText(m["content"])), 4000)
			s.label(id, text)
			if s.Title == "" {
				s.Title = messageLabel(text)
			}
		}
	}
	if r["type"] == "assistant" {
		if m["model"] != nil && m["model"] != "<synthetic>" {
			s.Meta["model"] = clip(m["model"], 160)
		}
		id := str(choose(m["id"], r["requestId"], r["uuid"]))
		turn := s.Current
		previous := s.Usage[id]
		advance := previous == nil || (ts >= num(previous["at"]) && (previous["complete"] != true || m["stop_reason"] != nil))
		if previous != nil {
			turn = str(previous["turnId"])
		}
		if u := obj(m["usage"]); id != "" && len(u) > 0 && m["model"] != "<synthetic>" {
			// Replayed records cannot move completion backwards or replace final
			// usage with a partial streaming block.
			if advance {
				s.put(s.Usage, id, M{"id": id, "turnId": turn, "at": ts, "usage": claudeUsage(u), "complete": m["stop_reason"] != nil})
			}
		}
		for _, raw := range blocks {
			b := obj(raw)
			if b["type"] != "tool_use" || str(b["id"]) == "" || s.Spans[str(b["id"])] != nil {
				continue
			}
			input, _ := json.Marshal(b["input"])
			args, name := obj(b["input"]), str(b["name"])
			command := ""
			if name == "Bash" {
				command = str(args["command"])
			}
			cat := category(name, command)
			if name == "Edit" || name == "Write" || name == "MultiEdit" || name == "NotebookEdit" {
				cat = "edit"
			}
			s.put(s.Spans, str(b["id"]), M{"id": b["id"], "turnId": turn, "name": name, "label": clip(choose(command, args["file_path"], args["description"], name), 160), "category": cat, "start": ts, "end": nil, "source": "call-pair", "inputKey": hash(string(input), 32), "input": clip(choose(command, string(input)), 16000), "cwd": s.Meta["cwd"]})
		}
		if t := s.Turns[turn]; advance && t != nil && ts >= num(t["lastEvent"]) {
			t = clone(t)
			t["lastEvent"] = ts
			t["end"], t["status"] = nil, "open"
			if stop := str(m["stop_reason"]); stop != "" && stop != "tool_use" && stop != "pause_turn" {
				t["end"], t["status"] = ts, "completed"
			}
			s.put(s.Turns, turn, t)
		}
	}
	if r["type"] == "system" && (r["subtype"] == "turn_duration" || (r["subtype"] == "stop_hook_summary" && r["preventedContinuation"] != true)) {
		if t := s.Turns[s.Current]; t != nil && t["end"] == nil {
			t = clone(t)
			t["end"], t["status"] = ts, "completed"
			s.put(s.Turns, s.Current, t)
		}
	}
}

func claudeUsage(u M) M {
	result := M{}
	if u["input_tokens"] != nil {
		// Anthropic input_tokens excludes cache reads and cache writes. Our
		// shared contract includes both in input, with reads as a subset.
		result["input_tokens"] = num(u["input_tokens"]) + num(u["cache_read_input_tokens"]) + num(u["cache_creation_input_tokens"])
	}
	for from, to := range map[string]string{"cache_read_input_tokens": "cached_input_tokens", "cache_creation_input_tokens": "cache_write_input_tokens", "output_tokens": "output_tokens"} {
		if u[from] != nil {
			result[to] = num(u[from])
		}
	}
	if thinking := obj(u["output_tokens_details"])["thinking_tokens"]; thinking != nil {
		result["reasoning_output_tokens"] = num(thinking)
	}
	if result["input_tokens"] != nil && result["output_tokens"] != nil {
		result["total_tokens"] = num(result["input_tokens"]) + num(result["output_tokens"])
	}
	return result
}

func (s *parseState) claudeResult(b, r M, ts float64) {
	id := str(b["tool_use_id"])
	old := s.Spans[id]
	if old == nil || old["deliveredAt"] != nil {
		return
	}
	span := clone(old)
	text := outputText(b["content"])
	span["end"], span["deliveredAt"] = math.Max(ts, num(span["start"])), ts
	span["deliveredChars"], span["outputChars"] = len([]rune(text)), len([]rune(text))
	span["output"], span["status"] = clip(text, 16000), "completed"
	result := obj(r["toolUseResult"])
	if b["is_error"] == true || result["interrupted"] == true {
		span["status"] = "failed"
	}
	if code := choose(result["exitCode"], result["exit_code"]); code != nil {
		span["exitCode"] = code
		if num(code) != 0 {
			span["status"] = "failed"
		}
	}
	s.put(s.Spans, id, span)
	if t := s.Turns[str(span["turnId"])]; t != nil {
		t = clone(t)
		t["lastEvent"] = math.Max(ts, num(t["lastEvent"]))
		s.put(s.Turns, str(span["turnId"]), t)
	}
}
