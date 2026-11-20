import { test } from "node:test";
import assert from "node:assert/strict";
import {
  annotationBadges,
  errorMessage,
  EXPIRED_MESSAGE,
  pollDelay,
  hasUnlistedFields,
  schemaFields,
  serverStatus,
  serverTools,
  stateLabel,
  toggled,
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
  assert.deepEqual(tools.map((t) => [t.name, t.title, t.description, t.hidden]), [
    ["a", "", "", false],
    ["b", "From annotations", "", true],
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

test("toggled opens a closed name and closes an open one, leaving the others", () => {
  const open = toggled(new Set(["a"]), "b");
  assert.deepEqual([...open], ["a", "b"]);
  assert.deepEqual([...toggled(open, "a")], ["b"]);
});

test("schemaFields lists an enum array's allowed values", () => {
  const [field] = schemaFields({ properties: { tags: { type: "array", items: { enum: ["a", "b"] } } } });
  assert.deepEqual(field.values, ['"a"', '"b"']);
});

test("hasUnlistedFields flags schemas whose fields schemaFields can't list", () => {
  assert.equal(hasUnlistedFields({ allOf: [{ properties: { q: {} } }] }), true);
  assert.equal(hasUnlistedFields({ type: "object", additionalProperties: { type: "string" } }), true);
  assert.equal(hasUnlistedFields({ type: "object", patternProperties: { "^x": {} } }), true);
  assert.equal(hasUnlistedFields({ type: "object", properties: {}, additionalProperties: false }), false);
  assert.equal(hasUnlistedFields({ type: "object", properties: {} }), false);
});

test("schemaFields lists each property with its type, requirement, description, values and default", () => {
  const fields = schemaFields({
    type: "object",
    required: ["query"],
    properties: {
      query: { type: "string", description: "Search text" },
      state: { type: "string", enum: ["open", "closed"], default: "open" },
      limit: { type: ["integer", "null"] },
    },
  });
  assert.deepEqual(fields, [
    { name: "query", type: "string", required: true, description: "Search text", values: [], defaultValue: null },
    { name: "state", type: "string", required: false, description: "", values: ['"open"', '"closed"'], defaultValue: '"open"' },
    { name: "limit", type: "integer | null", required: false, description: "", values: [], defaultValue: null },
  ]);
});

test("schemaFields labels arrays, unions, consts and untyped properties", () => {
  const types = (properties) => schemaFields({ properties }).map((f) => f.type);
  assert.deepEqual(
    types({
      labels: { type: "array", items: { type: "string" } },
      ids: { type: "array", items: { anyOf: [{ type: "string" }, { type: "integer" }] } },
      anything: { type: "array" },
      either: { oneOf: [{ type: "string" }, { type: "null" }] },
      kind: { const: "issue" },
      choice: { enum: [1, 2] },
      ref: { $ref: "#/$defs/Address" },
      described: { allOf: [{ $ref: "#/$defs/Address" }], description: "Where to ship" },
      optional: { anyOf: [{ $ref: "#/$defs/Address" }, { type: "null" }] },
      broken: "not a schema",
    }),
    ["string[]", "(string | integer)[]", "any[]", "string | null", '"issue"', "enum", "Address", "Address", "Address | null", "any"],
  );
});

test("schemaFields stops descending into deeply nested item types", () => {
  let prop = { type: "string" };
  for (let i = 0; i < 10; i++) prop = { type: "array", items: prop };
  const [field] = schemaFields({ properties: { deep: prop } });
  assert.equal(field.type, "…[][][][][]");
});

test("schemaFields returns no fields for a schema without properties", () => {
  assert.deepEqual(schemaFields(undefined), []);
  assert.deepEqual(schemaFields({ type: "object" }), []);
  assert.deepEqual(schemaFields({ properties: ["a"] }), []);
});

test("schemaFields ignores a non-string description", () => {
  const [field] = schemaFields({ properties: { a: { type: "string", description: ["<img>"] } } });
  assert.equal(field.description, "");
});
