import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { JSDOM } from "jsdom";
import { backend } from "./helpers/backend.js";
import { duration } from "../public/format.js";
const html = await readFile(
  new URL("../public/index.html", import.meta.url),
  "utf8",
);
const code = (
  await Promise.all(
    [
      "format",
      "dom",
      "api",
      "review",
      "inspector",
      "overview",
      "waterfall",
      "investigation",
      "app",
    ].map((name) =>
      readFile(new URL("../public/" + name + ".js", import.meta.url), "utf8"),
    ),
  )
)
  .map((s) =>
    s.replace(/^import[\s\S]*?from [^;]+;\n/gm, "").replace(/^export /gm, ""),
  )
  .join("\n");
const pendingHTTP = new Set();
const flush = async () => {
  for (let i = 0; i < 8; i++) {
    await Promise.all([...pendingHTTP]);
    await new Promise((r) => setImmediate(r));
  }
};
async function viewer(
  t,
  {
    session = "demo",
    duplicateLabels = false,
    reviewDefault = false,
    twoTurns = false,
  } = {},
) {
  const server = await backend(t, { twoTurns });
  if (twoTurns) session = server.session;
  const query = async (method, args) => {
    const p = server.query(method, args);
    pendingHTTP.add(p);
    try {
      return await p;
    } finally {
      pendingHTTP.delete(p);
    }
  };
  const dom = new JSDOM(html, {
    url: `http://127.0.0.1:4318/${session ? "?session=" + session : ""}`,
    runScripts: "outside-only",
    pretendToBeVisual: true,
  });
  t.after(() => dom.window.close());
  const w = dom.window,
    calls = [],
    timers = [];
  w.AbortController = AbortController;
  w.AbortSignal = AbortSignal;
  w.setInterval = (fn) => {
    timers.push(fn);
    return timers.length;
  };
  let summaryGate = null,
    listGate = null,
    spanGate = null,
    traceGate = null,
    investigationGate = null,
    version = 0;
  w.fetch = async (url, options = {}) => {
    const parsed = new URL(url, w.location.href),
      method = parsed.pathname.slice(5),
      args = Object.fromEntries(parsed.searchParams);
    calls.push({ method, args, signal: options.signal });
    if (method === "trace" && traceGate) await traceGate;
    if (method === "summary" && summaryGate) await summaryGate;
    if (method === "sessions" && listGate) await listGate;
    if (method === "span" && spanGate) await spanGate;
    if (method === "investigation_render") {
      if (investigationGate) await investigationGate;
      const trace = await query("trace", { session: "demo" });
      return {
        ok: true,
        json: async () => ({
          id: "saved",
          url: "/?investigation=saved",
          session: trace.session,
          trace,
          view: {
            mode: "review",
            category: "",
            turnId: "",
            offset: 0,
            limit: 1000,
            zoom: 1,
            overviewHidden: false,
            collapsed: [],
            expanded: [],
            selectedSpan: "test-1",
          },
        }),
      };
    }
    const result = await query(method, args);
    if (method === "summary" && duplicateLabels) {
      result.groups = result.groups.slice(0, 2).map((g) => ({
        ...g,
        category: "tests",
        label: "npm test " + "x".repeat(151),
      }));
    }
    if (method === "trace") {
      result.summary.activeMs += version * 1000;
      result.summary.review.presentation.totals.activeTime = duration(
        result.summary.activeMs,
      );
      result.spans[0].durationMs += version * 1000;
    }
    return { ok: true, json: async () => result };
  };
  await w.eval(
    `(async()=>{${code}\n window.testLoadInvestigation=loadInvestigation; })()`,
  );
  await flush();
  if (!reviewDefault && session) {
    w.document.querySelector('[data-view="waterfall"]').click();
    await flush();
  }
  return {
    w,
    doc: w.document,
    calls,
    holdTrace: () => {
      let release;
      traceGate = new Promise((r) => (release = r));
      return async () => {
        traceGate = null;
        release();
        await flush();
      };
    },
    holdInvestigation: () => {
      let release;
      investigationGate = new Promise((r) => (release = r));
      return async () => {
        investigationGate = null;
        release();
        await flush();
      };
    },
    tick: async () => {
      version++;
      timers.forEach((fn) => fn());
      await flush();
    },
    click: async (selector) => {
      const el = w.document.querySelector(selector);
      assert.ok(el, selector + " exists");
      el.click();
      await flush();
    },
    holdSpan: () => {
      let release;
      spanGate = new Promise((r) => (release = r));
      return async () => {
        spanGate = null;
        release();
        await flush();
      };
    },
    holdSummary: () => {
      let release;
      summaryGate = new Promise((r) => (release = r));
      return async () => {
        summaryGate = null;
        release();
        await flush();
      };
    },
    holdList: () => {
      let release;
      listGate = new Promise((r) => (release = r));
      return async () => {
        listGate = null;
        release();
        await flush();
      };
    },
  };
}
test("auto-refresh keeps panels, timeline, focus and scroll mounted", async (t) => {
  const v = await viewer(t),
    panel = v.doc.querySelector(".metrics"),
    scroller = v.doc.querySelector(".trace-scroller"),
    button = v.doc.querySelector("#category-filter"),
    value = panel.querySelector(".metric-value");
  const before = value.textContent;
  scroller.scrollTop = 80;
  scroller.scrollLeft = 120;
  button.focus();
  await v.tick();
  assert.equal(
    v.doc.querySelector(".metrics"),
    panel,
    "refresh replaced the metric panel",
  );
  assert.equal(
    v.doc.querySelector(".trace-scroller"),
    scroller,
    "refresh replaced the scroll container",
  );
  assert.equal(v.doc.activeElement, button);
  assert.equal(scroller.scrollTop, 80);
  assert.equal(scroller.scrollLeft, 120);
  assert.notEqual(value.textContent, before, "new values should still render");
});
test("aggregation remains visible while its refresh is pending", async (t) => {
  const v = await viewer(t);
  await v.click('[data-view="aggregate"]');
  const table = v.doc.querySelector(".aggregate");
  assert.ok(table);
  const release = v.holdSummary();
  await v.tick();
  assert.equal(
    v.doc.querySelector(".aggregate"),
    table,
    "refresh removed the table while fetching",
  );
  assert.doesNotMatch(
    v.doc.querySelector("#view-content").textContent,
    /Aggregating/,
  );
  await release();
  assert.equal(v.doc.querySelector(".aggregate"), table);
});
test("direct session load and background refresh never fetch the session list", async (t) => {
  const v = await viewer(t);
  await v.tick();
  await v.tick();
  assert.equal(v.calls.filter((c) => c.method === "sessions").length, 0);
});
test("drawer fetches on demand, closes after selection, and does not poll even when pinned", async (t) => {
  const v = await viewer(t),
    drawer = v.doc.querySelector("#session-drawer");
  assert.equal(drawer.hidden, true);
  await v.click("#open-sessions");
  assert.equal(drawer.hidden, false);
  assert.equal(v.doc.activeElement.id, "search");
  assert.equal(v.calls.filter((c) => c.method === "sessions").length, 1);
  await v.tick();
  assert.equal(v.calls.filter((c) => c.method === "sessions").length, 1);
  await v.click("#pin-sessions");
  assert.equal(
    v.doc.querySelector(".app").classList.contains("drawer-pinned"),
    true,
  );
  assert.equal(v.doc.querySelector("#drawer-backdrop").hidden, true);
  await v.tick();
  assert.equal(v.calls.filter((c) => c.method === "sessions").length, 1);
  await v.click("#pin-sessions");
  await v.click('[data-session="demo"]');
  assert.equal(drawer.hidden, true);
  await v.tick();
  assert.equal(v.calls.filter((c) => c.method === "sessions").length, 1);
});
test("closing drawer cancels in-flight list request and drops its late result", async (t) => {
  const v = await viewer(t),
    release = v.holdList();
  await v.click("#open-sessions");
  const call = v.calls.find((c) => c.method === "sessions");
  assert.ok(call);
  await v.click("#close-sessions");
  assert.equal(call.signal.aborted, true);
  const before = v.doc.querySelector("#sessions").innerHTML;
  await release();
  assert.equal(v.doc.querySelector("#sessions").innerHTML, before);
});
test("Command-K opens search, Escape closes it, and focus returns to opener", async (t) => {
  const v = await viewer(t),
    opener = v.doc.querySelector("#open-sessions");
  opener.focus();
  v.doc.dispatchEvent(
    new v.w.KeyboardEvent("keydown", {
      key: "k",
      metaKey: true,
      bubbles: true,
      cancelable: true,
    }),
  );
  await flush();
  assert.equal(v.doc.activeElement.id, "search");
  v.doc.dispatchEvent(
    new v.w.KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
  );
  assert.equal(v.doc.querySelector("#session-drawer").hidden, true);
  assert.equal(v.doc.activeElement, opener);
});
test("home lists metadata and waits for an explicit task selection", async (t) => {
  const v = await viewer(t, { session: null });
  assert.equal(
    v.calls.some((c) => c.method === "trace"),
    false,
  );
  assert.equal(v.calls.filter((c) => c.method === "sessions").length, 1);
  assert.equal(v.doc.querySelector("#live-toggle").disabled, true);
  await v.tick();
  assert.equal(
    v.calls.some((c) => c.method === "trace"),
    false,
  );
  await v.click('[data-session="demo"]');
  assert.equal(
    v.calls.filter((c) => c.method === "trace")[0].args.session,
    "demo",
  );
  assert.equal(v.doc.querySelector("#live-toggle").disabled, false);
});

