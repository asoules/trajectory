# Trajectory: on-demand analysis redesign

Status: implemented and verified on September 8, 2026. The production executable has been rebuilt. Switching the running installation over remains an operator step; existing databases and Codex exporter configuration were left untouched.

## Decision

Use Codex rollout files as the source of truth, keep recently inspected tasks in a bounded memory cache, and use SQLite only for explicitly saved investigations and findings.

The current reader already parses a selected rollout lazily, remembers its byte offset, and reads appended records on demand. Keep that mechanism. Delete automatic telemetry storage and derived-data persistence instead of replacing them with a new storage engine.

Keep the Go executable, embedded frontend, existing query routes, CLI/MCP clients, parser, and review calculations where they serve this design. Breaking changes are allowed; this does not require gratuitously renaming working interfaces. Do not implement legacy database migration, compatibility routes, or dual writes.

## Required behavior

- With no reads, Trajectory does no rollout parsing, directory polling, or telemetry collection.
- Opening the task list reads metadata only. Opening a task or an agent querying one analyzes that task.
- Repeated reads reuse its parser state; appended records are read from the last complete-record offset.
- Automatic analysis produces no database writes or disk cache files.
- Retained analysis and revision data have explicit memory budgets.
- Saved investigations preserve the exact displayed evidence and remain readable without the source files.
- Existing timing, token, output-size, uncertainty, and source-provenance semantics remain correct.

Internal Codex telemetry is outside this redesign. Normal rollout reviews cannot reconstruct runtime traces that were never collected; retain that limitation in the documentation.

## What changes

### 1. Delete telemetry ingestion and automatic persistence

Remove the OTLP handler, normalization code, raw `batches` archive, telemetry-derived sessions, and receipt-stat scans. Remove `/v1/logs`, `/v1/traces`, and `/api/telemetry`; do not maintain a discard-only compatibility endpoint. Make health checks cheap and independent of telemetry.

Delete `saveSnapshot` and the persistent live `sessions` and `spans` tables. Do not replace them with a disk cache, checkpoints, summary tables, or staging tables.

The new SQLite database contains only `investigations` and `findings`. Retain the current saved-session JSON representation and transactions. There is no demonstrated need for compressed content-addressed blobs, snapshot deduplication, reference counting, or a new evidence format.

Update setup instructions to remove the Trajectory log and trace exporters from Codex configuration and restart the relevant Codex backend. This is part of switching installations over, not an automatic configuration edit. The new server will not ingest data from exporters left pointed at the old endpoints.

### 2. Keep discovery demand-driven and in memory

Reuse discovery of the session index, rollout filenames, and file metadata. Return the existing metadata entries directly instead of writing them to SQLite and reading them back. Keep the existing short discovery cooldown, applied only when a request asks for discovery. Keep existing filtering and offset pagination over the metadata list; SQL and cursor pagination are unnecessary for this design.

Catalog discovery never reads rollout bodies. Known task reads use the cached path without rediscovering the catalog. Unknown tasks trigger discovery. Preserve current explicit thread UUID and rollout-ID resolution; a thread with several rollouts resolves to the most recently modified file and returns the selected rollout ID. Clients use that ID for subsequent inspection. Child tasks remain separate.

The home page shows the catalog or an empty selection. It must not automatically analyze `latest`. Require an explicit task for analysis in the browser, CLI, and MCP. Keep the useful existing identifiers; remove the implicit fallback rather than introducing a new identity system.

Cap retained catalog metadata at 16 MiB of accounted data. If an installation exceeds that limit, report it clearly; do not silently claim the catalog is complete. A paged source catalog is a follow-up only if a real installation needs it.

### 3. Keep synchronous, request-driven parsing

On a cold read, parse the selected rollout in the request. Capture its current file length first and stop at that watermark; continuous appends cannot make the request run forever. Leave an incomplete final record for a later request.

On a warm read, stat the source and read only appended complete records. On replacement, truncation, or detected same-size modification, discard its cached state and rebuild. Reuse the existing file-identity checks; do not add record offsets, field selectors, or per-record hashes for lazy detail access.

Keep bounded input/output excerpts in parser memory, as today. This makes inspection and saved evidence independent of subsequent source changes and avoids a second source-reading path. Preserve original text lengths and full-input fingerprints before truncation; do not retain encoded media in the normalized state.

Fix `materialize` so deriving presentation never mutates cached parser facts. In particular, displaying an open operation must not permanently close it or corrupt subsequent updates. Derive open durations using one `asOf` per response.

