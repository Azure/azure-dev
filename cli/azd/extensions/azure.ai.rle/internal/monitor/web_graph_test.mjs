// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import test from "node:test";
import assert from "node:assert/strict";
import { buildGraph, GRAPH_PAGE_SIZE, sequenceLabel } from "./web/data.mjs";

const turns = (...ids) => ids.map((node_id, index) => ({ node_id, index }));
const path = (node_ids, role = "agent") => ({ node_ids, role });
const pairs = (graph) => graph.edges.map((edge) => [edge.from, edge.to]);

test("builds branching paths from shared prefixes with unique nodes and edges", () => {
  const input = { turns: turns("a", "b", "c", "d"), sequences: [
    path(["a", "b", "c"]), path(["a", "b", "d"]), path(["a", "b", "d"]),
  ] };
  const before = JSON.stringify(input);
  const graph = buildGraph(input);
  assert.equal(graph.nodes.length, 4);
  assert.deepEqual(pairs(graph), [["id:a", "id:b"], ["id:b", "id:c"], ["id:b", "id:d"]]);
  assert.deepEqual(graph.nodes.map((node) => node.layer), [0, 1, 2, 2]);
  assert.deepEqual(graph.nodes[1].memberships, [0, 1, 2]);
  assert.equal(graph.nodes[2].y, graph.nodes[3].y);
  assert.notEqual(graph.nodes[2].x, graph.nodes[3].x);
  assert.equal(graph.notices.length, 0);
  assert.equal(JSON.stringify(input), before);
});

test("multiple roots remain separate, explicitly discarded calls remain visible", () => {
  const graph = buildGraph({ turns: turns("a", "b", "x", "y").map(turn => ({ ...turn, discarded: turn.node_id === "y" })),
    sequences: [path(["a", "b"]), path(["x", "y"], "discarded")] });
  assert.deepEqual(graph.nodes.filter((node) => node.root).map((node) => node.id), ["a", "x"]);
  assert.deepEqual(pairs(graph), [["id:a", "id:b"], ["id:x", "id:y"]]);
  assert.equal(graph.nodes.find((node) => node.id === "y").discarded, true);
});

test("sequence role does not invent a call-level discarded flag", () => {
  const graph = buildGraph({ turns: turns("a", "b", "c"),
    sequences: [path(["a", "b"]), path(["a", "c"], "discarded")] });
  assert.equal(graph.nodes.find((node) => node.id === "a").discarded, false);
  assert.equal(graph.nodes.find((node) => node.id === "c").discarded, false);
});

test("root_id and arrival order cannot manufacture edges or semantic labels", () => {
  const graph = buildGraph({ turns: [
    { node_id: "b", root_id: "a", index: 42, n_tools: 8 }, { node_id: "a", root_id: "a", index: 0 },
  ] });
  assert.deepEqual(graph.edges, []);
  assert.equal(graph.nodes.some((node) => node.root), false);
  assert.match(graph.notices.join(" "), /shown unconnected/);
  assert.deepEqual(graph.nodes.map((node) => node.label), ["Model call 1", "Model call 2"]);
  assert.doesNotMatch(graph.nodes.map((node) => node.label).join(" "), /system|prompt|tool|subagent|compaction|user/i);
});

test("a single call has an intentional rooted layout when supported by a sequence", () => {
  const graph = buildGraph({ turns: turns("a"), sequences: [path(["a"])] });
  assert.equal(graph.nodes[0].root, true);
  assert.equal(graph.nodes[0].x + 110, graph.width / 2);
  assert.equal(graph.height, 300);
  assert.equal(graph.edges.length, 0);
});

test("missing turn records preserve references and explicitly identify missing detail", () => {
  const graph = buildGraph({ turns: turns("a"), sequences: [path(["a", "missing"])] });
  const missing = graph.nodes.find((node) => node.id === "missing");
  assert.equal(missing.records.length, 0);
  assert.equal(missing.label, "Missing call details");
  assert.equal(graph.edges.length, 1);
  assert.match(graph.notices.join(" "), /no captured call details/);
});

test("duplicate IDs retain all records and do not silently choose one", () => {
  const graph = buildGraph({ turns: [{ node_id: "a", n_sampled: 5 }, { node_id: "a", n_sampled: 8 }],
    sequences: [path(["a"])] });
  assert.equal(graph.nodes.length, 1);
  assert.equal(graph.nodes[0].records.length, 2);
  assert.equal(graph.nodes[0].label, "Ambiguous model call");
  assert.match(graph.notices.join(" "), /duplicate node ID/);
});

