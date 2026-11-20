const POLL_MS = { connecting: 1000, failed: 5000 };
const MAX_TYPE_DEPTH = 4;

export const EXPIRED_MESSAGE =
  "This page's session has ended. Run `mini auth <server>` to open it again.";
export const UNREACHABLE_MESSAGE =
  "mini isn't reachable. If it restarted, run `mini auth <server>` to open this page again.";

const STATE_LABELS = { ready: "ready", connecting: "connecting", needs_auth: "needs login", failed: "failed" };

export function stateLabel(server) {
  return STATE_LABELS[serverStatus(server).kind];
}

export function serverStatus(server) {
  switch (server.state) {
    case "ready":
      return { kind: "ready", message: "" };
    case "connecting":
      return { kind: "connecting", message: `Connecting to ${server.name}…` };
    case "needs_auth":
      return { kind: "needs_auth", message: `Run \`mini auth ${server.name}\` to log in again.` };
    case "failed":
      return { kind: "failed", message: text(server.error) || `mini couldn't connect to ${server.name}.` };
    default:
      return { kind: "failed", message: `${server.name} is in an unknown state.` };
  }
}

export function pollDelay(state) {
  return POLL_MS[state] ?? null;
}

export function errorMessage(err) {
  if (err?.code === "session_expired") return EXPIRED_MESSAGE;
  if (err instanceof TypeError) return UNREACHABLE_MESSAGE;
  return text(err?.message) || "Something went wrong.";
}

export function serverTools(server) {
  if (!Array.isArray(server.tools)) return [];
  return server.tools.filter((t) => isObject(t) && typeof t.name === "string").map(toolView);
}

function toolView(tool) {
  const annotations = isObject(tool.annotations) ? tool.annotations : {};
  return {
    name: tool.name,
    title: text(tool.title) || text(annotations.title),
    description: text(tool.description),
    annotations,
    inputSchema: tool.inputSchema,
    outputSchema: tool.outputSchema,
    hidden: tool.hidden === true,
  };
}

export function annotationBadges(a) {
  const badges = [];
  // MCP gives the destructive and idempotent hints meaning only for a tool that isn't read-only.
  const writes = a.readOnlyHint !== true;
  if (!writes) badges.push({ label: "read-only", tone: "safe" });
  if (writes && a.destructiveHint === true) badges.push({ label: "destructive", tone: "danger" });
  if (writes && a.idempotentHint === true) badges.push({ label: "idempotent", tone: "plain" });
  if (a.openWorldHint === true) badges.push({ label: "open world", tone: "plain" });
  return badges;
}

export function toggled(names, name) {
  const next = new Set(names);
  if (!next.delete(name)) next.add(name);
  return next;
}

const UNLISTED = ["allOf", "anyOf", "oneOf", "$ref", "patternProperties"];

// schemaFields reads only direct properties, so these schemas can still take arguments it doesn't list.
export function hasUnlistedFields(schema) {
  if (!isObject(schema)) return false;
  const open = schema.additionalProperties !== undefined && schema.additionalProperties !== false;
  return open || UNLISTED.some((key) => key in schema);
}

export function schemaFields(schema) {
  if (!isObject(schema) || !isObject(schema.properties)) return [];
  const required = new Set(Array.isArray(schema.required) ? schema.required : []);
  return Object.entries(schema.properties).map(([name, prop]) => schemaField(name, prop, required.has(name)));
}

function schemaField(name, prop, required) {
  const p = isObject(prop) ? prop : {};
  return {
    name,
    type: typeLabel(p, 0),
    required,
    description: text(p.description),
    values: allowedValues(p),
    defaultValue: "default" in p ? formatValue(p.default) : null,
  };
}

// An array of enum values lists them on its items.
function allowedValues(p) {
  const values = Array.isArray(p.enum) ? p.enum : isObject(p.items) && Array.isArray(p.items.enum) ? p.items.enum : [];
  return values.map(formatValue);
}

function typeLabel(prop, depth) {
  if (!isObject(prop)) return "any";
  if (depth > MAX_TYPE_DEPTH) return "…";
  if ("const" in prop) return formatValue(prop.const);
  if (Array.isArray(prop.type)) return prop.type.map((t) => primitiveLabel(prop, t, depth)).join(" | ");
  if (typeof prop.type === "string") return primitiveLabel(prop, prop.type, depth);
  const union = prop.anyOf ?? prop.oneOf;
  if (Array.isArray(union)) return union.map((p) => typeLabel(p, depth + 1)).join(" | ");
  if (Array.isArray(prop.enum)) return "enum";
  // Pydantic types a model-valued field as a $ref, wrapped in a one-element allOf when it adds a description.
  if (typeof prop.$ref === "string") return prop.$ref.split("/").pop() || "object";
  if (Array.isArray(prop.allOf) && prop.allOf.length === 1) return typeLabel(prop.allOf[0], depth + 1);
  return "any";
}

function primitiveLabel(prop, type, depth) {
  if (type !== "array") return String(type);
  const item = typeLabel(prop.items, depth + 1);
  return item.includes(" | ") ? `(${item})[]` : `${item}[]`;
}

function formatValue(value) {
  return JSON.stringify(value) ?? String(value);
}

export function formatSchema(schema) {
  return JSON.stringify(schema, null, 2);
}

function text(value) {
  return typeof value === "string" ? value : "";
}

function isObject(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
