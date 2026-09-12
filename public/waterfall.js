import {
  escape,
  duration,
  count,
  labels,
  color,
  spanMeasure,
} from "./format.js";

export function waterfall(state) {
  const d = state.data;
  const turn = d.turns.find((t) => t.id === state.turn);
  if (!turn)
    return '<div class="empty">No turns recorded in this session.</div>';
  const index = d.turns.indexOf(turn);
  const start = turn.start;
  const range = Math.max(1, turn.end - start);
  const spans = d.spans.filter(
    (v) =>
      v.turnId === turn.id &&
      (!state.category || v.category === state.category),
  );
  const turns = d.turns
    .map((t, i) => {
      const tokens = t.usage
        ? (t.usage.total_tokens ??
          (t.usage.input_tokens ?? 0) + (t.usage.output_tokens ?? 0))
        : null;
      return `<button class="turn-choice" data-key="turn:${escape(t.id)}" data-turn="${escape(t.id)}" aria-current="${t.id === turn.id ? "true" : "false"}" tabindex="${t.id === turn.id ? 0 : -1}" title="${escape(t.label || `Turn ${i + 1}`)}"><span class="turn-number">Turn ${i + 1}${t.status === "running" ? " · live" : ""}</span><span class="turn-request">${escape(t.label || "Request not recorded")}</span><span class="turn-meta">${duration(t.end - t.start)} <span title="Input + output tokens, including cached input">· ${tokens == null ? "usage unavailable" : count(tokens) + " tokens"}</span></span></button>`;
    })
    .join("");
  const rows = spans
    .map((v) => {
      const left = Math.min(
        100,
        Math.max(0, ((v.start - start) / range) * 100),
      );
      const width = Math.max(
        0,
        ((Math.min(turn.end, v.end) - Math.max(start, v.start)) / range) * 100,
      );
      return `<div class="trace-row ${state.selectedSpan === v.id ? "selected" : ""}" data-key="span:${escape(v.id)}"><div class="trace-label" style="padding-left:${12 + Math.min(v.depth ?? 0, 12) * 14}px">${v.depth ? '<span class="nest-mark" aria-label="Nested call">↳</span>' : ""}<i class="swatch" style="background:${color(v.category)}"></i><button class="select-span ${v.status === "failed" ? "bad" : ""}" data-detail="${escape(v.id)}" title="${escape(v.label)}">${escape(v.label)}</button>${v.status === "failed" ? '<span class="bad">!</span>' : ""}</div><div class="trace-duration">${spanMeasure(v)}</div><div class="trace-lane"><button class="bar ${v.status === "running" ? "running" : ""}" style="left:${left === 100 ? "calc(100% - 2px)" : left + "%"};width:${Math.min(100 - left, width)}%;background-color:${color(v.category)}" data-detail="${escape(v.id)}" title="${escape(v.label)} · ${spanMeasure(v)}" aria-label="${escape(v.label)} ${spanMeasure(v)}"></button></div></div>`;
    })
    .join("");
  const breakdown = (d.summary.breakdown ?? []).filter((b) => b.ms > 0);
  return `<div class="timeline-layout"><nav class="turn-navigator" aria-label="Turns"><div class="turn-nav-heading">TURNS <span>${d.turns.length}</span></div><div class="turn-list" data-key="turn-list">${turns}</div><div class="turn-nav-hint">↑ ↓ to change turn</div></nav><section class="turn-timeline" aria-label="Turn ${index + 1} timeline"><header class="turn-heading"><div class="turn-heading-line"><span>TURN ${index + 1} <span class="muted">/ ${d.turns.length}</span></span><span>${duration(turn.end - turn.start)}</span></div><button id="inspect-turn-request" title="Read the recorded request">${escape(turn.label || "Request not recorded")} <span aria-hidden="true">↗</span></button><div class="turn-time-breakdown" aria-label="Turn time breakdown">${breakdown.map((b) => `<span><i class="swatch" style="background:${color(b.category)}"></i>${escape(labels[b.category] ?? b.category)} ${duration(b.ms)}</span>`).join("")}</div></header><div class="trace-scroller" data-key="waterfall"><div style="min-width:${Math.max(600, 600 * state.zoom)}px"><div class="trace-row ruler"><div class="trace-label">OPERATION</div><div class="trace-duration">TIME / USAGE</div><div class="trace-lane">${[0, 0.2, 0.4, 0.6, 0.8, 1].map((n) => `<span>${duration(range * n)}</span>`).join("")}</div></div>${rows || '<div class="empty">No recorded activity matches this filter.</div>'}</div></div><div class="trace-footer"><span>${state.category ? escape(labels[state.category] ?? state.category) : "All activity"} · time from turn start</span><span>${spans.length ? d.offset + 1 : 0}–${d.offset + spans.length} / ${d.total} spans ${d.previousOffset != null ? '<button id="previous-spans">← Earlier</button>' : ""} ${d.nextOffset != null ? '<button id="next-spans">Later →</button>' : ""}</span></div></section></div>`;
}
