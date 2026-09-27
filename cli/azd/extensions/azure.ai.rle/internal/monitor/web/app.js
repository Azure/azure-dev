// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import {
  buildGraph, chartGeometry, chartHoverAt, chartPath, chartScales, fetchRolloutIndex, fetchRunLog, fetchRunMetrics,
  fetchRunOverview, fetchSnapshot, isNumber, mapSnapshot, present, rewardGeometry,
  runCharts, runFacts, runHeadline, runWarnings, sequenceData, sequenceLabel, sequencePage, TOKEN_PAGE_SIZE,
  toolCalls, toolCallSummary, withGroupSignal,
} from "./data.mjs";

const byID = (id) => document.getElementById(id);
const format = (value) => isNumber(value) ? String(value) : "Not reported";
const count = (value) => new Intl.NumberFormat().format(value);
const short = (value, length = 28) => String(value).length > length ? `${String(value).slice(0, length - 1)}…` : String(value);
let model;
let graphPage = 0;
let graphView;
let selectedNode = null;
let tokenPage = 0;
let tokenData = null;
let stepPage = 0;
let callPage = 0;
const CHART_PAGE_SIZE = 100;

function applyTheme(theme) {
  document.documentElement.dataset.theme = theme;
  const next = theme === "dark" ? "light" : "dark";
  byID("theme-toggle").textContent = `${next === "dark" ? "Dark" : "Light"} mode`;
  byID("theme-toggle").setAttribute("aria-label", `Switch to ${next} mode`);
}

applyTheme(window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
byID("theme-toggle").addEventListener("click", () => {
  const theme = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
  applyTheme(theme);
});

function element(tag, text, className) {
  const node = document.createElement(tag);
  if (text !== undefined) node.textContent = text;
  if (className) node.className = className;
  return node;
}

function svgElement(tag, attributes, text) {
  const node = document.createElementNS("http://www.w3.org/2000/svg", tag);
  for (const [name, value] of Object.entries(attributes)) node.setAttribute(name, String(value));
  if (text !== undefined) node.textContent = text;
  return node;
}

function json(container, value) {
  const pre = element("pre", JSON.stringify(value, null, 2));
  pre.tabIndex = 0;
  pre.setAttribute("aria-label", "JSON data");
  container.append(pre);
}

function lazyJSON(container, value) {
  container.replaceChildren();
  const details = container.closest("details");
  // Large token exports are materialized as text only when explicitly opened.
  details.ontoggle = () => {
    if (details.open && !container.childNodes.length) json(container, value);
  };
  if (details.open) json(container, value);
}

function fields(container, entries) {
  const list = element("dl", undefined, "field-list");
  for (const [label, value] of entries) {
    if (present(value)) list.append(element("dt", label), element("dd", String(value)));
  }
  if (list.children.length) container.append(list);
}

function notices(id, messages) {
  const container = byID(id);
  container.replaceChildren(...messages.map((message) => element("p", message)));
  container.hidden = !messages.length;
}

function metric(label, value) {
  const card = element("div", undefined, "metric");
  card.append(element("p", format(value), "metric-value"), element("h2", label));
  byID("metrics").append(card);
}

function displayRole(role) {
  return role === "assistant" ? "Assistant" :
    role === "system" ? "System" :
      role === "user" ? "User" :
        role === "tool" ? "Tool" :
          role === "request" ? "Request" : "Other";
}

function renderActivitySummary() {
  const hasTimeline = model.turns?.some((turn) => turn.flow.length) ?? false;
  byID("activity-summary").hidden = !model.hasConversation && !hasTimeline;
  byID("tool-activity-count").textContent = model.toolActivity.count === 0
    ? "No tool calls captured"
    : `${count(model.toolActivity.count)} tool call${model.toolActivity.count === 1 ? "" : "s"} across model responses`;
  const names = byID("tool-activity-names");
  names.replaceChildren();
  for (const name of model.toolActivity.names) names.append(element("code", name, "tool-name"));
  if (model.toolActivity.count > 0 && !model.toolActivity.names.length) {
    names.append(element("span", "Tool names were not reported.", "activity-note"));
  }

  const timeline = byID("call-timeline");
  timeline.replaceChildren();
  for (const turn of model.turns ?? []) {
    if (!turn.flow.length) continue;
    const button = element("button", undefined, "timeline-call");
    button.type = "button";
    button.setAttribute("aria-label", `Open model call ${turn.position + 1} in the conversation`);
    button.append(element("span", `Call ${turn.position + 1}`, "timeline-call-label"));
    const flow = element("span", undefined, "timeline-flow");
    const hiddenRoles = Math.max(0, turn.flow.length - 7);
    const visibleRoles = hiddenRoles
      ? [...turn.flow.slice(0, 6), null, turn.flow.at(-1)]
      : turn.flow;
    visibleRoles.forEach((role, index) => {
      if (index) flow.append(element("span", "→", "timeline-arrow"));
      flow.append(role === null
        ? element("span", `+${hiddenRoles}`, "role-chip overflow-chip")
        : element("span", displayRole(role), `role-chip role-${role}`));
    });
    button.append(flow);
    if (turn.toolCalls.length) {
      const summary = toolCallSummary(turn.responseMessage);
      const chip = element("span", summary.length ? summary.join(", ")
        : `${turn.toolCalls.length} tool call${turn.toolCalls.length === 1 ? "" : "s"}`, "timeline-tools");
      chip.title = summary.join(", ");
      button.append(chip);
    }
    button.addEventListener("click", () => {
      selectTab("conversation");
      const details = byID(`conversation-call-${turn.position + 1}`);
      details.open = true;
      details.querySelector("summary").focus();
      details.scrollIntoView({ block: "nearest" });
    });
    timeline.append(button);
  }
}

// Two tablists share this page: the job's views and, inside a rollout, its
// detail panels. Only ever one is on screen, but a document-wide sweep would
// still deselect the other's panels, so each selection stays inside its bar.
function selectTab(name, focus = false) {
  const target = byID(`tab-${name}`);
  if (!target) return;
  for (const tab of target.closest('[role="tablist"]').querySelectorAll('[role="tab"]')) {
    const selected = tab === target;
    tab.setAttribute("aria-selected", String(selected));
    tab.tabIndex = selected ? 0 : -1;
    byID(tab.getAttribute("aria-controls")).hidden = !selected;
    if (selected && focus) tab.focus();
  }
  if (name === "tokens") renderSequence();
  // A hidden panel is not redrawn while it polls, so it is redrawn on the way in.
  if (JOB_TABS.has(name)) {
    activeJobTab = name;
    rememberJobTab(name);
    if (name === "metrics" && runOverview) renderRunView();
    if (name === "rollouts" && rolloutIndex) renderRolloutList();
    if (name === "logs") void refreshRunLog();
  }
}

function renderConversationMessage(message, fallbackRole) {
  const card = element("article", undefined, "conversation-message");
  const role = typeof message.role === "string" && message.role.trim() ? message.role : fallbackRole;
  card.append(element("p", role.toUpperCase(), `message-role role-${role.toLowerCase()}`));
  if (typeof message.content === "string") {
    card.append(element("p", message.content || "Empty content", "message-content"));
  } else if (present(message.content)) {
    const content = element("div", undefined, "message-json");
    json(content, message.content);
    card.append(content);
  } else {
    card.append(element("p", "No content reported.", "muted"));
  }
  if (present(message.tool_calls)) {
    const tools = element("details", undefined, "message-tools");
    tools.append(element("summary", "Tool calls"));
    json(tools, message.tool_calls);
    card.append(tools);
  }
  return card;
}

function renderConversation() {
  const container = byID("conversation");
  container.replaceChildren();
  if (!model.hasConversation) return;
  for (const turn of model.turns) {
    if (!turn.requestMessages.length && turn.responseMessage === null) continue;
    const details = element("details", undefined, "conversation-turn");
    details.id = `conversation-call-${turn.position + 1}`;
    const summaryParts = [
      `Model call ${turn.position + 1}`,
      `${turn.requestMessages.length} request message${turn.requestMessages.length === 1 ? "" : "s"}`,
      turn.raw.finish_reason ? `Finish: ${turn.raw.finish_reason}` : null,
    ].filter(Boolean);
    details.append(element("summary", summaryParts.join(" · ")));
    const body = element("div", undefined, "conversation-body");
    if (turn.requestMessages.length) {
      body.append(element("h3", "Request messages"));
      for (const message of turn.requestMessages) {
        body.append(renderConversationMessage(message, "request"));
      }
    }
    if (turn.responseMessage !== null) {
      body.append(element("h3", "Response message"));
      body.append(renderConversationMessage(turn.responseMessage, "assistant"));
    }
    details.append(body);
    container.append(details);
  }
  container.querySelector("details")?.setAttribute("open", "");
}

function renderGraph(focusKey = null) {
  const scope = byID("graph-sequence-select").value;
  graphView = buildGraph(model.graph, { sequenceIndex: scope === "" ? null : Number(scope), page: graphPage, focusKey });
  graphPage = graphView.page;
  notices("graph-notices", graphView.notices);
  byID("graph-count").textContent = `${count(graphView.total)} captured / referenced nodes`;
  byID("graph-page-status").textContent = `Page ${graphPage + 1} / ${graphView.pageCount}`;
  byID("graph-prev").disabled = graphPage === 0;
  byID("graph-next").disabled = graphPage + 1 === graphView.pageCount;
  byID("graph-empty").hidden = graphView.total !== 0;
  const svg = byID("rollout-graph");
  svg.toggleAttribute("hidden", !graphView.total);
  svg.replaceChildren();
  svg.setAttribute("viewBox", `0 0 ${graphView.width} ${graphView.height}`);
  svg.setAttribute("width", graphView.width);
  svg.setAttribute("height", graphView.height);
  const defs = svgElement("defs", {});
  const marker = svgElement("marker", { id: "graph-arrow", viewBox: "0 0 10 10", refX: 9, refY: 5,
    markerWidth: 6, markerHeight: 6, orient: "auto-start-reverse" });
  marker.append(svgElement("path", { d: "M 0 0 L 10 5 L 0 10 z", class: "arrow-head" }));
  defs.append(marker);
  svg.append(defs);
  const lookup = new Map(graphView.nodes.map((node) => [node.key, node]));
  for (const edge of graphView.edges) {
    const from = lookup.get(edge.from);
    const to = lookup.get(edge.to);
    const x1 = from.x + 110, y1 = from.y + 110, x2 = to.x + 110, y2 = to.y;
    const bend = Math.max(34, Math.abs(y2 - y1) / 2);
    svg.append(svgElement("path", { d: `M ${x1} ${y1} C ${x1} ${y1 + bend}, ${x2} ${y2 - bend}, ${x2} ${y2}`,
      class: `graph-edge${from.unresolved || to.unresolved ? " invalid-edge" : ""}`, "marker-end": "url(#graph-arrow)" }));
  }
  for (const node of graphView.nodes) {
    const kind = node.discarded ? "discarded" : node.root ? "root" : "call";
    const group = svgElement("g", { transform: `translate(${node.x} ${node.y})`, class: `graph-node ${kind}`,
      tabindex: 0, role: "button", "aria-pressed": "false",
      "aria-label": `${node.root ? "Root, " : ""}${node.label}${node.discarded ? ", discarded" : ""}, ${node.id ?? "node ID not reported"}`,
      "data-node-key": node.key });
    group.append(svgElement("title", {}, `${node.label}: ${node.id ?? "Node ID not reported"}`));
    group.append(svgElement("rect", { width: 220, height: 110, rx: 14, class: "node-card" }));
    group.append(svgElement("circle", { cx: 19, cy: 23, r: 4, class: "node-dot" }));
    group.append(svgElement("text", { x: 32, y: 27, class: "node-kind" }, node.kind));
    group.append(svgElement("text", { x: 16, y: 51, class: "node-label" }, node.label));
    // Which tool the call reached for, when it reached for one. The finish
    // reason it replaces says only that it did.
    const detail = node.records.length === 1 ? node.finishReason : null;
    const detailText = node.tools.length ? node.tools.join(", ")
      : detail ? `${detail} · ${node.id ?? "ID not reported"}` : node.id ?? "ID not reported";
    const detailNode = svgElement("text", { x: 16, y: 72, class: "node-detail" }, short(detailText, 26));
    if (node.tools.length) {
      detailNode.append(svgElement("title", {}, node.tools.join(", ")));
      group.setAttribute("aria-label", `${group.getAttribute("aria-label")}, called ${node.tools.join(", ")}`);
    }
    group.append(detailNode);
    const call = node.records.length === 1 ? node.records[0].raw : null;
    const tokenCounts = model.tokensCaptured && call
      ? [present(call.n_prompt) ? `${count(call.n_prompt)} prompt` : null,
        present(call.n_sampled) ? `${count(call.n_sampled)} sampled` : null].filter(Boolean).join(" · ")
      : "";
    const tokens = svgElement("text", { x: 16, y: 94, class: "node-detail" }, short(tokenCounts || "Token counts not reported", 26));
    if (tokenCounts) {
      tokens.append(svgElement("title", {}, tokenCounts));
      group.setAttribute("aria-label", `${group.getAttribute("aria-label")}, ${tokenCounts}`);
    }
    group.append(tokens);
    const select = () => selectGraphNode(node.key);
    group.addEventListener("click", select);
    group.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") { event.preventDefault(); select(); }
    });
    svg.append(group);
  }
  selectGraphNode(lookup.has(selectedNode) ? selectedNode : graphView.nodes[0]?.key ?? null);
}

