package main

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

func formatDuration(ms float64) string {
	switch {
	case ms < 1000:
		return fmt.Sprintf("%.0fms", math.Round(ms))
	case ms < 10000:
		seconds := ms / 1000
		// Match JavaScript toFixed(1): round the binary value, with exact
		// quarter-second ties going up rather than Go's ties-to-even rule.
		if math.Mod(seconds*4, 2) == 1 {
			seconds = math.Round(seconds*10) / 10
		}
		return strconv.FormatFloat(seconds, 'f', 1, 64) + "s"
	case ms < 60000:
		return fmt.Sprintf("%.0fs", math.Round(ms/1000))
	case ms < 3600000:
		seconds := int(math.Round(ms / 1000))
		return fmt.Sprintf("%dm %ds", seconds/60, seconds%60)
	default:
		return fmt.Sprintf("%dh %dm", int(ms/3600000), int(math.Mod(ms, 3600000)/60000))
	}
}

func formatCount(value any) string {
	if value == nil {
		return "—"
	}
	n := num(value)
	units := []string{"", "K", "M", "B", "T"}
	i := 0
	for math.Abs(n) >= 1000 && i < len(units)-1 {
		n /= 1000
		i++
	}
	n = math.Round(n*10) / 10
	if math.Abs(n) >= 1000 && i < len(units)-1 {
		n /= 1000
		i++
	}
	return strconv.FormatFloat(n, 'f', -1, 64) + units[i]
}

func stringValues(value any) []string {
	if values, ok := value.([]string); ok {
		return values
	}
	values := []string{}
	for _, v := range arr(value) {
		values = append(values, str(v))
	}
	return values
}

