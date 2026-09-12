package main

import (
	"context"
	"math"
	"sort"
)

type interval struct{ a, b float64 }

func union(intervals []interval) float64 {
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].a < intervals[j].a })
	total, end := 0.0, math.Inf(-1)
	for _, v := range intervals {
		total += math.Max(0, v.b-math.Max(v.a, end))
		end = math.Max(end, v.b)
	}
	return total
}
func analyze(ctx context.Context, session M, args M) (M, error) {
	services, err := backgroundServices(ctx, session)
	if err != nil {
		return nil, err
	}
	lo := math.Inf(-1)
	if args["since"] != nil {
		lo = timestamp(args["since"])
	}
	turnID, cat := str(args["turnId"]), str(args["category"])
	top := int(num(args["top"]))
	turns := []interval{}
	turnCount := 0
	for _, t := range maps(session["turns"]) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if turnID != "" && t["id"] != turnID {
			continue
		}
		a, b := math.Max(num(t["start"]), lo), num(t["end"])
		if b >= a {
			turnCount++
			turns = append(turns, interval{a, b})
		}
	}
	selected := []M{}
	children := map[string][]interval{}
	for _, v := range maps(session["spans"]) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if (turnID != "" && v["turnId"] != turnID) || num(v["end"]) < lo {
			continue
		}
		s := clone(v)
		s["start"] = math.Max(num(s["start"]), lo)
		s["durationMs"] = math.Max(0, num(s["end"])-num(s["start"]))
		selected = append(selected, s)
		if str(s["parentId"]) != "" {
			key := str(s["parentId"])
			children[key] = append(children[key], interval{num(s["start"]), num(s["end"])})
		}
	}
	active := union(turns)
	groups := map[string]M{}
	for _, s := range selected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s["selfMs"] = math.Max(0, num(s["durationMs"])-union(children[str(s["id"])]))
		if services[str(s["id"])] != nil || s["category"] == "model" {
			continue
		}
		label := str(s["label"])
		c := str(s["category"])
		if c == "shell" || c == "tests" || c == "build" {
			label = str(s["input"])
		}
		key := c + ":" + label
		g := groups[key]
		if g == nil {
			g = M{"id": hash(key, 24), "label": s["label"], "category": c, "calls": 0, "totalMs": 0, "selfMs": 0, "maxMs": 0, "failures": 0, "spanIds": []string{}}
			groups[key] = g
		}
		g["calls"] = num(g["calls"]) + 1
		g["totalMs"] = num(g["totalMs"]) + num(s["durationMs"])
		g["selfMs"] = num(g["selfMs"]) + num(s["selfMs"])
		g["maxMs"] = math.Max(num(g["maxMs"]), num(s["durationMs"]))
		if s["status"] == "failed" {
			g["failures"] = num(g["failures"]) + 1
		}
		ids := g["spanIds"].([]string)
		if len(ids) < 3 {
			g["spanIds"] = append(ids, str(s["id"]))
		}
	}
	type event struct {
		at    float64
		delta int
		span  M
	}
	events := []event{}
	for _, t := range turns {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		events = append(events, event{t.a, 1, nil}, event{t.b, -1, nil})
	}
	for _, s := range selected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if services[str(s["id"])] != nil {
			continue
		}
		if num(s["end"]) > num(s["start"]) {
			events = append(events, event{num(s["start"]), 1, s}, event{num(s["end"]), -1, s})
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].at < events[j].at })
	running := map[string]M{}
	categories := map[string]float64{}
	last := 0.0
	activeTurns := 0
	for _, e := range events {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		elapsed := e.at - last
		if activeTurns > 0 && elapsed > 0 {
			ancestors := map[string]bool{}
			for _, v := range running {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				p := str(v["parentId"])
				seen := map[string]bool{}
				for p != "" && !seen[p] {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					seen[p] = true
					ancestors[p] = true
					p = str(running[p]["parentId"])
				}
			}
			peers := []M{}
			for id, v := range running {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if !ancestors[id] {
					peers = append(peers, v)
				}
			}
			if len(peers) == 0 {
				categories["unobserved"] += elapsed
			}
			for _, v := range peers {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				categories[str(v["category"])] += elapsed / float64(len(peers))
			}
		}
		if e.span == nil {
			activeTurns += e.delta
		} else if e.delta > 0 {
			running[str(e.span["id"])] = e.span
		} else {
			delete(running, str(e.span["id"]))
		}
		last = e.at
	}
	ranked := []M{}
	for _, g := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if cat != "" && g["category"] != cat {
			continue
		}
		g["meanMs"] = math.Round(num(g["totalMs"]) / num(g["calls"]))
		for _, k := range []string{"totalMs", "selfMs", "maxMs"} {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			g[k] = math.Round(num(g[k]))
		}
		ranked = append(ranked, g)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i]["selfMs"] == ranked[j]["selfMs"] {
			return str(ranked[i]["id"]) < str(ranked[j]["id"])
		}
		return num(ranked[i]["selfMs"]) > num(ranked[j]["selfMs"])
	})
	breakdown := []M{}
	for c, ms := range categories {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		percent := 0.0
		if active > 0 {
			percent = math.Round(ms/active*1000) / 10
		}
		breakdown = append(breakdown, M{"category": c, "ms": math.Round(ms), "percent": percent})
	}
	sort.Slice(breakdown, func(i, j int) bool { return num(breakdown[i]["ms"]) > num(breakdown[j]["ms"]) })
	hotspots := []M{}
	for _, s := range selected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if services[str(s["id"])] == nil && (cat == "" || s["category"] == cat) && s["category"] != "model" && s["category"] != "reasoning" && s["category"] != "response" {
			hotspots = append(hotspots, M{"id": s["id"], "label": s["label"], "category": s["category"], "durationMs": s["durationMs"], "selfMs": s["selfMs"], "status": s["status"], "turnId": s["turnId"]})
		}
	}
	sort.SliceStable(hotspots, func(i, j int) bool { return num(hotspots[i]["selfMs"]) > num(hotspots[j]["selfMs"]) })
	tokens := tokenReport(session, args)["usage"]
	if len(ranked) > top {
		ranked = ranked[:top]
	}
	if len(hotspots) > top {
		hotspots = hotspots[:top]
	}
	result := M{"id": session["id"], "title": session["title"], "status": session["status"], "model": session["model"], "asOf": iso(num(session["end"])), "lastEvent": iso(num(session["lastEvent"])), "window": M{"since": choose(args["since"]), "turnId": choose(args["turnId"]), "category": choose(args["category"])}, "activeMs": math.Round(active), "elapsedMs": math.Max(0, num(session["end"])-num(session["start"])), "betweenTurnsMs": math.Max(0, num(session["end"])-num(session["start"])-active), "spanCount": len(selected), "turnCount": turnCount, "tokens": tokens, "breakdown": breakdown, "groups": ranked, "hotspots": hotspots, "findings": []M{}, "diagnostics": session["diagnostics"], "notes": []string{"Category wall time splits overlapping peers and excludes gaps between turns. Group self time can overlap across parallel calls.", "Unobserved time is not measured model latency. Call-pair timing includes transport overhead.", "Command categories are heuristics; inspect span details to verify. Open turns are inferred live for 120s after the last event."}}
	review, err := reviewReport(ctx, session, args, result, selected, services)
	if err != nil {
		return nil, err
	}
	result["review"] = review
	result["notes"] = []string{"Likely background services are excluded from time rankings and category attribution. Inspect their classification in Review.", "Time is recorded work, not proven delay or waste. Overlapping work is not additive wall time.", "Usage windows count completed responses; cached input is included in input and reasoning in output."}
	return result, ctx.Err()

}
func spanPage(session M, args M) M {
	spans := []M{}
	for _, s := range maps(session["spans"]) {
		if str(args["category"]) != "" && s["category"] != args["category"] {
			continue
		}
		if str(args["turnId"]) != "" && s["turnId"] != args["turnId"] {
			continue
		}
		if args["parentId"] != nil && s["parentId"] != args["parentId"] {
			continue
		}
		if num(s["durationMs"]) < num(args["minMs"]) {
			continue
		}
		if args["since"] != nil && math.Max(num(s["end"]), deliveryAt(s)) < timestamp(args["since"]) {
			continue
		}
		v := clone(s)
		delete(v, "input")
		delete(v, "inputKey")
		delete(v, "output")
		spans = append(spans, v)
	}
	if args["sort"] == "duration" {
		sort.SliceStable(spans, func(i, j int) bool { return num(spans[i]["durationMs"]) > num(spans[j]["durationMs"]) })
	}
	return pageResult(spans, int(num(args["offset"])), int(num(args["limit"])), "spans")
}
