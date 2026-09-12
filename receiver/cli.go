package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const cliHelp = `Trajectory — local Codex performance review

trajectory serve [-home DIR] [-db FILE] [-port 4318] [-demo]
trajectory review --session THREAD_ID [--since ISO] [--details] [--json]
trajectory sessions [--q TEXT] [--limit 20]
trajectory summary --session THREAD_ID [--top 8]
trajectory spans --session THREAD_ID [--turnId ID] [--sort duration]
trajectory span --session THREAD_ID --id SPAN_ID [--maxChars 2000]
trajectory trace --session THREAD_ID
trajectory export --session THREAD_ID
trajectory investigation create --session ID --span ID [--question TEXT]
trajectory investigation view ID [--span SPAN_ID] [--json]
trajectory investigation review ID [--details] [--json]
trajectory investigation spans ID [--sort duration]
trajectory investigation span ID --span SPAN_ID [--maxChars 2000]
trajectory investigation finding ID --file finding.json
trajectory mcp [--url http://127.0.0.1:4318]

Start trajectory serve before using the query commands or the stdio MCP adapter.
No arguments (or server flags without 'serve') also start the server.
Query commands use --url to select the running server and --pretty for indented JSON.
Use --revision REVISION from a prior read to inspect the same evidence; revisions expire.
Capture with --revision REVISION --view-file view.json to preserve displayed context.
Saved review inherits filters; --turnId '' or --category '' explicitly clears one.
Use investigation create --investigation ID to capture another view of saved evidence.
`

// cliClient always uses the running server: it owns the same revisions, source
// cache, and saved investigations that the browser is displaying.
type cliClient struct {
	baseURL string
	http    *http.Client
}

func newCLIClient(base string) (*cliClient, error) {
	if base == "" {
		base = "http://127.0.0.1:4318"
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("--url must be an HTTP(S) server URL without credentials, query, or fragment")
	}
	return &cliClient{strings.TrimRight(base, "/"), &http.Client{Timeout: 30 * time.Second}}, nil
}

func (c *cliClient) query(method string, args M) (M, error) {
	params := url.Values{}
	for key, value := range args {
		if value != nil {
			params.Set(key, str(value))
		}
	}
	return c.request(http.MethodGet, method+"?"+params.Encode(), nil)
}

func (c *cliClient) post(method string, args M) (M, error) {
	data, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return c.request(http.MethodPost, method, data)
}