function selectGraphNode(key) {
  selectedNode = key;
  for (const group of byID("rollout-graph").querySelectorAll(".graph-node")) {
    group.setAttribute("aria-pressed", String(group.getAttribute("data-node-key") === key));
  }
  const inspector = byID("graph-inspector");
  inspector.replaceChildren();
  const node = graphView.nodes.find((node) => node.key === key);
  if (!node) {
    inspector.append(element("p", "CALL INSPECTOR", "eyebrow"), element("h3", "A closer look"),
      element("p", "Select a captured call to explore its tokens, finish reason, and linked episode rewards.", "muted"));
    return;
  }
  inspector.append(element("p", "CALL INSPECTOR", "eyebrow"), element("h3", node.label),
    element("code", node.id ?? "Node ID not reported", "inspector-id"));
  if (!node.records.length) inspector.append(element("p", "This node is referenced by a sequence, but its call details were not exported.", "notice"));
  if (node.records.length > 1) inspector.append(element("p", "Duplicate ID: these records cannot be uniquely identified.", "notice"));
  for (const { raw, position } of node.records.slice(0, 100)) {
    if (node.records.length > 1) inspector.append(element("h4", `Capture record ${position + 1}`));
    fields(inspector, [["Capture index (0-based)", raw.index], ["Root ID", raw.root_id],
      ["Prompt tokens", format(model.tokensCaptured ? raw.n_prompt : null)],
      ["Sampled tokens", format(model.tokensCaptured ? raw.n_sampled : null)],
      ["Finish reason", raw.finish_reason ?? "Not reported"], ["Discarded", raw.discarded],
      ["Available tools (not calls)", raw.n_tools]]);
    const calls = toolCalls(raw.response_message);
    if (calls.length) {
      const details = element("details", undefined, "detail-section");
      details.append(element("summary", `Tool calls made (${calls.length})`));
      for (const call of calls) {
        const heading = element("p", undefined, "tool-call-heading");
        heading.append(element("code", call.name, "tool-name"));
        if (call.id) heading.append(element("span", call.id, "tool-call-id"));
        details.append(heading);
        // Arguments arrive as a JSON string the model produced, which is not
        // guaranteed to parse. Showing it raw beats showing nothing.
        let parsed = call.arguments;
        if (typeof call.arguments === "string") {
          try { parsed = JSON.parse(call.arguments); } catch { parsed = call.arguments; }
        }
        if (present(parsed)) json(details, parsed);
        else details.append(element("p", "No arguments reported.", "muted"));
      }
      inspector.append(details);
    }
    if (present(raw.sampling_params)) {
      const details = element("details", undefined, "detail-section");
      details.append(element("summary", "Sampling parameters"));
      json(details, raw.sampling_params);
      inspector.append(details);
    }
  }
  if (node.records.length > 100) {
    inspector.append(element("p", `Showing 100 of ${node.records.length} duplicate records. All records are in Rollout Stats → Response JSON.`, "muted"));
  }
  const steps = (model.steps ?? []).filter((step) => node.id !== null && step.raw.capture_node_id === node.id);
  inspector.append(element("h4", "Linked episode rewards"));
  if (!steps.length) inspector.append(element("p", "No linked episode steps reported.", "muted"));
  for (const step of steps.slice(0, 100)) {
    const row = element("p", undefined, "linked-step");
    row.append(navigationLink(`Step ${step.number}`, `#step-${step.number}`, () => {
      stepPage = Math.floor((step.number - 1) / CHART_PAGE_SIZE);
      renderStepChart();
      selectTab("details");
      const details = byID(`step-${step.number}`);
      details.open = true;
      details.querySelector("summary").focus();
    }), document.createTextNode(` · Reward ${format(step.reward)}${present(step.raw.episode_done) ? ` · Episode ended: ${step.raw.episode_done}` : ""}`));
    inspector.append(row);
  }
  if (steps.length > 100) inspector.append(element("p", `Showing 100 of ${steps.length} linked steps. All steps are in Rollout Stats → Response JSON.`, "muted"));
  inspector.append(element("h4", "Sequence membership"));
  if (!node.memberships.length) inspector.append(element("p", "No sequence path reported.", "muted"));
  for (const index of node.memberships.slice(0, 100)) {
    const sequence = model.graph.sequences[index];
    const button = element("button", sequenceLabel(sequence, index), "sequence-link");
    button.type = "button";
    button.addEventListener("click", () => {
      byID("sequence-select").value = String(index);
      tokenPage = 0;
      selectTab("tokens", true);
    });
    inspector.append(button);
  }
  if (node.memberships.length > 100) inspector.append(element("p", `Showing 100 of ${node.memberships.length} memberships. Use the sequence selector for all paths.`, "muted"));
}

