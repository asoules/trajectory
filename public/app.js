import { renderInspector } from "./inspector.js";
import { review } from "./review.js";
import { overview } from "./overview.js";
import { waterfall } from "./waterfall.js";
import {
  captureView,
  investigationBanner,
  investigationPrompt,
  reviewCommand,
} from "./investigation.js";
import { api, post } from "./api.js";
import { escape, duration, count, labels, color } from "./format.js";
import { patchHTML } from "./dom.js";
const $ = (s) => document.querySelector(s);
const state = {
  id: new URLSearchParams(location.search).get("session"),
  data: null,
  sessions: [],
  scope: "all",
  q: "",
  next: null,
  view: "review",
  category: "",
  turn: "",
  timelineTurn: "",
  analysisTurn: "",
  zoom: 1,
  collapsed: new Set(),
  expandedSpans: new Set(),
  live: true,
  limit: 1000,
  offset: undefined,
  controller: null,
  busy: false,
  listSeq: 0,
  selection: 0,
  overviewHidden: false,
  investigation: null,
  selectedSpan: null,
};
let searchTimer;
function requireRevision() {
  if (!state.investigation && !state.data?.revision)
    throw new Error(
      state.data?.captureUnavailable ||
        "This view expired. Refresh the task before inspecting it.",
    );
}
state.drawerOpen = false;
state.drawerPinned = false;
state.listController = null;
state.aggregateSeq = 0;
state.aggregateController = null;
let drawerReturnFocus = null;
function syncDrawer() {
  const overlay = state.drawerOpen && !state.drawerPinned;
  $("#session-drawer").hidden = !state.drawerOpen;
  $("#drawer-backdrop").hidden = !overlay;
  $(".app").classList.toggle(
    "drawer-pinned",
    state.drawerOpen && state.drawerPinned,
  );
  $("#open-sessions").setAttribute("aria-expanded", String(state.drawerOpen));
  $("#pin-sessions").setAttribute("aria-pressed", String(state.drawerPinned));
  $("#pin-sessions").textContent = state.drawerPinned
    ? "Unpin sidebar"
    : "Pin sidebar";
  $("#session-drawer").setAttribute(
    "role",
    overlay ? "dialog" : "complementary",
  );
  if (overlay) $("#session-drawer").setAttribute("aria-modal", "true");
  else $("#session-drawer").removeAttribute("aria-modal");
  $("main").inert = overlay;
}
function setDrawer(open) {
  if (open === state.drawerOpen) return;
  if (open) {
    drawerReturnFocus = document.activeElement;
    $("#session-drawer").style.setProperty(
      "--drawer-top",
      `${window.scrollY}px`,
    );
  }
  state.drawerOpen = open;
  if (!open) {
    clearTimeout(searchTimer);
    state.listController?.abort();
    state.listSeq++;
    state.drawerPinned = false;
    try {
      localStorage.setItem("trajectory.sessions.pinned", "false");
    } catch {}
  }
  syncDrawer();
  if (open) {
    renderSessions();
    loadSessions();
    $("#search").focus();
  } else
    (drawerReturnFocus?.isConnected
      ? drawerReturnFocus
      : $("#open-sessions")
    ).focus({ preventScroll: true });
}
function togglePinned() {
  state.drawerPinned = !state.drawerPinned;
  try {
    localStorage.setItem(
      "trajectory.sessions.pinned",
      String(state.drawerPinned),
    );
  } catch {}
  syncDrawer();
}

