package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Catalog discovery uses filenames and stat only. A transcript is opened only
// when selected, through the same bounded incremental reader as Codex rollouts.
func (s *sourceStore) discoverClaude(ctx context.Context, entries *[]M, size *int64) error {
	if s.claudeHome == "" {
		return nil
	}
	root := filepath.Join(s.claudeHome, "projects")
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		parts := strings.Split(rel, string(filepath.Separator))
		base := strings.TrimSuffix(d.Name(), ".jsonl")
		if base == "" || base == "agent-" {
			return nil
		}
		thread, parent, agent := base, "", ""
		switch {
		case len(parts) == 2 && uuidRE.FindString(base) == base:
			// Main conversation.
		case strings.HasPrefix(base, "agent-") && len(parts) == 2:
			// Older Claude Code versions placed sidechains beside main files.
			agent = strings.TrimPrefix(base, "agent-")
		case strings.HasPrefix(base, "agent-") && len(parts) >= 4 && parts[2] == "subagents" && uuidRE.FindString(parts[1]) == parts[1]:
			parent, agent = parts[1], strings.TrimPrefix(base, "agent-")
			thread = parent + "/" + base
		default:
			return nil
		}
		info, err := d.Info()
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		e := M{"id": hash(path, 16), "thread_id": thread, "title": thread, "path": path, "updated_at": float64(info.ModTime().UnixNano()) / 1e6, "archived": 0, "bytes": info.Size(), "source": "claude"}
		if agent != "" {
			e["agent_id"] = agent
			e["parent_id"] = parent
		}
		*size += retainedSize(e) + 32
		if *size > maxCatalogBytes {
			return apiError{413, "Task catalog exceeds the 16 MiB metadata limit"}
		}
		*entries = append(*entries, e)
		return nil
	})
}
