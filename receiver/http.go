package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"trajectory/public"
)

func application(store *sourceStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, e := net.SplitHostPort(host); e == nil {
			host = h
		}
		sendError := func(status int, message string) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(M{"error": message})
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'")
		if host != "127.0.0.1" && host != "localhost" {
			sendError(403, "Local host required")
			return
		}
		if (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+r.Host) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			sendError(403, "Same origin required")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") || r.URL.Path == "/api/telemetry" {
			sendError(404, "Telemetry collection has been removed")
			return
		}
		if r.Method == "POST" && (r.URL.Path == "/api/investigations" || r.URL.Path == "/api/findings") {
			if r.Header.Get("Content-Type") != "application/json" {
				sendError(415, "application/json required")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
			defer r.Body.Close()
			var args M
			decoder := json.NewDecoder(r.Body)
			if err := decoder.Decode(&args); err != nil || args == nil {
				sendError(400, "Invalid JSON object")
				return
			}
			var result M
			var err error
			if r.URL.Path == "/api/investigations" {
				result, err = store.createInvestigation(args)
			} else {
				result, err = store.recordFinding(args)
			}
			if err != nil {
				status := 400
				if errors.Is(err, context.DeadlineExceeded) {
					status = 504
					err = fmt.Errorf("Analysis timed out; retry to resume reading this task")
				}
				if errors.Is(err, context.Canceled) {
					status = 408
					err = fmt.Errorf("Request canceled")
				}
				var e apiError
				if errors.As(err, &e) {
					status = e.status
				}
				sendError(status, err.Error())
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			if command, ok := result["command"].(string); ok {
				// The copied command must target the database that saved this ID.
				origin := "http://" + r.Host
				result["command"] = command + " --url '" + strings.ReplaceAll(origin, "'", "'\\''") + "'"
			}
			json.NewEncoder(w).Encode(result)
			return
		}
		if r.Method != "GET" {
			sendError(405, "Read-only API")
			return
		}
		if r.URL.Path == "/healthz" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(M{"status": "ok"})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			args := M{}
			for k, v := range r.URL.Query() {
				args[k] = v[0]
			}
			result, err := store.query(r.Context(), strings.TrimPrefix(r.URL.Path, "/api/"), args)
			if err != nil {
				status := 400
				if errors.Is(err, context.DeadlineExceeded) {
					status = 504
					err = fmt.Errorf("Analysis timed out; retry to resume reading this task")
				}
				if errors.Is(err, context.Canceled) {
					status = 408
					err = fmt.Errorf("Request canceled")
				}
				var e apiError
				if errors.As(err, &e) {
					status = e.status
				}
				sendError(status, err.Error())
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(result)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if !strings.HasSuffix(path, ".js") && path != "index.html" && path != "style.css" && path != "favicon.svg" {
			sendError(404, "Not found")
			return
		}
		content, err := assets.Files.ReadFile(path)
		if err != nil {
			sendError(404, "Not found")
			return
		}
		typ := "text/javascript"
		if path == "index.html" {
			typ = "text/html"
		} else if path == "style.css" {
			typ = "text/css"
		} else if path == "favicon.svg" {
			typ = "image/svg+xml"
		}
		w.Header().Set("Content-Type", typ)
		w.Write(content)
	})
}
