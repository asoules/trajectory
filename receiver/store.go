package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	maxCatalogBytes = 16 << 20
	maxCacheBytes   = 128 << 20
	maxTaskBytes    = 32 << 20
	maxRecordBytes  = 16 << 20
	analysisTimeout = 25 * time.Second
)

var uuidRE = regexp.MustCompile(`[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}`)

type sourceStore struct {
	revisions revisionCache
	demo      M
	gate      chan struct{}
	db        *database
	home      string
	scanned   time.Time
	entries   []M
	cache     map[string]*cachedRollout
}

type cachedRollout struct {
	state  *parseState
	offset int64
	info   os.FileInfo
	used   time.Time
}

func newStore(db *database, home string) *sourceStore {
	return &sourceStore{db: db, home: home, gate: make(chan struct{}, 1), cache: map[string]*cachedRollout{}}
}

// All catalog/parser access is serialized by query's cancellable gate. These
// helpers never start work of their own or persist derived data.
func (s *sourceStore) discover(ctx context.Context) error {
	if s.demo != nil || time.Since(s.scanned) < 5*time.Second {
		return nil
	}
	names := map[string]string{}
	var namesBytes int64
	if f, e := os.Open(filepath.Join(s.home, "session_index.jsonl")); e == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				return err
			}
			var r M
			if json.Unmarshal(scanner.Bytes(), &r) == nil {
				id, title := str(r["id"]), str(r["thread_name"])
				if _, ok := names[id]; !ok {
					namesBytes += int64(len(id)) + 96
				}
				namesBytes += int64(len(title) - len(names[id]))
				if namesBytes > maxCatalogBytes {
					return apiError{413, "Task catalog exceeds the 16 MiB metadata limit"}
				}
				names[id] = title
			}
		}
		if err := scanner.Err(); err != nil {
			return err
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	entries := []M{}
	var size int64
	for _, folder := range []string{"sessions", "archived_sessions"} {
		err := filepath.WalkDir(filepath.Join(s.home, folder), func(path string, d os.DirEntry, err error) error {
			if e := ctx.Err(); e != nil {
				return e
			}
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			info, err := d.Info()
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			thread := uuidRE.FindString(filepath.Base(path))
			title := str(choose(names[thread], thread, filepath.Base(path)))
			archived := 0
			if folder == "archived_sessions" {
				archived = 1
			}
			e := M{"id": hash(path, 16), "thread_id": thread, "title": title, "path": path, "updated_at": float64(info.ModTime().UnixNano()) / 1e6, "archived": archived, "bytes": info.Size(), "source": "rollout"}
			size += retainedSize(e) + 32
			if size > maxCatalogBytes {
				return apiError{413, "Task catalog exceeds the 16 MiB metadata limit"}
			}
			entries = append(entries, e)
			return nil
		})
		if err != nil {
			return err
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i]["updated_at"] == entries[j]["updated_at"] {
			return str(entries[i]["id"]) < str(entries[j]["id"])
		}
		return num(entries[i]["updated_at"]) > num(entries[j]["updated_at"])
	})
	present := map[string]bool{}
	for _, e := range entries {
		present[str(e["id"])] = true
	}
	for id := range s.cache {
		if !present[id] {
			delete(s.cache, id)
		}
	}
	s.entries, s.scanned = entries, time.Now()
	return nil
}

func (s *sourceStore) list(ctx context.Context, args M) (M, error) {
	if err := s.discover(ctx); err != nil {
		return nil, err
	}
	selected := []M{}
	now := float64(time.Now().UnixMilli())
	for _, e := range s.entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		source := str(args["source"])
		if source != "" && source != "all" && source != str(e["source"]) {
			continue
		}
		arch, recent := num(e["archived"]) != 0, now-num(e["updated_at"]) < 120000
		if args["archived"] != nil && arch != (str(args["archived"]) == "true") {
			continue
		}
		if str(args["recent"]) == "true" && !recent {
			continue
		}
		if !strings.Contains(strings.ToLower(str(e["title"])+" "+str(e["thread_id"])), strings.ToLower(str(args["q"]))) {
			continue
		}
		selected = append(selected, M{"id": e["id"], "threadId": e["thread_id"], "title": e["title"], "updatedAt": e["updated_at"], "archived": arch, "recent": recent, "bytes": e["bytes"], "source": e["source"]})
	}
	return pageResult(selected, int(num(args["offset"])), int(num(args["limit"])), "sessions"), nil
}

func (s *sourceStore) entry(id string) M {
	// Exact rollout IDs take precedence over thread aliases. Entries are newest first.
	for _, e := range s.entries {
		if e["id"] == id {
			return e
		}
	}
	for _, e := range s.entries {
		if e["thread_id"] == id {
			return e
		}
	}
	return nil
}

