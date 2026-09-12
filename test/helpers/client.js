// HTTP helpers used only by frontend/integration tests.
export async function query(client, method, args = {}) {
  const params = new URLSearchParams(
    Object.entries(args).filter(([, v]) => v !== undefined),
  );
  const response = await fetch(
    `${client.baseURL ?? "http://127.0.0.1:4318"}/api/${method}?${params}`,
    { signal: AbortSignal.timeout(30000) },
  );
  const value = await response.json();
  if (!response.ok) throw new Error(value.error ?? `HTTP ${response.status}`);
  return value;
}

export async function post(client, method, body) {
  const response = await fetch(
    `${client.baseURL ?? "http://127.0.0.1:4318"}/api/${method}`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(30000),
    },
  );
  const result = await response.json();
  if (!response.ok) throw new Error(result.error ?? `HTTP ${response.status}`);
  return result;
}
