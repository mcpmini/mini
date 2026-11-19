import { h } from "./vendor/preact.js";
import htm from "./vendor/htm.js";
import {
  annotationBadges,
  formatSchema,
  isComposed,
  schemaFields,
  serverStatus,
  serverTools,
  stateLabel,
} from "./model.js";

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

export function ToolList({ server, expanded = new Set(), onToggle }) {
  const tools = serverTools(server);
  if (tools.length === 0) return html`<p class="muted">${server.name} offers no tools.</p>`;
  return html`<ul class="tool-list">
    ${tools.map((tool, i) => html`<li key=${tool.name}>
      <${ToolRow} tool=${tool} panelID=${`tool-${i}`} open=${expanded.has(tool.name)} onToggle=${onToggle} />
    </li>`)}
  </ul>`;
}

function ToolRow({ tool, panelID, open, onToggle }) {
  return html`<button
      type="button"
      class="tool-row"
      aria-expanded=${String(open)}
      aria-controls=${panelID}
      onClick=${() => onToggle(tool.name)}
    >
    <span class="tool-name">${tool.name}</span>
    ${tool.hidden && html`<span class="badge badge-hidden">hidden</span>`}
    <${Badges} annotations=${tool.annotations} />
    ${tool.title && html`<span class="tool-title">${tool.title}</span>`}
    </button>
    ${open && html`<${ToolDetails} id=${panelID} tool=${tool} />`}`;
}

// tool is one entry of serverTools(server), so every text field is already a string.
function ToolDetails({ id, tool }) {
  return html`<div class="tool-details" id=${id}>
    ${tool.description && html`<p class="description">${tool.description}</p>`}
    <${SchemaSection} title="Input" schema=${tool.inputSchema} empty="Takes no arguments." />
    ${tool.outputSchema && html`<${SchemaSection} title="Output" schema=${tool.outputSchema} empty="No fields." />`}
  </div>`;
}

function SchemaSection({ title, schema, empty }) {
  const fields = schemaFields(schema);
  return html`<section class="schema">
    <h3>${title}</h3>
    ${fields.length > 0
      ? html`<${FieldList} fields=${fields} />`
      : html`<p class="muted">${isComposed(schema) ? "See the JSON schema." : empty}</p>`}
    ${schema !== undefined && html`<details>
      <summary>JSON schema</summary>
      <pre>${formatSchema(schema)}</pre>
    </details>`}
  </section>`;
}

function FieldList({ fields }) {
  return html`<dl class="fields">
    ${fields.map((f) => html`<${Field} key=${f.name} field=${f} />`)}
  </dl>`;
}

function Field({ field }) {
  return html`<dt>
      <code class="field-name">${field.name}</code>
      <span class="field-type">${field.type}</span>
      ${field.required && html`<span class="badge badge-plain">required</span>`}
    </dt>
    <dd>
      ${field.description && html`<p class="description">${field.description}</p>`}
      ${field.values.length > 0 && html`<p class="muted">One of: <code>${field.values.join(", ")}</code></p>`}
      ${field.defaultValue !== null && html`<p class="muted">Default: <code>${field.defaultValue}</code></p>`}
    </dd>`;
}