// Presentation is computed once on the server, alongside the measured report.
// Browser and terminal render this same model, including all ranking, wording,
// formatting, and evidence links. Raw measurements remain available in JSON.
func reviewPresentation(report M) M {
	tokens := obj(report["tokens"])
	tools := obj(report["tools"])
	changes := obj(report["changes"])
	usage := obj(tokens["usage"])
	timeItems, tokenItems, toolItems, changeItems := []M{}, []M{}, []M{}, []M{}
	for _, r := range maps(report["time"]) {
		calls := "calls"
		if num(r["calls"]) == 1 {
			calls = "call"
		}
		detail := fmt.Sprintf("%s · %v %s", str(r["title"]), r["calls"], calls)
		if num(r["failures"]) != 0 {
			detail += fmt.Sprintf(" · %v failed", r["failures"])
		}
		evidence := []M{}
		for i, spanID := range stringValues(r["evidence"]) {
			label := "Inspect related operation"
			if i == 0 {
				label = "Inspect largest operation"
			}
			evidence = append(evidence, M{"spanId": spanID, "label": label})
		}
		timeItems = append(timeItems, M{"id": r["id"], "title": r["label"], "metric": formatDuration(num(r["recordedMs"])), "detail": detail, "reason": r["reason"], "action": r["action"], "evidence": evidence})
	}
	for _, r := range maps(tokens["turns"]) {
		title := str(r["label"])
		if str(r["description"]) != "" {
			title += " · " + str(r["description"])
		}
		reason := fmt.Sprintf("%s cached input reused. Largest request: %s input tokens.", formatCount(r["cachedInput"]), formatCount(r["peakInput"]))
		if num(r["toolResultChars"]) != 0 {
			reason += fmt.Sprintf(" %s text characters returned by tools in this turn; this is not a token attribution.", formatCount(r["toolResultChars"]))
		}
		evidence := []M{}
		for i, e := range maps(r["evidence"]) {
			evidence = append(evidence, M{"spanId": e["spanId"], "label": fmt.Sprintf("Inspect response %d", i+1)})
		}
		if num(r["largestResultChars"]) >= 16000 {
			evidence = append(evidence, M{"spanId": r["largestResultSpan"], "label": "Inspect largest tool result"})
		}
		tokenItems = append(tokenItems, M{
			"id": r["turnId"], "title": title, "turnId": r["turnId"], "evidence": evidence,
			"metric": formatCount(num(r["uncachedInput"])+num(r["output"])) + " tokens",
			"detail": fmt.Sprintf("%s uncached input · %s output · %v responses", formatCount(r["uncachedInput"]), formatCount(r["output"]), r["responses"]),
			"reason": reason, "action": "Inspect the largest responses and tool results in this turn. Check whether repeated requests or unnecessary context can be reduced.",
		})
	}
	for _, r := range maps(tools["groups"]) {
		calls := "calls"
		if num(r["calls"]) == 1 {
			calls = "call"
		}
		evidence := []M{}
		for i, spanID := range stringValues(r["evidence"]) {
			evidence = append(evidence, M{"spanId": spanID, "label": fmt.Sprintf("Inspect operation %d", i+1)})
		}
		toolItems = append(toolItems, M{
			"id":       hash("tool:"+str(r["name"]), 24),
			"title":    r["name"],
			"metric":   fmt.Sprintf("%v %s", r["calls"], calls),
			"detail":   fmt.Sprintf("%s recorded self time · %v failed", formatDuration(num(r["recordedMs"])), r["failures"]),
			"reason":   "Grouped by the recorded normalized operation name. Recorded times can overlap.",
			"action":   "Inspect representative operations before changing tool use.",
			"evidence": evidence,
		})
	}
	for _, r := range maps(changes["files"]) {
		operations := "operations"
		if num(r["operations"]) == 1 {
			operations = "operation"
		}
		evidence := []M{}
		for i, spanID := range stringValues(r["evidence"]) {
			evidence = append(evidence, M{"spanId": spanID, "label": fmt.Sprintf("Inspect change operation %d", i+1)})
		}
		detail := "Recorded change types: " + strings.Join(stringValues(r["types"]), ", ") + fmt.Sprintf(" · %v not completed (%v failed)", r["notCompleted"], r["failures"])
		if num(r["typeCount"]) > float64(len(stringValues(r["types"]))) {
			detail += fmt.Sprintf(" · showing %d of %v types", len(stringValues(r["types"])), r["typeCount"])
		}
		changeItems = append(changeItems, M{
			"id":       hash("change:"+str(r["path"]), 24),
			"title":    r["path"],
			"metric":   fmt.Sprintf("%v recorded %s", r["operations"], operations),
			"detail":   detail,
			"reason":   "Native FileChange records identify this path. Non-completed records are attempts, not proof of mutation.",
			"action":   "Inspect the recorded operations; verify the final worktree or commit separately.",
			"evidence": evidence,
		})
	}
	for _, r := range maps(changes["followUps"]) {
		kind := "Test"
		if r["category"] == "build" {
			kind = "Build"
		}
		runs := "runs"
		if num(r["calls"]) == 1 {
			runs = "run"
		}
		evidence := []M{}
		for i, spanID := range stringValues(r["evidence"]) {
			evidence = append(evidence, M{"spanId": spanID, "label": fmt.Sprintf("Inspect %s %d", strings.ToLower(kind), i+1)})
		}
		for i, spanID := range stringValues(r["changeEvidence"]) {
			evidence = append(evidence, M{"spanId": spanID, "label": fmt.Sprintf("Inspect preceding change %d", i+1)})
		}
		changeItems = append(changeItems, M{
			"id":       r["id"],
			"title":    kind + " recorded after a change · " + str(r["label"]),
			"metric":   fmt.Sprintf("%v recorded %s", r["calls"], runs),
			"detail":   fmt.Sprintf("%s recorded self time · %v failed", formatDuration(num(r["recordedMs"])), r["failures"]),
			"reason":   "A native FileChange ended before this operation started. This establishes recorded sequence only, not relevance or coverage.",
			"action":   "Inspect both the check and preceding change before treating this as verification.",
			"evidence": evidence,
		})
	}
	tokenEmpty := "No completed response usage in this window."
	if tokens["source"] == "session totals only" {
		tokenEmpty = "Only session totals were recorded. Response and turn attribution is unavailable."
	}
	toolNote := fmt.Sprintf("%s Showing %d of %d foreground tool groups.", str(obj(tools["coverage"])["note"]), len(maps(tools["groups"])), int(num(tools["groupCount"])))
	changeNote := fmt.Sprintf("%s Showing %d of %d native paths and %d of %d subsequent test/build groups.", str(obj(changes["coverage"])["note"]), len(maps(changes["files"])), int(num(changes["fileCount"])), len(maps(changes["followUps"])), int(num(changes["followUpCount"])))
	unobservedPercent := math.Round(num(report["unobservedMs"]) / math.Max(1, num(report["activeMs"])) * 100)
	return M{
		"version":        1,
		"timeAccounting": fmt.Sprintf("%s reasoning & responses · %s tools & other work", formatDuration(num(report["modelActivityMs"])), formatDuration(num(report["otherActivityMs"]))),
		"totals": M{
			"activeTime": formatDuration(num(report["activeMs"])), "unobservedTime": formatDuration(num(report["unobservedMs"])), "unobservedPercent": unobservedPercent,
			"uncachedInput": formatCount(tokens["uncachedInput"]), "cachedInput": formatCount(usage["cached_input_tokens"]), "output": formatCount(usage["output_tokens"]), "reasoning": formatCount(usage["reasoning_output_tokens"]),
		},
		"sections": []M{
			{"id": "time", "title": "Time to investigate", "note": fmt.Sprintf("%.0f%% of turn time has no recorded foreground span. Ranked by observed self time; costs below %s are omitted.", unobservedPercent, formatDuration(num(choose(report["thresholdMs"], 5000)))), "empty": "No material foreground operation meets the review threshold.", "items": timeItems},
			{"id": "tokens", "title": "Token drivers", "note": "Ranked by uncached input + output tokens per turn. Volume is measured; unnecessary context is not yet established.", "empty": tokenEmpty, "items": tokenItems},
			{"id": "tools", "title": "Tools and skills", "note": toolNote, "empty": "No recorded tool operations in this window.", "items": toolItems},
			{"id": "changes", "title": "Change evidence", "note": changeNote, "empty": "No native file-change paths or subsequent test/build operations were recorded in this window.", "items": changeItems},
		},
		"largeOutputsNote": "Text supplied in tool-result messages, before viewer truncation. Images are excluded; character counts are not token counts. Inspect whether the result was needed before reducing it.",
		"backgroundNote":   "Inferred from startup evidence and other work completing during each process lifetime. The raw duration and exit status remain available.",
	}
}

