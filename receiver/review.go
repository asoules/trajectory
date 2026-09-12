package main

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var serviceCommandRE = regexp.MustCompile(`^(?:(?:npm|pnpm|yarn|bun)\s+(?:run\s+)?(?:dev|serve|preview)(?:\s|$)|node\s+[^\s]+\s+serve(?:\s|$)|(?:npx\s+)?(?:http-server|uvicorn)(?:\s|$)|(?:npx\s+)?vite(?:\s+(?:serve|dev|preview)(?:\s|$)|\s+--(?:host|port)(?:[ =]|$)|$)|python[23]?\s+-m\s+http\.server(?:\s|$))`)
var serviceOutputRE = regexp.MustCompile(`(?im)(?:listening (?:on|at)|(?:HTTP|OTLP)\s*(?:server)?\s*(?:→|->|at|on|:)\s*)https?://(?:127\.0\.0\.1|localhost):\d+`)
var scriptTargetRE = regexp.MustCompile(`(?:['"])([A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+\.[A-Za-z0-9]+)(?:['"])`)

// Match this executable's server mode and full path. Its CLI/MCP subcommands
// are foreground work, even though they now use the same binary.
func receiverCommand(cmd, cwd, executable string) bool {
	parts := strings.Fields(cmd)
	if len(parts) == 0 || executable == "" {
		return false
	}
	path := parts[0]
	if !filepath.IsAbs(path) {
		if strings.HasPrefix(cwd, "file:") {
			u, err := url.Parse(cwd)
			if err != nil || (u.Host != "" && u.Host != "localhost") {
				return false
			}
			cwd = u.Path
		}
		if !filepath.IsAbs(cwd) || !strings.Contains(path, "/") {
			return false
		}
		path = filepath.Join(cwd, path)
	}
	if filepath.Clean(path) != filepath.Clean(executable) {
		return false
	}
	firstFlag := 1
	if len(parts) > 1 && parts[1] == "serve" {
		firstFlag = 2
	}
	for i := firstFlag; i < len(parts); i++ {
		key, value, equals := strings.Cut(parts[i], "=")
		switch key {
		case "-demo", "--demo":
			if equals && value != "true" && value != "false" {
				return false
			}
		case "-port", "--port", "-db", "--db", "-home", "--home":
			if !equals {
				i++
				if i >= len(parts) {
					return false
				}
				value = parts[i]
			}
			if value == "" || strings.ContainsAny(value, ";&|`$<>()\\\"'") {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// Classification is intentionally narrow and explained in the returned evidence.
// Duration and nonzero exit status alone never establish a background service.
func backgroundServices(ctx context.Context, session M) (map[string]M, error) {
	spans := maps(session["spans"])
	out := map[string]M{}
	executable, _ := os.Executable()
	announced := map[string]M{}
	for _, v := range spans {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cmd := strings.TrimSpace(str(v["input"]))
		if v["category"] == "shell" && strings.HasPrefix(cmd, "./") && !strings.Contains(cmd, "\n") && serviceOutputRE.MatchString(str(v["output"])) {
			key := str(v["turnId"]) + ":" + cmd
			if prior := announced[key]; prior == nil || num(v["start"]) < num(prior["start"]) {
				announced[key] = v
			}
		}
	}
	turns := map[string]M{}
	for _, t := range maps(session["turns"]) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		turns[str(t["id"])] = t
	}
	for _, v := range spans {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if v["category"] != "shell" || num(v["durationMs"]) < 30000 {
			continue
		}
		cmd := strings.TrimSpace(str(v["input"]))
		knownCommand := serviceCommandRE.MatchString(cmd) || receiverCommand(cmd, str(v["cwd"]), executable)
		// Arbitrary local scripts can run tests after starting a service, so a
		// startup announcement alone cannot exclude their full lifetime.
		turn := turns[str(v["turnId"])]
		startup := announced[str(v["turnId"])+":"+cmd]
		outlivesTurn := turn != nil && num(v["end"]) > num(turn["end"])+1000
		ownAnnouncement := strings.HasPrefix(cmd, "./") && outlivesTurn && serviceOutputRE.MatchString(str(v["output"]))
		relatedAnnouncement := startup != nil && num(startup["start"]) < num(v["start"]) && outlivesTurn
		if !knownCommand && !ownAnnouncement && !relatedAnnouncement {
			continue
		}

		// Multiline edit scripts often contain server commands as data.
		if strings.Contains(cmd, "\n") || strings.Contains(cmd, "<<") {
			continue
		}
		evidence := []string{}
		for _, w := range spans {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if w["id"] == v["id"] || w["parentId"] == v["id"] || w["category"] == "model" || w["category"] == "reasoning" || w["category"] == "response" || w["wrapper"] == true {
				continue
			}
			if w["status"] == "completed" && num(w["durationMs"]) > 0 && num(w["start"]) > num(v["start"])+1000 && num(w["end"]) < num(v["end"]) {
				evidence = append(evidence, str(w["id"]))
				if len(evidence) == 2 {
					break
				}
			}
		}
		if len(evidence) < 2 {
			continue
		}
		reason := "Recognized service startup and other operations completing during the process lifetime; classification is inferred."
		var startupID any
		if !knownCommand && !ownAnnouncement {
			reason = "An identical earlier invocation in this turn announced a local service. This process outlived the turn while other work completed; classification is inferred."
			startupID = startup["id"]
		}
		out[str(v["id"])] = M{"spanId": v["id"], "label": operationLabel(v), "durationMs": v["durationMs"], "confidence": "inferred", "reason": reason, "startupSpanId": startupID, "concurrentSpanIds": evidence}
	}
	return out, ctx.Err()
}

func operationLabel(v M) string {
	cmd := strings.TrimSpace(str(v["input"]))
	if v["category"] == "model" {
		return "Model response"
	}
	if v["category"] != "shell" && v["category"] != "tests" && v["category"] != "build" {
		return clip(v["label"], 100)
	}
	if cmd == "" {
		return clip(v["label"], 100)
	}
	line := strings.Split(cmd, "\n")[0]
	if strings.Contains(line, "<<") {
		// Keep the action and target visible; the full script stays in the inspector.
		if strings.Contains(line, "> ") {
			return clip(line, 100)
		}
		target := scriptTargetRE.FindStringSubmatch(cmd)
		label := strings.TrimSpace(strings.Split(line, "<<")[0]) + " script"
		if len(target) > 1 {
			label += " · " + target[1]
		}
		return clip(label, 100)
	}
	return clip(line, 100)
}

func sumUsage(dst M, src M) {
	for _, k := range []string{"input_tokens", "cached_input_tokens", "cache_write_input_tokens", "output_tokens", "reasoning_output_tokens", "total_tokens"} {
		dst[k] = num(dst[k]) + num(src[k])
	}
}
func deliveryAt(v M) float64 {
	if v["deliveredAt"] != nil {
		return num(v["deliveredAt"])
	}
	return num(v["end"])
}

func uncached(u M) float64 { return math.Max(0, num(u["input_tokens"])-num(u["cached_input_tokens"])) }

func representativeEvidence(items []M, limit int, includeFailure bool) []string {
	sort.SliceStable(items, func(i, j int) bool { return num(items[i]["selfMs"]) > num(items[j]["selfMs"]) })
	evidence := []string{}
	for _, item := range items {
		if len(evidence) == limit {
			break
		}
		evidence = append(evidence, str(item["id"]))
	}
	if !includeFailure {
		return evidence
	}
	failureID := ""
	failureShown := false
	for _, item := range items {
		if item["status"] == "failed" {
			failureID = str(item["id"])
		}
		for _, id := range evidence {
			failureShown = failureShown || id == failureID
		}
		if failureID != "" {
			break
		}
	}
	if failureID != "" && !failureShown {
		if len(evidence) < limit {
			evidence = append(evidence, failureID)
		} else {
			evidence[len(evidence)-1] = failureID
		}
	}
	return evidence
}

func toolReport(ctx context.Context, selected []M, args M, services map[string]M) (M, error) {
	type group struct {
		items    []M
		ms       float64
		failures int
	}
	grouped := map[string]*group{}
	since := math.Inf(-1)
	if args["since"] != nil {
		since = timestamp(args["since"])
	}
	backgroundOperations := 0
	for _, v := range selected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		category, name := str(v["category"]), str(v["name"])
		if num(v["end"]) <= since || (str(args["category"]) != "" && category != str(args["category"])) || name == "" || v["wrapper"] == true || category == "model" || category == "reasoning" || category == "response" || category == "compaction" {
			continue
		}
		if services[str(v["id"])] != nil {
			backgroundOperations++
			continue
		}
		g := grouped[name]
		if g == nil {
			g = &group{}
			grouped[name] = g
		}
		g.items = append(g.items, v)
		g.ms += num(v["selfMs"])
		if v["status"] == "failed" {
			g.failures++
		}
	}
	groups := []M{}
	for name, g := range grouped {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		evidence := representativeEvidence(g.items, 3, g.failures > 0)
		groups = append(groups, M{"name": name, "calls": len(g.items), "failures": g.failures, "recordedMs": math.Round(g.ms), "evidence": evidence})
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i]["recordedMs"] == groups[j]["recordedMs"] {
			if groups[i]["calls"] == groups[j]["calls"] {
				return str(groups[i]["name"]) < str(groups[j]["name"])
			}
			return num(groups[i]["calls"]) > num(groups[j]["calls"])
		}
		return num(groups[i]["recordedMs"]) > num(groups[j]["recordedMs"])
	})
	count := len(groups)
	if len(groups) > 5 {
		groups = groups[:5]
	}
	return M{
		"groups":                      groups,
		"groupCount":                  count,
		"backgroundServiceOperations": backgroundOperations,
		"coverage": M{
			"tools":   "observed",
			"skills":  "unavailable",
			"retries": "unavailable",
			"note":    fmt.Sprintf("Recorded foreground operations are grouped by normalized name; orchestration wrappers and %d likely background-service operations are excluded. Background classification is inferred. Skills unavailable: Codex skill activation is not recorded as a distinct event. Retry classification unavailable: repeated calls are not classified as retries.", backgroundOperations),
		},
	}, ctx.Err()
}

