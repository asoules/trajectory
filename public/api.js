/** Source-independent API client. All timestamps are milliseconds since epoch. */
export async function api(method, args = {}, signal) {
  const params = new URLSearchParams(
    Object.entries(args).filter(([, v]) => v !== undefined),
  );
  const response = await fetch(`/api/${method}?${params}`, {
    signal: signal
      ? AbortSignal.any([signal, AbortSignal.timeout(30000)])
      : AbortSignal.timeout(30000),
  });
  const value = await response
    .json()
    .catch(() => ({ error: `HTTP ${response.status}` }));
  if (!response.ok) throw new Error(value.error ?? `HTTP ${response.status}`);
  return value;
}

export async function post(method, body) {
  const response = await fetch(`/api/${method}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  if (!response.ok) throw new Error(value.error ?? `HTTP ${response.status}`);
  return value;
}