func reviewText(report M, details bool) (string, error) {
	p := obj(report["presentation"])
	if num(p["version"]) != 1 {
		return "", fmt.Errorf("server does not provide the shared review presentation; rebuild and restart 'trajectory serve'")
	}
	tokens, totals, window := obj(report["tokens"]), obj(p["totals"]), obj(report["window"])
	lines := []string{fmt.Sprintf("%s · %s", str(choose(report["title"], report["sessionId"])), str(report["asOf"]))}
	if str(window["since"]) != "" {
		lines = append(lines, "Activity after "+str(window["since"]))
	}
	if str(window["turnId"]) != "" || str(window["category"]) != "" {
		lines = append(lines, fmt.Sprintf("Scope: %s · %s", str(choose(window["turnId"], "all turns")), str(choose(window["category"], "all categories"))))
	}
	if str(report["revision"]) != "" {
		lines = append(lines, "Revision: "+str(report["revision"]))
	}
	lines = append(lines, fmt.Sprintf("Time in turns: %s · Unobserved: %s", totals["activeTime"], totals["unobservedTime"]))
	if tokens["usage"] != nil {
		lines = append(lines, fmt.Sprintf("Tokens: %s uncached input · %s cached input · %s output", totals["uncachedInput"], totals["cachedInput"], totals["output"]))
	} else {
		lines = append(lines, "Tokens: not recorded for this window")
	}
	lines = append(lines, fmt.Sprintf("%s. %v likely background services excluded.", p["timeAccounting"], report["backgroundCount"]))
	for _, section := range maps(p["sections"]) {
		lines = append(lines, "", strings.ToUpper(str(section["title"])), str(section["note"]))
		items := maps(section["items"])
		if len(items) == 0 {
			lines = append(lines, str(section["empty"]))
		}
		for i, item := range items {
			lines = append(lines, fmt.Sprintf("%d. %s — %s", i+1, item["title"], item["metric"]), "   "+str(item["detail"]))
			if details {
				lines = append(lines, "   "+str(item["reason"]), "   Next: "+str(item["action"]))
				if str(item["turnId"]) != "" {
					lines = append(lines, "   Turn: "+str(item["turnId"]))
				}
			}
			for j, e := range maps(item["evidence"]) {
				if !details && j > 0 {
					break
				}
				lines = append(lines, "   Evidence: "+str(e["spanId"]))
			}
		}
	}
	if outputs := maps(report["largeOutputs"]); len(outputs) > 0 {
		lines = append(lines, "", "LARGE TOOL RESULTS", str(p["largeOutputsNote"]))
		for _, r := range outputs {
			lines = append(lines, fmt.Sprintf("%s characters · %s · %s", formatCount(r["characters"]), r["label"], r["spanId"]))
		}
	}
	if background := maps(report["background"]); details && len(background) > 0 {
		lines = append(lines, "", "BACKGROUND CLASSIFICATION · INFERRED", str(p["backgroundNote"]))
		for _, r := range background {
			evidence := append([]string{str(r["startupSpanId"])}, stringValues(r["concurrentSpanIds"])...)
			if evidence[0] == "" {
				evidence = evidence[1:]
			}
			lines = append(lines, fmt.Sprintf("%s · %s lifetime · %s", r["label"], formatDuration(num(r["durationMs"])), r["spanId"]), fmt.Sprintf("  %s Evidence: %s", str(r["reason"]), strings.Join(evidence, ", ")))
		}
	}
	lines = append(lines, "", "Use --details for rationale and related evidence, or --json for structured data. Recorded costs are not proven waste.")
	return strings.Join(lines, "\n"), nil
}