func changeReport(ctx context.Context, selected []M, args M) (M, error) {
	type file struct {
		types            map[string]bool
		best             map[string]M
		bestNotCompleted map[string]M
		count            int
		failures         int
		notCompleted     int
	}
	type followUp struct {
		category       string
		label          string
		items          []M
		changeEvidence []string
		ms             float64
		failures       int
	}
	grouped := map[string]*file{}
	edits := []M{}
	since := math.Inf(-1)
	if args["since"] != nil {
		since = timestamp(args["since"])
	}
	for _, v := range selected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if num(v["end"]) <= since {
			continue
		}
		changes := maps(v["changes"])
		if len(changes) > 0 && v["status"] == "completed" {
			edits = append(edits, v)
		}
		if str(args["category"]) != "" && v["category"] != args["category"] {
			continue
		}
		for _, change := range changes {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			changeType := str(choose(change["type"], "unknown"))
			paths := []string{str(change["path"])}
			if movePath := str(change["movePath"]); movePath != "" && movePath != paths[0] {
				paths = append(paths, movePath)
			}
			for _, path := range paths {
				if path == "" {
					continue
				}
				f := grouped[path]
				if f == nil {
					f = &file{types: map[string]bool{}, best: map[string]M{}, bestNotCompleted: map[string]M{}}
					grouped[path] = f
				}
				f.count++
				f.types[changeType] = true
				if f.best[changeType] == nil || num(v["selfMs"]) > num(f.best[changeType]["selfMs"]) {
					f.best[changeType] = v
				}
				if v["status"] == "failed" {
					f.failures++
				}
				if v["status"] != "completed" {
					f.notCompleted++
					if f.bestNotCompleted[changeType] == nil || num(v["selfMs"]) > num(f.bestNotCompleted[changeType]["selfMs"]) {
						f.bestNotCompleted[changeType] = v
					}
				}
			}
		}
	}
	files := []M{}
	for path, f := range grouped {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		types := []string{}
		for changeType := range f.types {
			types = append(types, changeType)
		}
		sort.Strings(types)
		typeCount := len(types)
		notCompletedType := ""
		var notCompletedSpan M
		for changeType, span := range f.bestNotCompleted {
			if notCompletedSpan == nil || num(span["selfMs"]) > num(notCompletedSpan["selfMs"]) || (num(span["selfMs"]) == num(notCompletedSpan["selfMs"]) && changeType < notCompletedType) {
				notCompletedType, notCompletedSpan = changeType, span
			}
		}
		if len(types) > 3 {
			types = types[:3]
			if notCompletedSpan != nil {
				shown := false
				for _, changeType := range types {
					shown = shown || changeType == notCompletedType
				}
				if !shown {
					types[len(types)-1] = notCompletedType
					sort.Strings(types)
				}
			}
		}
		evidence := []string{}
		for _, changeType := range types {
			chosen := f.best[changeType]
			if changeType == notCompletedType {
				chosen = notCompletedSpan
			}
			if chosen != nil {
				evidence = append(evidence, str(chosen["id"]))
			}
		}
		files = append(files, M{"path": path, "operations": f.count, "failures": f.failures, "notCompleted": f.notCompleted, "types": types, "typeCount": typeCount, "evidence": evidence})
	}
	sort.SliceStable(files, func(i, j int) bool {
		if files[i]["operations"] == files[j]["operations"] {
			return str(files[i]["path"]) < str(files[j]["path"])
		}
		return num(files[i]["operations"]) > num(files[j]["operations"])
	})
	count := len(files)
	if len(files) > 5 {
		files = files[:5]
	}
	sort.SliceStable(edits, func(i, j int) bool {
		if num(edits[i]["end"]) == num(edits[j]["end"]) {
			return str(edits[i]["id"]) < str(edits[j]["id"])
		}
		return num(edits[i]["end"]) < num(edits[j]["end"])
	})
	checks := []M{}
	for _, v := range selected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		category := str(v["category"])
		if num(v["end"]) <= since || (category != "tests" && category != "build") || (str(args["category"]) != "" && v["category"] != args["category"]) {
			continue
		}
		checks = append(checks, v)
	}
	sort.SliceStable(checks, func(i, j int) bool {
		if num(checks[i]["start"]) == num(checks[j]["start"]) {
			return str(checks[i]["id"]) < str(checks[j]["id"])
		}
		return num(checks[i]["start"]) < num(checks[j]["start"])
	})
	followed := map[string]*followUp{}
	nextEdit := 0
	var preceding M
	for _, v := range checks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for nextEdit < len(edits) && num(edits[nextEdit]["end"]) <= num(v["start"]) {
			preceding = edits[nextEdit]
			nextEdit++
		}
		if preceding == nil {
			continue
		}
		category := str(v["category"])
		key := category + ":" + str(choose(v["inputKey"], v["input"], v["label"], v["name"]))
		g := followed[key]
		if g == nil {
			g = &followUp{category: category, label: operationLabel(v)}
			followed[key] = g
		}
		g.items = append(g.items, v)
		g.ms += num(v["selfMs"])
		if v["status"] == "failed" {
			g.failures++
		}
		changeID := str(preceding["id"])
		seen := false
		for _, id := range g.changeEvidence {
			seen = seen || id == changeID
		}
		if !seen && len(g.changeEvidence) < 3 {
			g.changeEvidence = append(g.changeEvidence, changeID)
		}
	}
	followUps := []M{}
	for key, g := range followed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		evidence := representativeEvidence(g.items, 3, g.failures > 0)
		followUps = append(followUps, M{"id": hash("change-follow-up:"+key, 24), "category": g.category, "label": g.label, "calls": len(g.items), "failures": g.failures, "recordedMs": math.Round(g.ms), "evidence": evidence, "changeEvidence": g.changeEvidence})
	}
	sort.SliceStable(followUps, func(i, j int) bool {
		if followUps[i]["recordedMs"] == followUps[j]["recordedMs"] {
			if followUps[i]["label"] == followUps[j]["label"] {
				return str(followUps[i]["id"]) < str(followUps[j]["id"])
			}
			return str(followUps[i]["label"]) < str(followUps[j]["label"])
		}
		return num(followUps[i]["recordedMs"]) > num(followUps[j]["recordedMs"])
	})
	followUpCount := len(followUps)
	if len(followUps) > 3 {
		followUps = followUps[:3]
	}
	fileCoverage := "unavailable"
	if count > 0 {
		fileCoverage = "observed"
	}
	checkCoverage := "unavailable"
	if len(edits) > 0 {
		checkCoverage = "not observed"
	}
	if followUpCount > 0 {
		checkCoverage = "observed"
	}
	return M{
		"files":         files,
		"fileCount":     count,
		"followUps":     followUps,
		"followUpCount": followUpCount,
		"coverage": M{
			"fileChanges":      fileCoverage,
			"subsequentChecks": checkCoverage,
			"shellChanges":     "unavailable",
			"gitChanges":       "unavailable",
			"note":             "Paths come only from native FileChange records, including move destinations. Non-completed FileChange records are attempts and do not anchor later checks. Tests and builds are reported as subsequent when they started after an earlier completed change ended; sequence does not establish relevance or coverage. Shell commands or scripts may have changed other files. This is not a final filesystem or Git diff.",
		},
	}, ctx.Err()
}

