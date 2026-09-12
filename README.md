# Trajectory

A local performance review for Codex tasks. Humans and CLI agents see the same
ranked costs, uncertainty, and evidence. Start with **Review**, then investigate an
operation in the timeline or save a snapshot for an agent.

## Install

Trajectory publishes one self-contained executable for macOS and Linux on Intel
and ARM. The installer downloads the matching archive, verifies its SHA-256
checksum, and places `trajectory` in `~/.local/bin` without `sudo`:

```sh
curl -fsSL https://raw.githubusercontent.com/asoules/trajectory/main/install.sh | sh
```

If that directory is not already on your shell path, add it:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Set `TRAJECTORY_INSTALL_DIR` to choose another destination, or
`TRAJECTORY_VERSION` to install a particular release:

```sh
TRAJECTORY_INSTALL_DIR=/usr/local/bin sh install.sh
TRAJECTORY_VERSION=v0.1.0 sh install.sh
```

The release binaries require macOS 13 or newer; Linux binaries are built natively
on Ubuntu 22.04. `trajectory version` reports the release, source commit, and build
date. Homebrew Core does not currently carry Trajectory; see
[Releasing](docs/releasing.md) for the first-party tap plan.

### Build from source

```sh
sh scripts/build.sh  # Go 1.22+ and a C compiler
mkdir -p "$HOME/.local/bin"
install -m 0755 bin/trajectory "$HOME/.local/bin/trajectory"
```

One Go executable includes the server, CLI, stdio MCP adapter, frontend, and
SQLite. Running any of those commands needs no Node, system SQLite, collector,
or container. Node is used only for frontend development and tests (22.13+ within
22.x, or 24+). Build with `GO=/path/to/go sh scripts/build.sh` if Go is not on PATH.
Put `bin/trajectory` on your PATH to use `trajectory` from any directory, or invoke
its absolute path. `trajectory help` lists the commands. Existing invocations
without a subcommand, such as `trajectory -port 4321`, still start the server.

## Run

```sh
trajectory serve  # http://127.0.0.1:4318
trajectory serve -demo -port 4321
```

The default source is `$CODEX_HOME` or `~/.codex`. Saved investigations use a stable
user-data directory, independent of the working directory:

- macOS: `~/Library/Application Support/trajectory/investigations.sqlite`
- Linux: `$XDG_DATA_HOME/trajectory/investigations.sqlite`, or
  `~/.local/share/trajectory/investigations.sqlite` when unset or not absolute
- Windows (not yet verified in CI): `%LOCALAPPDATA%\trajectory\investigations.sqlite`,
  falling back to `%USERPROFILE%\AppData\Local\trajectory\investigations.sqlite`

Override with `-home`, `-db`, and `-port`. Relative `-db` paths remain relative to
the working directory. Demo mode uses generated data and defaults to
`demo-investigations.sqlite` in the same user-data directory. An explicit `-db`
always takes precedence, including in demo mode. Startup logs the selected path.

If you have saved evidence in the previous `.trajectory/investigations.sqlite`,
continue using it with `./bin/trajectory -db /absolute/path/to/.trajectory/investigations.sqlite`.
Files are not moved automatically. To move an existing database, stop the server
and back up the database and any `-wal` / `-shm` companions together before moving
them to the new directory; keep the original backup until the saved evidence is
verified. The old telemetry database uses a different schema (see below).

The backend never modifies the source session files. SQLite holds only saved
investigations and findings. Task discovery and parsing happen on demand; ordinary
reads keep bounded data in memory and write nothing to disk. Restarting the server
or evicting a task means its next read parses the rollout again.

## Review a task

**Review** is the default view. Its three totals have distinct meanings: time
inside turns, uncached input, and output tokens. Cached input and reasoning are
shown as subsets/context, not added again. Time without a recorded foreground
span is visible beside the total and above the time ranking.

- **Time to investigate:** up to three substantial operations, ranked by summed
  self time. The threshold is the larger of five seconds and 0.5% of turn time.
  Repeated commands, failed work, and long operations have different explanations.
  A recorded cost is a reason to investigate, not proof of waste.
- **Token drivers:** up to three turns ranked by uncached input plus output
  volume. Expand a turn for context size, cached input, and links to its largest
  responses and returned tool result. This ranking does not estimate money.
- **Tools and skills:** up to five recorded foreground operation types, ranked
  by summed self time with call, failure, and evidence counts. Orchestration
  wrappers and likely background services are excluded, with the latter count
  labeled as inferred. Codex does not record skill activation as a distinct
  event, so the review reports skill coverage as unavailable instead of
  inferring usage; repeated calls are likewise not labeled as retries.
- **Change evidence:** up to five paths named by native `FileChange` records,
  including move destinations, with their recorded change types, non-completed
  and failed-attempt counts, and operation evidence. Shell commands and scripts
  can mutate other files, and the session does not establish a final filesystem
  or Git diff. Up to three tests or builds recorded after a completed native
  change are shown as sequence evidence, not proof of relevance or coverage.
  Those limits remain visible in every client.
