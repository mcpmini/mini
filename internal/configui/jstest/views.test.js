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
