export const escape = (s) =>
  String(s ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
export const duration = (ms) =>
  ms < 1000
    ? `${Math.round(ms)}ms`
    : ms < 60000
      ? `${(ms / 1000).toFixed(ms < 10000 ? 1 : 0)}s`
      : ms < 3600000
        ? `${Math.floor(Math.round(ms / 1000) / 60)}m ${Math.round(ms / 1000) % 60}s`
        : `${Math.floor(ms / 3600000)}h ${Math.floor((ms % 3600000) / 60000)}m`;
export const count = (n) =>
  n == null
    ? "—"
    : new Intl.NumberFormat("en", {
        notation: "compact",
        maximumFractionDigits: 1,
      }).format(n);
export const labels = {
  model: "Model usage",
  tests: "Tests",
  build: "Build",
  reasoning: "Reasoning",
  response: "Response",
  tools: "Tools",
  shell: "Shell",
  edit: "Edits",
  wait: "Waiting",
  compaction: "Compaction",
  unobserved: "Unobserved",
};
export const color = (c) => `var(--${Object.hasOwn(labels, c) ? c : "tools"})`;

export const spanMeasure = (v) =>
  v.usage
    ? v.usage.input_tokens == null || v.usage.output_tokens == null
      ? "usage"
      : `${count(Math.max(0, v.usage.input_tokens - (v.usage.cached_input_tokens ?? 0)) + v.usage.output_tokens)} tok`
    : duration(v.durationMs ?? v.end - v.start);