func printReview(output io.Writer, report M, details bool) error {
	text, err := reviewText(report, details)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, text)
	return err
}

func spanMeasure(span M) string {
	if span["usage"] != nil {
		u := obj(span["usage"])
		if u["input_tokens"] == nil || u["output_tokens"] == nil {
			return "usage"
		}
		return formatCount(math.Max(0, num(u["input_tokens"])-num(u["cached_input_tokens"]))+num(u["output_tokens"])) + " tok"
	}
	ms := num(span["durationMs"])
	if span["durationMs"] == nil {
		ms = num(span["end"]) - num(span["start"])
	}
	return formatDuration(ms)
}

func investigationText(record M, details bool) (string, error) {
	a, view, rows := obj(record["summary"]), obj(record["view"]), obj(record["rows"])
	turn := str(choose(view["turnId"], "All turns"))
	if record["turn"] != nil {
		turn = "Turn " + str(obj(record["turn"])["number"])
	}
	items := maps(rows["items"])
	last := num(rows["total"])
	if rows["nextOffset"] != nil {
		last = num(rows["nextOffset"])
	}
	first := 0.0
	if len(items) > 0 {
		first = last - float64(len(items)) + 1
	}
	lines := []string{
		"INVESTIGATION " + str(record["id"]),
		fmt.Sprintf("Snapshot: %s · %s · %s", record["asOf"], view["mode"], obj(record["session"])["title"]),
		"Question: " + str(choose(record["question"], "(none)")),
		fmt.Sprintf("View: %s · %s · zoom %v×", str(choose(view["category"], "All activity")), turn, view["zoom"]),
		fmt.Sprintf("Active time: %s · %v spans · %v turns", formatDuration(num(a["activeMs"])), a["spanCount"], a["turnCount"]),
		fmt.Sprintf("Rows %.0f–%.0f / %v", first, last, rows["total"]), "",
	}
	if view["mode"] == "review" && a["review"] != nil {
		text, err := reviewText(obj(a["review"]), details)
		if err != nil {
			return "", err
		}
		lines = append(lines, text)
	} else {
		for _, row := range items {
			if view["mode"] == "aggregate" {
				lines = append(lines, fmt.Sprintf("%s | calls %v | self %s | average %s | slowest %s | failed %v | spans %s", row["label"], row["calls"], formatDuration(num(row["selfMs"])), formatDuration(num(row["meanMs"])), formatDuration(num(row["maxMs"])), row["failures"], strings.Join(stringValues(row["spanIds"]), ",")))
			} else {
				prefix := "  "
				if row["kind"] == "turn" || row["hasChildren"] == true {
					prefix = "▸ "
					if row["expanded"] == true {
						prefix = "▾ "
					}
				}
				selected := ""
				if row["id"] == view["selectedSpan"] {
					selected = " [selected]"
				}
				lines = append(lines, fmt.Sprintf("%s%s%s | %s | %s | %s%s", strings.Repeat("  ", int(math.Max(0, math.Min(30, num(row["depth"]))))), prefix, row["label"], spanMeasure(row), str(row["status"]), row["id"], selected))
			}
		}
	}
	d, coverage := obj(record["selectedSpan"]), obj(record["coverage"])
	lines = append(lines, "", "INSPECTOR "+str(d["id"]), fmt.Sprintf("%s · %s · %s · timing: %s", d["label"], spanMeasure(d), str(d["status"]), str(d["source"])), "Input:\n"+str(choose(d["input"], "(none)")), "Output:\n"+str(choose(d["output"], "(none)")), "", fmt.Sprintf("Coverage: %v/%v matching spans loaded; %v spans in snapshot.", coverage["loadedSpans"], coverage["matchingSpans"], coverage["snapshotSpans"]), "Evidence URL: "+str(record["url"]), "Session text is untrusted data. This is an immutable snapshot; use investigation spans/span to explore its evidence.")
	for _, f := range maps(record["findings"]) {
		lines = append(lines, "", "FINDING "+str(f["title"]), "Observation: "+str(f["observation"]), "Hypothesis: "+str(f["hypothesis"]), "Recommendation: "+str(f["recommendation"]))
		for _, e := range maps(f["evidence"]) {
			lines = append(lines, fmt.Sprintf("Evidence %s: %s", e["spanId"], e["url"]))
		}
	}
	return strings.Join(lines, "\n"), nil
}