function rawNumber(value) {
  if (value === undefined || value === null) return "Missing";
  return isNumber(value) ? String(value) : "Invalid value";
}

function renderSequence() {
  if (!model) return;
  const index = Number(byID("sequence-select").value);
  const sequence = model.graph.sequences?.[index];
  const hasSequence = sequence !== undefined;
  byID("sequence-empty").hidden = hasSequence;
  byID("sequence-select").disabled = !hasSequence;
  byID("sequence-meta").replaceChildren();
  byID("sequence-counts").replaceChildren();
  byID("token-visual").replaceChildren();
  byID("token-table").querySelector("tbody").replaceChildren();
  byID("token-raw").hidden = !hasSequence;
  tokenData = null;
  notices("sequence-notices", []);
  if (!hasSequence) return;
  fields(byID("sequence-meta"), [["Reported role", sequence?.role ?? "Not reported"],
    ["Reported trainable", sequence?.trainable ?? "Not reported"], ["Reported turns", sequence?.n_turns],
    ["Reported prompt length", sequence?.prompt_len]]);
  tokenData = sequenceData(sequence, model.graph.capture_level);
  notices("sequence-notices", tokenData.notices);
  fields(byID("sequence-counts"), [["Input IDs", tokenData.arrays.input_ids?.length ?? "Not reported"],
    ["Target positions (mask 1)", tokenData.positions.target.length],
    ["Excluded positions (mask 0)", tokenData.positions.excluded.length],
    ["Missing / invalid mask", tokenData.positions.unknown.length],
    ["Recorded target log probabilities", tokenData.scored]]);
  byID("token-first-target").disabled = !tokenData.positions.target.length;
  byID("token-position").max = Math.max(0, tokenData.length - 1);
  byID("token-jump-form").hidden = !tokenData.length;
  renderTokenOverview();
  renderTokenTable();
}

function renderTokenTable() {
  if (!tokenData) return;
  const page = sequencePage(tokenData, tokenPage, TOKEN_PAGE_SIZE, byID("token-filter").value);
  tokenPage = page.page;
  byID("token-page-status").textContent = page.total
    ? `Rows ${count(page.start + 1)}–${count(page.end)} of ${count(page.total)} matching positions · Page ${page.page + 1} / ${page.pageCount}`
    : "No positions match this filter.";
  byID("token-prev").disabled = page.page === 0;
  byID("token-next").disabled = page.page + 1 === page.pageCount;
  byID("token-caption").textContent = `Sequence ${Number(byID("sequence-select").value) + 1} · Original sequence positions`;
  const tbody = byID("token-table").querySelector("tbody");
  tbody.replaceChildren();
  byID("token-table-wrap").scrollTop = 0;
  for (const row of page.rows) {
    const tr = element("tr");
    tr.id = `token-position-${row.position}`;
    tr.tabIndex = -1;
    const position = element("th", String(row.position));
    position.scope = "row";
    const token = element("td", rawNumber(row.token));
    const mask = element("td", rawNumber(row.mask));
    if (row.mask === 0 || row.mask === 1) {
      mask.replaceChildren(element("span", String(row.mask), `mask-badge mask-${row.mask}`));
    }
    const probability = element("td", row.score === "excluded" ? "Not scored" : rawNumber(row.logprob));
    if (row.score === "excluded") probability.title = `Stored logprobs value: ${rawNumber(row.logprob)}`;
    const statusText = {
      excluded: "Excluded / not scored", unknown: "Mask missing / invalid",
      scored: "Target position", missing: "Target · log probability missing / invalid",
    }[row.score];
    tr.append(position, token, mask, probability, element("td", statusText, `score-status ${row.score}`));
    tbody.append(tr);
  }
}

function jumpToPosition(position) {
  byID("token-filter").value = "all";
  tokenPage = Math.floor(position / TOKEN_PAGE_SIZE);
  byID("token-raw").open = true;
  byID("token-position").value = position;
  renderTokenTable();
  byID(`token-position-${position}`).focus();
}

function binControl(group, label, position) {
  group.setAttribute("role", "button");
  group.setAttribute("tabindex", "0");
  group.setAttribute("aria-label", label);
  group.append(svgElement("title", {}, label));
  group.addEventListener("click", () => jumpToPosition(position));
  group.addEventListener("keydown", (event) => {
    if (event.key === "Enter" || event.key === " ") { event.preventDefault(); jumpToPosition(position); }
  });
}

