package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

type M = map[string]any

func obj(v any) M {
	m, _ := v.(map[string]any)
	if m == nil {
		return M{}
	}
	return m
}
func arr(v any) []any { a, _ := v.([]any); return a }
func str(v any) string {
	if v == nil {
		return ""
	}
	s, ok := v.(string)
	if ok {
		return s
	}
	return fmt.Sprint(v)
}
func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case json.Number:
		n, _ := x.Float64()
		return n
	}
	n, _ := strconv.ParseFloat(str(v), 64)
	return n
}
func clone(m M) M {
	n := M{}
	for k, v := range m {
		n[k] = v
	}
	return n
}
func choose(v ...any) any {
	for _, x := range v {
		if x != nil && x != "" {
			return x
		}
	}
	return nil
}
func clip(v any, n int) string {
	var s string
	if x, ok := v.(string); ok {
		s = x
	} else if v != nil {
		b, _ := json.Marshal(v)
		s = string(b)
	}
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return strings.Clone(s)
}
func timestamp(v any) float64 {
	if _, ok := v.(float64); ok {
		return num(v)
	}
	t, e := time.Parse(time.RFC3339Nano, str(v))
	if e != nil {
		return math.NaN()
	}
	return float64(t.UnixNano()) / 1e6
}
func turnTimestamp(v any, fallback float64) float64 {
	n := timestamp(v)
	if math.IsNaN(n) || n == 0 {
		return fallback
	}
	if _, ok := v.(float64); ok && n < 1e11 {
		return n * 1000
	}
	return n
}
func hash(s string, n int) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s)))[:n] }
func iso(n float64) string        { return time.UnixMilli(int64(n)).UTC().Format("2006-01-02T15:04:05.000Z") }
func maps(v any) []M {
	if a, ok := v.([]M); ok {
		return a
	}
	out := []M{}
	for _, x := range arr(v) {
		out = append(out, obj(x))
	}
	return out
}
func ordered(m map[string]M) []M {
	a := []M{}
	for _, v := range m {
		a = append(a, clone(v))
	}
	sort.SliceStable(a, func(i, j int) bool {
		if num(a[i]["start"]) == num(a[j]["start"]) {
			return str(a[i]["id"]) < str(a[j]["id"])
		}
		return num(a[i]["start"]) < num(a[j]["start"])
	})
	return a
}