// Reserve a full task allowance before decoding more records. This avoids
// per-record scans of other tasks and bounds retained state even during append.
func (s *sourceStore) reserve(id string, allowance int64) {
	var total int64
	for key, c := range s.cache {
		if key != id {
			total += c.state.bytes
		}
	}
	for total+allowance > maxCacheBytes {
		oldest := ""
		var used time.Time
		for key, c := range s.cache {
			if key != id && (oldest == "" || c.used.Before(used)) {
				oldest, used = key, c.used
			}
		}
		if oldest == "" {
			break
		}
		total -= s.cache[oldest].state.bytes
		delete(s.cache, oldest)
	}
}

func (s *sourceStore) get(ctx context.Context, id string) (M, error) {
	if id == "" || id == "latest" {
		return nil, apiError{400, "Select a task or provide --session TASK_ID"}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entry := s.entry(id)
	if entry == nil || entry["id"] != id {
		if err := s.discover(ctx); err != nil {
			return nil, err
		}
		entry = s.entry(id)
	}
	if entry == nil {
		return nil, apiError{404, "Session not found"}
	}
	if s.demo != nil {
		return s.demo, nil
	}
	path, key := str(entry["path"]), str(entry["id"])
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	cached := s.cache[key]
	if cached == nil || info.Size() < cached.info.Size() || !os.SameFile(info, cached.info) || (info.Size() == cached.info.Size() && !info.ModTime().Equal(cached.info.ModTime())) {
		s.reserve(key, maxTaskBytes)
		cached = &cachedRollout{state: newState(), info: info}
		s.cache[key] = cached
	}
	cached.used = time.Now()
	if info.Size() > cached.offset {
		s.reserve(key, maxTaskBytes)
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		opened, err := f.Stat()
		if err != nil {
			return nil, err
		}
		if !os.SameFile(info, opened) || opened.Size() < info.Size() {
			delete(s.cache, key)
			return nil, apiError{409, "Source changed while opening it; retry this task"}
		}
		if _, err := f.Seek(cached.offset, io.SeekStart); err != nil {
			return nil, err
		}
		reader := bufio.NewReader(io.LimitReader(f, info.Size()-cached.offset))
		// Record the observed watermark even when canceled between records so
		// the next request can distinguish a normal resume from a file edit.
		cached.info = info
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			line, e := readRecord(ctx, reader)
			if e == io.EOF {
				break
			}
			if e != nil {
				return nil, e
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if len(bytes.TrimSpace(line)) != 0 {
				var r M
				if json.Unmarshal(line, &r) != nil {
					cached.state.Malformed++
				} else {
					cached.state.ingest(r)
					if cached.state.bytes > maxTaskBytes {
						delete(s.cache, key)
						return nil, apiError{413, "Task exceeds the 32 MiB parsed-data limit"}
					}
				}
			}
			cached.offset += int64(len(line))
		}
		after, err := f.Stat()
		if err != nil {
			return nil, err
		}
		current, err := os.Stat(path)
		if err != nil || !os.SameFile(info, current) {
			delete(s.cache, key)
			return nil, apiError{409, "Source changed while reading it; retry this task"}
		}
		if after.Size() < info.Size() || (after.Size() == info.Size() && !after.ModTime().Equal(info.ModTime())) {
			delete(s.cache, key)
			return nil, apiError{409, "Source changed while reading it; retry this task"}
		}
	}
	cached.info = info
	session, err := cached.state.materialize(ctx, float64(time.Now().UnixMilli()))
	if err != nil {
		return nil, err
	}
	session["threadId"] = choose(session["id"], entry["thread_id"])
	session["id"], session["archived"], session["source"] = key, num(entry["archived"]) != 0, "rollout"
	if entry["title"] != entry["thread_id"] {
		session["title"] = entry["title"]
	} else {
		session["title"] = choose(session["title"], entry["title"])
	}
	diag := obj(session["diagnostics"])
	diag["pendingBytes"], diag["bytesRead"] = info.Size()-cached.offset, cached.offset
	return session, nil
}

func readRecord(ctx context.Context, reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) > maxRecordBytes {
			return nil, apiError{413, "Source record exceeds the 16 MiB limit"}
		}
		if len(line)+len(part) > cap(line) {
			capacity := cap(line) * 2
			if capacity < len(line)+len(part) {
				capacity = len(line) + len(part)
			}
			if capacity > maxRecordBytes {
				capacity = maxRecordBytes
			}
			grown := make([]byte, len(line), capacity)
			copy(grown, line)
			line = grown
		}
		line = append(line, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return line, err
	}
}

func pageResult(rows []M, offset, limit int, key string) M {
	n := len(rows)
	start := int(math.Min(float64(offset), float64(n)))
	end := int(math.Min(float64(start+limit), float64(n)))
	var next any
	if offset+limit < n {
		next = offset + limit
	}
	return M{"total": n, "nextOffset": next, key: rows[start:end]}
}
func sortSpans(spans []M) {
	sort.SliceStable(spans, func(i, j int) bool { return num(spans[i]["start"]) < num(spans[j]["start"]) })
}