Keep parsing serialized initially. Make waiting for the parse lock and record processing observe the request context. A second request for the same task uses the updated cache when it gets the lock; it does not parse the same prefix again. No worker pool, background task, shared-job registry, interest token, lease, or polling endpoint.

Use the existing 30-second client request window, with a 25-second server analysis deadline and a server write timeout long enough to return the resulting error. Cancel parsing when the request is canceled or the deadline expires. Retain only fully applied parser progress so a retry can continue; never publish a partial review as complete. Check cancellation during analysis as well as reading. Keep ordinary loading/error UI; add no progress protocol.

### 4. Bound the existing memory caches

Replace the current arbitrary eviction of one of eight tasks with least-recently-used eviction charged by retained data size. Start with named constants, not new user-facing configuration:

| Retained data | Limit |
| --- | --- |
| Task catalog | 16 MiB |
| Parsed task cache | 128 MiB total; 32 MiB per task |
| Frozen revision cache | Existing 64 MiB total, 32 entries, 15-minute expiry |
| Saved snapshot | Existing 32 MiB per snapshot |
| One source record buffer | 16 MiB |

These are budgets for accounted retained data, not a guarantee about total process RSS. Include strings, collection entries, and conservative structural overhead in parser accounting, and evict before admitting more data. Avoid serializing the entire parser state on each record to measure it. Account for materialization copies and temporary decode buffers separately when measuring peak memory.

Enforce the record limit while reading, before allocating an unbounded line. A record or task that cannot fit returns a clear resource-limit error; do not skip it and return apparently complete results. No spill-to-disk fallback or custom streaming JSON parser in this change. Measure actual large rollouts to determine whether these starting limits are usable before release.

A cache eviction or server restart means parsing again on the next read. This is an intentional tradeoff: straightforward restart behavior and no disk-cache maintenance. Only add persistent checkpoints if measured cold reads demonstrate that this tradeoff is unacceptable.

### 5. Reuse frozen revisions and saved investigations

Keep the existing bounded, serialized revision cache. It already solves the need to save the exact frame shown to the user without database row versioning. Return a revision from successful analysis reads that need follow-up inspection, including agent reviews, and let secondary panels and agent detail queries reuse it.

A frozen revision includes its `asOf`, normalized session, bounded text excerpts, and existing uncertainty metadata. Reading it does not refresh the source. A viewer refresh creates or reuses an appropriate frozen revision; unchanged serialized content can reuse the existing hash. Budget pressure may evict a revision before its time limit; return the existing explicit expired-view error, never silently substitute newer evidence.

Retain existing capture limits: a task that can be reviewed but exceeds the snapshot limit may report saving unavailable. Saving uses the exact cached revision in a transaction. It does not reread source records or reconstruct an earlier frame. Keep findings linked to that saved snapshot.

Saved reads never discover or parse rollouts. Saved data grows only through explicit saves. Duplicate saves may duplicate evidence; optimizing that is not required to prevent automatic growth.

### 6. Keep the existing clients and layout

Keep the current review, summary, spans, detail, export, and investigation routes, adjusting their internals and requiring an explicit task when no revision is supplied. Update frontend, CLI, and MCP together where contracts actually change; do not add an analysis/job/view route hierarchy.

Reuse the browser's four-second refresh while the selected task is live and visible. Hidden, paused, closed, and saved views make no refresh requests. An agent read runs once and returns. Keep fixed-revision inspection consistent across panels, including after source changes.

The original cutover used `.trajectory/investigations.sqlite` to avoid opening the old telemetry database. Distribution preparation subsequently moved the default to the platform's user-data directory; see README.md for current paths and how to keep using a previous file with `-db`. Startup rejects an incompatible explicitly supplied database with a clear error; it never opens it as a legacy store or silently migrates it.

Demo mode remains generated in-memory data and uses a separate investigation file when needed. Preserve loopback restrictions, same-origin checks, and request body limits.

## Implementation order

