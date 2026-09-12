package main

import (
	_ "embed"
	"encoding/json"
)

//go:embed testdata/demo.json
var demoJSON string

func (s *sourceStore) seedDemo() error {
	var m M
	if err := json.Unmarshal([]byte(demoJSON), &m); err != nil {
		return err
	}
	s.demo = m
	s.entries = []M{{"id": "demo", "thread_id": "demo", "title": m["title"], "updated_at": m["end"], "archived": 0, "bytes": 0, "source": "demo"}}
	return nil
}
