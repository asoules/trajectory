package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"
)

// Revisions pin the exact server data displayed by a live client. Saving a view
// consumes this revision, never a fresh read of a still-changing source file.
type viewRevision struct {
	data    []byte
	created time.Time
}
type revisionCache struct {
	mu      sync.Mutex
	entries map[string]viewRevision
}

func (s *sourceStore) remember(session M) (string, error) {
	data, err := json.Marshal(session)
	if err != nil {
		return "", err
	}
	if len(data) > 32<<20 {
		return "", apiError{413, "Session exceeds the 32 MiB snapshot limit"}
	}
	id := hash(string(data), 32)
	s.revisions.mu.Lock()
	defer s.revisions.mu.Unlock()
	if s.revisions.entries == nil {
		s.revisions.entries = map[string]viewRevision{}
	}
	s.revisions.entries[id] = viewRevision{data, time.Now()}
	total := 0
	for _, r := range s.revisions.entries {
		total += len(r.data)
	}
	for len(s.revisions.entries) > 32 || total > 64<<20 {
		old := ""
		var at time.Time
		for key, r := range s.revisions.entries {
			if key != id && (old == "" || r.created.Before(at)) {
				old = key
				at = r.created
			}
		}
		if old == "" {
			break
		}
		total -= len(s.revisions.entries[old].data)
		delete(s.revisions.entries, old)
	}
	return id, nil
}
func (s *sourceStore) revision(id string) (M, error) {
	s.revisions.mu.Lock()
	r, ok := s.revisions.entries[id]
	s.revisions.mu.Unlock()
	if !ok || time.Since(r.created) > 15*time.Minute {
		return nil, apiError{409, "This live view expired. Refresh it before saving an investigation."}
	}
	var session M
	err := json.Unmarshal(r.data, &session)
	return session, err
}
func randomID() (string, error) {
	b := make([]byte, 12)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}
func validateView(raw M, session M) (M, error) {
	v := M{"mode": "waterfall", "category": "", "turnId": "", "offset": 0, "limit": 1000, "zoom": 1, "collapsed": []string{}, "expanded": []string{}, "overviewHidden": false, "scrollLeft": 0, "scrollTop": 0, "scrollRoot": "timeline"}
	for k, x := range raw {
		if _, ok := v[k]; !ok && k != "selectedSpan" {
			return nil, fmt.Errorf("Unknown view field %s", k)
		}
		v[k] = x
	}
	if v["scrollRoot"] != "page" && v["scrollRoot"] != "timeline" {
		return nil, fmt.Errorf("Invalid scroll root")
	}
	if v["mode"] != "waterfall" && v["mode"] != "aggregate" && v["mode"] != "review" {
		return nil, fmt.Errorf("Invalid view mode")
	}
	for _, p := range []struct {
		k      string
		d, max int
	}{{"offset", 0, 1000000}, {"limit", 1000, 3000}, {"zoom", 1, 16}, {"scrollLeft", 0, 10000000}, {"scrollTop", 0, 10000000}} {
		if err := integer(v, p.k, p.d, p.max); err != nil {
			return nil, err
		}
	}
	if num(v["limit"]) < 1 || num(v["zoom"]) < 1 {
		return nil, fmt.Errorf("limit and zoom must be positive")
	}
	if _, ok := v["overviewHidden"].(bool); !ok {
		return nil, fmt.Errorf("overviewHidden must be boolean")
	}
	ids := map[string]bool{}
	for _, p := range maps(session["spans"]) {
		ids[str(p["id"])] = true
	}
	turns := map[string]bool{}
	for _, p := range maps(session["turns"]) {
		turns[str(p["id"])] = true
	}
	for _, key := range []string{"category", "turnId", "selectedSpan"} {
		if v[key] != nil {
			if _, ok := v[key].(string); !ok {
				return nil, fmt.Errorf("%s must be a string", key)
			}
		}
	}
	if !ids[str(v["selectedSpan"])] {
		return nil, fmt.Errorf("Select a span from this view before investigating")
	}
	if str(v["turnId"]) != "" && !turns[str(v["turnId"])] {
		return nil, fmt.Errorf("Unknown turn")
	}
	for _, key := range []string{"collapsed", "expanded"} {
		var values []string
		switch a := v[key].(type) {
		case []string:
			values = a
		case []any:
			for _, x := range a {
				t, ok := x.(string)
				if !ok {
					return nil, fmt.Errorf("%s must contain IDs", key)
				}
				values = append(values, t)
			}
		default:
			return nil, fmt.Errorf("%s must be an array", key)
		}
		if len(values) > 10000 {
			return nil, fmt.Errorf("Too many expanded/collapsed IDs")
		}
		valid := ids
		if key == "collapsed" {
			valid = turns
		}
		out := []string{}
		for _, id := range values {
			if valid[id] {
				out = append(out, id)
			}
		}
		v[key] = out
	}
	normalizeTimelineView(session, v)
	if str(v["turnId"]) != "" {
		for _, span := range maps(session["spans"]) {
			if span["id"] == v["selectedSpan"] && span["turnId"] != v["turnId"] {
				return nil, fmt.Errorf("Selected span must belong to the displayed turn")
			}
		}
	}
	return v, nil
}
func (s *sourceStore) createInvestigation(args M) (M, error) {
	var session M
	var err error
	if args["investigation"] != nil {
		_, session, err = s.loadInvestigation(str(args["investigation"]))
	} else {
		session, err = s.revision(str(args["revision"]))
	}
	if err != nil {
		return nil, err
	}
	view, err := validateView(obj(args["view"]), session)
	if err != nil {
		return nil, err
	}
	question := str(args["question"])
	if len([]rune(question)) > 2000 {
		return nil, fmt.Errorf("Question exceeds 2000 characters")
	}
	id, err := randomID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	data, _ := json.Marshal(session)
	if len(data) > 32<<20 {
		return nil, apiError{413, "Session exceeds the 32 MiB snapshot limit"}
	}
	state, _ := json.Marshal(view)
	_, err = s.db.run("INSERT INTO investigations(id,created_at,question,session,view) VALUES(?,?,?,?,?)", id, now, question, string(data), string(state))
	if err != nil {
		return nil, err
	}
	return M{"id": id, "createdAt": now, "asOf": iso(num(session["end"])), "url": "/?investigation=" + id, "command": "trajectory investigation view " + id, "question": question}, nil
}
func (s *sourceStore) loadInvestigation(id string) (M, M, error) {
	rows, err := s.db.run("SELECT * FROM investigations WHERE id=?", id)
	if err != nil {
		return nil, nil, err
	}
	if len(rows) == 0 {
		return nil, nil, apiError{404, "Investigation not found"}
	}
	r := rows[0]
	var session, view M
	if err = json.Unmarshal([]byte(str(r["session"])), &session); err != nil {
		return nil, nil, err
	}
	if err = json.Unmarshal([]byte(str(r["view"])), &view); err != nil {
		return nil, nil, err
	}
	return M{"id": r["id"], "createdAt": r["created_at"], "question": r["question"], "view": view, "asOf": iso(num(session["end"])), "url": "/?investigation=" + id}, session, nil
}
func viewSet(v any) map[string]bool {
	out := map[string]bool{}
	if a, ok := v.([]string); ok {
		for _, s := range a {
			out[s] = true
		}
	}
	for _, s := range arr(v) {
		out[str(s)] = true
	}
	return out
}

