import test from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { review } from "../public/review.js";
import { backend } from "./helpers/backend.js";
import { cli } from "./helpers/cli.js";

test("review reports observed tool usage without inventing skill activity", async (t) => {
  const server = await backend(t, { twoTurns: true });
  const report = await server.query("review", { session: server.session });
  const tools = report.tools;
  assert.equal(tools.coverage.tools, "observed");
  assert.equal(tools.coverage.skills, "unavailable");
  assert.equal(tools.coverage.retries, "unavailable");
  assert.equal(report.changes.coverage.subsequentChecks, "unavailable");
  assert.match(tools.coverage.note, /skill activation.*not recorded/i);
  assert.deepEqual(tools.groups, [
    {
      name: "CommandExecution",
      calls: 2,
      failures: 0,
      recordedMs: 2000,
      evidence: ["test-1", "child-2"],
    },
  ]);
  const section = report.presentation.sections.find((s) => s.id === "tools");
  assert.ok(section);
  assert.match(section.note, /Skills unavailable/i);
  assert.match(section.note, /not classified as retries/i);
  assert.deepEqual(
    section.items[0].evidence.map((e) => e.spanId),
    ["test-1", "child-2"],
  );
});

test("review reports native file-change evidence without claiming a final diff", async (t) => {
  const server = await backend(t, { twoTurns: true, changes: true });
  const report = await server.query("review", { session: server.session });
  assert.equal(report.changes.coverage.fileChanges, "observed");
  assert.equal(report.changes.coverage.shellChanges, "unavailable");
  assert.equal(report.changes.coverage.gitChanges, "unavailable");
  assert.match(report.changes.coverage.note, /shell commands or scripts/i);
  assert.equal(report.changes.coverage.subsequentChecks, "observed");
  assert.deepEqual(report.changes.files, [
    {
      path: "/repo/a.txt",
      operations: 2,
      failures: 0,
      notCompleted: 0,
      types: ["add", "update"],
      typeCount: 2,
      evidence: ["edit-1", "edit-2"],
    },
    {
      path: "/repo/b.txt",
      operations: 1,
      failures: 0,
      notCompleted: 0,
      types: ["update"],
      typeCount: 1,
      evidence: ["edit-1"],
    },
    {
      path: "/repo/new.txt",
      operations: 1,
      failures: 0,
      notCompleted: 0,
      types: ["update"],
      typeCount: 1,
      evidence: ["edit-1"],
    },
    {
      path: "/repo/old.txt",
      operations: 1,
      failures: 0,
      notCompleted: 0,
      types: ["update"],
      typeCount: 1,
      evidence: ["edit-1"],
    },
  ]);
  const followUps = report.changes.followUps.map(({ id, ...item }) => item);
  assert.equal(new Set(report.changes.followUps.map((item) => item.id)).size, 2);
  assert.deepEqual(followUps, [
    {
      category: "tests",
      label: "go test ./...",
      calls: 2,
      failures: 0,
      recordedMs: 2000,
      evidence: ["verify-1", "verify-2"],
      changeEvidence: ["edit-1", "edit-2"],
    },
    {
      category: "tests",
      label: "npm test",
      calls: 1,
      failures: 0,
      recordedMs: 1000,
      evidence: ["child-2"],
      changeEvidence: ["edit-1"],
    },
  ]);
  const section = report.presentation.sections.find((s) => s.id === "changes");
  assert.ok(section);
  assert.match(section.note, /not a final filesystem or Git diff/i);
  assert.match(section.items[0].metric, /recorded operations/);
  assert.deepEqual(
    section.items[0].evidence.map((e) => e.spanId),
    ["edit-1", "edit-2"],
  );
  const followUpItem = section.items.find((item) =>
    /Test recorded after a change/i.test(item.title),
  );
  assert.ok(followUpItem);
  assert.match(followUpItem.reason, /sequence only/i);
  assert.deepEqual(
    followUpItem.evidence.map((e) => e.spanId),
    ["verify-1", "verify-2", "edit-1", "edit-2"],
  );
  const detail = await server.query("span", {
    revision: report.revision,
    id: "edit-1",
  });
  assert.deepEqual(detail.changes, [
    { path: "/repo/a.txt", type: "add" },
    { path: "/repo/b.txt", type: "update" },
    {
      path: "/repo/old.txt",
      type: "update",
      movePath: "/repo/new.txt",
    },
  ]);
  assert.match(detail.input, /add \/repo\/a\.txt/);
  assert.match(detail.input, /\/repo\/old\.txt -> \/repo\/new\.txt/);
  assert.doesNotMatch(JSON.stringify(detail), /first|second|not retained/);

  const testsOnly = await server.query("review", {
    session: server.session,
    category: "tests",
  });
  assert.deepEqual(testsOnly.changes.files, []);
  assert.equal(testsOnly.changes.coverage.subsequentChecks, "observed");
  assert.deepEqual(
    testsOnly.changes.followUps.map((item) => item.category),
    ["tests", "tests"],
  );
  const editsOnly = await server.query("review", {
    session: server.session,
    category: "edit",
  });
  assert.equal(editsOnly.changes.files.length, 4);
  assert.deepEqual(editsOnly.changes.followUps, []);
  assert.equal(editsOnly.changes.coverage.subsequentChecks, "not observed");
});

