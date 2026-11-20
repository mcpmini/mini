import { render } from "./vendor/preact.js";
import { useEffect, useState } from "./vendor/preact-hooks.js";
import { getServer, listServers, startSession } from "./api.js";
import { errorMessage, pollDelay, toggled } from "./model.js";
import { html, ServerHeader, StatusNotice, ToolList } from "./views.js";

function App() {
  const [servers, setServers] = useState(null);
  const [error, setError] = useState(null);
  useEffect(() => {
    startSession().then(listServers).then(setServers).catch(setError);
  }, []);
  if (error) return html`<p class="notice notice-failed" role="alert">${errorMessage(error)}</p>`;
  if (!servers) return html`<p class="muted">Loading…</p>`;
  return servers.map((s) => html`<${ServerPage} key=${s.name} name=${s.name} onError=${setError} />`);
}

function ServerPage({ name, onError }) {
  const server = usePolledServer(name, onError);
  const [expanded, setExpanded] = useState(() => new Set());
  if (!server) return html`<p class="muted">Loading ${name}…</p>`;
  const toggle = (tool) => setExpanded((names) => toggled(names, tool));
  return html`<section class="server">
    <${ServerHeader} server=${server} />
    ${server.state !== "ready" && html`<${StatusNotice} server=${server} />`}
    ${server.state === "ready" && html`<${ToolList} server=${server} expanded=${expanded} onToggle=${toggle} />`}
  </section>`;
}

function usePolledServer(name, onError) {
  const [server, setServer] = useState(null);
  useEffect(() => {
    let stopped = false;
    let timer;
    const load = () =>
      getServer(name)
        .then((view) => {
          if (stopped) return;
          setServer(view);
          const delay = pollDelay(view.state);
          if (delay !== null) timer = setTimeout(load, delay);
        })
        .catch((err) => stopped || onError(err));
    load();
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [name]);
  return server;
}

render(html`<${App} />`, document.getElementById("app"));