// Completion timestamps define incremental usage windows. Tool output characters
// are a separate pressure signal, never converted to an invented token estimate.
func tokenReport(session M, args M) M {
	lo := math.Inf(-1)
	if args["since"] != nil {
		lo = timestamp(args["since"])
	}
	turnID := str(args["turnId"])
	turns := map[string]M{}
	for i, t := range maps(session["turns"]) {
		turns[str(t["id"])] = M{"turnId": t["id"], "label": fmt.Sprintf("Turn %d", i+1), "description": t["label"], "start": t["start"], "end": t["end"], "responses": 0, "usage": M{}, "peakInput": 0, "evidence": []M{}}
	}
	totals := M{}
	n := 0
	for _, v := range maps(session["spans"]) {
		u := obj(v["usage"])
		if len(u) == 0 || (turnID != "" && v["turnId"] != turnID) || (args["since"] != nil && num(v["end"]) <= lo) {
			continue
		}
		t := turns[str(v["turnId"])]
		if t == nil {
			t = M{"turnId": v["turnId"], "label": "Unassigned responses", "responses": 0, "usage": M{}, "peakInput": 0, "evidence": []M{}}
			turns[str(v["turnId"])] = t
		}
		n++
		sumUsage(totals, u)
		sumUsage(obj(t["usage"]), u)
		t["responses"] = num(t["responses"]) + 1
		t["peakInput"] = math.Max(num(t["peakInput"]), num(u["input_tokens"]))
		evidence := maps(t["evidence"])
		evidence = append(evidence, M{"spanId": v["id"], "uncachedInput": uncached(u), "output": u["output_tokens"], "input": u["input_tokens"], "at": v["end"]})
		t["evidence"] = evidence
	}
	for _, v := range maps(session["spans"]) {
		if v["deliveredChars"] == nil || deliveryAt(v) <= lo {
			continue
		}
		if t := turns[str(v["turnId"])]; t != nil {
			t["toolResultChars"] = num(t["toolResultChars"]) + num(v["deliveredChars"])
			if num(v["deliveredChars"]) > num(t["largestResultChars"]) {
				t["largestResultChars"] = v["deliveredChars"]
				t["largestResultSpan"] = v["id"]
			}
		}
	}

	ranked := []M{}
	for _, t := range turns {
		if num(t["responses"]) == 0 {
			continue
		}
		u := obj(t["usage"])
		t["uncachedInput"] = uncached(u)
		t["cachedInput"] = num(u["cached_input_tokens"])
		t["output"] = num(u["output_tokens"])
		ev := maps(t["evidence"])
		sort.SliceStable(ev, func(i, j int) bool {
			return num(ev[i]["uncachedInput"])+num(ev[i]["output"]) > num(ev[j]["uncachedInput"])+num(ev[j]["output"])
		})
		if len(ev) > 3 {
			ev = ev[:3]
		}
		t["evidence"] = ev
		delete(t, "usage")
		ranked = append(ranked, t)
	}
	sort.Slice(ranked, func(i, j int) bool {
		a, b := num(ranked[i]["uncachedInput"])+num(ranked[i]["output"]), num(ranked[j]["uncachedInput"])+num(ranked[j]["output"])
		if a == b {
			return str(ranked[i]["turnId"]) < str(ranked[j]["turnId"])
		}
		return a > b
	})
	source := "response records"
	var usage any = totals
	if n == 0 {
		source = "unavailable"
		usage = nil
		if args["since"] == nil && turnID == "" && session["tokens"] != nil {
			source = "session totals only"
			usage = session["tokens"]
		}
	}
	return M{"source": source, "responses": n, "usage": usage, "uncachedInput": uncached(obj(usage)), "turns": ranked, "window": "Responses completed after since; cached input is included in input; reasoning is included in output. No monetary cost or recoverable-token estimate."}
}