function showError(e) {
  if (e.name === "AbortError") return;
  $("#error").textContent = e.message;
  $("#error").classList.remove("hidden");
  $("#connection").textContent = "Refresh failed";
}
async function loadSessions(more = false) {
  if (!state.drawerOpen) return;
  const seq = ++state.listSeq,
    controller = new AbortController();
  state.listController?.abort();
  state.listController = controller;
  $("#session-error").hidden = true;
  try {
    const data = await api(
      "sessions",
      {
        q: state.q,
        offset: more ? state.sessions.length : 0,
        limit: 40,
        archived: state.scope === "archived" ? "true" : undefined,
        recent: state.scope === "recent" ? "true" : undefined,
      },
      controller.signal,
    );
    if (seq !== state.listSeq || controller.signal.aborted || !state.drawerOpen)
      return;
    state.sessions = more
      ? [...state.sessions, ...data.sessions]
      : data.sessions;
    state.next = data.nextOffset;
    $("#session-count").textContent = data.total;
    renderSessions();
  } catch (e) {
    if (controller.signal.aborted || seq !== state.listSeq || !state.drawerOpen)
      return;
    $("#session-error").textContent = e.message;
    $("#session-error").hidden = false;
  }
}
function renderSessions() {
  if (!state.drawerOpen) return;
  const rows = state.sessions.filter(
    (s) => state.scope !== "recent" || s.recent,
  );
  $("#sessions").innerHTML =
    rows
      .map(
        (s) =>
          `<button class="session ${s.id === state.id ? "active" : ""}" data-session="${escape(s.id)}"><div class="session-title">${escape(s.title)}</div><div class="session-meta"><span>${s.recent ? '<i class="dot"></i>Recently updated' : s.archived ? "Archived" : "Historical"}</span><span>${new Date(s.updatedAt).toLocaleDateString(undefined, { month: "short", day: "numeric" })}</span></div></button>`,
      )
      .join("") || '<div class="empty">No matching sessions.</div>';
  $("#more-sessions").classList.toggle("hidden", state.next == null);
}
async function selectSession(id) {
  state.investigation = null;
  state.selectedSpan = null;
  state.live = true;
  $("#live-toggle").disabled = false;
  $("#refresh").disabled = false;
  $("#live-toggle").textContent = "Pause refresh";
  if (!state.drawerPinned) setDrawer(false);
  state.aggregateController?.abort();
  state.aggregateSeq++;
  state.controller?.abort();
  state.selection++;
  state.id = id;
  state.data = null;
  state.category = "";
  state.turn = "";
  state.timelineTurn = "";
  state.analysisTurn = "";
  state.zoom = 1;
  state.limit = 1000;
  state.offset = undefined;
  state.collapsed.clear();
  state.expandedSpans.clear();
  $("#inspector").classList.add("hidden");
  history.replaceState(null, "", `?session=${encodeURIComponent(id)}`);
  renderSessions();
  $("#content").innerHTML =
    '<div class="empty welcome">Reading the trajectory…</div>';
  await loadTrace(true, true);
}
async function selectTurn(id, focus = false) {
  if (!state.data.turns.some((t) => t.id === id)) return;
  state.selection++;
  state.selectedSpan = null;
  state.turn = id;
  state.timelineTurn = id;
  state.offset = 0;
  state.zoom = 1;
  $("#inspector").classList.add("hidden");
  await loadTrace();
  if (state.turn !== id) return;
  if ($(".trace-scroller")) {
    $(".trace-scroller").scrollLeft = 0;
  }
  const button = [...document.querySelectorAll("[data-turn]")].find(
    (b) => b.dataset.turn === id,
  );
  if (focus) button?.focus({ preventScroll: true });
  $(".turn-heading")?.scrollIntoView?.({ block: "start" });
}

