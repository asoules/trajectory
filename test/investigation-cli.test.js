import test from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { waterfall } from "../public/waterfall.js";
import {
  captureView,
  reviewCommand,
  agentCommand,
} from "../public/investigation.js";
import { backend } from "./helpers/backend.js";
import { cli } from "./helpers/cli.js";

test("Go CLI captures the displayed revision and preserves the browser's saved view", async (t) => {
  const server = await backend(t, { twoTurns: true });
  const directory = await mkdtemp(join(tmpdir(), "trajectory-cli-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const trace = await server.query("trace", {
    session: server.session,
    turnId: "turn-1",
  });
  const view = {
    mode: "waterfall",
    category: "",
    turnId: "turn-1",
    offset: 0,
    limit: 1000,
    zoom: 2,
    collapsed: [],
    expanded: ["parent-1"],
    selectedSpan: "test-1",
    scrollRoot: "page",
    scrollTop: 420,
    scrollLeft: 125,
  };
  const file = join(directory, "view.json");
  await writeFile(file, JSON.stringify(view));
  const run = (args) =>
    cli(["investigation", ...args, "--url", server.client.baseURL], {
      cwd: directory,
      env: { ...process.env, PATH: "" },
    });
  const created = await run([
    "create",
    "--revision",
    trace.revision,
    "--view-file",
    file,
    "--question",
    "Why this cost?",
  ]);
  assert.equal(created.code, 0, created.stderr);
  const saved = JSON.parse(created.stdout);
  assert.match(saved.command, /^trajectory investigation view /);
  assert.ok(saved.command.includes(`--url '${server.client.baseURL}'`));
  const json = await run(["view", saved.id, "--json"]);
  assert.equal(json.code, 0, json.stderr);
  const record = JSON.parse(json.stdout);
  for (const [key, value] of Object.entries(view))
    assert.deepEqual(record.view[key], value, key);
  const rendered = await server.query("investigation_render", { id: saved.id });
  assert.deepEqual(record.rows.items, rendered.trace.viewRows);
  assert.deepEqual(record.selectedSpan, rendered.selectedSpan);
  assert.deepEqual(record.summary, rendered.summary);
  const revealed = await run(["view", saved.id, "--span", "child-2", "--json"]);
  assert.equal(revealed.code, 0, revealed.stderr);
  const browserEvidence = await server.query("investigation_render", {
    id: saved.id,
    span: "child-2",
  });
  assert.deepEqual(JSON.parse(revealed.stdout).view, browserEvidence.view);
  assert.equal(JSON.parse(revealed.stdout).selectedSpan.id, "child-2");
  const text = await run(["view", saved.id]);
  assert.equal(text.code, 0, text.stderr);
  const html = waterfall({
    data: rendered.trace,
    investigation: rendered,
    view: "waterfall",
    turn: "turn-1",
    category: "",
    collapsed: new Set(),
    expandedSpans: new Set(view.expanded),
    zoom: 2,
    selectedSpan: "test-1",
  });
  for (const label of ["Turn 1", "npm test", "1.0s"]) {
    assert.ok(text.stdout.includes(label), label);
    assert.ok(html.includes(label), label);
  }
  const finding = join(directory, "finding.json");
  await writeFile(
    finding,
    JSON.stringify({
      title: "Measured cost",
      observation: "A test ran",
      hypothesis: "Expected work",
      recommendation: "Inspect inputs",
      evidence: ["test-1"],
    }),
  );
  const recorded = await run(["finding", saved.id, "--file", finding]);
  assert.equal(recorded.code, 0, recorded.stderr);
  const reread = await run(["view", saved.id]);
  assert.match(reread.stdout, /FINDING Measured cost/);
  const detail = await run([
    "span",
    saved.id,
    "--span",
    "test-1",
    "--maxChars",
    "2",
  ]);
  assert.equal(detail.code, 0, detail.stderr);
  assert.equal(JSON.parse(detail.stdout).output.length, 2);
  const expired = await run([
    "create",
    "--revision",
    "expired",
    "--view-file",
    file,
  ]);
  assert.notEqual(expired.code, 0);
  assert.match(expired.stderr, /expired/);
  assert.equal(expired.stdout, "");
});

