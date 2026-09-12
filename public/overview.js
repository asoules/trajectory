import { escape, duration, count, labels } from "./format.js";
export function overview(state) {
  const { session: s, summary: a } = state.data;
  const r = a.review,
    totals = r?.presentation?.totals,
    usage = r?.tokens?.usage ?? a.tokens;
  const uncached =
    r?.tokens?.uncachedInput ??
    Math.max(0, (usage?.input_tokens ?? 0) - (usage?.cached_input_tokens ?? 0));
  const unknown =
    r?.unobservedMs ??
    a.breakdown.find((b) => b.category === "unobserved")?.ms ??
    0;
  return `<div class="heading"><div><h1>${escape(s.title)}</h1><div class="subline"><span>${escape(s.cwd ?? (s.source === "otlp" ? "OpenTelemetry trace" : "Local session"))}</span>${s.model ? `<span>· ${escape(s.model)}</span>` : ""}</div></div><div class="heading-actions"><button id="agent-help">CLI</button><button id="export">Export trace</button></div></div>
<div id="summary-content" ${state.overviewHidden ? "hidden" : ""}><div class="metrics" id="metrics"><div class="metric"><div class="metric-label">${state.turn ? "Time in turn" : "Time in turns"}</div><div class="metric-value">${totals?.activeTime ?? duration(a.activeMs)}</div><div class="metric-note">${totals?.unobservedTime ?? duration(unknown)} unobserved · ${totals?.unobservedPercent ?? Math.round((unknown / Math.max(1, a.activeMs)) * 100)}%</div></div><div class="metric"><div class="metric-label">Uncached input</div><div class="metric-value">${usage ? (totals?.uncachedInput ?? count(uncached)) : "—"}</div><div class="metric-note">${usage ? (totals?.cachedInput ?? count(usage.cached_input_tokens)) + " cached input reused" : "Usage not recorded"}</div></div><div class="metric"><div class="metric-label">Output tokens</div><div class="metric-value">${totals?.output ?? count(usage?.output_tokens)}</div><div class="metric-note">${usage ? (totals?.reasoning ?? count(usage.reasoning_output_tokens)) + " reasoning, included" : "Usage not recorded"}</div></div></div></div>
<section class="panel trace-panel ${state.overviewHidden ? "focused" : ""}" id="trace-panel"><div class="view-header"><div class="tabs">${[
    ["review", "Review"],
    ["waterfall", "Timeline"],
    ["aggregate", "Operations"],
  ]
    .map(
      ([v, l]) =>
        `<button aria-pressed="${state.view === v}" data-view="${v}" class="${state.view === v ? "active" : ""}">${l}</button>`,
    )
    .join(
      "",
    )}</div><div class="trace-controls">${state.view !== "waterfall" ? `<select id="turn-filter" aria-label="Filter turn"><option value="">All turns</option>${state.data.turns.map((t, i) => `<option value="${escape(t.id)}" ${state.turn === t.id ? "selected" : ""}>Turn ${i + 1} · ${duration(t.end - t.start)}</option>`).join("")}</select>` : ""}${
    state.view !== "review"
      ? `<select id="category-filter" aria-label="Filter category"><option value="">All activity</option>${Object.entries(
          labels,
        )
          .filter(([k]) => k !== "unobserved")
          .map(
            ([k, l]) =>
              `<option value="${k}" ${state.category === k ? "selected" : ""}>${l}</option>`,
          )
          .join("")}</select>`
      : ""
  }${state.view === "waterfall" ? `<button id="zoom-out" aria-label="Zoom out">−</button><span class="muted">${state.zoom}×</span><button id="zoom-in" aria-label="Zoom in">+</button>` : ""}<button id="toggle-overview" aria-expanded="${!state.overviewHidden}">${state.overviewHidden ? "Show totals" : "Hide totals"}</button></div></div><div id="view-content" data-preserve></div></section>
<details class="method-note" data-key="measurement"><summary>Measurement & coverage${unknown ? ` · ${Math.round((unknown / Math.max(1, a.activeMs)) * 100)}% of time unobserved` : ""}</summary><p>Time in turns excludes gaps between turns. Unobserved intervals have no recorded foreground span; they are not assumed model latency. Likely background services are excluded from attribution.</p><p>Recorded operation times can overlap. Usage is summed from completed responses when available; cached input is included in input, and reasoning is included in output. Tokens are volume, not a cost estimate.</p><p>${a.spanCount} evidence events · ${a.turnCount} turns · ${a.diagnostics?.malformed ?? 0} malformed records${a.diagnostics?.pendingBytes ? " · waiting for an incomplete record" : ""}</p></details>`;
}
