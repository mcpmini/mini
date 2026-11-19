const BEARER_KEY = "mini-bearer";

export class APIError extends Error {
  constructor(body) {
    super(body?.message || "Request failed.");
    this.code = body?.error;
  }
}

export async function startSession() {
  const entry = new URLSearchParams(location.hash.slice(1)).get("t");
  if (!entry) return;
  history.replaceState(null, "", location.pathname);
  const resp = await fetch("/api/session", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ entry }),
  });
  if (!resp.ok) throw new APIError({ error: "session_expired" });
  sessionStorage.setItem(BEARER_KEY, (await resp.json()).bearer);
}

async function getJSON(path) {
  const bearer = sessionStorage.getItem(BEARER_KEY) || "";
  const resp = await fetch(path, { headers: { Authorization: "Bearer " + bearer } });
  if (resp.status === 401) throw new APIError({ error: "session_expired" });
  const body = await resp.json().catch(() => null);
  if (!resp.ok) throw new APIError(body);
  return body;
}

export function listServers() {
  return getJSON("/api/servers");
}

export function getServer(name) {
  return getJSON("/api/server?server=" + encodeURIComponent(name));
}