1. **Remove automatic storage.** Remove telemetry and `saveSnapshot`; convert catalog discovery to memory; initialize only the investigation tables in the new default file. Update affected health/setup behavior and tests. Gate: live reads and incoming requests create no persisted analysis or telemetry.
2. **Make the existing reader bounded and correct.** Add byte-accounted eviction, bounded record reads, context cancellation, a fixed source watermark, and pure materialization. Keep serialized access. Gate: cold, warm, appended, canceled, evicted, and restarted reads produce correct reviews within the stated limits.
3. **Finish client and evidence behavior.** Explicit task selection, fixed revisions for follow-up inspection, empty home state, existing visible-only polling, and clear resource-limit errors. Gate: browser, CLI, and MCP agree on a fixture revision; saved evidence works after source deletion and restart.
4. **Cut over and measure.** Update README, API docs, and repository skill; remove obsolete telemetry fixtures and references. Run tests and benchmark representative real and synthetic rollouts. Document exporter removal and stopping the old receiver before explicitly deleting the old database and WAL/SHM files. Do not delete user data as part of implementation.

No old schema reader or migration is required. Reusing current routes, IDs, and parser code is a simplicity choice, not a commitment to backward compatibility.

## Focused verification

- An idle server performs no recurring source work. Browsing the catalog opens no rollout bodies; reading task A does not parse task B.
- Repeated queries create no disk analysis data. SQLite grows only when an investigation or finding is explicitly saved.
- Warm reads reuse parser state; appends consume only new records; incomplete lines resume correctly.
- Materializing an open turn repeatedly does not mutate parser state. Late completions, overlapping spans, deduplicated usage, delivered-text lengths, and background classification preserve existing semantics.
- Concurrent requests serialize without duplicate prefix parsing. Canceled/deadline-expired work stops and leaves resumable complete-record state, never a published incomplete review.
- Cache pressure evicts old tasks. Oversized tasks/records fail clearly. Compare peak RSS with retained-data accounting; do not label the latter a hard process-memory limit.
- Replacing/truncating a source invalidates its parser cache. Frozen revisions remain unchanged, and expired revisions fail explicitly.
- Saved investigations survive cache eviction, source deletion, and restart. Their text, timing horizon, and findings remain consistent.
- Hidden/paused viewers and one-shot agent reads leave no ongoing work. Browser, CLI, and MCP report the same review for the same revision and filters.

Run existing Go and Node tests, frontend syntax checks, and the production build. Add representative benchmarks for cold parse, unchanged read, appended read, and peak memory. Measure actual large tasks before deciding whether asynchronous jobs, lazy details, parser checkpoints, more concurrency, or per-turn summary caching are needed. None is part of this implementation without that evidence.

## Implementation results

The implementation removes OTLP ingestion and automatic SQLite analysis storage, reuses the incremental rollout parser with bounded in-memory caches, and requires explicit task selection. Browser navigation and agent follow-up queries can reuse a frozen revision; saved investigations remain self-contained. The duplicated JavaScript backend was removed, its useful parser regressions were moved to Go, and browser/CLI/MCP integration tests now exercise the production Go executable.

Verification passed: production build, Go tests with the race detector, `go vet ./...`, all 27 Node tests, frontend syntax checks, and `git diff --check`. Coverage includes metadata-only discovery, no automatic database writes, partial records, cancellation and resumption, replacement/truncation, cache eviction and limits, frozen navigation, hidden-view cancellation, saved evidence after source deletion, and rejection of legacy databases without modifying their main file or WAL.

Single-iteration measurements on Apple M4 (source sizes use decimal MB):

| Rollout | Cold read | Unchanged warm read | Accounted retained parser data |
| --- | --- | --- | --- |
| Synthetic, 0.477 MB / 2,000 operations | 33.8 ms | 22.2 ms | 4.43 MB |
| Historical, 22.6 MB | 367 ms | 45.8 ms | 5.81 MB |
| Historical, 129.5 MB | 1.12 s | 3.39 ms | 0.63 MB |

The synthetic appended read took 22.5 ms. A separate isolated cold-read measurement of the 129.5 MB source took 1.14 seconds and reached 47.3 MB peak RSS. Its cumulative allocations were 587 MB, which is allocation volume across the read, not simultaneously retained memory. Source content affects these results; the retained-data limits do not constitute a process RSS cap. These samples support retaining synchronous reads without adding background jobs or persistent checkpoints.

## Removed from the previous plan

Persistent analysis cache and checkpoints; versioned database rows; source-generation identity redesign; lazy detail references and hashes; build jobs and interest leases; worker and writer queues; new route hierarchy; disk-budget accounting and vacuum maintenance for automatic data; snapshot compression/deduplication; new storage dashboards; diagnostic capture and filtering; broad typed-module refactoring.

These mechanisms addressed consequences of keeping a persistent analysis system or introduced capabilities beyond the demonstrated request. Deleting automatic storage and reusing the existing lazy reader removes the need for them.