- **Large tool results:** text supplied in tool-result messages, measured before
  viewer truncation. Encoded images/audio and raw process stdout are not counted
  as delivered text. Character counts are not model token counts.
- **Background services:** inferred from narrowly recognized service startup and
  concurrent work; these lifetimes are excluded from time rankings and category
  attribution. The raw duration, exit status, and classification evidence remain
  inspectable. A long duration alone does not classify a process as background.

**Timeline** shows one turn at a time. The left navigator lists requests, elapsed
time, and recorded token usage; click a turn or use ↑/↓ while the list is focused.
It opens on the latest turn and keeps your selection during refresh. Nested spans
are always visible through indentation, with timing relative to the turn start.
Select a span for the right-hand inspector, or the request heading to read the
recorded request. Zoom and paging stay within that turn. The page is the only vertical scroll
area; the timeline can still scroll horizontally. **Operations** groups
recorded work by self time. Hide totals to give the timeline more room.

The home page lists sessions without parsing their contents. Select a task to
start analysis. The Sessions drawer opens with **⌘K / Ctrl+K**. Pin it if useful. It never
polls; only the selected task refreshes, every four seconds while visible.
Inspection pauses refresh. Refresh updates mounted rows without discarding focus,
scroll, or open disclosures. Internal OTel traces are not collected.

## Agent CLI

Run `trajectory serve` to read session files, calculate performance data, save
investigations, and serve the browser UI and HTTP API. Query commands in the same
executable connect to that running server; they share its live revisions and saved
evidence rather than opening a separate database or parsing another copy. Query
commands do not start a server automatically.

With `trajectory` on PATH and the server running:

```sh
trajectory review --session THREAD_ID
trajectory review --session THREAD_ID --details
trajectory review --session THREAD_ID --json
trajectory review --session THREAD_ID --since 2026-09-06T16:00:00Z
trajectory spans --session THREAD_ID --turnId TURN_ID --limit 10
trajectory span --session THREAD_ID --id SPAN_ID --maxChars 2000
trajectory sessions --q trajectory --limit 10
```

The server produces one review presentation model for the browser and CLI: the
same ranked items, formatted totals, labels, caveats, actions, and evidence links.
Neither client rebuilds the ranking or independently chooses the review wording.
Default text is compact; `--details` expands rationale and related evidence.
`--json` returns structured data. Use `--url` to select a different backend.
An explicit `--session` is required unless `--revision` is supplied. It accepts a
rollout ID or a Codex thread UUID. There is no implicit `latest`. When a thread
has multiple rollout files, its UUID resolves to the most recently updated file;
use the rollout ID for a particular historical file. Child-agent tasks are not
silently combined with their parent.

Review, summary, and trace responses include a `revision` for fixed follow-up
reads. For example, use `span --revision REVISION --id SPAN_ID` to inspect the same
evidence after the task has advanced. Revisions live in a bounded memory cache
for up to 15 minutes and may expire earlier under pressure. Save an investigation
for durable evidence.

For periodic checks, use the previous review's `asOf` as `--since`. Time spans
crossing that boundary contribute only their subsequent time. Usage counts
responses completed after the boundary; returned output uses its delivery time,
which can be later than command completion. Late-arriving source records may
revise prior observations. A review is not an exactly-once accounting ledger.
Report only a new evidence-backed opportunity; investigate before changing the
workflow. Scheduling and agent execution belong to the host agent.

The `summary`, `spans`, `span`, and `export` JSON commands remain
available. `trajectory mcp` exposes read-only stdio tools for sessions, review,
summary, spans, and span. Configure your MCP host to launch the executable with
`mcp` (and `--url` if needed); keep `trajectory serve` running. MCP review returns
the same structured report and presentation as the browser. Saved-investigation
workflows use the CLI.

The browser’s CLI handoff includes its displayed revision, filters, and server
address. Revisions expire; use **Save an investigation** for a durable handoff.
After upgrading from the Node CLI, replace `node src/cli.js …` with `trajectory …`
in scripts and MCP configuration, and restart the server to load the shared
presentation contract. Node is no longer needed for agent access.

## Install the agent skill

From the Trajectory checkout on macOS or Linux, link the bundled skill into your
Codex skills directory:

```sh
mkdir -p "${CODEX_HOME:-$HOME/.codex}/skills"
ln -s "$PWD/skills/trajectory" "${CODEX_HOME:-$HOME/.codex}/skills/"
```

The command leaves an existing installation untouched and fails if the destination
already exists. The skill follows the link back to this checkout and works when
reviewing other projects. Keep the checkout available. If you copy the skill
instead of linking it, put `trajectory` on your agent’s PATH or configure
`TRAJECTORY_BIN` to the absolute executable path. A configured `TRAJECTORY_ROOT`
checkout remains a fallback for finding `bin/trajectory`. The skill uses the Go
executable and a running server; it does not configure telemetry exporters.

## Save an investigation

Select an operation, choose **Investigate this operation**, and add a question.
Saving preserves the displayed source revision, selected span, filters, expansion,
and scroll state in SQLite. **Copy agent command** supplies the handoff.

