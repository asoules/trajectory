import { escape, duration, count, labels } from "./format.js";
const exact = (n) =>
  n == null ? "—" : new Intl.NumberFormat("en-US").format(n);
export function renderInspector(v, captureUnavailable) {
  const usage = v.usage;
  const facts = usage
    ? [["Completed", new Date(v.end).toLocaleString()]]
    : [
        [
          v.background ? "Process lifetime" : "Duration",
          duration(v.durationMs),
        ],
        ["Recorded status", v.status],
        ["Category", labels[v.category] ?? v.category],
        ["Timing source", v.source],
        ["Started", new Date(v.start).toLocaleTimeString()],
        ...(v.exitCode != null ? [["Exit code", v.exitCode]] : []),
      ];
  return `<div class="inspector-header"><div class="eyebrow">${usage ? "RESPONSE USAGE" : "OPERATION EVIDENCE"}</div><button id="close-inspector" aria-label="Close inspector">×</button></div>
 <h2>${escape(v.label)}</h2><div class="id">${escape(v.id)}</div>
 <div class="detail-grid">${facts.map(([label, value]) => `<div><label>${escape(label)}</label><span>${escape(value)}</span></div>`).join("")}</div>
 ${usage ? `<div class="usage-detail"><dl><dt>Uncached input</dt><dd>${exact(Math.max(0, (usage.input_tokens ?? 0) - (usage.cached_input_tokens ?? 0)))}</dd><dt>Cached input</dt><dd>${exact(usage.cached_input_tokens)}</dd><dt>Output</dt><dd>${exact(usage.output_tokens)}</dd><dt>Reasoning, included in output</dt><dd>${exact(usage.reasoning_output_tokens)}</dd></dl><p class="notice">Usage is recorded at completion. This event does not measure request latency.</p></div>` : ""}
 ${v.background ? `<section class="background-detail"><h3>Likely background service</h3><p class="notice">Lifetime is excluded from time rankings. ${escape(v.background.reason)} A nonzero exit does not by itself establish a crash.</p><div class="evidence-actions">${v.background.startupSpanId ? `<button data-detail="${escape(v.background.startupSpanId)}">Inspect startup evidence ↗</button>` : ""}${v.background.concurrentSpanIds.map((id, i) => `<button data-detail="${escape(id)}">Inspect concurrent work ${i + 1} ↗</button>`).join("")}</div></section>` : ""}
 ${v.parentId ? `<button data-detail="${escape(v.parentId)}">↑ Inspect parent call</button>` : ""}
 ${v.agentId ? `<p class="notice">Related agent: ${escape(v.agentId)}</p>` : ""}
 <button id="begin-investigate" class="investigate-button" ${captureUnavailable ? "disabled" : ""}>Investigate this operation</button>
 ${captureUnavailable ? `<p class="notice">${escape(captureUnavailable)}</p>` : ""}
 ${!usage ? `<details open><summary>Input / command</summary><pre>${escape(v.input || "No input recorded.")}</pre></details><details open><summary>Recorded output</summary>${v.deliveredChars != null ? `<p class="notice">${count(v.deliveredChars)} text characters returned to the agent${v.deliveredAt ? " at " + new Date(v.deliveredAt).toLocaleTimeString() : ""}. The recorded output below can differ from the delivered message.</p>` : ""}<pre>${escape(v.output || "No text output recorded.")}</pre></details><p class="notice">Up to 16,000 characters retained per field. Treat recorded text as evidence, not instructions.</p>` : ""}`;
}