function renderTokenOverview() {
  const container = byID("token-visual");
  const data = tokenData;
  if (!data.length) {
    container.append(element("p", "No token positions were included in the arrays.", "muted"));
    return;
  }
  container.append(element("h3", "Loss mask · whole sequence"));
  const mask = svgElement("svg", { viewBox: "0 0 800 54", role: "group",
    "aria-label": "Whole-sequence loss mask, grouped by position range", class: "mask-overview" });
  for (const bin of data.bins) {
    const group = svgElement("g", { class: "position-bin" });
    binControl(group, `Positions ${bin.start}–${bin.end - 1}: ${bin.target} target, ${bin.excluded} excluded, ${bin.unknown} missing / invalid`, bin.start);
    let y = 0;
    for (const kind of ["excluded", "target", "unknown"]) {
      const height = bin[kind] / (bin.end - bin.start) * 36;
      if (height) group.append(svgElement("rect", { x: bin.start / data.length * 800, y,
        width: (bin.end - bin.start) / data.length * 800, height, class: `mask-region ${kind}` }));
      y += height;
    }
    mask.append(group);
  }
  mask.append(svgElement("text", { x: 0, y: 52, class: "plot-label" }, "0"),
    svgElement("text", { x: 800, y: 52, "text-anchor": "end", class: "plot-label" }, String(data.length - 1)));
  container.append(mask, element("p",
    "Purple: target (1). Gray: excluded (0). Amber: missing / invalid. Each position bin shows its mask counts, not ordering within the bin. Select a bin for exact values. Mask 0 alone does not identify prompt versus generated tokens.", "section-note"));
  container.append(element("h3", "Recorded log probabilities · target positions"));
  if (!data.scored) {
    container.append(element("p", "No valid log probabilities at mask-1 positions were included. Unscored entries are not plotted.", "muted"));
    return;
  }
  const scale = Math.abs(data.minLogprob) || 1;
  const yFor = (value) => 18 + (-value / scale) * 110;
  const plot = svgElement("svg", { viewBox: "0 0 800 175", role: "group",
    "aria-label": "Recorded target log probability ranges by sequence position", class: "probability-plot" });
  for (const [value, y] of [[0, 18], [-scale, 128]]) {
    plot.append(svgElement("line", { x1: 90, x2: 790, y1: y, y2: y, class: "plot-axis" }),
      svgElement("text", { x: 82, y: y + 4, "text-anchor": "end", class: "plot-label" },
        new Intl.NumberFormat("en", { maximumSignificantDigits: 4,
          notation: value !== 0 && (Math.abs(value) < 0.001 || Math.abs(value) >= 1e6) ? "scientific" : "standard" }).format(value)));
  }
  for (const bin of data.bins) {
    if (!bin.scored) continue;
    const x = 90 + (bin.start + bin.end) / 2 / data.length * 700;
    const group = svgElement("g", { class: "position-bin" });
    binControl(group, `Positions ${bin.start}–${bin.end - 1}: ${bin.scored} recorded target log probabilities, minimum ${bin.min}, maximum ${bin.max}`, bin.firstScored);
    group.append(svgElement("rect", { x: x - 4, y: 13, width: 8, height: 120, class: "plot-hit" }),
      svgElement("line", { x1: x, x2: x, y1: yFor(bin.max), y2: yFor(bin.min), class: "plot-range" }),
      svgElement("circle", { cx: x, cy: yFor(bin.max), r: 2, class: "plot-point" }),
      svgElement("circle", { cx: x, cy: yFor(bin.min), r: 2, class: "plot-point" }));
    plot.append(group);
  }
  plot.append(svgElement("text", { x: 90, y: 148, class: "plot-label" }, "0"),
    svgElement("text", { x: 790, y: 148, "text-anchor": "end", class: "plot-label" }, String(data.length - 1)),
    svgElement("text", { x: 440, y: 170, "text-anchor": "middle", class: "plot-label" }, "Sequence position (0-based)"));
  container.append(plot, element("p",
    `Recorded range: ${data.minLogprob} to ${data.maxLogprob}. Each mark shows the minimum–maximum within a position bin, not an average or a per-token trace. Mask-0 and missing / invalid scores are omitted; recorded zeros at mask-1 positions are retained.`, "section-note"));
}

function navigationLink(label, href, action) {
  const link = element("a", label);
  link.href = href;
  link.addEventListener("click", (event) => { event.preventDefault(); action(); });
  return link;
}

function callLink(position) {
  const turn = model.turns[position];
  return navigationLink(`Model call ${position + 1}`, "#rollout-graph", () => {
    selectedNode = turn.raw.node_id ? `id:${turn.raw.node_id}` : `turn:${position}`;
    byID("graph-sequence-select").value = "";
    selectTab("graph");
    renderGraph(selectedNode);
    const node = [...byID("rollout-graph").querySelectorAll(".graph-node")]
      .find((node) => node.getAttribute("data-node-key") === selectedNode);
    node?.focus();
    node?.scrollIntoView({ block: "nearest", inline: "nearest" });
  });
}

function chartPagination(id, page, total, onChange) {
  const container = byID(id);
  container.replaceChildren();
  container.hidden = total <= CHART_PAGE_SIZE;
  if (container.hidden) return;
  const pages = Math.ceil(total / CHART_PAGE_SIZE);
  const previous = element("button", "← Previous");
  const next = element("button", "Next →");
  previous.type = next.type = "button";
  previous.disabled = page === 0;
  next.disabled = page + 1 === pages;
  const change = (targetPage, preferredIndex) => {
    onChange(targetPage);
    const buttons = byID(id).querySelectorAll("button");
    const preferred = buttons[preferredIndex];
    (preferred?.disabled ? [...buttons].find((button) => !button.disabled) : preferred)?.focus();
  };
  previous.addEventListener("click", () => change(page - 1, 0));
  next.addEventListener("click", () => change(page + 1, 1));
  const status = element("span", `Page ${page + 1} / ${pages} · ${count(total)} total`, "muted");
  status.setAttribute("role", "status");
  container.append(previous, status, next);
}

function rewardBar(value, max) {
  const chart = svgElement("svg", { viewBox: "0 0 400 28", preserveAspectRatio: "none",
    "aria-hidden": "true", class: "reward-bar", focusable: "false" });
  chart.append(svgElement("line", { x1: 200, x2: 200, y1: 0, y2: 28, class: "zero-line" }));
  const geometry = rewardGeometry(value, max);
  if (geometry) {
    chart.append(svgElement("rect", { x: geometry.x, y: 7, width: geometry.width, height: 14,
      rx: 2, class: geometry.negative ? "negative" : "positive" }));
    if (geometry.zero) chart.append(svgElement("circle", { cx: 200, cy: 14, r: 3, class: "zero-point" }));
  }
  return chart;
}

function renderStepChart() {
  const steps = model.steps ?? [];
  const max = chartScales(steps).reward;
  byID("steps-panel").hidden = !steps.length;
  byID("reward-min").textContent = max ? format(-max) : "";
  byID("reward-max").textContent = max ? format(max) : "";
  const container = byID("steps");
  container.replaceChildren();
  container.scrollTop = 0;
  for (const step of steps.slice(stepPage * CHART_PAGE_SIZE, (stepPage + 1) * CHART_PAGE_SIZE)) {
    const details = element("details", undefined, "step-details");
    details.id = `step-${step.number}`;
    const summary = element("summary", undefined, "reward-summary");
    summary.append(element("span", `Step ${step.number}`, "step-label"), rewardBar(step.reward, max),
      element("span", format(step.reward), "bar-value"));
    summary.setAttribute("aria-label", `Step ${step.number}, reward: ${format(step.reward)}. Expand for details.`);
    details.append(summary);
    fields(details, [["Episode ended", step.raw.episode_done], ["Capture node", step.raw.capture_node_id]]);
    const links = element("p", "Model calls: ", "cross-links");
    if (!step.turnPositions.length) links.append(document.createTextNode("No matching captured turn in this snapshot"));
    step.turnPositions.slice(0, 100).forEach((position, index) => {
      if (index) links.append(document.createTextNode(", "));
      links.append(callLink(position));
    });
    if (step.turnPositions.length > 100) links.append(document.createTextNode(" · First 100 matching records shown; all records are in Response JSON."));
    details.append(links);
    container.append(details);
  }
  chartPagination("steps-pagination", stepPage, steps.length, (page) => { stepPage = page; renderStepChart(); });
}

function renderCallChart() {
  const turns = model.turns ?? [];
  const available = model.tokensCaptured && turns.some((turn) => present(turn.prompt) || present(turn.sampled));
  byID("tokens-panel").hidden = !available;
  byID("token-chart").replaceChildren();
  byID("token-chart").scrollTop = 0;
  byID("calls-pagination").replaceChildren();
  if (!available) return;
  const max = chartScales([], turns).tokens || 1;
  for (const turn of turns.slice(callPage * CHART_PAGE_SIZE, (callPage + 1) * CHART_PAGE_SIZE)) {
    const group = element("div", undefined, "token-group");
    group.append(callLink(turn.position));
    for (const [kind, label, value] of [["prompt", "Prompt", turn.prompt], ["sampled", "Sampled", turn.sampled]]) {
      const row = element("div", undefined, `token-series ${kind}`);
      row.append(element("span", label, "token-label"));
      if (isNumber(value)) {
        const meter = element("meter");
        meter.min = 0;
        meter.max = max;
        meter.value = value;
        meter.setAttribute("aria-label", `Model call ${turn.position + 1}, ${label.toLowerCase()}: ${format(value)} tokens`);
        row.append(meter);
      } else {
        row.append(element("span"));
      }
      row.append(element("span", format(value), "bar-value"));
      group.append(row);
    }
    byID("token-chart").append(group);
  }
  chartPagination("calls-pagination", callPage, turns.length, (page) => { callPage = page; renderCallChart(); });
}