func (c *cliClient) request(verb, path string, data []byte) (M, error) {
	r, err := http.NewRequest(verb, c.baseURL+"/api/"+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if data != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(r)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Trajectory: %w; start 'trajectory serve' or check --url", err)
	}
	defer response.Body.Close()
	const maxResponse = 64 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxResponse {
		return nil, fmt.Errorf("server response exceeds 64 MiB")
	}
	var result M
	if err := json.Unmarshal(body, &result); err != nil || result == nil {
		return nil, fmt.Errorf("server returned invalid JSON (HTTP %d); check --url", response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s", choose(result["error"], response.Status))
	}
	return result, nil
}

func writeJSON(output io.Writer, value any, pretty bool) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	if pretty {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(value)
}

// Unlike flag.FlagSet, this accepts options after positional investigation IDs
// and distinguishes an explicitly empty filter from an omitted one.
func parseCLIArgs(argv []string) (M, []string, error) {
	booleans := map[string]bool{"json": true, "pretty": true, "details": true, "archived": true, "recent": true}
	stringsAllowed := strings.Fields("url session revision since turnId category top q source offset limit minMs sort parentId id maxChars span mode question view-file investigation file")
	allowed := map[string]bool{}
	for _, key := range stringsAllowed {
		allowed[key] = true
	}
	args, positional := M{}, []string{}
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			positional = append(positional, argv[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "--") {
			positional = append(positional, a)
			continue
		}
		key, value, hasValue := strings.Cut(a[2:], "=")
		if booleans[key] {
			if !hasValue && i+1 < len(argv) && (argv[i+1] == "true" || argv[i+1] == "false") {
				i++
				value, hasValue = argv[i], true
			}
			if !hasValue {
				value = "true"
			}
			b, err := strconv.ParseBool(value)
			if err != nil {
				return nil, nil, fmt.Errorf("--%s must be true or false", key)
			}
			args[key] = b
			continue
		}
		if !allowed[key] {
			return nil, nil, fmt.Errorf("unknown option --%s", key)
		}
		if !hasValue {
			if i+1 >= len(argv) || strings.HasPrefix(argv[i+1], "--") {
				return nil, nil, fmt.Errorf("--%s requires a value", key)
			}
			i++
			value = argv[i]
		}
		args[key] = value
	}
	return args, positional, nil
}

func runCLI(argv []string, input io.Reader, output io.Writer) error {
	command, rest := argv[0], argv[1:]
	for _, a := range rest {
		if a == "--help" || a == "-h" {
			_, err := fmt.Fprint(output, cliHelp)
			return err
		}
	}
	args, positional, err := parseCLIArgs(rest)
	if err != nil {
		return err
	}
	client, err := newCLIClient(str(args["url"]))
	if err != nil {
		return err
	}
	delete(args, "url")
	pretty, asJSON, details := args["pretty"] == true, args["json"] == true, args["details"] == true
	delete(args, "pretty")
	delete(args, "json")
	delete(args, "details")
	if command == "investigation" {
		result, err := client.investigation(positional, args)
		if err != nil {
			return err
		}
		if !asJSON && positional[0] == "view" {
			text, err := investigationText(result, details)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(output, text)
			return err
		}
		if !asJSON && positional[0] == "review" {
			return printReview(output, result, details)
		}
		return writeJSON(output, result, pretty)
	}
	if len(positional) > 0 {
		return fmt.Errorf("unexpected argument %q", positional[0])
	}
	if command == "mcp" {
		if len(args) > 0 {
			return fmt.Errorf("mcp accepts only --url")
		}
		return serveMCP(client, input, output)
	}
	switch command {
	case "review", "sessions", "summary", "spans", "span", "trace", "export":
	default:
		return fmt.Errorf("unknown command %q; use 'trajectory help'", command)
	}
	result, err := client.query(command, args)
	if err != nil {
		return err
	}
	if command == "review" && !asJSON {
		return printReview(output, result, details)
	}
	return writeJSON(output, result, pretty)
}

func readJSONObject(path string) (M, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	// The server caps these writes at 128 KiB; don't load unbounded local files.
	data, err := io.ReadAll(io.LimitReader(file, (128<<10)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 128<<10 {
		return nil, fmt.Errorf("JSON file exceeds 128 KiB")
	}
	var result M
	if err := json.Unmarshal(data, &result); err != nil || result == nil {
		return nil, fmt.Errorf("%s must contain a JSON object", path)
	}
	return result, nil
}

func (c *cliClient) investigation(positional []string, args M) (M, error) {
	if len(positional) == 0 {
		return nil, fmt.Errorf("investigation requires create, view, review, spans, span, or finding")
	}
	action := positional[0]
	if action == "create" {
		if len(positional) != 1 {
			return nil, fmt.Errorf("create accepts options, not a positional ID")
		}
		view := M{}
		if path := str(args["view-file"]); path != "" {
			var err error
			view, err = readJSONObject(path)
			if err != nil {
				return nil, err
			}
		}
		revision := args["revision"]
		if str(revision) == "" && str(args["investigation"]) == "" {
			if str(args["session"]) == "" {
				return nil, fmt.Errorf("create requires --session, --revision, or --investigation")
			}
			trace, err := c.query("trace", M{"session": args["session"], "category": args["category"], "turnId": args["turnId"], "limit": 1000})
			if err != nil {
				return nil, err
			}
			if str(trace["captureUnavailable"]) != "" {
				return nil, fmt.Errorf("%s", trace["captureUnavailable"])
			}
			revision = trace["revision"]
			defaults := M{"offset": trace["offset"], "limit": 1000, "category": str(args["category"]), "turnId": str(args["turnId"])}
			for k, v := range view {
				defaults[k] = v
			}
			view = defaults
		}
		if span, ok := args["span"]; ok {
			view["selectedSpan"] = span
		}
		if mode, ok := args["mode"]; ok {
			view["mode"] = mode
		} else if view["mode"] == nil {
			view["mode"] = "waterfall"
		}
		body := M{"question": str(args["question"]), "view": view}
		if revision != nil {
			body["revision"] = revision
		}
		if investigation, ok := args["investigation"]; ok {
			body["investigation"] = investigation
		}
		return c.post("investigations", body)
	}
	if len(positional) != 2 || positional[1] == "" {
		return nil, fmt.Errorf("%s requires one investigation ID", action)
	}
	id := positional[1]
	switch action {
	case "finding":
		if str(args["file"]) == "" {
			return nil, fmt.Errorf("finding requires --file finding.json")
		}
		finding, err := readJSONObject(str(args["file"]))
		if err != nil {
			return nil, err
		}
		finding["investigation"] = id
		return c.post("findings", finding)
	case "span":
		if str(args["span"]) == "" {
			return nil, fmt.Errorf("span requires --span SPAN_ID")
		}
	case "view", "review", "spans", "summary", "trace", "export":
	default:
		return nil, fmt.Errorf("unknown investigation command %q", action)
	}
	args["id"] = id
	return c.query("investigation_"+action, args)
}
