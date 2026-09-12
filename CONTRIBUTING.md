# Contributing

Trajectory is an early-stage local performance explorer for Codex sessions.
Small, focused bug fixes and improvements are welcome. For a substantial change,
open an issue describing the problem and proposed behavior before building it.

## Development

Use Go 1.22+ with cgo enabled and a C compiler, plus Node 22.13+ within 22.x or
Node 24+ for frontend tests and development. End users need only the compiled
Go executable, including for CLI and MCP access. On macOS, the Xcode Command Line Tools provide the compiler; on Linux,
install your distribution's C development toolchain. SQLite is bundled.

```sh
npm ci
npm run check
go test ./...
go vet ./...
npm test
```

`npm test` builds the real backend and runs JavaScript client and UI tests. Those
tests launch temporary servers and need permission to bind loopback ports.
Run `npm run build` after changing embedded frontend assets. Use
`./bin/trajectory serve -demo` for generated sample data without reading your sessions.

CI covers macOS and Linux, including the minimum Go and Node versions on Linux.
Windows builds and behavior are not yet verified. Codex rollout formats may
change; there is no guaranteed compatibility range across all Codex versions.
When reporting parser problems, include the Codex version and a minimal,
sanitized example of the relevant event shape.

## Pull requests

Describe the concrete problem, resulting behavior, and checks you ran. Add a
regression test for changed parsing, attribution, persistence, or API behavior.
Preserve browser/CLI parity: review presentation belongs in Go; both clients
consume the same server-provided values, scope, and evidence. Test CLI and MCP
changes through the compiled executable.
Keep synthetic fixtures small; avoid committing actual session logs. Update
README.md or API.md when a user-facing behavior or contract changes.

Format Go changes with `gofmt`; follow nearby JavaScript style. Changes to bundled
SQLite should follow [receiver/SQLITE.md](receiver/SQLITE.md).

For bugs, include reproduction steps, expected and actual behavior, OS, relevant
tool versions, and the Trajectory commit. Remove private paths, prompts, command
output, and credentials. Follow [SECURITY.md](SECURITY.md) for suspected
vulnerabilities or sensitive diagnostic data.
