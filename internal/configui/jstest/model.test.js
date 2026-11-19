import { test } from "node:test";
import assert from "node:assert/strict";
import {
  annotationBadges,
  errorMessage,
  EXPIRED_MESSAGE,
  pollDelay,
  serverStatus,
  serverTools,
  stateLabel,
  UNREACHABLE_MESSAGE,
} from "../assets/model.js";

test("serverStatus maps each API state to what the page shows", () => {
  const cases = [
    [{ name: "linear", state: "ready" }, { kind: "ready", message: "" }],
    [{ name: "linear", state: "connecting" }, { kind: "connecting", message: "Connecting to linear…" }],
    [{ name: "linear", state: "needs_auth" }, { kind: "needs_auth", message: "Run `mini auth linear` to log in again." }],
    [{ name: "linear", state: "failed", error: "missing LINEAR_KEY" }, { kind: "failed", message: "missing LINEAR_KEY" }],
    [{ name: "linear", state: "failed" }, { kind: "failed", message: "mini couldn't connect to linear." }],
    [{ name: "linear", state: "rebooting" }, { kind: "failed", message: "linear is in an unknown state." }],
  ];
  for (const [server, want] of cases) assert.deepEqual(serverStatus(server), want, server.state);
});

test("stateLabel reads as words, and an unknown state as failed", () => {
  assert.equal(stateLabel({ name: "linear", state: "needs_auth" }), "needs login");
  assert.equal(stateLabel({ name: "linear", state: "rebooting" }), "failed");
});

test("pollDelay polls connecting every second, failed every five, and stops otherwise", () => {
  assert.equal(pollDelay("connecting"), 1000);
  assert.equal(pollDelay("failed"), 5000);
  assert.equal(pollDelay("ready"), null);
  assert.equal(pollDelay("needs_auth"), null);
});

test("errorMessage tells an expired session apart from an unreachable daemon", () => {
  assert.equal(errorMessage({ code: "session_expired", message: "server text" }), EXPIRED_MESSAGE);
  assert.equal(errorMessage(new TypeError("Failed to fetch")), UNREACHABLE_MESSAGE);
  assert.equal(errorMessage(new Error("server linear not found")), "server linear not found");
});

test("serverTools drops malformed entries and coerces untrusted fields to safe types", () => {
  const tools = serverTools({
    tools: [
      { name: "a", description: { html: "<b>x</b>" }, title: 7, hidden: "yes", annotations: [] },
      { title: "no name" },
      null,
      { name: "b", annotations: { title: "From annotations" }, hidden: true },
    ],
  });
  assert.deepEqual(tools.map((t) => [t.name, t.title, t.hidden]), [
    ["a", "", false],
    ["b", "From annotations", true],
  ]);
  assert.deepEqual(tools[0].annotations, {});
});

test("serverTools prefers the tool title over the annotation title", () => {
  const [tool] = serverTools({ tools: [{ name: "a", title: "Tool", annotations: { title: "Annotation" } }] });
  assert.equal(tool.title, "Tool");
});

test("serverTools returns nothing when the view has no tool list", () => {
  assert.deepEqual(serverTools({ name: "linear", state: "connecting" }), []);
});

test("annotationBadges shows only hints set to true, and write hints only when not read-only", () => {
  const labels = (a) => annotationBadges(a).map((b) => b.label);
  assert.deepEqual(labels({}), []);
  assert.deepEqual(labels({ readOnlyHint: true, destructiveHint: true, idempotentHint: true }), ["read-only"]);
  assert.deepEqual(labels({ destructiveHint: true, idempotentHint: true, openWorldHint: true }), [
    "destructive",
    "idempotent",
    "open world",
  ]);
  assert.deepEqual(labels({ readOnlyHint: "true", openWorldHint: false }), []);
});