test("absent call IDs remain distinct and cannot collide with captured string IDs", () => {
  const graph = buildGraph({ turns: [{}, {}, { node_id: "turn:0" }] });
  assert.equal(graph.nodes.length, 3);
  assert.equal(new Set(graph.nodes.map((node) => node.key)).size, 3);
  assert.equal(graph.edges.length, 0);
  assert.equal(graph.nodes[0].id, null);
});

test("cycles, repeated IDs, and conflicting roots are explicit and layout terminates", () => {
  const graph = buildGraph({ turns: turns("a", "b", "c"),
    sequences: [path(["a", "b", "a", "c"])] });
  assert.match(graph.notices.join(" "), /cycle/);
  assert.match(graph.notices.join(" "), /Repeated node IDs/);
  assert.match(graph.notices.join(" "), /conflicting root/);
  assert.equal(graph.nodes.length, 3);
  assert.equal(graph.nodes.every((node) => node.unresolved), true);
  assert.equal(graph.nodes.every((node) => Number.isFinite(node.x) && Number.isFinite(node.y)), true);
});

test("an invalid path element breaks adjacency instead of joining across the gap", () => {
  const graph = buildGraph({ turns: turns("a", "b", "c"),
    sequences: [path(["a", null, "b", "c"]), null, {}] });
  assert.deepEqual(pairs(graph), [["id:b", "id:c"]]);
  assert.equal(graph.nodes.find(node => node.id === "b").root, false);
  assert.match(graph.notices.join(" "), /paths are missing or invalid/);
});

test("graphs are bounded and pages explicitly disclose cross-window edges", () => {
  const ids = Array.from({ length: 1000 }, (_, index) => `node-${index}`);
  const graph = buildGraph({ turns: turns(...ids), sequences: [path(ids)] });
  assert.equal(graph.nodes.length, GRAPH_PAGE_SIZE);
  assert.equal(graph.edges.length, GRAPH_PAGE_SIZE - 1);
  assert.equal(graph.total, 1000);
  assert.match(graph.notices.join(" "), /Cross-window edges are not drawn/);
  const last = buildGraph({ turns: turns(...ids), sequences: [path(ids)] }, { page: 999 });
  assert.equal(last.page, last.pageCount - 1);
  assert.equal(last.nodes.at(-1).id, "node-999");
  assert.ok(last.height < 13000);
});

test("sequence scope shows only its real path, preserving original call arrival labels", () => {
  const graph = buildGraph({ turns: turns("a", "b", "x", "y"),
    sequences: [path(["a", "b"]), path(["x", "y"], "auxiliary")] }, { sequenceIndex: 1 });
  assert.deepEqual(graph.nodes.map((node) => node.id), ["x", "y"]);
  assert.deepEqual(pairs(graph), [["id:x", "id:y"]]);
  assert.equal(graph.nodes[0].label, "Model call 3");
  assert.deepEqual(graph.nodes[0].memberships, [1]);
});

test("sequence labels use role and path without inventing names and bound long IDs", () => {
  assert.match(sequenceLabel(path(["a", "b"], "auxiliary"), 2), /Sequence 3 · auxiliary · a → b/);
  assert.match(sequenceLabel({}, 0), /role not reported · path not reported/);
  assert.ok(sequenceLabel(path(["a".repeat(10000)]), 0).length < 100);
});

test("chart links can focus calls beyond the first graph page without unbounding the graph", () => {
  const ids = Array.from({ length: 300 }, (_, index) => `node-${index}`);
  const graph = buildGraph({ turns: turns(...ids), sequences: [path(ids)] },
    { focusKey: "id:node-250" });
  assert.equal(graph.page, 3);
  assert.equal(graph.nodes.some((node) => node.id === "node-250"), true);
  assert.ok(graph.nodes.length <= GRAPH_PAGE_SIZE);
  const noID = buildGraph({ turns: Array.from({ length: 90 }, () => ({})) }, { focusKey: "turn:85" });
  assert.equal(noID.page, 1);
  assert.equal(noID.nodes.some((node) => node.key === "turn:85"), true);
});