function viewReady() {
  return (
    state.data &&
    state.dataContext?.turn === state.turn &&
    state.dataContext?.category === state.category
  );
}
async function loadTrace(initial = false, refresh = false) {
  state.inspectorController?.abort();
  if (!refresh && !state.investigation && state.data && !state.data.revision) {
    showError(
      new Error(
        state.data.captureUnavailable ||
          "This view expired. Refresh the task before navigating.",
      ),
    );
    return;
  }
  state.selectedSpan = null;
  state.selection++;
  $("#inspector").classList.add("hidden");
  const id = state.id,
    category = state.category,
    turn = state.turn;
  if (!id) return;
  const controller = new AbortController();
  state.controller?.abort();
  state.controller = controller;
  state.busy = true;
  if ($("#view-content"))
    $("#view-content").setAttribute("aria-busy", String(!viewReady()));
  if ($(".turn-timeline")) $(".turn-timeline").inert = !viewReady();
  try {
    const data = await api(
      state.investigation ? "investigation_trace" : "trace",
      {
        session: id,
        revision:
          refresh || state.investigation ? undefined : state.data?.revision,
        id: state.investigation?.id,
        limit: state.limit,
        offset: state.offset,
        category: state.category,
        turnId: state.turn,
      },
      controller.signal,
    );
    if (
      state.id !== id ||
      controller.signal.aborted ||
      state.category !== category ||
      state.turn !== turn
    )
      return;
    state.data = data;
    state.dataContext = { turn, category };
    if (id !== data.session.id && !state.investigation) {
      state.id = data.session.id;
      history.replaceState(
        null,
        "",
        `?session=${encodeURIComponent(state.id)}`,
      );
    }
    if (
      state.view === "waterfall" &&
      !data.turns.some((t) => t.id === state.turn) &&
      data.turns.length
    ) {
      state.turn = data.turns.at(-1).id;
      state.timelineTurn = state.turn;
      state.offset = 0;
      return loadTrace(true);
    }
    $("#error").classList.add("hidden");
    $("#connection").textContent = state.investigation
      ? "Saved snapshot"
      : state.live
        ? "Updated " +
          new Date().toLocaleTimeString(undefined, {
            hour: "2-digit",
            minute: "2-digit",
            second: "2-digit",
          })
        : "Refresh paused";
    render();
    if (initial && state.view === "waterfall") {
      $(".turn-heading")?.scrollIntoView?.({ block: "start" });
    }
  } catch (e) {
    showError(e);
  } finally {
    if (state.controller === controller) {
      state.busy = false;
      if ($("#view-content"))
        $("#view-content").setAttribute("aria-busy", String(!viewReady()));
      if ($(".turn-timeline")) $(".turn-timeline").inert = !viewReady();
    }
  }
}
function render() {
  if (!viewReady()) return;
  const previous = $(".trace-scroller"),
    scroll = previous ? { left: previous.scrollLeft } : null;
  patchHTML(
    $("#content"),
    investigationBanner(state.investigation) + overview(state),
  );
  renderView();
  if (scroll && $(".trace-scroller")) {
    $(".trace-scroller").scrollLeft = scroll.left;
  }
}
function renderView() {
  if (!viewReady()) return;
  if (state.view === "review") {
    patchHTML($("#view-content"), review(state));
    return;
  }
  if (state.view === "aggregate") {
    renderAggregates();
    return;
  }
  patchHTML($("#view-content"), waterfall(state));
}
async function renderAggregates() {
  const id = state.id,
    cat = state.category,
    turn = state.turn,
    seq = ++state.aggregateSeq;
  const revision = state.data.revision;
  const controller = new AbortController();
  state.aggregateController?.abort();
  state.aggregateController = controller;
  if (!$("#view-content").children.length)
    $("#view-content").innerHTML = '<div class="empty">Aggregating…</div>';
  try {
    requireRevision();
    const a = await api(
      state.investigation ? "investigation_summary" : "summary",
      {
        session: id,
        id: state.investigation?.id,
        revision: state.investigation ? undefined : revision,
        turnId: turn,
        category: cat,
        top: 30,
      },
      controller.signal,
    );
    if (
      controller.signal.aborted ||
      seq !== state.aggregateSeq ||
      state.id !== id ||
      state.view !== "aggregate" ||
      state.turn !== turn ||
      state.category !== cat
    )
      return;
    state.aggregateRevision = revision;
    const groups = a.groups.filter((g) => !cat || g.category === cat);
    patchHTML(
      $("#view-content"),
      `<div class="toolbar"><span>Ranked by self time · parent wrappers exclude child durations</span><span>Top ${groups.length} groups</span></div><div style="overflow:auto" data-key="aggregate"><table class="aggregate"><thead><tr><th>Operation</th><th>Calls</th><th>Self time</th><th>Average</th><th>Slowest</th><th>Failed</th></tr></thead><tbody>${groups.map((g) => `<tr data-key="${escape(g.id)}"><td><button data-detail="${escape(g.spanIds[0])}"><i class="swatch" style="background:${color(g.category)};margin-right:7px"></i>${escape(g.label)}</button><div class="agg-bar"><span style="width:${(g.selfMs / Math.max(1, groups[0]?.selfMs)) * 100}%;background:${color(g.category)}"></span></div></td><td class="num">${g.calls}</td><td class="num">${duration(g.selfMs)}</td><td class="num">${duration(g.meanMs)}</td><td class="num">${duration(g.maxMs)}</td><td class="num ${g.failures ? "bad" : ""}">${g.failures || "—"}</td></tr>`).join("") || '<tr><td colspan="6" class="empty">No matching operations in the top 30 groups.</td></tr>'}</tbody></table></div><div class="trace-footer">Self time is summed work, and can overlap across parallel operations. Average and slowest use inclusive duration.</div>`,
    );
    return true;
  } catch (e) {
    if (!controller.signal.aborted && seq === state.aggregateSeq) showError(e);
    return false;
  }
}
async function inspect(id) {
  if (!viewReady()) return;
  const session = state.id,
    seq = ++state.selection,
    revision = state.data.revision;
  if (!state.investigation) {
    state.live = false;
    state.controller?.abort();
    $("#live-toggle").textContent = "Resume refresh";
    $("#connection").textContent = "Inspection paused at displayed snapshot";
  }
  if (state.view === "aggregate" && (await renderAggregates()) !== true) return;
  if (seq !== state.selection || session !== state.id) return;

  $("#inspector").style.top = `${window.scrollY}px`;
  $("#inspector").classList.remove("hidden");
  $("#inspector").innerHTML = '<div class="empty">Loading span…</div>';
  state.inspectorController?.abort();
  const controller = new AbortController();
  state.inspectorController = controller;
  try {
    requireRevision();
    const v = await api(
      state.investigation ? "investigation_span" : "span",
      {
        session,
        id: state.investigation?.id ?? id,
        span: state.investigation ? id : undefined,
        revision: state.investigation ? undefined : revision,
        maxChars: 16000,
      },
      controller.signal,
    );
    if (
      controller.signal.aborted ||
      session !== state.id ||
      seq !== state.selection
    )
      return;
    state.selectedSpan = id;
    if (state.view === "waterfall") renderView();
    $("#inspector").innerHTML = renderInspector(
      v,
      state.data.captureUnavailable,
    );
  } catch (e) {
    if (
      controller.signal.aborted ||
      session !== state.id ||
      seq !== state.selection
    )
      return;
    showError(e);
    $("#inspector").innerHTML =
      '<div class="inspector-header">Could not load this span.<button id="close-inspector" aria-label="Close inspector">×</button></div>';
  }
}
document.addEventListener("click", (event) => {
  const b = event.target.closest("button");
  if (!b) return;
  const d = b.dataset;
  if (d.session) return selectSession(d.session);
  if (d.scope) {
    state.scope = d.scope;
    document
      .querySelectorAll("[data-scope]")
      .forEach((x) =>
        x.classList.toggle("active", x.dataset.scope === d.scope),
      );
    return loadSessions();
  }
  if (d.detail) return inspect(d.detail);
  if (d.turn) return selectTurn(d.turn);
  if (d.reviewTurn) {
    state.analysisTurn = state.turn;
    state.category = "";
    state.view = "waterfall";
    return selectTurn(d.reviewTurn);
  }
  if (d.view) {
    const previous = state.view;
    state.selection++;
    state.selectedSpan = null;
    $("#inspector").classList.add("hidden");
    state.view = d.view;
    if (d.view === "waterfall" && previous !== "waterfall") {
      state.analysisTurn = state.turn;
      const id =
        state.turn || state.timelineTurn || state.data.turns.at(-1)?.id;
      if (id) return selectTurn(id);
    }
    if (previous === "waterfall" && d.view !== "waterfall") {
      state.turn = state.analysisTurn;
      state.offset = undefined;
      if (d.view === "review") state.category = "";
      return loadTrace();
    }
    if (d.view === "review" && state.category) {
      state.category = "";
      state.offset = undefined;
      return loadTrace();
    }
    return render();
  }
  if (d.category) {
    if (d.category === "unobserved") {
      state.category = "";
      $("#error").textContent =
        "Unobserved time has no logged span. Use the gaps in the timeline to investigate; it is not measured reasoning time.";
      $("#error").classList.remove("hidden");
    } else state.category = state.category === d.category ? "" : d.category;
    state.collapsed.clear();
    state.offset = undefined;
    return loadTrace();
  }
  switch (b.id) {
    case "inspect-turn-request": {
      if (!viewReady()) return;
      const turn = state.data.turns.find((t) => t.id === state.turn);
      state.selection++;
      state.selectedSpan = null;
      state.live = false;
      state.controller?.abort();
      if (!state.investigation)
        $("#live-toggle").textContent = "Resume refresh";
      $("#inspector").style.top = `${window.scrollY}px`;
      $("#inspector").classList.remove("hidden");
      $("#inspector").innerHTML =
        `<div class="inspector-header"><span class="eyebrow">TURN REQUEST</span><button id="close-inspector" aria-label="Close inspector">×</button></div><h2>Turn ${state.data.turns.indexOf(turn) + 1}</h2><pre>${escape(turn?.request || turn?.label || "Request not recorded.")}</pre>`;
      return renderView();
    }

    case "begin-investigate":
      return beginInvestigation();
    case "cancel-investigate":
      $("#investigate-dialog").close();
      return;
    case "copy-investigation":
      return showHandoff(state.investigation);
    case "back-to-live":
      return selectSession(state.id);
    case "close-handoff":
      $("#handoff-dialog").close();
      return;
    case "copy-handoff":
      try {
        awaitClipboard();
      } catch {}
      return;
    case "toggle-overview":
      state.overviewHidden = !state.overviewHidden;
      return render();
    case "open-sessions":
      return setDrawer(!state.drawerOpen);
    case "close-sessions":
      return setDrawer(false);
    case "pin-sessions":
      return togglePinned();
    case "refresh-sessions":
      return loadSessions();
    case "refresh":
      return loadTrace(false, true);
    case "live-toggle":
      state.live = !state.live;
      if (!state.live) {
        state.controller?.abort();
        state.aggregateController?.abort();
      }
      b.textContent = state.live ? "Pause refresh" : "Resume refresh";
      $("#connection").textContent = state.investigation
        ? "Saved snapshot"
        : state.live
          ? "Auto refresh on"
          : "Refresh paused";
      if (state.live) loadTrace(false, true);
      break;
    case "more-sessions":
      return loadSessions(true);
    case "zoom-in":
      state.zoom = Math.min(16, state.zoom * 2);
      return render();
    case "zoom-out":
      state.zoom = Math.max(1, state.zoom / 2);
      return render();
    case "previous-spans":
      state.offset = state.data.previousOffset;
      state.collapsed.clear();
      return loadTrace();
    case "next-spans":
      state.offset = state.data.nextOffset;
      state.collapsed.clear();
      return loadTrace();
    case "close-inspector":
      state.inspectorController?.abort();
      state.selection++;
      $("#inspector").classList.add("hidden");
      break;
    case "agent-help":
      $("#cli-command").textContent = reviewCommand(state);
      $("#help").showModal();
      break;
    case "close-help":
      $("#help").close();
      break;
    case "export":
      return download();
  }
});
document.addEventListener("visibilitychange", () => {
  if (document.hidden) {
    state.controller?.abort();
    state.aggregateController?.abort();
    state.listController?.abort();
    state.inspectorController?.abort();
  }
});
window.addEventListener("pagehide", () => {
  state.controller?.abort();
  state.aggregateController?.abort();
  state.listController?.abort();
  state.inspectorController?.abort();
});
document.addEventListener("change", (e) => {
  if (e.target.id === "category-filter") {
    state.category = e.target.value;
    state.collapsed.clear();
    state.offset = undefined;
    loadTrace();
  }
  if (e.target.id === "turn-filter") {
    state.turn = e.target.value;
    state.collapsed.delete(state.turn);
    state.offset = undefined;
    loadTrace();
  }
});
$("#search").addEventListener("input", (e) => {
  clearTimeout(searchTimer);
  state.q = e.target.value;
  searchTimer = setTimeout(() => loadSessions(), 250);
});
$("#drawer-backdrop").addEventListener("click", () => setDrawer(false));
document.addEventListener("keydown", (e) => {
  if (
    e.target.closest?.(".turn-navigator") &&
    ["ArrowUp", "ArrowDown", "Home", "End"].includes(e.key)
  ) {
    e.preventDefault();
    const turns = state.data.turns;
    let index = turns.findIndex((t) => t.id === state.turn);
    index =
      e.key === "Home"
        ? 0
        : e.key === "End"
          ? turns.length - 1
          : Math.max(
              0,
              Math.min(
                turns.length - 1,
                index + (e.key === "ArrowDown" ? 1 : -1),
              ),
            );
    return selectTurn(turns[index].id, true);
  }
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
    e.preventDefault();
    if (!state.drawerOpen) setDrawer(true);
    else $("#search").focus();
    return;
  }
  if (e.key === "Escape") {
    if (state.drawerOpen && !state.drawerPinned) {
      e.preventDefault();
      setDrawer(false);
      return;
    }
    state.selection++;
    $("#inspector").classList.add("hidden");
  }
  if (e.key === "Tab" && state.drawerOpen && !state.drawerPinned) {
    const items = [
      ...$("#session-drawer").querySelectorAll(
        "button:not([disabled]),input,a[href],select",
      ),
    ].filter((el) => !el.closest("[hidden],.hidden"));
    const first = items[0],
      last = items.at(-1);
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last?.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first?.focus();
    }
  }
});
async function download() {
  try {
    requireRevision();
    const trace = await api(
      state.investigation ? "investigation_export" : "export",
      {
        session: state.id,
        id: state.investigation?.id,
        revision: state.investigation ? undefined : state.data?.revision,
      },
    );
    const url = URL.createObjectURL(
      new Blob([JSON.stringify(trace)], { type: "application/json" }),
    );
    const a = document.createElement("a");
    a.href = url;
    a.download = `trajectory-${state.id}.json`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  } catch (e) {
    showError(e);
  }
}
try {
  state.drawerPinned =
    localStorage.getItem("trajectory.sessions.pinned") === "true";
} catch {}
if (state.drawerPinned) setDrawer(true);
const savedID = new URLSearchParams(location.search).get("investigation");
if (savedID) {
  try {
    await loadInvestigation(
      savedID,
      new URLSearchParams(location.search).get("span"),
    );
  } catch (e) {
    showError(e);
  }
} else if (state.id) await loadTrace(true, true);
else {
  $("#connection").textContent = "Select a task";
  $("#live-toggle").disabled = true;
  $("#refresh").disabled = true;
  setDrawer(true);
}
setInterval(() => {
  if (
    state.id &&
    state.live &&
    !state.investigation &&
    !document.hidden &&
    !state.busy
  ) {
    loadTrace(false, true);
  }
}, 4000);

