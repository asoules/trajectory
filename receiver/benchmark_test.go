package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func benchmarkRead(b *testing.B, mode, path string) {
	home := b.TempDir()
	if path == "" {
		data := metaRecord(testThread)
		for i := 0; i < 2000; i++ {
			raw, _ := json.Marshal(M{"type": "event_msg", "timestamp": iso(float64(1000 + i*100)), "payload": M{"type": "item_completed", "started_at_ms": float64(1000 + i*100), "completed_at_ms": float64(1050 + i*100), "item": M{"type": "CommandExecution", "id": fmt.Sprint(i), "command": "npm test", "exit_code": 0, "stdout": "ok"}}})
			data += string(raw) + "\n"
		}
		path = rollout(b, home, "benchmark", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	s := testStore(b, home)
	id := hash(path, 16)
	s.entries = []M{{"id": id, "thread_id": testThread, "path": path, "title": "Benchmark", "source": "rollout"}}
	run := func() {
		if _, err := s.query(context.Background(), "review", M{"session": id}); err != nil {
			b.Fatal(err)
		}
	}
	if mode != "cold" {
		run()
	}
	b.ReportAllocs()
	if mode == "cold" {
		b.SetBytes(info.Size())
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if mode == "cold" {
			s.cache = map[string]*cachedRollout{}
			s.revisions = revisionCache{}
		}
		if mode == "append" {
			appendRollout(b, path, metaRecord(testThread))
		}
		run()
	}
	b.StopTimer()
	b.ReportMetric(float64(s.cache[id].state.bytes), "retained-B")
}
func BenchmarkRollout(b *testing.B) {
	for _, mode := range []string{"cold", "warm", "append"} {
		b.Run(mode, func(b *testing.B) { benchmarkRead(b, mode, "") })
	}
}
func BenchmarkLocalRollout(b *testing.B) {
	path := os.Getenv("TRAJECTORY_BENCH_ROLLOUT")
	if path == "" {
		b.Skip("set TRAJECTORY_BENCH_ROLLOUT to benchmark a local source read-only")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		b.Fatal(err)
	}
	for _, mode := range []string{"cold", "warm"} {
		b.Run(mode, func(b *testing.B) { benchmarkRead(b, mode, path) })
	}
}
