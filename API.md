# Trajectory API

One Go process owns on-demand rollout parsing, review calculations, and saved
investigations. The browser and Go CLI/MCP commands share its query contract. CLI and MCP query
the running `trajectory serve` process, preserving its revisions and database.
Assets are embedded in Go. Source paths and source formats are not accepted from
the browser.

All routes bind to loopback. Browser APIs require the same origin. There is no
telemetry ingestion. Ordinary reads populate memory caches only; SQLite is used
only for explicitly saved investigations and findings.

| Route                | Contract                                                                                                                        |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| `GET /api/sessions`  | `q`, `source`, `archived`, `recent`, `offset`, `limit` → sessions, total, nextOffset                                            |
| `GET /api/trace`     | `session`, `turnId`, `category`, `offset`, `limit` → session, turns, spans, summary, paging                                     |
| `GET /api/review`    | `session`, `since`, `turnId`, `category` → bounded time, token, tool, change, output, and background evidence                      |
| `GET /api/summary`   | `session`, `since`, `turnId`, `category`, `top` → timing totals, breakdown, groups, hotspots, findings                          |
| `GET /api/spans`     | `session`, `turnId`, `parentId`, `category`, `since`, `minMs`, `sort`, `offset`, `limit` → metadata only                        |
| `GET /api/span`      | `session`, `id`, `maxChars`, `offset` → one bounded detail                                                                      |
| `GET /api/export`    | `session` → Chrome trace-event JSON                                                                                             |
| `GET /healthz`       | cheap process health                                                                                      |

Timestamps are Unix milliseconds; durations are milliseconds. IDs are opaque.
Empty collections are arrays; absent paging offsets are null. API errors contain
`error`. Missing task selection returns 400, an expired revision returns 409,
resource limits return 413, and an analysis deadline returns 504. A canceled
request stops work at the next cooperative check.

A source adapter produces normalized session/turn/span objects. Every span has
`id`, `turnId`, `name`, `label`, `category`, `start`, `end`, `durationMs`, `status`,
`source`, `parentId`, and `depth`. Operation details are bounded to 16,000 Unicode
characters each. Native `FileChange` spans can also contain a sorted `changes`
list with `path`, `type`, and optional `movePath`; file contents and diffs are
not retained in that list. API list responses omit inputs and outputs.

Task analysis requires an explicit `session` (thread UUID or rollout ID), or a
`revision` from an earlier read. There is no `latest` fallback. Thread UUIDs
resolve to their most recently modified rollout; clients pin the returned rollout
ID for follow-up reads. Child tasks remain separate. `sessions` reads only index,
filename, and file metadata and never triggers analysis.

A selected rollout is parsed synchronously through the byte length observed at
request start. Complete-record offsets and parser state are cached in memory;
appends are read on the next request. Replacement, truncation, and detected
same-size modification invalidate that parser cache. Reading a known rollout
does not rediscover the catalog. A restart or eviction requires reparsing.

The catalog is limited to 16 MiB of accounted metadata, parsed tasks to 128 MiB
total / 32 MiB each, and individual source records to 16 MiB. Retained data is
accounted conservatively; temporary allocations and total process RSS are not
covered by those numbers. Parsing and analysis observe a 25-second deadline.
Canceled reads retain only fully applied records so a retry can resume. No
resource-limit error produces a partial successful review.