test("Go CLI preserves omitted saved filters and explicitly clears empty filters", async (t) => {
  const server = await backend(t, { twoTurns: true });
  const run = (args) =>
    cli(["investigation", ...args, "--url", server.client.baseURL]);
  const created = await run([
    "create",
    "--session",
    server.session,
    "--span",
    "test-1",
    "--mode",
    "review",
    "--turnId",
    "turn-1",
    "--category",
    "tests",
  ]);
  assert.equal(created.code, 0, created.stderr);
  const id = JSON.parse(created.stdout).id;
  const scoped = await run(["review", id, "--json"]);
  assert.equal(scoped.code, 0, scoped.stderr);
  const cleared = await run([
    "review",
    id,
    "--turnId",
    "",
    "--category=",
    "--json",
  ]);
  assert.equal(cleared.code, 0, cleared.stderr);
  const full = JSON.parse(cleared.stdout);
  assert.deepEqual(
    full,
    await server.query("investigation_review", {
      id,
      turnId: "",
      category: "",
    }),
  );
  assert.ok(full.activeMs > JSON.parse(scoped.stdout).activeMs);
  const reread = await run(["review", id, "--json"]);
  assert.deepEqual(
    JSON.parse(reread.stdout),
    JSON.parse(scoped.stdout),
    "temporary filter overrides do not change saved context",
  );
  const text = await run(["review", id, "--details"]);
  assert.equal(text.code, 0, text.stderr);
  assert.ok(text.stdout.includes("Scope: turn-1 · tests"));
});

test("agent handoffs pin the displayed revision, filters, and backend", () => {
  const command = reviewCommand(
    {
      id: "task",
      data: { revision: "frozen" },
      turn: "turn-2",
      category: "tests",
    },
    "http://127.0.0.1:4321",
  );
  assert.equal(
    command,
    "trajectory review --revision frozen --turnId turn-2 --category tests --url http://127.0.0.1:4321",
  );
  assert.match(
    reviewCommand(
      { id: "task", data: { revision: "frozen" } },
      "http://localhost:4318",
    ),
    /--turnId '' --category ''/,
  );
  assert.equal(
    agentCommand(
      ["review", "--session", "x'; echo unsafe"],
      "http://localhost:4318",
    ),
    "trajectory review --session 'x'\\''; echo unsafe' --url http://localhost:4318",
  );
  const saved = {
    id: "saved",
    view: { selectedSpan: "original", mode: "review" },
  };
  const origin = "http://127.0.0.1:4321";
  assert.equal(
    reviewCommand({ investigation: saved, selectedSpan: "original" }, origin),
    "trajectory investigation view saved --url " + origin,
    "unchanged saved review retains its original mode and filters",
  );
  assert.equal(
    reviewCommand({ investigation: saved, selectedSpan: "other" }, origin),
    "trajectory investigation view saved --span other --url " + origin,
  );
  const openedEvidence = {
    ...saved,
    view: { selectedSpan: "other", mode: "waterfall" },
    revealedSpan: "other",
  };
  assert.equal(
    reviewCommand(
      { investigation: openedEvidence, selectedSpan: "other" },
      origin,
    ),
    "trajectory investigation view saved --span other --url " + origin,
    "opening an evidence link must not fall back to the saved selection",
  );
});

test("saved views capture page height and horizontal timeline position separately", (t) => {
  const previous = globalThis.window;
  globalThis.window = { scrollY: 420 };
  t.after(() => {
    if (previous === undefined) delete globalThis.window;
    else globalThis.window = previous;
  });
  const saved = captureView(
    {
      view: "waterfall",
      turn: "t",
      category: "",
      data: { offset: 0 },
      limit: 1000,
      zoom: 2,
      collapsed: new Set(),
      expandedSpans: new Set(),
      selectedSpan: "s",
    },
    { scrollTop: 99, scrollLeft: 125 },
  );
  assert.equal(saved.scrollRoot, "page");
  assert.equal(saved.scrollTop, 420);
  assert.equal(saved.scrollLeft, 125);
});
