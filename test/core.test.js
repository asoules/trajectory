import test from "node:test";
import assert from "node:assert/strict";
import { request } from "node:http";
import { backend } from "./helpers/backend.js";
import { cli } from "./helpers/cli.js";
import { query, post } from "./helpers/client.js";

test("compiled CLI version identifies its source revision", async () => {
  const result = await cli(["version"]);
  assert.equal(result.code, 0, result.stderr);
  assert.match(
    result.stdout,
    /^trajectory dev \(commit [0-9a-f]{12}(?:-dirty)?, built \d{4}-\d{2}-\d{2}T[^)]+\)\n$/,
  );
  assert.equal(result.stderr, "");
});

test("production pages, frozen revisions, and saved evidence share one backend", async (t) => {
  const { client } = await backend(t);
  await assert.rejects(() => query(client, "review", {}), /Select a task/);
  const report = await query(client, "review", { session: "demo" });
  assert.equal(report.activeMs, 412000);
  assert.ok(report.revision);
  const page = await query(client, "spans", {
    revision: report.revision,
    category: "tests",
    limit: 1,
  });
  assert.equal(page.total, 3);
  assert.equal(page.nextOffset, 1);
  assert.equal(page.spans[0].output, undefined);
  const detail = await query(client, "span", {
    revision: report.revision,
    id: "test-1",
    maxChars: 10,
  });
  assert.equal(detail.output.length, 10);
  assert.equal(detail.outputTruncated, true);
  const saved = await post(client, "investigations", {
    revision: report.revision,
    question: "Why repeat?",
    view: { mode: "review", selectedSpan: "test-1" },
  });
  const savedReport = await query(client, "investigation_review", {
    id: saved.id,
  });
  const { revision, ...withoutRevision } = report;
  assert.deepEqual(savedReport, withoutRevision);
  const trace = await query(client, "export", { revision });
  assert.equal(trace.traceEvents.length, 13);
});

test("CLI and MCP read the same fixed revision and reject missing tasks", async (t) => {
  const { client } = await backend(t);
  const summary = await query(client, "summary", { session: "demo", top: 2 });
  const requests = [
    { id: 1, method: "initialize", params: { protocolVersion: "2025-11-25" } },
    { method: "notifications/initialized" },
    { id: 2, method: "tools/list" },
    {
      id: 3,
      method: "tools/call",
      params: {
        name: "trajectory_summary",
        arguments: { revision: summary.revision, top: 2 },
      },
    },
    {
      id: 4,
      method: "tools/call",
      params: { name: "trajectory_summary", arguments: {} },
    },
    {
      id: 5,
      method: "tools/call",
      params: {
        name: "trajectory_review",
        arguments: { revision: summary.revision },
      },
    },
  ];
  const mcp = await cli(["mcp", "--url", client.baseURL], {
    input:
      requests.map((r) => JSON.stringify({ jsonrpc: "2.0", ...r })).join("\n") +
      "\n",
  });
  assert.equal(mcp.code, 0, mcp.stderr);
  assert.equal(mcp.stderr, "");
  const replies = mcp.stdout.trim().split("\n").map(JSON.parse);
  assert.equal(replies.length, 5, "notifications produce no reply");
  assert.equal(replies[0].result.protocolVersion, "2025-11-25");
  assert.deepEqual(
    replies[1].result.tools.map((t) => t.name),
    [
      "trajectory_sessions",
      "trajectory_review",
      "trajectory_summary",
      "trajectory_spans",
      "trajectory_span",
    ],
  );
  assert.deepEqual(replies[2].result.structuredContent, summary);
  assert.equal(replies[3].result.isError, true);
  assert.deepEqual(
    replies[4].result.structuredContent,
    await query(client, "review", { revision: summary.revision }),
  );
  const result = await cli([
    "summary",
    "--revision",
    summary.revision,
    "--top",
    "2",
    "--url",
    client.baseURL,
  ]);
  assert.equal(result.code, 0, result.stderr);
  assert.deepEqual(JSON.parse(result.stdout), summary);
});

test("production HTTP rejects telemetry, cross-origin requests, and rebinding", async (t) => {
  const { client } = await backend(t);
  for (const path of ["/v1/logs", "/v1/traces", "/api/telemetry"]) {
    const response = await fetch(client.baseURL + path, {
      method: "POST",
      body: "{}",
    });
    assert.equal(response.status, 404);
  }
  for (const headers of [
    { host: "evil.example:4318" },
    { origin: "https://evil.example" },
    { "sec-fetch-site": "cross-site" },
  ]) {
    const status = await new Promise((resolve, reject) => {
      const r = request(
        client.baseURL + "/api/summary?session=demo",
        { headers },
        (res) => {
          res.resume();
          resolve(res.statusCode);
        },
      );
      r.on("error", reject);
      r.end();
    });
    assert.equal(status, 403, JSON.stringify(headers));
  }
  assert.equal(
    (await fetch(client.baseURL + "/api/summary", { method: "POST" })).status,
    405,
  );
  assert.deepEqual(await (await fetch(client.baseURL + "/healthz")).json(), {
    status: "ok",
  });
});