```sh
trajectory investigation view INVESTIGATION_ID
trajectory investigation review INVESTIGATION_ID --details
trajectory investigation spans INVESTIGATION_ID --sort duration --limit 10
trajectory investigation span INVESTIGATION_ID --span SPAN_ID --maxChars 2000
trajectory investigation finding INVESTIGATION_ID --file finding.json
```

A finding contains `title`, `observation`, `hypothesis`, `recommendation`, and
`evidence` (one to eight span IDs). Evidence links reopen the frozen data and
reveal the exact operation even after the live task advances. Saved Review uses
its saved filters; pass `--turnId ''` or `--category ''` to clear one explicitly.

Agents can also capture evidence:

```sh
trajectory investigation create --session THREAD_ID --span SPAN_ID --mode review --question 'What explains this cost?'
```

Saved evidence remains available after restart. Live revisions expire after 15
minutes, so stale captures require a refresh; they never silently substitute newer
data. A session above 32 MiB still renders but cannot be captured. Saved findings
show the latest three entries, with omission counts in the API.

## Sources and measurement

Rollout events provide turns, execution records, tool messages, and response
usage. OTel provides supplementary runtime traces and logs when configured. The
sources remain separate until reliable correlation can establish that merging
will not double-count work. Raw OTel sessions can be discovered explicitly with
`trajectory sessions --source otlp` and opened by their IDs.

- Active time is the union of turn intervals. Gaps between turns are excluded.
  Category attribution shares overlap among foreground leaf spans; self time
  subtracts child intervals. Parallel work can make summed self time exceed wall
  time. Neither is a causal critical-path or recoverable-time measurement.
- Command categories and background-service classifications are inferred.
  Transport call-pair timing includes overhead; explicit execution timing is used
  where available. An unfinished task stops accumulating time after its last
  event becomes stale. Silent execution and abandonment cannot always be separated.
- Response usage is deduplicated by response ID. Usage evidence is a completion
  point with zero duration, not an invented model-request latency. Historical
  records containing only session token totals cannot support turn attribution.
- The inspector retains at most 16,000 characters per input/output field. Textual
  tool-result size is measured before this truncation. Source logs themselves may
  already be truncated. Images can incur usage but their token cost is not inferred
  from their encoded size.
- Raw rollouts stay in their original files. Normalized operation details and
  short task/turn labels are cached in memory. Malformed and incomplete source
  records are reported. Session discovery and parsing happen on demand.

## Storage and limits

Trajectory does no background collection. The task catalog uses at most 16 MiB
of accounted metadata. The parsed-task cache evicts least recently used tasks at
128 MiB total, with a 32 MiB per-task limit. Frozen revisions retain at most
64 MiB / 32 entries; a saved snapshot is limited to 32 MiB. These are retained-data
budgets, not a hard process-memory limit: parsing and rendering also allocate
temporary memory. Source records larger than 16 MiB fail explicitly.

Reads have a 25-second analysis deadline. A canceled or timed-out read retains
its fully parsed prefix, so a retry can resume. A resource-limit failure never
returns an apparently complete review. Large historical tasks may require a
retry or exceed the per-task limit; this version does not spill analysis to disk.

## Switching from the telemetry receiver

This version removes OTLP reception and the old database schema. Stop the old
Trajectory process, remove the Trajectory `exporter` and `trace_exporter` entries
from Codex's `[otel]` configuration, and restart the Codex backend. Preserve any
other exporters you intentionally use. The new receiver returns 404 for the old
telemetry endpoints.

Start the new binary normally. It uses the user-data directory described above and
does not open or migrate `.trajectory/trajectory.sqlite`. An incompatible file
passed via `-db` is rejected. Existing saved investigations in the old database
are not imported. Keep that file if you need its old data. To reclaim its space,
explicitly remove the old database and its `-wal` / `-shm` companions only after
the old process has stopped. This upgrade never deletes them automatically.

The HTTP service binds only to loopback and rejects cross-origin browser reads
and writes. Session details can contain private content. No data is forwarded
to a remote service.

## Development

```sh
npm ci
npm test           # builds and tests the actual Go backend plus JS clients
npm run check
go test ./...
go vet ./...
npm run build
```

Go owns rollout parsing, analysis, the shared review presentation, CLI commands,
and MCP. `public/` contains the browser UI. `test/helpers/` contains HTTP and
process helpers used only by the JavaScript test suite. Tests cover attribution, snapshot persistence,
filter parity, output delivery, navigation races, and stable rendering. Browser and agent integration tests launch the actual Go executable against
temporary data. They need permission to bind localhost ports. Assets are embedded,
so rebuild the binary after frontend edits.

See [API.md](API.md) for contracts and [receiver/SQLITE.md](receiver/SQLITE.md) for
the bundled SQLite provenance.

See [CONTRIBUTING.md](CONTRIBUTING.md) for development and compatibility expectations,
and [SECURITY.md](SECURITY.md) for private reporting and safe diagnostic sharing.

## License

Trajectory is available under the [MIT License](LICENSE). Bundled SQLite is
public domain; see [receiver/SQLITE.md](receiver/SQLITE.md) for its provenance.
