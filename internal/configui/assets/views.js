import { h } from "./vendor/preact.js";
import htm from "./vendor/htm.js";
import { annotationBadges, serverStatus, serverTools, stateLabel } from "./model.js";

export const html = htm.bind(h);

export function StatusNotice({ server }) {
  const status = serverStatus(server);
  return html`<p class="notice notice-${status.kind}" role="status">${status.message}</p>`;
}

export function ServerHeader({ server }) {
  return html`<header class="server-header">
    <h1>${server.name}</h1>
    <span class="state state-${serverStatus(server).kind}">${stateLabel(server)}</span>
  </header>`;
}

export function Badges({ annotations }) {
  return annotationBadges(annotations).map(
    (b) => html`<span class="badge badge-${b.tone}">${b.label}</span>`,
  );
}

export function ToolList({ server }) {
  const tools = serverTools(server);
  if (tools.length === 0) return html`<p class="muted">${server.name} offers no tools.</p>`;
  return html`<ul class="tool-list">
    ${tools.map((tool) => html`<li key=${tool.name}><${ToolRow} tool=${tool} /></li>`)}
  </ul>`;
}

function ToolRow({ tool }) {
  return html`<div class="tool-row">
    <span class="tool-name">${tool.name}</span>
    ${tool.hidden && html`<span class="badge badge-hidden">hidden</span>`}
    <${Badges} annotations=${tool.annotations} />
    ${tool.title && html`<span class="tool-title">${tool.title}</span>`}
  </div>`;
}