function renderDetails() {
  const container = byID("capture-details");
  container.replaceChildren();
  fields(container, [["Capture level", model.graph.capture_level ?? "Not reported"],
    ["Rollout type", model.graph.rollout_type], ["Rollout trainable", model.graph.trainable]]);
  for (const [label, value] of [["Statistics", model.graph.stats], ["Metadata", model.graph.metadata]]) {
    if (!present(value)) continue;
    const details = element("details", undefined, "detail-section");
    details.append(element("summary", label));
    const content = element("div");
    details.append(content);
    container.append(details);
    lazyJSON(content, value);
  }
  renderStepChart();
  renderCallChart();
  byID("charts").hidden = byID("steps-panel").hidden && byID("tokens-panel").hidden;
  byID("validation-panel").hidden = !model.graph.validation?.length;
  byID("validation-count").textContent = model.graph.validation?.length ? `(${model.graph.validation.length})` : "";
  lazyJSON(byID("validation"), model.graph.validation);
  lazyJSON(byID("raw-content"), model.response);
}

// Transport-independent replacement: reset selections and pages, never reuse previous snapshot details.
export function setSnapshot(snapshot) {
  const next = mapSnapshot(snapshot);
  model = next;
  graphPage = 0;
  tokenPage = 0;
  byID("token-filter").value = "target";
  byID("token-position").value = "";
  stepPage = 0;
  callPage = 0;
  selectedNode = null;
  for (const scrollRegion of byID("snapshot").querySelectorAll(".graph-canvas, .table-scroll, #graph-inspector")) {
    scrollRegion.scrollTop = 0;
    scrollRegion.scrollLeft = 0;
  }
  for (const details of byID("snapshot").querySelectorAll("details")) details.open = false;
  byID("metrics").replaceChildren();
  notices("snapshot-notices", model.warnings);
  const outcome = byID("outcome");
  outcome.hidden = model.outcome === null;
  outcome.textContent = model.outcome ?? "";
  outcome.className = `badge ${model.response.success === true ? "positive" : "negative"}`;
  byID("rollout-id").textContent = model.response.rollout_id;
  byID("source").textContent = model.source;
  byID("environment-context").hidden = model.environment === null;
  byID("environment-name").textContent = model.environment?.name ?? "";
  byID("environment-version").textContent = model.environment ? `Version ${model.environment.version}` : "";
  const savedAt = byID("saved-at");
  const hasSavedAt = model.savedAt !== undefined;
  savedAt.hidden = !hasSavedAt;
  byID("saved-at-separator").hidden = !hasSavedAt;
  savedAt.textContent = hasSavedAt ? `Saved ${new Date(model.savedAt).toLocaleString()}` : "";
  if (hasSavedAt) {
    savedAt.dateTime = model.savedAt;
    savedAt.title = model.savedAt;
  } else {
    savedAt.removeAttribute("datetime");
    savedAt.removeAttribute("title");
  }
  byID("final-reward").textContent = String(model.response.reward);
  byID("final-response-panel").hidden = model.finalResponse === null;
  byID("final-response-text").textContent = model.finalResponsePreview ?? "";
  byID("final-response-text").classList.toggle("collapsed", model.finalResponseExpandable);
  byID("final-response-note").hidden = !model.finalResponseReasoningHidden;
  byID("final-response-toggle").hidden = !model.finalResponseExpandable;
  byID("final-response-toggle").setAttribute("aria-expanded", "false");
  byID("final-response-toggle").textContent = "Show full response";
  metric("Episode steps", model.steps?.length);
  metric("Model calls", model.turns?.length);
  metric("Captured sequences", model.graph.sequences?.length);
  renderActivitySummary();
  const episodeParts = [];
  if (model.episode.kind) episodeParts.push(model.episode.kind);
  if (model.episode.termination_reason) episodeParts.push(`Termination: ${model.episode.termination_reason}`);
  if (present(model.episode.ungraded)) episodeParts.push(`Ungraded: ${model.episode.ungraded ? "yes" : "no"}`);
  byID("episode-description").textContent = episodeParts.join(" · ");
  byID("episode-description").hidden = !episodeParts.length;
  byID("sequence-select").replaceChildren();
  byID("graph-sequence-select").replaceChildren(element("option", "All sequences & calls"));
  byID("graph-sequence-select").firstChild.value = "";
  (model.graph.sequences ?? []).forEach((sequence, index) => {
    for (const id of ["sequence-select", "graph-sequence-select"]) {
      const option = element("option", sequenceLabel(sequence, index));
      option.value = String(index);
      byID(id).append(option);
    }
  });
  renderGraph();
  byID("tab-conversation").hidden = !model.hasConversation;
  renderConversation();
  renderDetails();
  // Clear training output even when its panel stays hidden.
  renderSequence();
  selectTab("graph");
  byID("load-error").hidden = true;
  byID("snapshot").hidden = false;
  byID("load-status").textContent = "Saved snapshot loaded.";
  byID("load-status").className = "sr-only";
}

for (const tab of document.querySelectorAll('[role="tab"]')) {
  tab.addEventListener("click", () => selectTab(tab.id.slice(4)));
  tab.addEventListener("keydown", (event) => {
    const bar = tab.closest('[role="tablist"]');
    const tabs = [...bar.querySelectorAll('[role="tab"]')].filter((candidate) => !candidate.hidden);
    let index = tabs.indexOf(tab);
    if (event.key === "ArrowRight") index = (index + 1) % tabs.length;
    else if (event.key === "ArrowLeft") index = (index + tabs.length - 1) % tabs.length;
    else if (event.key === "Home") index = 0;
    else if (event.key === "End") index = tabs.length - 1;
    else return;
    event.preventDefault();
    selectTab(tabs[index].id.slice(4), true);
  });
}
byID("graph-sequence-select").addEventListener("change", () => { graphPage = 0; selectedNode = null; renderGraph(); });
byID("graph-prev").addEventListener("click", () => { graphPage--; renderGraph(); });
byID("graph-next").addEventListener("click", () => { graphPage++; renderGraph(); });
byID("sequence-select").addEventListener("change", () => {
  tokenPage = 0;
  byID("token-position").value = "";
  renderSequence();
});
byID("token-filter").addEventListener("change", () => { tokenPage = 0; renderTokenTable(); });
byID("token-prev").addEventListener("click", () => { tokenPage--; renderTokenTable(); });
byID("token-next").addEventListener("click", () => { tokenPage++; renderTokenTable(); });
byID("token-first-target").addEventListener("click", () => jumpToPosition(tokenData.positions.target[0]));
byID("token-jump-form").addEventListener("submit", (event) => {
  event.preventDefault();
  if (byID("token-position").reportValidity()) jumpToPosition(byID("token-position").valueAsNumber);
});

let rolloutIndex = null;

// A job monitor is left open and reloaded, so the view being watched outlives a
// refresh. Session storage can be barred outright, which is not worth failing over.
const JOB_TAB_KEY = "rle-monitor-job-tab";
const JOB_TABS = new Set(["metrics", "rollouts", "logs"]);
let activeJobTab = "metrics";
try {
  const stored = sessionStorage.getItem(JOB_TAB_KEY);
  if (JOB_TABS.has(stored)) activeJobTab = stored;
} catch { /* storage is unavailable; the default view still works */ }

function rememberJobTab(name) {
  try {
    sessionStorage.setItem(JOB_TAB_KEY, name);
  } catch { /* nothing to do: the tab still switches, it just will not persist */ }
}