test("distinct aggregate groups with identical truncated labels survive refresh", async (t) => {
  const v = await viewer(t, { duplicateLabels: true });
  await v.click('[data-view="aggregate"]');
  const rows = [...v.doc.querySelectorAll(".aggregate tbody tr")];
  assert.equal(rows.length, 2);
  await v.tick();
  assert.equal(v.doc.querySelectorAll(".aggregate tbody tr").length, 2);
  assert.equal(v.doc.querySelectorAll(".aggregate tbody tr")[0], rows[0]);
  assert.equal(v.doc.querySelectorAll(".aggregate tbody tr")[1], rows[1]);
});

test("timeline focus mode survives refresh without replacing the waterfall", async (t) => {
  const v = await viewer(t);
  const scroller = v.doc.querySelector(".trace-scroller");
  await v.click("#toggle-overview");
  assert.equal(v.doc.querySelector("#summary-content").hidden, true);
  assert.equal(
    v.doc.querySelector("#toggle-overview").getAttribute("aria-expanded"),
    "false",
  );
  await v.tick();
  assert.equal(v.doc.querySelector("#summary-content").hidden, true);
  assert.equal(v.doc.querySelector(".trace-scroller"), scroller);
  await v.click("#toggle-overview");
  assert.equal(v.doc.querySelector("#summary-content").hidden, false);
});