let pendingInvestigation;
function beginInvestigation() {
  if (!state.selectedSpan) return;
  state.live = false;
  state.controller?.abort();
  state.aggregateController?.abort();
  $("#live-toggle").textContent = "Resume refresh";
  pendingInvestigation = {
    revision: state.data.revision,
    investigation: state.investigation?.id,
    view: captureView(state, $(".trace-scroller")),
  };
  $("#investigate-operation").textContent = $("#inspector h2").textContent;
  $("#investigate-error").hidden = true;
  $("#investigate-dialog").showModal();
  $("#investigate-question").focus();
}
$("#investigate-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = $("#save-investigation");
  button.disabled = true;
  try {
    const record = await post("investigations", {
      ...pendingInvestigation,
      question: $("#investigate-question").value,
    });
    $("#investigate-dialog").close();
    if (await loadInvestigation(record.id)) showHandoff(record);
  } catch (e) {
    $("#investigate-error").textContent = e.message;
    $("#investigate-error").hidden = false;
  } finally {
    button.disabled = false;
  }
});
function showHandoff(record) {
  const current =
    state.investigation?.id === record.id ? state.investigation : record;
  $("#handoff-text").value = investigationPrompt(current, state.selectedSpan);
  $("#copy-status").textContent = "";
  $("#handoff-dialog").showModal();
  $("#handoff-text").focus();
  $("#handoff-text").select();
}
async function awaitClipboard() {
  try {
    await navigator.clipboard.writeText($("#handoff-text").value);
    $("#copy-status").textContent = " Copied";
  } catch {
    $("#handoff-text").focus();
    $("#handoff-text").select();
    $("#copy-status").textContent = " Select and copy the handoff above";
  }
}
async function loadInvestigation(id, span) {
  state.controller?.abort();
  state.aggregateController?.abort();
  const seq = ++state.selection;
  const controller = new AbortController();
  state.controller = controller;
  state.live = false;
  const record = await api(
    "investigation_render",
    {
      id,
      span: span || undefined,
    },
    controller.signal,
  );
  if (controller.signal.aborted || seq !== state.selection) return false;
  const v = record.view;
  // Keep an evidence-link override when handing this displayed view to an agent.
  record.revealedSpan = span || null;
  state.investigation = record;
  state.data = record.trace;
  state.dataContext = { turn: v.turnId, category: v.category };
  state.id = record.session.id;
  state.view = v.mode;
  state.category = v.category;
  state.turn = v.turnId;
  state.analysisTurn = v.mode === "waterfall" ? "" : v.turnId;
  if (v.mode === "waterfall") state.timelineTurn = v.turnId;
  state.offset = v.offset;
  state.limit = v.limit;
  state.zoom = v.zoom;
  state.overviewHidden = v.overviewHidden;
  state.collapsed = new Set(v.collapsed);
  state.expandedSpans = new Set(v.expanded);
  state.selectedSpan = v.selectedSpan;
  $("#live-toggle").disabled = true;
  $("#live-toggle").textContent = "Saved snapshot";
  $("#connection").textContent = "Frozen " + record.asOf;
  history.replaceState(
    null,
    "",
    record.url + (span ? "&span=" + encodeURIComponent(span) : ""),
  );
  render();
  await inspect(v.selectedSpan);
  if (
    controller.signal.aborted ||
    state.investigation?.id !== id ||
    state.selectedSpan !== v.selectedSpan
  )
    return false;
  const scroller = $(".trace-scroller");
  if (scroller) scroller.scrollLeft = v.scrollLeft;
  const top =
    v.scrollRoot === "page"
      ? v.scrollTop
      : scroller
        ? scroller.getBoundingClientRect().top + window.scrollY + v.scrollTop
        : window.scrollY;
  window.scrollTo({ top });
  if (span)
    $(".trace-row.selected")?.scrollIntoView?.({
      block: "center",
      inline: "nearest",
    });
  $("#inspector").style.top = `${window.scrollY}px`;
  return true;
}