function optionsFor(select, values, label) {
  const current = select.value;
  // An <option> with no value attribute reports its text as its value, so the
  // "all" entry needs an explicit empty one or clearing the filter below finds
  // no match and the select renders blank.
  const blank = element("option", label);
  blank.value = "";
  select.replaceChildren(blank);
  for (const value of values) {
    const option = element("option", String(value));
    option.value = String(value);
    select.append(option);
  }
  select.value = values.map(String).includes(current) ? current : "";
}

// The training path knows which checkpoint sampled a rollout but not the step
// number, so the checkpoint is what actually separates one step from the next.
// Preferring it keeps a step's validation and training rollouts under one key.
function stepLabel(entry) {
  if (entry.checkpoint_id) return entry.checkpoint_id;
  return isNumber(entry.step) ? String(entry.step) : "";
}

function visibleEntries() {
  const split = byID("list-split").value;
  const step = byID("list-step").value;
  return rolloutIndex.data.filter((entry) => {
    if (split && (entry.split || "") !== split) return false;
    if (step && stepLabel(entry) !== step) return false;
    return true;
  });
}

// The same page serves one captured rollout and a whole training job. Only a
// job carries a job id, so that is what decides which of the two it is called.
function applyMonitorTitle(jobID) {
  const label = jobID ? "Job monitor" : "Rollout monitor";
  const brand = document.querySelector(".brand");
  if (brand) {
    const name = brand.querySelector("span:last-child");
    if (name) name.textContent = label;
    brand.setAttribute("aria-label", `Foundry RLE ${label.toLowerCase()} home`);
  }
  document.title = jobID ? `Foundry RLE job monitor · ${jobID}` : "Foundry RLE rollout monitor";
}

function renderRolloutList() {
  const entries = visibleEntries();
  applyMonitorTitle(rolloutIndex.job_id || "");
  byID("list-job-id").textContent = rolloutIndex.job_id || "";
  byID("list-count").textContent = entries.length === rolloutIndex.data.length
    ? `· ${count(entries.length)} recorded`
    : `· ${count(entries.length)} of ${count(rolloutIndex.data.length)} recorded`;
  const body = byID("list-body");
  body.replaceChildren();
  for (const entry of entries) {
    const row = element("tr");
    row.append(element("td", String(entry.sequence ?? "")));
    const open = element("button", short(entry.rollout_id, 14), "link-button");
    open.type = "button";
    open.title = entry.rollout_id;
    open.addEventListener("click", () => openRollout(entry.rollout_id));
    const identity = element("td");
    identity.append(open);
    row.append(identity);
    row.append(element("td", entry.split || "—"));
    row.append(element("td", stepLabel(entry) || "—"));
    row.append(element("td", isNumber(entry.reward) ? entry.reward.toFixed(3) : "—"));
    // A recorded rollout may legitimately carry no verdict, which is not a failure.
    const outcome = element("td");
    if (entry.success === true) outcome.append(element("span", "Success", "badge positive"));
    else if (entry.success === false) outcome.append(element("span", "Failure", "badge negative"));
    else outcome.append(element("span", "Not reported", "badge neutral"));
    row.append(outcome);
    row.append(element("td", isNumber(entry.latency_s) ? `${entry.latency_s.toFixed(1)}s` : "—"));
    const task = element("td", short(entry.task_id || "—", 22));
    if (entry.task_id) task.title = entry.task_id;
    row.append(task);
    body.append(row);
  }
  byID("list-empty").hidden = rolloutIndex.data.length > 0;
  byID("list-table").hidden = entries.length === 0;
  renderRolloutTabCount();
}

function showRolloutList() {
  byID("snapshot").hidden = true;
  // Tabs only earn their place when there are two views to hold. A job with no
  // local mirror has rollouts and nothing else, and is shown as the plain list.
  const tabbed = hasRunView;
  byID("job-tabs").hidden = !tabbed;
  for (const id of ["run-overview", "rollout-list", "run-log"]) {
    if (tabbed) byID(id).setAttribute("role", "tabpanel");
    else byID(id).removeAttribute("role");
  }
  if (tabbed) {
    selectTab(activeJobTab);
  } else {
    byID("rollout-list").hidden = false;
    byID("run-overview").hidden = true;
    byID("run-log").hidden = true;
    renderRolloutList();
  }
  byID("load-status").className = "sr-only";
  byID("load-status").textContent = `${rolloutIndex.data.length} rollouts recorded.`;
}

// The count belongs on the tab because the list it describes is usually the
// view that is not on screen, and a run's rollout count is how you tell it is
// still producing.
function renderRolloutTabCount() {
  if (!rolloutIndex) return;
  byID("tab-rollouts-count").textContent = count(rolloutIndex.data.length);
}

// Reading a chart raises exactly one question -- what happened at that step --
// and the answer is in the other tab. Clicking a step carries the filter over.
function showRolloutsForStep(step) {
  if (!rolloutIndex || byID("job-tabs").hidden) return;
  // The list keys steps by checkpoint where one was reported, so the chart's
  // step number is matched through a rollout rather than used as the value.
  const match = rolloutIndex.data.find((entry) => entry.step === step);
  const value = match ? stepLabel(match) : String(step);
  const select = byID("list-step");
  if (![...select.options].some((option) => option.value === value)) return;
  select.value = value;
  selectTab("rollouts", true);
}

async function openRollout(rolloutID) {
  byID("load-status").className = "notice";
  byID("load-status").textContent = "Loading rollout…";
  byID("load-error").hidden = true;
  try {
    setSnapshot(await fetchSnapshot(fetch, rolloutID));
    byID("job-tabs").hidden = true;
    byID("rollout-list").hidden = true;
    byID("run-overview").hidden = true;
    byID("run-log").hidden = true;
    byID("back-to-list").hidden = false;
    byID("main").focus();
  } catch (error) {
    byID("load-status").className = "sr-only";
    byID("load-status").textContent = "";
    byID("load-error").hidden = false;
    byID("error-message").textContent = error.message;
  }
}

function refreshFilters() {
  const splits = [...new Set(rolloutIndex.data.map((entry) => entry.split).filter(Boolean))].sort();
  const steps = [...new Set(rolloutIndex.data.map(stepLabel).filter(Boolean))].sort();
  optionsFor(byID("list-split"), splits, "All");
  optionsFor(byID("list-step"), steps, "All");
}

// A training run records rollouts for as long as it lasts, so the list is
// polled rather than read once. Only what is new is asked for, and only the
// list view polls: reading a rollout should not be interrupted by the table
// underneath it changing.
const pollIntervalMs = 5000;
let pollTimer = null;

function startPolling() {
  if (pollTimer !== null) return;
  pollTimer = setInterval(() => {
    pollForNewRollouts();
    pollRunView();
  }, pollIntervalMs);
}

async function pollForNewRollouts() {
  // Polling follows the job view rather than the visible tab: a count that
  // stopped moving whenever the charts were up would report a live run as done.
  if (!rolloutIndex || !byID("snapshot").hidden) return;
  const last = rolloutIndex.data.length
    ? rolloutIndex.data[rolloutIndex.data.length - 1].rollout_id
    : "";
  let update;
  try {
    update = await fetchRolloutIndex(fetch, last);
  } catch {
    // A poll that cannot reach the monitor is not worth reporting: the rollouts
    // already listed are still valid, and the next tick retries.
    return;
  }
  if (!update || !update.data || update.data.length === 0) return;
  // `reset` means the monitor answered with the whole list rather than the part
  // that is new, so appending it would double every rollout already shown.
  if (update.reset) rolloutIndex.data = update.data;
  else rolloutIndex.data.push(...update.data);
  refreshFilters();
  renderRolloutTabCount();
  // Drawing a table nobody is looking at costs more than it is worth; the tab
  // redraws it on the way in, from data this poll has already kept current.
  if (!byID("rollout-list").hidden) renderRolloutList();
}

// ---------------------------------------------------------------------------
// Training run view
//
// A job is shown as the run it is -- what it trains, on what, and whether it is
// working -- above the rollouts it has produced. Everything here reads the
// local mirror `train --follow` writes, so it appears only for a run that was
// followed on this machine.
// ---------------------------------------------------------------------------