test("inspection pins the displayed data until an explicit refresh", async (t) => {
  const v = await viewer(t);
  await v.click('[data-detail="test-1"]');
  const before = v.doc.querySelector("#inspector").textContent;
  const count = v.calls.filter((c) => c.method === "trace").length;
  await v.tick();
  assert.equal(v.calls.filter((c) => c.method === "trace").length, count);
  assert.equal(v.doc.querySelector("#inspector").textContent, before);
  assert.ok(v.doc.querySelector("#begin-investigate"));
  await v.click("#refresh");
  assert.equal(
    v.doc.querySelector("#inspector").classList.contains("hidden"),
    true,
  );
});

test("a late inspector response cannot replace a newer live view", async (t) => {
  const v = await viewer(t),
    release = v.holdSpan();
  await v.click('[data-detail="test-1"]');
  await v.click("#refresh");
  await release();
  assert.equal(
    v.doc.querySelector("#inspector").classList.contains("hidden"),
    true,
  );
  assert.equal(v.doc.querySelector("#begin-investigate"), null);
});

test("changing view mode invalidates investigation selection while table loads", async (t) => {
  const v = await viewer(t);
  await v.click('[data-detail="test-1"]');
  const release = v.holdSummary();
  await v.click('[data-view="aggregate"]');
  assert.equal(
    v.doc.querySelector("#inspector").classList.contains("hidden"),
    true,
  );
  await release();
});

