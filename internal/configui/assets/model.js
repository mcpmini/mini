const POLL_MS = { connecting: 1000, failed: 5000 };

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
    annotations,
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

function text(value) {
  return typeof value === "string" ? value : "";
}

function isObject(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