let runOverview = null;
let runMetrics = [];
let runLogOffset = 0;
let hasRunView = false;

function formatMetric(value, style) {
  if (!isNumber(value)) return "—";
  if (style === "percent") return `${(value * 100).toFixed(1)}%`;
  // Seconds are read as a clock. Four thousand of them is 1h 6m to a human and
  // an unparseable number of digits to everyone.
  if (style === "duration") {
    const total = Math.max(0, Math.round(value));
    const hours = Math.floor(total / 3600);
    const minutes = Math.floor((total % 3600) / 60);
    const seconds = total % 60;
    if (hours) return `${hours}h ${minutes}m`;
    if (minutes) return `${minutes}m ${seconds}s`;
    return `${seconds}s`;
  }
  if (value === 0) return "0";
  if (Math.abs(value) >= 1000) return value.toFixed(0);
  // A learning rate of 4e-5 shown as "0.000" is a number nobody can act on.
  // Below the third decimal the exponent carries the information instead.
  if (Math.abs(value) < 1e-3) return value.toExponential(2);
  return value.toFixed(3);
}

// A change in a percentage is measured in points, not percent: success going
// from 25% to 50% is +25 points, and calling that "+25%" invites reading it as
// a quarter more rather than double.
function formatDelta(value, style) {
  if (style === "percent") return `${(value * 100).toFixed(1)}pp`;
  return formatMetric(value, style);
}

function renderRunHeadline() {
  const container = byID("run-headline");
  container.replaceChildren();
  for (const entry of runHeadline(runMetrics)) {
    const card = element("div", undefined, "metric");
    card.append(element("p", formatMetric(entry.value, entry.format), "metric-value"));
    card.append(element("h2", entry.label));
    // A fraction of the run completed is read faster as a bar than as a
    // percentage, which is why loom's overview card carries one.
    if (entry.key === "progress/done_frac" && isNumber(entry.value)) {
      const track = element("div", undefined, "metric-progress");
      const fill = element("div", undefined, "metric-progress-fill");
      fill.style.width = `${Math.max(0, Math.min(1, entry.value)) * 100}%`;
      track.append(fill);
      card.append(track);
    }
    // The direction of travel is the point; a bare number cannot show it.
    if (isNumber(entry.delta) && entry.delta !== 0) {
      const rising = entry.delta > 0;
      const sign = rising ? "+" : "−";
      const change = element("p",
        `${sign}${formatDelta(Math.abs(entry.delta), entry.format)} vs previous`,
        `metric-delta ${rising ? "rising" : "falling"}`);
      card.append(change);
    }
    container.append(card);
  }
}

function factGroup(title, rows) {
  const group = element("section", undefined, "fact-group");
  group.append(element("p", title, "eyebrow"));
  const list = element("dl", undefined, "field-list");
  for (const [label, value] of rows) list.append(element("dt", label), element("dd", String(value)));
  group.append(list);
  return group;
}

function renderRunFacts() {
  const container = byID("run-facts");
  container.replaceChildren();
  const facts = runFacts(runOverview);
  for (const [title, rows] of [
    ["ENVIRONMENT", facts.identity], ["MODEL", facts.model],
    ["DATA", facts.dataset], ["HYPERPARAMETERS", facts.training],
  ]) {
    if (rows.length) container.append(factGroup(title, rows));
  }
}

function chartLegend(geometry) {
  const legend = element("div", undefined, "chart-legend");
  for (const entry of geometry.series) {
    const item = element("span", undefined, `legend-series tone-${entry.tone}`);
    item.append(element("span", "", "legend-swatch"), element("span", entry.name));
    legend.append(item);
  }
  return legend;
}

// Axis labels are read at a glance, so they drop the trailing zeros the
// tooltip keeps: a gridline reading "0.25" is clearer than "0.250", and the
// exact value is one hover away.
function formatTick(value) {
  if (!isNumber(value)) return "";
  if (value === 0) return "0";
  if (Math.abs(value) >= 1000 || Math.abs(value) < 1e-3) return formatMetric(value);
  return String(Number(value.toPrecision(6)));
}

// Room outside the plot area for the axis labels, in viewBox units.
const CHART_GUTTER = { left: 46, right: 10, top: 8, bottom: 24 };

// Translate a pointer event into viewBox coordinates.
//
// The plot is drawn at a fixed 400x160 but stretched to whatever width the
// card ends up, so the browser letterboxes it. getScreenCTM is the only
// mapping that survives that; ratios off getBoundingClientRect do not.
function viewBoxPoint(svg, event) {
  const matrix = svg.getScreenCTM();
  if (!matrix) return null;
  const point = svg.createSVGPoint();
  point.x = event.clientX;
  point.y = event.clientY;
  return point.matrixTransform(matrix.inverse());
}

function chartTooltipRow(reading) {
  // The tone lives on the row so the swatch can reuse the legend's colours,
  // in both themes, without a second copy of the palette.
  const row = element("div", undefined, `chart-tooltip-row tone-${reading.tone}`);
  row.append(element("span", "", "legend-swatch"));
  row.append(element("span", reading.name, "chart-tooltip-name"));
  row.append(element("span", formatMetric(reading.value), "chart-tooltip-value"));
  return row;
}

// Wire hover onto a drawn panel: a crosshair at the nearest step, every series
// marked at that step, and one tooltip listing them all.
function attachChartHover(panel, svg, geometry, markers, crosshair) {
  const tooltip = element("div", undefined, "chart-tooltip");
  tooltip.hidden = true;
  panel.append(tooltip);

  const clear = () => {
    tooltip.hidden = true;
    crosshair.setAttribute("visibility", "hidden");
    markers.replaceChildren();
  };

  const move = (event) => {
    const point = viewBoxPoint(svg, event);
    const hover = point ? chartHoverAt(geometry, point.x) : null;
    if (!hover) {
      clear();
      return;
    }
    crosshair.setAttribute("visibility", "visible");
    crosshair.setAttribute("x1", hover.x);
    crosshair.setAttribute("x2", hover.x);
    markers.replaceChildren(...hover.readings.map((reading) => svgElement("circle", {
      cx: reading.x, cy: reading.y, r: 4.5, class: `chart-marker tone-${reading.tone}`,
    })));

    tooltip.replaceChildren(element("p", `Step ${hover.step}`, "chart-tooltip-step"));
    for (const reading of hover.readings) tooltip.append(chartTooltipRow(reading));
    tooltip.hidden = false;

    // Place it beside the cursor, flipping before it runs off the card rather
    // than after, so the reading stays on screen at the right-hand edge.
    const bounds = panel.getBoundingClientRect();
    const x = event.clientX - bounds.left;
    const y = event.clientY - bounds.top;
    const width = tooltip.offsetWidth;
    const height = tooltip.offsetHeight;
    tooltip.style.left = `${x + width + 24 > bounds.width ? Math.max(4, x - width - 14) : x + 14}px`;
    // Centred on the cursor rather than floated above it: anchoring it above
    // parks the tooltip over the title and legend on every mid-height hover.
    tooltip.style.top = `${Math.min(Math.max(4, y - height / 2), Math.max(4, bounds.height - height - 4))}px`;
  };

  svg.addEventListener("pointermove", move);
  svg.addEventListener("pointerleave", clear);
  svg.addEventListener("click", (event) => {
    const point = viewBoxPoint(svg, event);
    const hover = point ? chartHoverAt(geometry, point.x) : null;
    if (hover) showRolloutsForStep(hover.step);
  });
  // A touch drag reads the chart the same way a mouse does, but it never fires
  // pointerleave, so the crosshair has to be taken down on release.
  svg.addEventListener("pointercancel", clear);
  svg.addEventListener("pointerup", clear);
}