// This row projection is shared by the browser and the CLI saved-view renderer.
// Timeline rows are flat, chronological spans from one turn. Indentation
// retains recorded nesting without making evidence conditional on disclosures.
func visibleRows(trace M, view M) []M {
	rows := []M{}
	for _, span := range maps(trace["spans"]) {
		if span["turnId"] == view["turnId"] {
			row := clone(span)
			row["kind"] = "span"
			rows = append(rows, row)
		}
	}
	return rows
}

// Older snapshots could show many turns. Restore their selected span's turn
// without changing the captured session or the saved evidence.
func normalizeTimelineView(session M, view M) {
	if view["mode"] != "waterfall" || str(view["turnId"]) != "" {
		return
	}
	for _, span := range maps(session["spans"]) {
		if span["id"] == view["selectedSpan"] {
			view["turnId"] = span["turnId"]
			index := 0
			for _, v := range maps(session["spans"]) {
				if v["id"] == span["id"] {
					break
				}
				if v["turnId"] == span["turnId"] && (str(view["category"]) == "" || v["category"] == view["category"]) {
					index++
				}
			}
			limit := int(num(view["limit"]))
			if limit > 0 {
				view["offset"] = (index / limit) * limit
			}
			return
		}
	}
}

func revealEvidence(session M, view M, id string) error {
	var selected M
	spans := maps(session["spans"])
	byID := map[string]M{}
	for _, s := range spans {
		byID[str(s["id"])] = s
		if s["id"] == id {
			selected = s
		}
	}
	if selected == nil {
		return apiError{404, "Evidence span not in saved snapshot"}
	}
	view["selectedSpan"] = id
	view["mode"] = "waterfall"
	view["category"] = ""
	view["turnId"] = selected["turnId"]
	view["collapsed"] = []string{}
	view["scrollTop"] = 0
	view["scrollLeft"] = 0
	view["zoom"] = 1
	turnSpans := []M{}
	for _, s := range spans {
		if s["turnId"] == selected["turnId"] {
			turnSpans = append(turnSpans, s)
		}
	}
	index := 0
	for i, s := range turnSpans {
		if s["id"] == id {
			index = i
		}
	}
	limit := int(num(view["limit"]))
	view["offset"] = (index / limit) * limit
	parents := []string{}
	seen := map[string]bool{}
	for p := str(selected["parentId"]); p != "" && !seen[p]; p = str(byID[p]["parentId"]) {
		seen[p] = true
		parents = append(parents, p)
	}
	view["expanded"] = parents
	return nil
}
func (s *sourceStore) investigationQuery(ctx context.Context, method string, args M) (any, error) {
	meta, session, err := s.loadInvestigation(str(args["id"]))
	if err != nil {
		return nil, err
	}
	view := obj(meta["view"])
	normalizeTimelineView(session, view)
	switch method {
	case "investigation_spans", "investigation_span", "investigation_export", "investigation_summary", "investigation_review":
		op := "spans"
		if method == "investigation_review" {
			op = "review"
			for _, key := range []string{"turnId", "category"} {
				if args[key] == nil {
					args[key] = view[key]
				}
			}
		}
		if method == "investigation_summary" {
			op = "summary"
		}
		if method == "investigation_span" {
			op = "span"
			args["id"] = args["span"]
		}
		if method == "investigation_export" {
			op = "export"
		}
		return querySession(ctx, session, op, args)
	case "investigation_trace":
		for _, key := range []string{"category", "turnId", "offset", "limit"} {
			if args[key] == nil {
				args[key] = view[key]
			}
		}
		return querySession(ctx, session, "trace", args)
	case "investigation_view", "investigation_render":
		if str(args["span"]) != "" {
			if err := revealEvidence(session, view, str(args["span"])); err != nil {
				return nil, err
			}
		}
		raw, err := querySession(ctx, session, "trace", clone(view))
		if err != nil {
			return nil, err
		}
		trace := obj(raw)
		trace["viewRows"] = visibleRows(trace, view)
		detail, err := querySession(ctx, session, "span", M{"id": view["selectedSpan"], "maxChars": 16000})
		if err != nil {
			return nil, err
		}
		aggregate, err := analyze(ctx, session, M{"category": view["category"], "turnId": view["turnId"], "top": 30})
		if err != nil {
			return nil, err
		}
		findings, err := s.db.run("SELECT id,created_at,data FROM findings WHERE investigation_id=? ORDER BY created_at DESC LIMIT 3", str(meta["id"]))
		if err != nil {
			return nil, err
		}
		items := []M{}
		for _, r := range findings {
			var v M
			if json.Unmarshal([]byte(str(r["data"])), &v) == nil {
				v["id"] = r["id"]
				v["createdAt"] = r["created_at"]
				items = append(items, v)
			}
		}
		counts, err := s.db.run("SELECT count(*) AS n FROM findings WHERE investigation_id=?", str(meta["id"]))
		if err != nil {
			return nil, err
		}
		meta["findingCount"] = counts[0]["n"]
		meta["findingsTruncated"] = num(counts[0]["n"]) > float64(len(items))
		meta["findings"] = items
		meta["selectedSpan"] = detail
		meta["summary"] = trace["summary"]
		meta["session"] = trace["session"]
		for i, turn := range maps(trace["turns"]) {
			if turn["id"] == view["turnId"] {
				meta["turn"] = M{"id": turn["id"], "number": i + 1, "label": turn["label"]}
				break
			}
		}
		meta["groups"] = aggregate["groups"]
		if method == "investigation_render" {
			meta["trace"] = trace
			return meta, nil
		}
		if err := integer(args, "offset", 0, 1000000); err != nil {
			return nil, err
		}
		if err := integer(args, "limit", 50, 200); err != nil {
			return nil, err
		}
		rows := maps(trace["viewRows"])
		if view["mode"] == "aggregate" {
			rows = maps(aggregate["groups"])
		}
		if view["mode"] == "review" {
			rows = maps(obj(trace["summary"])["review"].(M)["time"])
		}
		meta["rows"] = pageResult(rows, int(num(args["offset"])), int(num(args["limit"])), "items")
		delete(meta, "groups")
		meta["coverage"] = M{"loadedSpans": len(maps(trace["spans"])), "matchingSpans": trace["total"], "snapshotSpans": len(maps(session["spans"]))}
		return meta, nil
	}
	return nil, apiError{404, "Unknown investigation operation"}
}
func (s *sourceStore) recordFinding(args M) (M, error) {
	id := str(args["investigation"])
	_, session, err := s.loadInvestigation(id)
	if err != nil {
		return nil, err
	}
	data := M{}
	for _, key := range []string{"title", "observation", "hypothesis", "recommendation"} {
		v, ok := args[key].(string)
		if !ok || v == "" || len([]rune(v)) > 2000 {
			return nil, fmt.Errorf("%s must be nonempty and at most 2000 characters", key)
		}
		data[key] = v
	}
	evidence := arr(args["evidence"])
	if len(evidence) == 0 || len(evidence) > 8 {
		return nil, fmt.Errorf("Provide 1–8 evidence span IDs")
	}
	byID := map[string]bool{}
	for _, s := range maps(session["spans"]) {
		byID[str(s["id"])] = true
	}
	links := []M{}
	for _, v := range evidence {
		span, ok := v.(string)
		if !ok || !byID[span] {
			return nil, fmt.Errorf("Evidence must belong to the saved snapshot")
		}
		links = append(links, M{"spanId": span, "url": "/?investigation=" + id + "&span=" + url.QueryEscape(span)})
	}
	data["evidence"] = links
	findingID, err := randomID()
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(data)
	_, err = s.db.run("INSERT INTO findings(id,investigation_id,created_at,data) VALUES(?,?,?,?)", findingID, id, time.Now().UTC().Format(time.RFC3339Nano), string(raw))
	if err != nil {
		return nil, err
	}
	data["id"] = findingID
	return data, nil
}