SQLite contains only `investigations` and `findings` and uses WAL with synchronous
FULL. The default file is `investigations.sqlite` in the platform's user-data
directory (see [README.md](README.md#run)); `-db` overrides it. Old databases are
rejected rather than migrated. `/v1/logs`, `/v1/traces`, and `/api/telemetry` have
been removed. Remove old exporter configuration when upgrading.

## Investigation snapshots

`GET /api/trace`, `/api/review`, and `/api/summary` include a short-lived `revision` for the exact session data used
by that response. Follow-up reads can pass this revision to keep all panels on
one frame. Revisions expire after at most 15 minutes (earlier under cache pressure) and are held in a bounded 64 MiB / 32
entry cache. A source session above 32 MiB still renders, but reports
`captureUnavailable`; it cannot be captured in this first slice.

`POST /api/investigations` accepts `revision`, `question`, and `view` (mode,
category, turnId, offset, limit, zoom, collapsed turn IDs, expanded span IDs,
selectedSpan, overviewHidden, scrollLeft, scrollTop, scrollRoot). Modes are `review`,
`waterfall`, and `aggregate`. Timeline captures show one turn with all loaded
spans visible; legacy collapsed/expanded fields are accepted but no longer hide
rows. Older multi-turn snapshots open on the selected evidence’s turn. A selected
span must belong to the scoped turn. It preserves the complete
normalized source session and presentation state in SQLite. New views use
`scrollRoot: "page"`; legacy views default to `"timeline"` when restoring scroll. An expired revision
returns 409; it never substitutes fresh source data. A new saved view of an
existing snapshot may use `investigation` instead of `revision`.

Saved-view routes use `id` for the investigation ID:

- `GET /api/investigation_view`: the CLI projection, including the shared visible
  rows, exact overview, selected detail and coverage. Rows default to 50, max 200;
  offset/nextOffset page the flat span rows or operations table.
- `GET /api/investigation_render`: browser projection, including the same visible
  row model plus loaded span metadata. `span` reveals a cited span from the frozen
  source, clearing conflicting filters and expanding its ancestors.
- `GET /api/investigation_spans` and `/api/investigation_span`: bounded exploration
  of the frozen source, using the normal span query options; the latter uses
  `span` for the span ID.
- `GET /api/investigation_review`: the same review as the saved browser view,
  including its saved turn/category filters unless explicitly overridden.
- `GET /api/investigation_trace`, `/api/investigation_summary`, and
  `/api/investigation_export`: navigation and export from the frozen source.
  Explicit empty category/turnId values clear saved filters.
- `POST /api/findings`: `investigation`, `title`, `observation`, `hypothesis`,
  `recommendation`, and `evidence` (1–8 span IDs in that snapshot). Text fields
  are limited to 2000 characters. Returns restorable evidence links.

Both saved-view renderers show the latest three findings, with findingCount and
findingsTruncated indicating omitted older records. The initial slice does not
execute an agent, modify the workflow, or start periodic monitoring. The handoff
is a CLI reference and prompt. Evidence URLs are local and require this backend
and its database. Capturing evidence is a write; querying saved evidence is read
only. POST requests require application/json and the existing host/origin checks.

## Review semantics

Every review includes `presentation` (version 1), generated by the server for both
browser and CLI. Its `totals`, `timeAccounting`, and ordered `sections` supply
formatted values, labels, notes, empty-state explanations, and item evidence.
Each section item carries `id`, `title`, `metric`, `detail`, `reason`, `action`,
`evidence` (`spanId` and label), and an optional `turnId`. Supporting caveats are
`largeOutputsNote` and `backgroundNote`. Raw measurements remain alongside this
model. Clients render it directly rather than recomputing rankings or wording.
Saved reviews regenerate the same model from their frozen source and saved scope.

The Go text CLI requires this presentation contract; if connected to an older
server it requests a rebuild/restart instead of silently inventing a different
review. JSON output remains the HTTP response object.

`summary.review`, `GET /api/review`, and the saved-review route use the same
analysis. The review returns at most three time candidates, three token turns,
five normalized tool groups, five native file paths, three subsequent test/build
groups, three large delivered results, and five background-service examples.
Count fields indicate additional records. Each candidate contains stable evidence
span IDs. No prompt, input, or output bodies are included in a review.

Time candidates must exceed `max(5000 ms, activeMs * .005)` and are sorted by
summed self time, with stable IDs breaking ties. Likely background services are
excluded from both operation rankings and category attribution. Classification
requires narrowly recognized startup plus independent work completing during the
lifetime; it remains an inference. Tool families and records with unknown inputs
are not described as identical repetition. Full input fingerprints distinguish
long commands whose retained excerpts share a prefix.

`tools.groups` ranks normalized recorded foreground operation names by summed
self time and reports calls, failures, and up to three evidence spans.
Orchestration wrappers are excluded to avoid counting both a wrapper and its
recorded child operations. Likely background-service operations are also excluded
using the existing inferred classification; `backgroundServiceOperations` reports
how many were omitted in the selected window.
Repeated calls are not classified as retries, so `tools.coverage.retries` is
`unavailable`. Codex rollouts do not record skill activation as a distinct event,
so `tools.coverage.skills` is also `unavailable`.

`changes.files` groups paths from native `FileChange` records and reports their
recorded change types, non-completed and failed-attempt counts, and evidence.
Native move destinations are included; file contents and diffs are not retained.
Non-completed file-change records do not anchor `changes.followUps`, which reports
test or build operations whose recorded start follows the end of an earlier completed native file change. This
establishes sequence, not relevance, coverage, or successful verification.
`changes.coverage` states that shell/script mutations and a final filesystem or
Git diff are unavailable; absence from this list is not evidence that a file was
unchanged.

Tool and change operations must end strictly after `since`; boundary events are
not repeated in adjacent incremental reviews. A category filter limits visible
file rows and test/build follow-ups to that category. Test/build categories may
still cite an earlier completed native change in the same time/turn window as the
sequence anchor. `groupCount`, `fileCount`, and `followUpCount` are calculated
before result truncation and the shared presentation reports shown/total counts.

`tokens.usage` separates input, cached input (a subset), output, and reasoning
(a subset of output). `tokens.turns` rank uncached input plus output volume.
`source` distinguishes response records, session totals only, and unavailable
usage. Token evidence uses zero-duration `category=model`, `source=usage` points
with a `usage` object; these do not invent request latency. Session totals alone
are never assigned to a filtered turn or incremental window.

`deliveredChars` counts textual tool-result message content, omitting encoded
images/audio. `deliveredAt` is the delivery timestamp, which can differ from an
execution span's `end`. Incremental output review uses `deliveredAt`, response
usage uses completion timestamps, and time uses clipped execution intervals.
Raw stdout size (`outputChars`) is retained separately and does not imply that
all of it entered model context. Character counts are never converted to tokens.

Trace turn metadata always covers the full session, even when spans are filtered
or paged. Each turn includes a short `label`, up to 4,000 characters of recorded
`request` when available, and `usage` from completed response records. Missing
usage remains unavailable. Timeline navigation does not fetch the session list.