function chartFigure(chart) {
  const geometry = chartGeometry(chart);
  if (!geometry) return null;
  const panel = element("section", undefined, "run-chart");
  panel.append(element("h3", chart.title));
  panel.append(chartLegend(geometry));

  const svg = svgElement("svg", {
    viewBox: `${-CHART_GUTTER.left} ${-CHART_GUTTER.top}`
      + ` ${geometry.width + CHART_GUTTER.left + CHART_GUTTER.right}`
      + ` ${geometry.height + CHART_GUTTER.top + CHART_GUTTER.bottom}`,
    class: "run-chart-plot", role: "img",
    "aria-label": `${chart.title} across steps ${geometry.minStep} to ${geometry.maxStep}`,
  });

  for (const tick of geometry.yTicks) {
    svg.append(svgElement("line", {
      x1: 0, y1: tick.y, x2: geometry.width, y2: tick.y,
      class: tick.value === 0 ? "chart-grid chart-zero" : "chart-grid",
    }));
    svg.append(svgElement("text", { x: -9, y: tick.y, class: "chart-tick chart-tick-y" },
      formatTick(tick.value)));
  }
  for (const tick of geometry.xTicks) {
    svg.append(svgElement("line", {
      x1: tick.x, y1: 0, x2: tick.x, y2: geometry.height, class: "chart-grid",
    }));
    svg.append(svgElement("text", { x: tick.x, y: geometry.height + 16, class: "chart-tick chart-tick-x" },
      String(tick.value)));
  }
  svg.append(svgElement("text", {
    x: geometry.width / 2, y: geometry.height + CHART_GUTTER.bottom, class: "chart-tick chart-axis-title",
  }, "step"));

  // An SVG only hit-tests painted children, so the empty space between the
  // lines raises no pointer events at all. This unpainted rect exists purely
  // to make the whole panel hoverable, which is how the cursor can read a step
  // it happens not to be exactly on top of.
  svg.append(svgElement("rect", {
    x: -CHART_GUTTER.left, y: -CHART_GUTTER.top,
    width: geometry.width + CHART_GUTTER.left + CHART_GUTTER.right,
    height: geometry.height + CHART_GUTTER.top + CHART_GUTTER.bottom,
    class: "chart-hit",
  }));

  const crosshair = svgElement("line", {
    x1: 0, y1: 0, x2: 0, y2: geometry.height, class: "chart-crosshair", visibility: "hidden",
  });
  svg.append(crosshair);
  for (const entry of geometry.series) {
    svg.append(svgElement("path", {
      d: chartPath(entry.coordinates), class: `chart-line tone-${entry.tone}`, fill: "none",
    }));
    // Marking the points keeps a two-step run from looking like a bare line and
    // makes a single reading visible at all.
    for (const point of entry.coordinates) {
      svg.append(svgElement("circle", { cx: point.x, cy: point.y, r: 2.5, class: `chart-dot tone-${entry.tone}` }));
    }
  }

  const markers = svgElement("g", { class: "chart-markers" });
  svg.append(markers);
  panel.append(svg);
  panel.append(element("p", chart.note, "section-note"));
  attachChartHover(panel, svg, geometry, markers, crosshair);
  return panel;
}

function renderRunCharts() {
  const container = byID("run-charts");
  container.replaceChildren();
  for (const chart of runCharts(runRows())) {
    const figure = chartFigure(chart);
    if (figure) container.append(figure);
  }
  byID("run-pending").hidden = runMetrics.length > 0;
}

// The environment reports a step's metrics; the rollouts it recorded carry what
// happened inside that step. Charting the two together is what lets the run
// view show a measurement the environment does not report, without the rollouts
// having to be loaded a second time.
function runRows() {
  return withGroupSignal(runMetrics, rolloutIndex?.data ?? []);
}

function renderRunProgress() {
  const steps = runMetrics.length;
  const maxSteps = runOverview?.config?.max_steps;
  byID("run-progress").textContent = steps === 0
    ? "· no steps yet"
    : isNumber(maxSteps) ? `· step ${steps} of ${maxSteps}` : `· ${count(steps)} steps`;
}

function renderRunView() {
  byID("run-job-id").textContent = rolloutIndex?.job_id || "";
  renderRunProgress();
  notices("run-warnings", runWarnings(runRows()));
  renderRunHeadline();
  renderRunFacts();
  renderRunCharts();
}

// The log is appended to, so only what is new is fetched and appended. Reading
// it should not jump the reader back to the top on every poll.
function appendRunLog(tail) {
  if (!tail || typeof tail.text !== "string") return;
  const view = byID("run-log-text");
  if (tail.text) view.append(document.createTextNode(tail.text));
  runLogOffset = isNumber(tail.offset) ? tail.offset : runLogOffset;
  byID("run-log-size").textContent = isNumber(tail.size) && tail.size > 0
    ? `· ${count(Math.round(tail.size / 1024))} KB written` : "";
  const panel = byID("run-log");
  if (!panel.hidden && view.scrollHeight - view.scrollTop - view.clientHeight < 80) {
    view.scrollTop = view.scrollHeight;
  }
}

// A job that was never followed on this machine has no local artifacts. That is
// a normal way to open the monitor, so the run panel simply stays away.
async function loadRunView() {
  let overview;
  try {
    overview = await fetchRunOverview();
  } catch {
    return false;
  }
  if (!overview || !overview.run) return false;
  runOverview = overview.run;
  await refreshRunMetrics();
  renderRunView();
  return true;
}

async function refreshRunMetrics() {
  try {
    const metrics = await fetchRunMetrics();
    if (metrics && Array.isArray(metrics.data)) runMetrics = metrics.data;
  } catch {
    // The rollouts are still worth showing; the next poll retries.
  }
}

async function pollRunView() {
  if (!runOverview || !byID("snapshot").hidden) return;
  try {
    const overview = await fetchRunOverview();
    if (overview && overview.run) runOverview = overview.run;
  } catch {
    return;
  }
  await refreshRunMetrics();
  // Metrics are kept current whichever tab is up, so switching to the charts
  // shows the run as it is now rather than as it was when the tab was left.
  if (!byID("run-overview").hidden) renderRunView();
  await refreshRunLog();
}

// The log is only read while it is being looked at. It is the largest artifact
// by far, and a background tab polling it would cost more than everything else
// on the page put together.
async function refreshRunLog() {
  if (byID("run-log").hidden) return;
  try {
    appendRunLog(await fetchRunLog(fetch, runLogOffset));
  } catch {
    // A log that cannot be read does not invalidate the charts above it.
  }
}

async function load() {
  byID("retry").disabled = true;
  byID("load-error").hidden = true;
  byID("load-status").className = "notice";
  byID("load-status").textContent = "Loading saved snapshot…";
  try {
    rolloutIndex = await fetchRolloutIndex();
    if (rolloutIndex) {
      refreshFilters();
      renderRolloutList();
      // The run panel is best-effort: a job can always be browsed by its
      // rollouts, with or without a local mirror to describe it.
      hasRunView = await loadRunView();
      showRolloutList();
      startPolling();
      return;
    }
    setSnapshot(await fetchSnapshot());
  } catch (error) {
    for (const id of ["snapshot", "job-tabs", "rollout-list", "run-overview", "run-log"]) byID(id).hidden = true;
    byID("load-status").className = "sr-only";
    byID("load-status").textContent = "";
    byID("load-error").hidden = false;
    byID("error-message").textContent = error.message;
  } finally {
    byID("retry").disabled = false;
  }
}

byID("back-button").addEventListener("click", () => {
  byID("back-to-list").hidden = true;
  renderRolloutList();
  showRolloutList();
});
byID("list-split").addEventListener("change", renderRolloutList);
byID("list-step").addEventListener("change", renderRolloutList);

byID("retry").addEventListener("click", load);
byID("final-response-toggle").addEventListener("click", () => {
  const toggle = byID("final-response-toggle");
  const expanded = toggle.getAttribute("aria-expanded") === "true";
  toggle.setAttribute("aria-expanded", String(!expanded));
  toggle.textContent = expanded ? "Show full response" : "Hide full response";
  byID("final-response-text").textContent = expanded ? model.finalResponsePreview : model.finalResponse;
  byID("final-response-text").classList.toggle("collapsed", expanded);
  byID("final-response-note").hidden = !expanded || !model.finalResponseReasoningHidden;
});
load();
