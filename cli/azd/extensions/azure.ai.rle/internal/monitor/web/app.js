// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import {
  bootstrapSession, buildGraph, chartScales, fetchSnapshot, isNumber, mapSnapshot, present, rewardGeometry,
  sequenceData, sequenceLabel, sequencePage, SessionRequiredError, TOKEN_PAGE_SIZE, unlockSession,
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

function selectTab(name, focus = false) {
  for (const tab of document.querySelectorAll('[role="tab"]')) {
    const selected = tab.id === `tab-${name}`;
    tab.setAttribute("aria-selected", String(selected));
    tab.tabIndex = selected ? 0 : -1;
    byID(tab.getAttribute("aria-controls")).hidden = !selected;
    if (selected && focus) tab.focus();
  }
  if (name === "tokens") renderSequence();
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
    group.append(svgElement("text", { x: 32, y: 27, class: "node-kind" },
      node.unresolved ? "UNORDERED" : node.discarded ? "DISCARDED" : node.root ? "ROOT CALL" : "MODEL CALL"));
    group.append(svgElement("text", { x: 16, y: 51, class: "node-label" }, node.label));
    const detail = node.records.length === 1 ? node.records[0].raw.finish_reason : null;
    group.append(svgElement("text", { x: 16, y: 72, class: "node-detail" },
      short(detail ? `${detail} · ${node.id ?? "ID not reported"}` : node.id ?? "ID not reported", 26)));
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
  metric("Episode steps", model.steps?.length);
  metric("Model calls", model.turns?.length);
  metric("Captured sequences", model.graph.sequences?.length);
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
  renderDetails();
  // Clear training output even when its panel stays hidden.
  renderSequence();
  selectTab("graph");
  byID("load-error").hidden = true;
  byID("access-panel").hidden = true;
  byID("access-error").textContent = "";
  byID("access-code").value = "";
  byID("snapshot").hidden = false;
  byID("load-status").textContent = "Saved snapshot loaded.";
  byID("load-status").className = "sr-only";
}

for (const tab of document.querySelectorAll('[role="tab"]')) {
  tab.addEventListener("click", () => selectTab(tab.id.slice(4)));
  tab.addEventListener("keydown", (event) => {
    const tabs = [...document.querySelectorAll('[role="tab"]')];
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

function showLocked(message = "") {
  byID("snapshot").hidden = true;
  byID("load-error").hidden = true;
  byID("access-panel").hidden = false;
  byID("access-error").textContent = message;
  byID("load-status").className = "sr-only";
  byID("load-status").textContent = "Snapshot locked. Enter the local access code to continue.";
  byID("access-code").focus();
}

async function load() {
  byID("retry").disabled = true;
  byID("load-error").hidden = true;
  byID("access-panel").hidden = true;
  byID("load-status").className = "notice";
  byID("load-status").textContent = "Loading saved snapshot…";
  try {
    await bootstrapSession(window.location, window.history);
    setSnapshot(await fetchSnapshot());
  } catch (error) {
    if (error instanceof SessionRequiredError) {
      showLocked();
    } else {
      byID("snapshot").hidden = true;
      byID("load-status").className = "sr-only";
      byID("load-status").textContent = "";
      byID("load-error").hidden = false;
      byID("error-message").textContent = error.message;
    }
  } finally {
    byID("retry").disabled = false;
  }
}

byID("access-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const code = byID("access-code").value.trim();
  byID("access-code").value = "";
  byID("unlock").disabled = true;
  byID("access-code").disabled = true;
  byID("access-error").textContent = "";
  byID("load-status").textContent = "Unlocking snapshot…";
  try {
    await unlockSession(code);
    await load();
    if (!byID("snapshot").hidden) byID("main").focus();
  } catch (error) {
    showLocked(error instanceof SessionRequiredError
      ? "The access code was not accepted. Check the code in the monitor terminal and try again."
      : error.message);
  } finally {
    byID("unlock").disabled = false;
    byID("access-code").disabled = false;
    if (!byID("access-panel").hidden) byID("access-code").focus();
  }
});

byID("retry").addEventListener("click", load);
load();