test("review is the default and keeps evidence disclosure stable across refresh", async (t) => {
  const v = await viewer(t, { reviewDefault: true });
  assert.equal(
    v.doc.querySelector('[data-view="review"]').getAttribute("aria-pressed"),
    "true",
  );
  assert.equal(v.doc.querySelector(".trace-scroller"), null);
  assert.ok(v.doc.querySelector('[aria-label="Time to investigate"]'));
  assert.equal(
    v.doc.querySelector(".metric-label").textContent,
    "Time in turns",
  );
  const details = v.doc.querySelector(".opportunities details");
  details.open = true;
  const evidence = details.querySelector("button");
  evidence.focus();
  await v.tick();
  assert.equal(v.doc.querySelector(".opportunities details"), details);
  assert.equal(details.open, true);
  assert.equal(v.doc.activeElement, evidence);
  await v.click('.opportunities [data-detail="test-1"]');
  assert.match(v.doc.querySelector("#inspector").textContent, /test/);
  assert.ok(v.doc.querySelector("#begin-investigate"));
  const calls = v.calls.filter((c) => c.method === "trace").length;
  await v.tick();
  assert.equal(v.calls.filter((c) => c.method === "trace").length, calls);
});

test("a late saved investigation cannot replace a newer session selection", async (t) => {
  const v = await viewer(t, { reviewDefault: true }),
    release = v.holdInvestigation();
  const pending = v.w.testLoadInvestigation("saved");
  await flush();
  await v.click("#open-sessions");
  await v.click('[data-session="demo"]');
  await release();
  await pending;
  assert.equal(v.doc.querySelector(".investigation-banner"), null);
  assert.equal(
    new URL(v.w.location.href).searchParams.get("investigation"),
    null,
  );
  assert.equal(v.doc.querySelector("#live-toggle").disabled, false);
});

test("timeline shows one turn, all nested spans, and keeps keyboard selection through refresh", async (t) => {
  const v = await viewer(t, { twoTurns: true });
  assert.equal(
    v.doc.querySelector('[data-turn][aria-current="true"]').dataset.turn,
    "turn-2",
  );
  assert.ok(v.doc.querySelector('[data-detail="parent-2"]'));
  assert.ok(
    v.doc.querySelector('[data-detail="child-2"]'),
    "nested call should be visible without expanding",
  );
  assert.equal(v.doc.querySelector('[data-detail="test-1"]'), null);
  assert.equal(v.doc.querySelector("#turn-filter"), null);
  assert.equal(
    v.doc.querySelector(
      "[data-toggle-turn], [data-toggle-span], #expand-all, #collapse-all",
    ),
    null,
  );
  assert.equal(
    v.calls.filter((c) => c.method === "trace").at(-1).args.turnId,
    "turn-2",
  );
  const selected = v.doc.querySelector('[data-turn="turn-2"]');
  selected.focus();
  selected.dispatchEvent(
    new v.w.KeyboardEvent("keydown", {
      key: "ArrowUp",
      bubbles: true,
      cancelable: true,
    }),
  );
  await flush();
  assert.equal(v.doc.activeElement.dataset.turn, "turn-1");
  assert.ok(v.doc.querySelector('[data-detail="test-1"]'));
  assert.equal(v.doc.querySelector('[data-detail="child-2"]'), null);
  await v.tick();
  assert.equal(
    v.doc.querySelector('[data-turn][aria-current="true"]').dataset.turn,
    "turn-1",
  );
  assert.equal(v.doc.activeElement.dataset.turn, "turn-1");
  await v.click("#inspect-turn-request");
  assert.match(v.doc.querySelector("#inspector").textContent, /More detail/);
  await v.click('[data-turn="turn-2"]');
  assert.equal(
    v.doc.querySelector("#inspector").classList.contains("hidden"),
    true,
  );
  await v.click('[data-view="review"]');
  assert.equal(
    v.doc.querySelector("#turn-filter").value,
    "",
    "timeline navigation should not narrow the prior review",
  );
});

