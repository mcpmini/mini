import { test } from "node:test";
import assert from "node:assert/strict";
import { html, ServerHeader, ToolList } from "../assets/views.js";

const hostile = '<img src=x onerror="alert(1)">';

// The views under test use no hooks, so their function components can be expanded by calling them.
function expand(node) {
  if (Array.isArray(node)) return node.flatMap(expand);
  if (node === null || node === undefined || typeof node === "boolean") return [];
  if (typeof node !== "object") return [node];
  if (typeof node.type === "function") return expand(node.type(node.props));
  return [node, ...expand(node.props.children)];
}

function renderTree(vnode) {
  const nodes = expand(vnode);
  return {
    texts: nodes.filter((n) => typeof n === "string"),
    elements: nodes.filter((n) => typeof n === "object"),
  };
}

test("ToolList renders upstream text as text nodes, never as HTML", () => {
  const server = { name: "linear", tools: [{ name: hostile, title: hostile }] };
  const { texts, elements } = renderTree(html`<${ToolList} server=${server} />`);
  assert.equal(texts.filter((t) => t === hostile).length, 2, "the name and title should appear as text");
  assert.equal(elements.filter((e) => e.type === "img").length, 0);
  assert.equal(elements.filter((e) => "dangerouslySetInnerHTML" in e.props).length, 0);
});

test("ToolList marks hidden tools", () => {
  const server = { name: "linear", tools: [{ name: "a", hidden: true }, { name: "b" }] };
  const { elements } = renderTree(html`<${ToolList} server=${server} />`);
  const rows = elements.filter((e) => e.props.class === "tool-row");
  const hiddenBadges = elements.filter((e) => e.props.class === "badge badge-hidden");
  assert.equal(rows.length, 2);
  assert.equal(hiddenBadges.length, 1);
});

test("ServerHeader labels the state in words", () => {
  const { texts } = renderTree(html`<${ServerHeader} server=${{ name: "linear", state: "needs_auth" }} />`);
  assert.deepEqual(texts, ["linear", "needs login"]);
});

function listOf(tools, open) {
  const server = { name: "linear", tools };
  return renderTree(html`<${ToolList} server=${server} expanded=${new Set(open)} onToggle=${() => {}} />`);
}

test("ToolList toggles a row by its tool's name", () => {
  let toggled = null;
  const server = { name: "linear", tools: [{ name: "a" }, { name: "b" }] };
  const { elements } = renderTree(html`<${ToolList} server=${server} expanded=${new Set()} onToggle=${(n) => (toggled = n)} />`);
  elements.filter((e) => e.props.class === "tool-row")[1].props.onClick();
  assert.equal(toggled, "b");
});

test("ToolList expands only the open rows, and says so to assistive tech", () => {
  const { elements } = listOf([{ name: "a" }, { name: "b" }, { name: "c" }], ["a", "c"]);
  const rows = elements.filter((e) => e.props.class === "tool-row");
  const panels = elements.filter((e) => e.props.class === "tool-details");
  assert.deepEqual(rows.map((r) => r.props["aria-expanded"]), ["true", "false", "true"]);
  assert.deepEqual(panels.map((p) => p.props.id), [rows[0].props["aria-controls"], rows[2].props["aria-controls"]]);
});

test("ToolList gives each server's panels their own ids", () => {
  const ids = ["linear", "github"].flatMap((name) => {
    const server = { name, tools: [{ name: "a" }] };
    const { elements } = renderTree(html`<${ToolList} server=${server} expanded=${new Set(["a"])} onToggle=${() => {}} />`);
    return elements.filter((e) => e.props.class === "tool-details").map((e) => e.props.id);
  });
  assert.equal(new Set(ids).size, 2, `panel ids ${ids} should differ between servers`);
});

function detailOf(tool) {
  return listOf([tool], [tool.name]);
}

test("An expanded row renders upstream text as text nodes, never as HTML", () => {
  const { texts, elements } = detailOf({
    name: "search",
    title: hostile,
    description: hostile,
    inputSchema: { properties: { q: { type: "string", description: hostile, enum: [hostile] } } },
  });
  assert.ok(texts.filter((t) => t.includes(hostile)).length >= 3, "hostile strings should appear as text");
  assert.equal(elements.filter((e) => e.type === "img").length, 0);
  assert.equal(elements.filter((e) => "dangerouslySetInnerHTML" in e.props).length, 0);
});

test("An expanded row shows the output schema only when the tool has one", () => {
  const headings = (tool) => detailOf(tool).elements.filter((e) => e.type === "h3").map((e) => e.props.children);
  assert.deepEqual(headings({ name: "a", inputSchema: {} }), ["Input"]);
  assert.deepEqual(headings({ name: "a", inputSchema: {}, outputSchema: { properties: {} } }), ["Input", "Output"]);
});

test("An expanded row lists each input field's name and description", () => {
  const { texts } = detailOf({
    name: "a",
    inputSchema: { properties: { query: { type: "string", description: "Search text" } } },
  });
  assert.ok(texts.includes("query"));
  assert.ok(texts.includes("Search text"));
});

test("An expanded row points to the JSON schema when it can't list a composed schema's fields", () => {
  const { texts } = detailOf({ name: "a", inputSchema: { allOf: [{ properties: { q: { type: "string" } } }] } });
  assert.ok(texts.includes("See the JSON schema."));
  assert.ok(!texts.includes("Takes no arguments."));
});
