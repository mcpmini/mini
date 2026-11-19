const app = document.getElementById("app");
const bearerKey = "mini-bearer";

async function api(path, options = {}) {
  const headers = { Authorization: "Bearer " + (sessionStorage.getItem(bearerKey) || "") };
  if (options.body) headers["Content-Type"] = "application/json";
  const resp = await fetch(path, { ...options, headers });
  if (resp.status === 401) throw new Error("expired");
  if (!resp.ok) throw new Error((await resp.json()).message);
  return resp.json();
}

async function startSession() {
  const entry = new URLSearchParams(location.hash.slice(1)).get("t");
  if (!entry) return;
  history.replaceState(null, "", location.pathname);
  const resp = await fetch("/api/session", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ entry }),
  });
  if (!resp.ok) throw new Error("expired");
  sessionStorage.setItem(bearerKey, (await resp.json()).bearer);
}

function line(tag, text) {
  const el = document.createElement(tag);
  el.textContent = text;
  return el;
}

async function render() {
  const servers = await api("/api/servers");
  const views = await Promise.all(servers.map((s) => api("/api/server?server=" + encodeURIComponent(s.name))));
  app.replaceChildren(...views.flatMap((v) => [
    line("h1", v.name + " (" + v.state + ")"),
    ...(v.error ? [line("p", v.error)] : []),
    ...v.tools.map((t) => line("p", t.name + (t.hidden ? " (hidden)" : ""))),
  ]));
  if (views.some((v) => v.state === "connecting")) setTimeout(() => render().catch(show), 1000);
}

function show(err) {
  const text = err.message === "expired"
    ? "This page's session has ended. Run `mini auth <server>` to open it again."
    : err.message;
  app.replaceChildren(line("p", text));
}

startSession().then(render).catch(show);
