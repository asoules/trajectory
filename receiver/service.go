package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type apiError struct {
	status  int
	message string
}

func (e apiError) Error() string { return e.message }
func integer(args M, key string, fallback, max int) error {
	if args[key] == nil {
		args[key] = fallback
		return nil
	}
	n, err := strconv.ParseFloat(str(args[key]), 64)
	if err != nil || n < 0 || n != math.Trunc(n) || math.IsInf(n, 0) || math.IsNaN(n) {
		return fmt.Errorf("%s must be a non-negative integer", key)
	}
	args[key] = int(math.Min(n, float64(max)))
	return nil
}
func (s *sourceStore) query(ctx context.Context, method string, args M) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, analysisTimeout)
	defer cancel()
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if strings.HasPrefix(method, "investigation_") {
		return s.investigationQuery(ctx, method, args)
	}
	for _, key := range []string{"since", "turnId", "category"} {
		if args[key] == "" {
			delete(args, key)
		}
	}
	if args["since"] != nil && math.IsNaN(timestamp(args["since"])) {
		return nil, fmt.Errorf("since must be an ISO timestamp")
	}
	if method == "sessions" {
		if e := integer(args, "offset", 0, 1000000); e != nil {
			return nil, e
		}
		if e := integer(args, "limit", 20, 100); e != nil {
			return nil, e
		}
		return s.list(ctx, args)
	}
	switch method {
	case "review", "summary", "spans", "span", "trace", "export":
	default:
		return nil, apiError{404, "Unknown operation"}
	}
	var session M
	var err error
	if args["revision"] != nil {
		session, err = s.revision(str(args["revision"]))
	} else {
		session, err = s.get(ctx, str(args["session"]))
	}
	if err != nil {
		return nil, err
	}
	result, err := querySession(ctx, session, method, args)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if method == "trace" || method == "review" || method == "summary" {
		revision := str(args["revision"])
		if revision == "" {
			revision, err = s.remember(session)
		}
		if err != nil {
			var e apiError
			if errors.As(err, &e) && e.status == 413 {
				obj(result)["captureUnavailable"] = e.message
			} else {
				return nil, err
			}
		} else {
			obj(result)["revision"] = revision
		}
	}
	return result, ctx.Err()
}
func querySession(ctx context.Context, session M, method string, args M) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if args["since"] != nil && math.IsNaN(timestamp(args["since"])) {
		return nil, fmt.Errorf("since must be an ISO timestamp")
	}
	if method == "summary" || method == "review" {
		if e := integer(args, "top", 8, 30); e != nil {
			return nil, e
		}
		analysis, err := analyze(ctx, session, args)
		if err != nil {
			return nil, err
		}
		if method == "review" {
			return analysis["review"], nil
		}
		return analysis, nil
	}
	if method == "span" {
		if e := integer(args, "maxChars", 4000, 16000); e != nil {
			return nil, e
		}
		if e := integer(args, "offset", 0, 16000); e != nil {
			return nil, e
		}
		for _, v := range maps(session["spans"]) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if v["id"] == args["id"] {
				span := clone(v)
				delete(span, "inputKey")
				services, err := backgroundServices(ctx, session)
				if err != nil {
					return nil, err
				}
				if background := services[str(v["id"])]; background != nil {
					span["background"] = background
				}
				max, offset := int(num(args["maxChars"])), int(num(args["offset"]))
				out := []rune(str(v["output"]))
				start := int(math.Min(float64(offset), float64(len(out))))
				span["input"] = clip(v["input"], max)
				span["output"] = clip(string(out[start:]), max)
				span["outputOffset"] = offset
				span["outputTruncated"] = len(out) > offset+max
				span["retainedOutputLimit"] = 16000
				return span, nil
			}
		}
		return nil, apiError{404, "Span not found"}
	}
	if method == "export" {
		events := []M{}
		for _, v := range maps(session["spans"]) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			events = append(events, M{"name": v["label"], "cat": v["category"], "ph": "X", "ts": num(v["start"]) * 1000, "dur": num(v["durationMs"]) * 1000, "pid": 1, "tid": v["turnId"], "args": M{"id": v["id"], "parentId": v["parentId"], "status": v["status"], "timing": v["source"]}})
		}
		return M{"traceEvents": events, "displayTimeUnit": "ms"}, nil
	}
	if method == "spans" {
		for _, p := range []struct {
			k    string
			d, m int
		}{{"offset", 0, 1000000}, {"limit", 50, 200}, {"minMs", 0, 9007199254740991}} {
			if e := integer(args, p.k, p.d, p.m); e != nil {
				return nil, e
			}
		}
		return spanPage(session, args), nil
	}
	filters := M{"category": args["category"], "turnId": args["turnId"], "limit": 0}
	total := int(num(spanPage(session, filters)["total"]))
	if e := integer(args, "limit", 1000, 3000); e != nil {
		return nil, e
	}
	limit := int(num(args["limit"]))
	if e := integer(args, "offset", int(math.Max(0, float64(total-limit))), 1000000); e != nil {
		return nil, e
	}
	offset := int(num(args["offset"]))
	filters["offset"] = offset
	filters["limit"] = limit
	result := spanPage(session, filters)
	meta := clone(session)
	for _, k := range []string{"spans", "turns", "tokens", "diagnostics"} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		delete(meta, k)
	}
	result["session"] = meta
	result["turns"] = traceTurns(session)
	summary, err := analyze(ctx, session, M{"top": 12, "turnId": args["turnId"], "category": args["category"]})
	if err != nil {
		return nil, err
	}
	result["summary"] = summary
	result["offset"] = offset
	result["previousOffset"] = nil
	if offset > 0 {
		result["previousOffset"] = int(math.Max(0, float64(offset-limit)))
	}
	return result, nil
}

// Navigation metadata covers every turn, independent of the loaded span page.
func traceTurns(session M) []M {
	usage := map[string]M{}
	for _, span := range maps(session["spans"]) {
		if u := obj(span["usage"]); len(u) > 0 {
			id := str(span["turnId"])
			if usage[id] == nil {
				usage[id] = M{}
			}
			normalized := clone(u)
			if normalized["total_tokens"] == nil {
				normalized["total_tokens"] = num(u["input_tokens"]) + num(u["output_tokens"])
			}
			sumUsage(usage[id], normalized)
		}
	}
	turns := []M{}
	for _, turn := range maps(session["turns"]) {
		t := clone(turn)
		if u := usage[str(t["id"])]; u != nil {
			t["usage"] = u
		}
		turns = append(turns, t)
	}
	return turns
}