func reviewReport(ctx context.Context, session M, args M, summary M, selected []M, services map[string]M) (M, error) {
	threshold := math.Max(5000, num(summary["activeMs"])*0.005)
	// Use all groups before API pagination. Ignore transport/orchestration wrappers
	// and tiny events; they are available in the full timeline, not recommendations.
	type group struct {
		items  []M
		ms     float64
		failed int
	}
	groups := map[string]*group{}
	output := []M{}
	for _, v := range maps(session["spans"]) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if (str(args["turnId"]) != "" && v["turnId"] != args["turnId"]) || (str(args["category"]) != "" && v["category"] != args["category"]) {
			continue
		}
		if num(v["deliveredChars"]) >= 16000 && (args["since"] == nil || deliveryAt(v) > timestamp(args["since"])) {
			output = append(output, M{"spanId": v["id"], "label": operationLabel(v), "characters": v["deliveredChars"], "turnId": v["turnId"], "deliveredAt": deliveryAt(v)})
		}
	}

	for _, v := range selected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if services[str(v["id"])] != nil || (str(args["category"]) != "" && v["category"] != args["category"]) {
			continue
		}
		c := str(v["category"])
		name := str(v["name"])
		if c == "model" || c == "reasoning" || c == "response" || c == "compaction" || v["wrapper"] == true || wrapperRE.MatchString(name) {
			continue
		}

		key := c + ":" + str(choose(v["inputKey"], v["input"], v["label"]))
		if c == "edit" && len(maps(v["changes"])) > 0 {
			key = c + ":" + str(v["label"])
		}
		if c == "tools" {
			key = c + ":" + name
		}
		g := groups[key]
		if g == nil {
			g = &group{}
			groups[key] = g
		}
		g.items = append(g.items, v)
		g.ms += num(v["selfMs"])
		if v["status"] == "failed" {
			g.failed++
		}
	}
	timeItems := []M{}
	for key, g := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if g.ms < threshold {
			continue
		}
		// One leaf's elapsed time is evidence of cost, not proof it blocked progress.
		title, reason, action := "Long operation", "Recorded execution time; overlap may reduce its effect on elapsed time.", "Inspect the operation and overlapping work before changing the workflow."
		if g.failed > 0 {
			title = "Failed work"
			reason = "A recorded nonzero exit or tool error. Cancellation can also produce this status."
			action = "Inspect the failure and the next attempt; establish the cause before rerunning."
		}
		if len(g.items) >= 3 && str(g.items[0]["category"]) == "tools" {
			title = "Tool activity"
			reason = "Calls to this tool are grouped by name; their inputs and purpose may differ."
			action = "Inspect the largest calls. Look for oversized results or redundant inspection before reducing tool use."
		} else if len(g.items) >= 3 && str(g.items[0]["input"]) != "" && (g.items[0]["inputKey"] != nil || len([]rune(str(g.items[0]["input"]))) < 16000) {
			title = "Repeated operation"
			reason = "The same recorded operation ran at least three times. Source changes may justify reruns."
			action = "Compare inputs, intervening edits, and results. Reuse work only when its inputs are unchanged."
		}
		if len(g.items) >= 3 && (str(g.items[0]["input"]) == "" || len(maps(g.items[0]["changes"])) > 0) && str(g.items[0]["category"]) != "tools" {
			title = "Recorded activity"
			reason = "Grouped by activity label; individual inputs were not recorded."
			action = "Inspect the largest operations. Matching labels alone do not establish redundant work."
			if len(maps(g.items[0]["changes"])) > 0 {
				reason = "Recorded file-change operations are grouped by activity label; their paths and purposes may differ."
				action = "Inspect the largest operations and their path evidence before changing the workflow."
			}
		}
		evidence := []string{}
		sort.SliceStable(g.items, func(i, j int) bool { return num(g.items[i]["selfMs"]) > num(g.items[j]["selfMs"]) })
		for _, v := range g.items {
			if len(evidence) < 3 {
				evidence = append(evidence, str(v["id"]))
			}
		}
		timeItems = append(timeItems, M{"id": hash(key, 24), "title": title, "label": operationLabel(g.items[0]), "recordedMs": math.Round(g.ms), "calls": len(g.items), "failures": g.failed, "reason": reason, "action": action, "evidence": evidence, "confidence": "measured cost; avoidability unproven"})
	}
	sort.Slice(timeItems, func(i, j int) bool {
		if timeItems[i]["recordedMs"] == timeItems[j]["recordedMs"] {
			return str(timeItems[i]["id"]) < str(timeItems[j]["id"])
		}
		return num(timeItems[i]["recordedMs"]) > num(timeItems[j]["recordedMs"])
	})
	candidateCount := len(timeItems)
	if len(timeItems) > 3 {
		timeItems = timeItems[:3]
	}
	sort.Slice(output, func(i, j int) bool {
		if output[i]["characters"] == output[j]["characters"] {
			return str(output[i]["spanId"]) < str(output[j]["spanId"])
		}
		return num(output[i]["characters"]) > num(output[j]["characters"])
	})
	outputCount := len(output)
	if len(output) > 3 {
		output = output[:3]
	}
	background := []M{}
	for _, v := range selected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if b := services[str(v["id"])]; b != nil {
			background = append(background, b)
		}
	}
	sort.Slice(background, func(i, j int) bool { return num(background[i]["durationMs"]) > num(background[j]["durationMs"]) })
	backgroundCount := len(background)
	if len(background) > 5 {
		background = background[:5]
	}
	tokens := tokenReport(session, args)
	turnCount := len(maps(tokens["turns"]))
	if turnCount > 3 {
		tokens["turns"] = maps(tokens["turns"])[:3]
	}
	tokens["turnCount"] = turnCount
	unknown, modelActivity, otherActivity := 0.0, 0.0, 0.0
	for _, b := range maps(summary["breakdown"]) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if b["category"] == "unobserved" {
			unknown = num(b["ms"])
		} else if b["category"] == "reasoning" || b["category"] == "response" {
			modelActivity += num(b["ms"])
		} else {
			otherActivity += num(b["ms"])
		}
	}
	tools, err := toolReport(ctx, selected, args, services)
	if err != nil {
		return nil, err
	}
	changes, err := changeReport(ctx, selected, args)
	if err != nil {
		return nil, err
	}
	report := M{"version": 1, "asOf": summary["asOf"], "sessionId": session["id"], "title": session["title"], "window": summary["window"], "activeMs": summary["activeMs"], "unobservedMs": unknown, "modelActivityMs": modelActivity, "otherActivityMs": otherActivity, "time": timeItems, "timeCount": candidateCount, "thresholdMs": math.Round(threshold), "tokens": tokens, "tools": tools, "changes": changes, "largeOutputs": output, "largeOutputCount": outputCount, "background": background, "backgroundCount": backgroundCount, "method": "Time ranks summed self time excluding likely background services. Overlapping work is not additive wall time; recorded cost is not proven waste. Token turns rank uncached input plus output volume, not monetary cost."}
	report["presentation"] = reviewPresentation(report)
	return report, ctx.Err()
}