test("changing turns discards a late span response", async (t) => {
  const v = await viewer(t, { twoTurns: true }),
    release = v.holdSpan();
  await v.click('[data-detail="child-2"]');
  await v.click('[data-turn="turn-1"]');
  await release();
  assert.equal(
    v.doc.querySelector("#inspector").classList.contains("hidden"),
    true,
  );
  assert.equal(v.doc.querySelector('[data-detail="child-2"]'), null);
});

test("pending turn navigation disables stale evidence and request actions", async (t) => {
  const v = await viewer(t, { twoTurns: true }),
    release = v.holdTrace();
  const spanCalls = v.calls.filter((c) => c.method === "span").length;
  await v.click('[data-turn="turn-1"]');
  assert.equal(
    v.doc.querySelector("#view-content").getAttribute("aria-busy"),
    "true",
  );
  await v.click('[data-detail="child-2"]');
  await v.click("#inspect-turn-request");
  assert.equal(v.calls.filter((c) => c.method === "span").length, spanCalls);
  assert.equal(
    v.doc.querySelector("#inspector").classList.contains("hidden"),
    true,
  );
  assert.equal(
    v.calls.filter((c) => c.method === "trace").at(-1).signal.aborted,
    false,
  );
  await release();
  assert.equal(
    v.doc.querySelector('[data-turn][aria-current="true"]').dataset.turn,
    "turn-1",
  );
  assert.ok(v.doc.querySelector('[data-detail="test-1"]'));
  assert.equal(
    v.doc.querySelector("#view-content").getAttribute("aria-busy"),
    "false",
  );
});

test("paused navigation reuses the displayed revision until an explicit refresh", async (t) => {
  const v = await viewer(t, { twoTurns: true });
  await v.click('[data-detail="child-2"]');
  const revision = v.calls.filter((c) => c.method === "span").at(-1)
    .args.revision;
  assert.ok(revision);
  await v.click('[data-turn="turn-1"]');
  assert.equal(
    v.calls.filter((c) => c.method === "trace").at(-1).args.revision,
    revision,
  );
  await v.click('[data-view="review"]');
  assert.equal(
    v.calls.filter((c) => c.method === "trace").at(-1).args.revision,
    revision,
  );
  await v.click("#refresh");
  assert.equal(
    v.calls.filter((c) => c.method === "trace").at(-1).args.revision,
    undefined,
  );
});

test("hiding the viewer cancels pending work and stops automatic reads", async (t) => {
  const v = await viewer(t),
    release = v.holdTrace();
  await v.tick();
  const pending = v.calls.filter((c) => c.method === "trace").at(-1);
  Object.defineProperty(v.doc, "hidden", { configurable: true, value: true });
  v.doc.dispatchEvent(new v.w.Event("visibilitychange"));
  assert.equal(pending.signal.aborted, true);
  const count = v.calls.length;
  await v.tick();
  assert.equal(v.calls.length, count);
  await release();
});