test("browser and Go CLI render identical server-ranked facts, caveats, and evidence", async (t) => {
  const server = await backend(t);
  const report = await server.query("review", { session: server.session });
  const result = await cli([
    "review",
    "--revision",
    report.revision,
    "--details",
    "--url",
    server.client.baseURL,
  ]);
  assert.equal(result.code, 0, result.stderr);
  const doc = new JSDOM(review({ data: { summary: { review: report } } }))
    .window.document;
  assert.ok(
    report.presentation.sections[0].items.length,
    "fixture must exercise material operations",
  );
  for (const section of report.presentation.sections) {
    const panel = doc.querySelector(`[aria-label="${section.title}"]`);
    assert.ok(panel);
    for (const value of [
      section.note,
      ...(section.items.length ? [] : [section.empty]),
    ]) {
      assert.ok(panel.textContent.includes(value), value);
      assert.ok(result.stdout.includes(value), value);
    }
    for (const [index, item] of section.items.entries()) {
      const row = panel.querySelectorAll(".opportunities > li")[index];
      for (const value of [
        item.title,
        item.metric,
        item.detail,
        item.reason,
        item.action,
      ]) {
        assert.ok(row.textContent.includes(value), value);
        assert.ok(result.stdout.includes(value), value);
      }
      assert.deepEqual(
        [...row.querySelectorAll("[data-detail]")].map((b) => b.dataset.detail),
        item.evidence.map((e) => e.spanId),
      );
      for (const e of item.evidence)
        assert.ok(result.stdout.includes(`Evidence: ${e.spanId}`));
    }
  }
  const json = await cli([
    "review",
    "--revision",
    report.revision,
    "--json",
    "--url",
    server.client.baseURL,
  ]);
  assert.equal(json.code, 0, json.stderr);
  assert.deepEqual(JSON.parse(json.stdout), report);
  assert.doesNotMatch(result.stdout, /save 40|wasted 40/i);
  assert.match(result.stdout, /Only session totals/);
});

test("shared presentation strings are escaped and not recomputed in the browser", async (t) => {
  const server = await backend(t);
  const report = await server.query("review", { session: server.session });
  const item = report.presentation.sections[0].items[0];
  item.title = '<script>alert("unsafe")</script>';
  item.metric = "server-formatted metric";
  item.turnId = "turn-from-server";
  const doc = new JSDOM(review({ data: { summary: { review: report } } }))
    .window.document;
  assert.equal(doc.querySelector("script"), null);
  assert.ok(doc.body.textContent.includes(item.title));
  assert.ok(doc.body.textContent.includes(item.metric));
  assert.ok(doc.querySelector('[data-review-turn="turn-from-server"]'));
});

test("missing response usage stays unknown in browser and Go CLI", async (t) => {
  const server = await backend(t, { twoTurns: true });
  const report = await server.query("review", { session: server.session });
  const result = await cli([
    "review",
    "--revision",
    report.revision,
    "--url",
    server.client.baseURL,
  ]);
  assert.equal(result.code, 0, result.stderr);
  assert.match(result.stdout, /Tokens: not recorded/);
  assert.match(result.stdout, /No material foreground/);
  const doc = new JSDOM(review({ data: { summary: { review: report } } }))
    .window.document;
  assert.match(doc.body.textContent, /No completed response usage/);
});
