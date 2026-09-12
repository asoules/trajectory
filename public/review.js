import { escape, duration, count } from "./format.js";

export function review(state) {
  const report = state.data.summary.review;
  const presentation = report?.presentation;
  if (!presentation)
    return '<p class="empty">This snapshot has no shared performance review. Rebuild and restart the server, or use Timeline to inspect its recorded operations.</p>';
  return `<div class="review-columns">${presentation.sections
    .map(
      (section) =>
        `<section class="review-section" aria-label="${escape(section.title)}"><div class="review-section-heading"><h2>${escape(section.title)}</h2></div>${section.id === "time" ? `<p class="time-accounting">${escape(presentation.timeAccounting)}</p>` : ""}<p class="review-note">${escape(section.note)}</p><ol class="opportunities">${section.items.map((item) => `<li data-key="${escape(item.id)}"><div class="opportunity-title"><span>${escape(item.title)}</span><strong>${escape(item.metric)}</strong></div><p class="opportunity-fact">${escape(item.detail)}</p><details><summary>Why investigate</summary><p>${escape(item.reason)}</p><p>${escape(item.action)}</p><div class="evidence-actions">${item.evidence.map((e) => `<button data-detail="${escape(e.spanId)}">${escape(e.label)} ↗</button>`).join("")}${item.turnId ? `<button data-review-turn="${escape(item.turnId)}">View turn in timeline ↗</button>` : ""}</div></details></li>`).join("") || `<li class="review-empty">${escape(section.empty)}</li>`}</ol></section>`,
    )
    .join("")}</div>
  ${report.largeOutputs?.length ? `<details class="supporting-evidence" data-key="large-outputs"><summary>Large tool results <span>${report.largeOutputCount} recorded · largest ${count(report.largeOutputs[0].characters)} characters</span></summary><p class="review-note">${escape(presentation.largeOutputsNote)}</p><ul class="supporting-list">${report.largeOutputs.map((r) => `<li><button data-detail="${escape(r.spanId)}">${escape(r.label)} ↗</button><span>${count(r.characters)} characters</span></li>`).join("")}</ul></details>` : ""}
  ${report.backgroundCount ? `<details class="supporting-evidence" data-key="background"><summary>Background services <span>${report.backgroundCount} likely services excluded from time rankings</span></summary><p class="review-note">${escape(presentation.backgroundNote)}</p><ul class="supporting-list">${report.background.map((r) => `<li><button data-detail="${escape(r.spanId)}">${escape(r.label)} ↗</button><span>${duration(r.durationMs)} lifetime</span></li>`).join("")}</ul></details>` : ""}`;
}
