// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

export const isRecord = (value) => value !== null && typeof value === "object" && !Array.isArray(value);
export const isNumber = (value) => typeof value === "number" && Number.isFinite(value);
export const present = (value) => value !== undefined && value !== null;
const isCount = (value) => Number.isSafeInteger(value) && value >= 0;
const isString = (value) => typeof value === "string";
const isBoolean = (value) => typeof value === "boolean";

function requireValue(condition, field) {
  if (!condition) throw new Error(`Invalid snapshot: ${field}.`);
}

function optional(record, key, predicate, path) {
  if (present(record[key])) requireValue(predicate(record[key]), `${path}.${key} has an unexpected value`);
}

function records(value, path) {
  if (!present(value)) return null;
  requireValue(Array.isArray(value) && value.every(isRecord), `${path} must be an array of objects`);
  return value;
}

function validateFields(record, fields, path) {
  for (const [key, predicate] of Object.entries(fields)) optional(record, key, predicate, path);
}

function messageRole(message, fallback) {
  if (!isString(message?.role) || !message.role.trim()) return fallback;
  const role = message.role.trim().toLowerCase();
  return ["assistant", "system", "tool", "user"].includes(role) ? role : "other";
}

function toolCallName(call) {
  if (!isRecord(call)) return null;
  if (isString(call.function?.name) && call.function.name.trim()) return call.function.name.trim();
  if (isString(call.name) && call.name.trim()) return call.name.trim();
  return null;
}

// The tool calls a captured response made, in the order the model emitted them.
//
// A turn that ends in tool calls reports its finish reason as "tool_calls",
// which says that the model called something but never which tool, and that is
// the question a reader has when they are looking at the call.
export function toolCalls(message) {
  const calls = Array.isArray(message?.tool_calls) ? message.tool_calls.filter(isRecord) : [];
  return calls.map((call, index) => ({
    index,
    name: toolCallName(call) ?? "unnamed tool",
    id: isString(call.id) && call.id.trim() ? call.id.trim() : null,
    arguments: call.function?.arguments ?? call.arguments ?? null,
  }));
}

// The same calls collapsed to "name ×n" for the places that have room for a
// line rather than a list. Repeats are counted because a model calling one tool
// three times and calling three tools are different behaviours.
export function toolCallSummary(message) {
  const counts = new Map();
  for (const call of toolCalls(message)) counts.set(call.name, (counts.get(call.name) ?? 0) + 1);
  return [...counts].map(([name, times]) => (times > 1 ? `${name} \u00d7${times}` : name));
}

function finalResponseSummary(value) {
  if (!isString(value) || !value.trim()) {
    return { full: null, preview: null, reasoningHidden: false, expandable: false };
  }
  const full = value.trim();
  const withoutReasoning = full.replace(/<think(?:\s[^>]*)?>[\s\S]*?<\/think\s*>/gi, "").trim();
  const preview = withoutReasoning || full;
  return {
    full,
    preview,
    reasoningHidden: preview !== full,
    expandable: preview !== full || preview.length > 420 || preview.split(/\r?\n/).length > 6,
  };
}

// Leave the parsed response unchanged; derive only display values, never missing metrics.
export function mapSnapshot(snapshot) {
  requireValue(isRecord(snapshot), "expected an object");
  const response = snapshot.response;
  requireValue(isRecord(response), "response must be an object");
  requireValue(isString(response.rollout_id) && response.rollout_id.trim().length > 0, "response.rollout_id is required");
  requireValue(isNumber(response.reward), "response.reward must be a finite number");
  if (Object.hasOwn(response, "success")) requireValue(isBoolean(response.success), "response.success must be a boolean");
  optional(response, "final_response", isString, "response");
  requireValue(isString(snapshot.source) && snapshot.source.trim().length > 0, "source is required");
  optional(snapshot, "environment", isRecord, "snapshot");
  const environment = snapshot.environment ?? null;
  optional(snapshot, "warnings", (value) => Array.isArray(value) && value.every(isString), "snapshot");
  if (environment) {
    for (const key of ["name", "version"]) {
      requireValue(isString(environment[key]) && environment[key].trim().length > 0, `environment.${key} is required`);
    }
  }
  if (Object.hasOwn(snapshot, "saved_at")) {
    requireValue(
      isString(snapshot.saved_at) &&
        /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(snapshot.saved_at) &&
        Number.isFinite(Date.parse(snapshot.saved_at)),
      "saved_at must be an RFC 3339 timestamp",
    );
  }
  optional(response, "episode", isRecord, "response");
  optional(response, "rollout", isRecord, "response");
  const episode = response.episode ?? {};
  const graph = response.rollout ?? {};
  validateFields(episode, { kind: isString, termination_reason: isString, ungraded: isBoolean }, "episode");
  validateFields(graph, { capture_level: isString, trainable: isBoolean, stats: isRecord,
    sequences: Array.isArray, validation: Array.isArray }, "rollout");
  const stats = graph.stats ?? {};
  for (const key of ["n_turns", "n_roots", "n_forks", "n_discarded", "n_sequences",
    "n_trainable_sequences", "n_trainable_tokens"]) optional(stats, key, isCount, "rollout.stats");
  const steps = records(episode.steps, "episode.steps");
  const turns = records(graph.turns, "rollout.turns");
  steps?.forEach((step, index) => validateFields(step,
    { capture_node_id: isString, reward: isNumber, episode_done: isBoolean }, `episode.steps[${index}]`));
  turns?.forEach((turn, index) => validateFields(turn, {
    node_id: isString, index: isCount, root_id: isString, n_prompt: isCount, n_sampled: isCount,
    n_tools: isCount, finish_reason: isString, discarded: isBoolean,
    request_messages: (value) => Array.isArray(value) && value.every(isRecord),
    response_message: isRecord,
  }, `rollout.turns[${index}]`));

  const turnsByID = new Map();
  turns?.forEach((turn, position) => {
    if (turn.node_id) {
      const matches = turnsByID.get(turn.node_id) ?? [];
      matches.push(position);
      turnsByID.set(turn.node_id, matches);
    }
  });
  const stepNumbersByTurn = new Map();
  const mappedSteps = steps?.map((step, position) => {
    const number = position + 1;
    const turnPositions = step.capture_node_id ? turnsByID.get(step.capture_node_id) ?? [] : [];
    for (const turnPosition of turnPositions) {
      const numbers = stepNumbersByTurn.get(turnPosition) ?? [];
      numbers.push(number);
      stepNumbersByTurn.set(turnPosition, numbers);
    }
    return { raw: step, number, reward: step.reward ?? null, turnPositions };
  }) ?? null;
  const mappedTurns = turns?.map((turn, position) => {
    const requestMessages = turn.request_messages ?? [];
    const responseMessage = turn.response_message ?? null;
    const toolCalls = Array.isArray(responseMessage?.tool_calls)
      ? responseMessage.tool_calls.filter(isRecord)
      : [];
    return {
      raw: turn, position, label: `Turn ${position + 1}`,
      stepNumbers: stepNumbersByTurn.get(position) ?? [],
      prompt: graph.capture_level === "tokens" ? turn.n_prompt ?? null : null,
      sampled: graph.capture_level === "tokens" ? turn.n_sampled ?? null : null,
      requestMessages,
      responseMessage,
      toolCalls,
      flow: [
        ...requestMessages.map((message) => messageRole(message, "request")),
        ...(responseMessage === null ? [] : [messageRole(responseMessage, "assistant")]),
      ],
    };
  }) ?? null;
  const finalResponse = finalResponseSummary(response.final_response);
  const hasConversation = mappedTurns?.some((turn) =>
    turn.requestMessages.length > 0 || turn.responseMessage !== null) ?? false;
  const allToolCalls = mappedTurns?.flatMap((turn) => turn.toolCalls) ?? [];
  const toolNames = [...new Set(allToolCalls.map(toolCallName).filter(isString))];

  return { response, source: snapshot.source, savedAt: snapshot.saved_at, environment, warnings: snapshot.warnings ?? [],
    episode, graph, stats,
    steps: mappedSteps, turns: mappedTurns, tokensCaptured: graph.capture_level === "tokens",
    finalResponse: finalResponse.full, finalResponsePreview: finalResponse.preview,
    finalResponseReasoningHidden: finalResponse.reasoningHidden,
    finalResponseExpandable: finalResponse.expandable,
    hasConversation, toolActivity: { count: allToolCalls.length, names: toolNames },
    execution: executionStatus(response),
    outcome: response.success === true ? "Task succeeded" : response.success === false ? "Task unsuccessful" : null };
}

// The harness grades a rollout whether or not it finished, so `success` is a task
// verdict and says nothing about execution. A rollout killed by a transport error
// still carries a reward, a graph and success=false, which is indistinguishable
// from a complete but poor attempt unless the agent's own output is read.
const ROLLOUT_ERROR_PREFIX = "ROLLOUT ERROR";
const PYTHON_EXCEPTION = /^([A-Za-z_][\w.]*(?:Error|Exception|Timeout|Interrupt))(?::\s*(.*))?$/;

// A Python traceback ends with the exception that escaped, so the last match is
// the one that stopped the rollout; earlier matches are the frames it wrapped.
export function exceptionFromTraceback(text) {
  if (!isString(text)) return null;
  let found = null;
  for (const line of text.split("\n")) {
    const match = PYTHON_EXCEPTION.exec(line.trim());
    if (match) found = { type: match[1], message: match[2]?.trim() || null };
  }
  return found;
}

export function executionStatus(response) {
  const agentResponse = response?.result?.agent_response;
  if (isString(agentResponse) && agentResponse.startsWith(ROLLOUT_ERROR_PREFIX)) {
    const failure = exceptionFromTraceback(agentResponse);
    return { state: "failed", error: failure?.type ?? null, detail: failure?.message ?? null };
  }
  const turns = response?.rollout?.turns;
  if (Array.isArray(turns) && turns.length === 0) {
    return { state: "failed", error: "No model calls", detail: "The rollout recorded no turns." };
  }
  return { state: "completed", error: null, detail: null };
}

export const GRAPH_PAGE_SIZE = 80;
export const TOKEN_PAGE_SIZE = 100;
export const SEQUENCE_BIN_LIMIT = 160;
const isLogprob = (value) => isNumber(value) && value <= 0;
const nodeID = (value) => typeof value === "string" && value.length > 0;

export function sequenceLabel(sequence, index) {
  const role = typeof sequence?.role === "string" ? sequence.role : "role not reported";
  const path = Array.isArray(sequence?.node_ids) ? sequence.node_ids.filter(nodeID) : [];
  const short = (id) => id.length > 20 ? `${id.slice(0, 17)}…` : id;
  return `Sequence ${index + 1} · ${short(role)} · ${path.length ? `${short(path[0])} → ${short(path.at(-1))}` : "path not reported"}`;
}

// Only exported sequence paths establish relationships. Arrival order is not a trace.
export function buildGraph(graph = {}, { sequenceIndex = null, page = 0, limit = GRAPH_PAGE_SIZE, focusKey = null } = {}) {
  const sequences = Array.isArray(graph.sequences) ? graph.sequences : [];
  const turns = Array.isArray(graph.turns) ? graph.turns : [];
  const nodes = new Map();
  const edges = new Map();
  const notices = [];
  const ensure = (key, id) => {
    if (!nodes.has(key)) nodes.set(key, { key, id, records: [], memberships: [], first: false, incoming: 0, layer: 0 });
    return nodes.get(key);
  };
  let invalidPaths = 0;
  let repeatedPaths = 0;
  sequences.forEach((sequence, index) => {
    if (sequenceIndex !== null && index !== sequenceIndex) return;
    if (!Array.isArray(sequence?.node_ids)) { invalidPaths++; return; }
    const seen = new Set();
    sequence.node_ids.forEach((id, position, path) => {
      if (!nodeID(id)) { invalidPaths++; return; }
      const node = ensure(`id:${id}`, id);
      if (!node.memberships.includes(index)) node.memberships.push(index);
      if (seen.has(id)) repeatedPaths++;
      seen.add(id);
      if (position === 0) node.first = true;
      // Invalid IDs break the path; never bridge a missing relationship.
      if (position && nodeID(path[position - 1])) {
        const from = `id:${path[position - 1]}`;
        const key = JSON.stringify([from, node.key]);
        if (!edges.has(key)) edges.set(key, { from, to: node.key });
      }
    });
  });
  turns.forEach((turn, position) => {
    const key = nodeID(turn.node_id) ? `id:${turn.node_id}` : `turn:${position}`;
    if (sequenceIndex !== null && !nodes.has(key)) return;
    ensure(key, nodeID(turn.node_id) ? turn.node_id : null).records.push({ raw: turn, position });
  });
  const adjacency = new Map([...nodes.keys()].map((key) => [key, []]));
  for (const edge of edges.values()) {
    nodes.get(edge.to).incoming++;
    adjacency.get(edge.from).push(edge.to);
  }
  const remaining = new Map([...nodes.values()].map((node) => [node.key, node.incoming]));
  const queue = [...nodes.values()].filter((node) => !node.incoming);
  let visited = 0;
  for (let head = 0; head < queue.length; head++) {
    const node = queue[head];
    visited++;
    for (const target of adjacency.get(node.key)) {
      const next = nodes.get(target);
      next.layer = Math.max(next.layer, node.layer + 1);
      remaining.set(target, remaining.get(target) - 1);
      if (!remaining.get(target)) queue.push(next);
    }
  }
  const unresolved = nodes.size - visited;
  const all = [...nodes.values()];
  const duplicateCount = all.filter((node) => node.records.length > 1).length;
  const missingCount = all.filter((node) => !node.records.length).length;
  const unlinked = all.filter((node) => !node.memberships.length).length;
  const conflictingRoots = all.filter((node) => node.first && node.incoming > 0).length;
  if (duplicateCount) notices.push(`${duplicateCount} duplicate node ID(s): call details are ambiguous; inspect the records for each ID.`);
  if (missingCount) notices.push(`${missingCount} referenced node(s) have no captured call details.`);
  if (unlinked) notices.push(`${unlinked} call(s) have no sequence path. Relationships are not reported; these calls are shown unconnected.`);
  if (invalidPaths) notices.push("Some sequence paths are missing or invalid. No relationships are inferred across invalid IDs.");
  if (repeatedPaths) notices.push("Repeated node IDs occur within sequence paths.");
  if (conflictingRoots) notices.push(`${conflictingRoots} path start(s) also have incoming edges: conflicting root evidence.`);
  if (unresolved) notices.push(`Invalid topology: a cycle prevents ordering ${unresolved} node(s), including any dependent calls. They are shown in a separate row.`);
  for (const node of all) {
    node.unresolved = remaining.get(node.key) > 0;
    node.root = node.first && !node.incoming;
    node.discarded = node.records.length === 1 && node.records[0].raw.discarded === true;
    node.label = !node.records.length ? "Missing call details" :
      node.records.length > 1 ? "Ambiguous model call" : `Model call ${node.records[0].position + 1}`;
    // What the call did, as opposed to where it sits in the graph. A node whose
    // record is ambiguous or missing has no single answer, so it reports none.
    const only = node.records.length === 1 ? node.records[0].raw : null;
    node.tools = only ? toolCallSummary(only.response_message) : [];
    node.finishReason = isString(only?.finish_reason) && only.finish_reason.trim()
      ? only.finish_reason.trim() : null;
    node.kind = node.unresolved ? "UNORDERED" : node.discarded ? "DISCARDED"
      : node.tools.length ? (node.root ? "ROOT TOOL CALL" : "TOOL CALL")
      : node.root ? "ROOT CALL" : "MODEL CALL";
  }
  const pageCount = Math.max(1, Math.ceil(all.length / limit));
  const focusIndex = focusKey === null ? -1 : all.findIndex((node) => node.key === focusKey);
  const requestedPage = focusIndex < 0 ? page : Math.floor(focusIndex / limit);
  const currentPage = Math.max(0, Math.min(pageCount - 1, Math.floor(requestedPage) || 0));
  const visible = all.slice(currentPage * limit, (currentPage + 1) * limit);
  const keys = new Set(visible.map((node) => node.key));
  const visibleEdges = [...edges.values()].filter((edge) => keys.has(edge.from) && keys.has(edge.to));
  if (all.length > limit) notices.push(`Graph window: ${visible.length} of ${all.length} nodes. Cross-window edges are not drawn. Select a sequence or use the node-page controls to inspect the rest.`);
  const levels = [...new Set(visible.filter((node) => !node.unresolved).map((node) => node.layer))].sort((a, b) => a - b);
  const rows = new Map();
  for (const node of visible) {
    const row = node.unresolved ? levels.length : levels.indexOf(node.layer);
    if (!rows.has(row)) rows.set(row, []);
    rows.get(row).push(node);
  }
  const columns = Math.max(1, ...[...rows.values()].map((row) => row.length));
  const width = Math.max(360, columns * 250 + 64);
  for (const [row, rowNodes] of rows) {
    rowNodes.forEach((node, column) => {
      node.x = (width - rowNodes.length * 250) / 2 + column * 250 + 15;
      node.y = 55 + row * 155;
    });
  }
  return { nodes: visible, edges: visibleEdges, notices, total: all.length, page: currentPage, pageCount,
    width, height: Math.max(300, rows.size * 155 + 100) };
}

export function sequenceData(sequence, captureLevel) {
  const arrays = Object.fromEntries(["input_ids", "loss_mask", "logprobs"].map((key) =>
    [key, Array.isArray(sequence?.[key]) ? sequence[key] : null]));
  const lengths = Object.values(arrays).filter(Array.isArray).map((array) => array.length);
  const length = Math.max(0, ...lengths);
  const notices = [];
  if (captureLevel !== "tokens") notices.push('Capture level is not "tokens". Only arrays actually included in the export are shown.');
  for (const [key, value] of Object.entries(arrays)) {
    if (value === null) notices.push(present(sequence?.[key]) ? `${key} is not an array.` : `${key} is not included.`);
    else if (!value.length) notices.push(`${key} is present but empty.`);
  }

  if (new Set(lengths).size > 1) notices.push("Array lengths differ. All positions are retained; missing values are not filled with zero.");
  const positions = { target: [], excluded: [], unknown: [] };
  const binSize = Math.max(1, Math.ceil(length / SEQUENCE_BIN_LIMIT));
  const bins = [];
  let scored = 0, invalidTokens = 0, missingScores = 0;
  let minLogprob = null, maxLogprob = null;
  for (let position = 0; position < length; position++) {
    const mask = arrays.loss_mask?.[position];
    const kind = mask === 1 ? "target" : mask === 0 ? "excluded" : "unknown";
    positions[kind].push(position);
    if (position % binSize === 0) {
      bins.push({ start: position, end: Math.min(length, position + binSize),
        target: 0, excluded: 0, unknown: 0, scored: 0, min: null, max: null, firstScored: null });
    }
    const bin = bins.at(-1);
    bin[kind]++;
    if (present(arrays.input_ids?.[position]) && !isCount(arrays.input_ids[position])) invalidTokens++;
    if (mask !== 1) continue;
    const value = arrays.logprobs?.[position];
    if (!isLogprob(value)) { missingScores++; continue; }
    scored++;
    bin.scored++;
    bin.firstScored ??= position;
    bin.min = bin.min === null ? value : Math.min(bin.min, value);
    bin.max = bin.max === null ? value : Math.max(bin.max, value);
    minLogprob = minLogprob === null ? value : Math.min(minLogprob, value);
    maxLogprob = maxLogprob === null ? value : Math.max(maxLogprob, value);
  }
  if (positions.unknown.length) notices.push(`${positions.unknown.length} position(s) have a missing or invalid loss mask.`);
  if (missingScores) notices.push(`${missingScores} target position(s) have missing or invalid log probabilities; these are not plotted.`);
  if (invalidTokens) notices.push(`${invalidTokens} token ID(s) are not nonnegative safe integers. Consult the original artifact for exact values.`);
  if (isCount(sequence?.n_trainable) && arrays.loss_mask && !positions.unknown.length &&
      sequence.n_trainable !== positions.target.length) {
    notices.push("Reported target count differs from the number of mask-1 positions. Overview counts are computed from the arrays.");
  }
  return { arrays, length, notices, positions, bins, scored, minLogprob, maxLogprob };
}

export function chartScales(steps = [], turns = []) {
  return {
    reward: steps.reduce((max, step) => isNumber(step.reward) ? Math.max(max, Math.abs(step.reward)) : max, 0),
    tokens: turns.reduce((max, turn) => Math.max(max,
      isNumber(turn.prompt) ? turn.prompt : 0, isNumber(turn.sampled) ? turn.sampled : 0), 0),
  };
}

export function rewardGeometry(value, max) {
  if (!isNumber(value)) return null;
  // Divide first so even extreme finite rewards cannot overflow the SVG coordinates.
  const width = max > 0 ? Math.abs(value) / max * 200 : 0;
  return { x: value < 0 ? 200 - width : 200, width, zero: value === 0, negative: value < 0 };
}

export function sequencePage(data, page = 0, size = TOKEN_PAGE_SIZE, filter = "all") {
  requireValue(["all", "target", "excluded", "unknown"].includes(filter), "unknown token filter");
  const positions = filter === "all" ? null : data.positions[filter];
  const total = positions === null ? data.length : positions.length;
  const pageCount = Math.max(1, Math.ceil(total / size));
  const currentPage = Math.max(0, Math.min(pageCount - 1, Math.floor(page) || 0));
  const start = currentPage * size;
  const end = Math.min(total, start + size);
  const rows = [];
  for (let index = start; index < end; index++) {
    const position = positions === null ? index : positions[index];
    const token = data.arrays.input_ids?.[position];
    const mask = data.arrays.loss_mask?.[position];
    const logprob = data.arrays.logprobs?.[position];
    rows.push({ position, token, mask, logprob,
      score: mask === 0 ? "excluded" : mask !== 1 ? "unknown" : isLogprob(logprob) ? "scored" : "missing" });
  }
  return { rows, page: currentPage, pageCount, start, end, total };
}

export async function fetchSnapshot(fetcher = fetch, rolloutID = "") {
  let result;
  const path = rolloutID ? `/api/rollout?id=${encodeURIComponent(rolloutID)}` : "/api/rollout";
  try {
    result = await fetcher(path, { credentials: "same-origin", cache: "no-store",
      headers: { Accept: "application/json" } });
  } catch {
    throw new Error("Could not reach the local monitor. Check that the monitor command is still running, then try again.");
  }
  if (!result.ok) {
    throw new Error(`The local monitor returned HTTP ${result.status}. Check the monitor terminal, then try again.`);
  }
  try {
    return await result.json();
  } catch {
    throw new Error("The local monitor returned invalid JSON. Check the saved execution response.");
  }
}

// Returns null when the monitor serves a single saved rollout and has no set to browse.
export async function fetchRolloutIndex(fetcher = fetch, after = "") {
  const url = after ? `/api/rollouts?after=${encodeURIComponent(after)}` : "/api/rollouts";
  let result;
  try {
    result = await fetcher(url, { credentials: "same-origin", cache: "no-store",
      headers: { Accept: "application/json" } });
  } catch {
    throw new Error("Could not reach the local monitor. Check that the monitor command is still running, then try again.");
  }
  if (result.status === 404) return null;
  if (!result.ok) {
    throw new Error(`The local monitor returned HTTP ${result.status}. Check the monitor terminal, then try again.`);
  }
  try {
    return await result.json();
  } catch {
    throw new Error("The local monitor returned invalid JSON. Check the monitor terminal, then try again.");
  }
}

// Execution state for the listed rollouts, keyed by rollout id.
//
// The index cannot answer this. A rollout that died mid-flight is still graded,
// so it arrives with a reward, a graph and success=false -- the same row shape
// as one that merely scored badly. Only the agent's output separates them, and
// at ~342KB a body the monitor classifies them server-side and the page reads
// the verdicts here, which is why this is a separate call rather than a field
// on the index.
//
// The result is partial while the monitor works through a run, so a rollout
// missing from `data` means "not classified yet", never "completed". A failure
// to reach it is not worth reporting: the list is still valid without the
// column, so this returns null rather than throwing the way the index does.
export async function fetchRolloutStates(fetcher = fetch) {
  let result;
  try {
    result = await fetcher("/api/rollouts/states", { credentials: "same-origin", cache: "no-store",
      headers: { Accept: "application/json" } });
  } catch {
    return null;
  }
  if (!result.ok) return null;
  try {
    return await result.json();
  } catch {
    return null;
  }
}

// ---------------------------------------------------------------------------
// Training run artifacts
//
// A job is not only the rollouts it recorded. These read what the run is -- its
// environment, model and hyperparameters -- and how it is going, so a run can
// be judged from the dashboard rather than from the terminal that submitted it.
// ---------------------------------------------------------------------------

// The charts a training run is actually read by. Each panel answers one
// question, which is why related series share an axis rather than getting a
// panel each: reward means little without the validation reward beside it.
//
// Keys are the recipe's own metric names. A series whose key never appears is
// dropped rather than drawn flat at zero, because a metric the recipe did not
// emit is not the same as a metric that was zero.
export const RUN_CHARTS = [
  {
    id: "reward",
    title: "Reward",
    note: "Training reward is the policy on rollouts it learns from; validation is held out. "
      + "They should rise together. Training alone rising is overfitting.",
    series: [
      { key: "env/all/reward/total", name: "Train", tone: "primary" },
      { key: "rle_harness/validation_mean_reward", name: "Validation", tone: "accent" },
    ],
  },
  {
    id: "success",
    title: "Task success rate",
    note: "The fraction of rollouts the environment judged successful, which is the demo number.",
    range: [0, 1],
    series: [
      { key: "env/all/rle_harness/task_success", name: "Train", tone: "primary" },
      { key: "rle_harness/validation_success_rate", name: "Validation", tone: "accent" },
    ],
  },
  {
    id: "signal",
    title: "Learning signal",
    note: "A group teaches only through the spread of its rollouts: advantage is each reward's "
      + "distance from its group's mean, so a group whose rollouts agree contributes nothing no "
      + "matter how they scored. Spread is that distance averaged over groups. All-good means the "
      + "tasks are too easy to learn from; all-bad means too hard.",
    range: [0, 1],
    series: [
      { key: "derived/by_group/reward_sd", name: "Spread", tone: "positive" },
      { key: "derived/by_group/frac_all_good", name: "All good", tone: "primary" },
      { key: "derived/by_group/frac_all_bad", name: "All bad", tone: "negative" },
    ],
    // The environment reports the same split, but it counts a group as teaching
    // something when its rewards are not all *exactly* equal. That holds for a
    // reward like a passing test suite and never holds for a weighted score, so
    // for most environments the reported series read 1/0/0 for the whole run.
    // They are kept for the case where no rollout index is loaded to derive from.
    fallback: [
      { key: "env/all/by_group/frac_mixed", name: "Mixed", tone: "positive" },
      { key: "env/all/by_group/frac_all_good", name: "All good", tone: "primary" },
      { key: "env/all/by_group/frac_all_bad", name: "All bad", tone: "negative" },
    ],
  },
  {
    id: "optim",
    title: "Optim: grad norm · entropy · LR",
    note: "Each series is scaled to its own range, because a learning rate of 4e-5 and a gradient norm "
      + "of 1.2 share no axis. Hover for the real numbers. A grad-norm spike is an update large enough "
      + "to move the policy somewhere it cannot recover from, and usually precedes a reward collapse on "
      + "the next step. Entropy falling fast is the policy giving up exploring, after which reward flattens.",
    scale: "independent",
    series: [
      { key: "skyrl.ai/grad_norm", name: "Grad norm", tone: "negative" },
      { key: "optim/entropy", name: "Entropy", tone: "primary" },
      { key: "optim/lr", name: "LR", tone: "accent" },
    ],
  },
  {
    id: "divergence",
    title: "KL from the sampling policy",
    note: "How far the trained policy has moved from the one that generated the rollouts. "
      + "Spikes here tend to show up as a reward dip one step later.",
    series: [
      { key: "optim/kl_sample_train_v1", name: "KL v1", tone: "primary" },
      { key: "optim/kl_sample_train_v2", name: "KL v2", tone: "accent" },
    ],
  },
  {
    id: "validity",
    title: "Validation validity",
    note: "The share of validation rollouts the grader could actually score. This is the number to "
      + "check before believing a flat reward curve: a low validity rate means the reward is being "
      + "averaged over a handful of cases and the run is not being measured, only sampled.",
    range: [0, 1],
    series: [
      { key: "rle_harness/validation_validity_rate", name: "Validity", tone: "primary" },
    ],
  },
  {
    id: "yield",
    title: "Harness yield",
    note: "Paths the harness kept against those it threw away. Discarded paths are rollouts that cost "
      + "GPU time and taught nothing, so this is where a run silently gets expensive.",
    series: [
      { key: "env/all/rle_harness/trainable_roots", name: "Trainable", tone: "positive" },
      { key: "env/all/rle_harness/discarded_paths", name: "Discarded", tone: "negative" },
      { key: "env/all/rle_harness/auxiliary_paths", name: "Auxiliary", tone: "accent" },
    ],
  },
  {
    id: "truncation",
    title: "Completion truncation",
    note: "Rollouts that ran into the token ceiling. A rising fraction at max means answers are being "
      + "cut off mid-thought and graded as failures, which looks identical to the policy getting worse.",
    range: [0, 1],
    series: [
      { key: "env/all/ac_tokens_frac_at_max", name: "At max", tone: "negative" },
      { key: "env/all/ac_tokens_frac_near_max", name: "Near max", tone: "accent" },
    ],
  },
  {
    id: "tokens",
    title: "Token counts per turn",
    note: "Generated and observed tokens per turn, with the turns each episode took. Scaled "
      + "independently; hover for the real numbers. Rising tokens per turn at flat reward is the "
      + "policy paying more for the same answer.",
    scale: "independent",
    series: [
      { key: "env/all/ac_tokens_per_turn", name: "Generated", tone: "primary" },
      { key: "env/all/ob_tokens_per_turn", name: "Observed", tone: "accent" },
      { key: "env/all/turns_per_episode", name: "Turns", tone: "positive" },
    ],
  },
  {
    id: "timing",
    title: "Step timing breakdown",
    note: "Where each step's wall clock went. Training is the part that buys policy improvement; "
      + "everything else is overhead, and eval time is what a shorter eval cadence would buy back.",
    series: [
      { key: "time/train", name: "Train", tone: "primary" },
      { key: "time/run_evals", name: "Evals", tone: "accent" },
      { key: "time/assemble_training_data", name: "Assemble", tone: "positive" },
      { key: "time/compute_kl_sample_train", name: "KL", tone: "negative" },
      { key: "time/save_checkpoint", name: "Checkpoint", tone: "muted" },
    ],
  },
];

// The step number a metrics row belongs to. Rows carry it explicitly; falling
// back to arrival order keeps a run readable if a row ever omits it.
function stepOf(row, index) {
  return isNumber(row?.step) ? row.step : index;
}

// runCharts turns metrics rows into drawable panels.
//
// Rows arrive one per step over hours, and different metrics appear at
// different steps -- validation only on evaluation steps, entropy only for RL.
// Every series is therefore its own sparse list of points rather than a column
// that has to line up with the others.
export function runCharts(rows = [], specs = RUN_CHARTS) {
  const usable = Array.isArray(rows) ? rows.filter(isRecord) : [];
  const charts = [];
  for (const spec of specs) {
    const { fallback, ...panel } = spec;
    // A spec may name a preferred source and a weaker one to fall back to when
    // the preferred metrics are absent from the run, rather than drawing both.
    const series = chartSeries(usable, spec.series);
    const resolved = series.length ? series : chartSeries(usable, fallback ?? []);
    if (resolved.length) charts.push({ ...panel, series: resolved });
  }
  return charts;
}

function chartSeries(rows, definitions = []) {
  const series = [];
  for (const definition of definitions) {
    const points = [];
    rows.forEach((row, index) => {
      const value = row[definition.key];
      if (isNumber(value)) points.push({ step: stepOf(row, index), value });
    });
    if (points.length) series.push({ ...definition, points });
  }
  return series;
}

// The reward at which a group that agreed with itself counts as having agreed
// on success rather than on failure. It matches the environment's own split.
const GOOD_REWARD = 0.5;

// The step a rollout belongs to. Training rollouts leave `step` unset and name
// the checkpoint they were sampled from instead -- "step0", then "1", "2" -- and
// the batch drawn from a checkpoint is the one that checkpoint's gradient is
// computed from, so the two line up. Validation rollouts carry `step` directly.
function entryStep(entry) {
  if (isNumber(entry?.step)) return entry.step;
  const match = /(\d+)$/.exec(entry?.checkpoint_id ?? "");
  return match ? Number(match[1]) : null;
}

// groupSignal measures, per step, how much the rollouts within a group disagree.
//
// This is the quantity the run actually learns from. It is derived here from the
// rollout index rather than read from the environment's own by_group metrics
// because those ask whether a group's rewards are exactly equal, which a
// weighted score never is -- so they report every group as mixed and the chart
// they feed reads 1/0/0 for the whole run. Standard deviation answers the same
// question for a score of any shape, and reduces to the environment's answer
// when the reward is pass/fail.
//
// Only training rollouts count: advantage is computed over the groups a step
// trains on, and validation rollouts are not grouped.
export function groupSignal(entries = []) {
  const steps = new Map();
  for (const entry of Array.isArray(entries) ? entries : []) {
    if (!isRecord(entry) || entry.split === "validation") continue;
    if (!isNumber(entry.reward) || !entry.task_id) continue;
    const step = entryStep(entry);
    if (step === null) continue;
    if (!steps.has(step)) steps.set(step, new Map());
    const groups = steps.get(step);
    if (!groups.has(entry.task_id)) groups.set(entry.task_id, []);
    groups.get(entry.task_id).push(entry.reward);
  }

  const rows = [];
  for (const [step, groups] of [...steps].sort((a, b) => a[0] - b[0])) {
    // A group of one has no spread to measure, only a missing sibling.
    const scored = [...groups.values()].filter((rewards) => rewards.length > 1);
    if (!scored.length) continue;
    let spread = 0;
    let good = 0;
    let bad = 0;
    for (const rewards of scored) {
      const mean = rewards.reduce((total, reward) => total + reward, 0) / rewards.length;
      const variance = rewards.reduce((total, reward) => total + (reward - mean) ** 2, 0) / rewards.length;
      const deviation = Math.sqrt(variance);
      spread += deviation;
      if (deviation === 0) {
        if (mean >= GOOD_REWARD) good += 1;
        else bad += 1;
      }
    }
    rows.push({
      step,
      "derived/by_group/reward_sd": spread / scored.length,
      "derived/by_group/frac_all_good": good / scored.length,
      "derived/by_group/frac_all_bad": bad / scored.length,
    });
  }
  return rows;
}

// withGroupSignal folds the derived group measurements into the metrics rows so
// they chart through the same path as everything the environment reported.
// Steps with no rollouts recorded are left as they were.
export function withGroupSignal(rows = [], entries = []) {
  const usable = Array.isArray(rows) ? rows : [];
  const derived = new Map(groupSignal(entries).map((row) => [row.step, row]));
  if (!derived.size) return usable;
  return usable.map((row, index) => {
    if (!isRecord(row)) return row;
    const match = derived.get(stepOf(row, index));
    if (!match) return row;
    const { step, ...values } = match;
    return { ...row, ...values };
  });
}

// Round a span down to a "nice" tick stride: 1, 2, 2.5 or 5 times a power of
// ten. Plotly picks strides this way, and the reason is legibility -- an axis
// labelled 0.25 / 0.50 / 0.75 is read at a glance where one labelled
// 0.2833 / 0.5666 is not.
function niceStride(span, target) {
  if (!(span > 0) || !(target > 0)) return 0;
  const rough = span / target;
  const magnitude = 10 ** Math.floor(Math.log10(rough));
  const normalised = rough / magnitude;
  const stride = normalised <= 1 ? 1 : normalised <= 2 ? 2 : normalised <= 2.5 ? 2.5 : normalised <= 5 ? 5 : 10;
  return stride * magnitude;
}

// Tick values across [min, max] on a nice stride.
//
// Steps are counts, so their axis is asked for whole numbers: a gridline at
// "step 2.5" labels a step that was never run.
function axisTicks(min, max, target, wholeNumbers = false) {
  let stride = niceStride(max - min, target);
  if (!stride) return [];
  if (wholeNumbers) stride = Math.max(1, Math.round(stride));
  const ticks = [];
  // Accumulating a float stride drifts, so every tick is recomputed from the
  // index instead. That is what keeps 0.30000000000000004 off the axis.
  const first = Math.ceil(min / stride);
  const last = Math.floor(max / stride);
  for (let index = first; index <= last; index += 1) {
    ticks.push(Number((index * stride).toPrecision(12)));
  }
  return ticks;
}

// chartGeometry places a panel's points in a fixed 400x160 viewBox.
//
// Held separately from the drawing so the arithmetic that decides whether a
// collapse is visible can be tested without a DOM. A single step is drawn at
// the left edge rather than the middle: a run with one step should look like a
// run that has just started, not one centred and finished.
export function chartGeometry(chart, width = 400, height = 160) {
  const points = chart.series.flatMap((entry) => entry.points);
  if (!points.length) return null;
  const steps = points.map((point) => point.step);
  const minStep = Math.min(...steps);
  const maxStep = Math.max(...steps);
  const values = points.map((point) => point.value);
  let minValue = chart.range ? chart.range[0] : Math.min(...values);
  let maxValue = chart.range ? chart.range[1] : Math.max(...values);
  if (!chart.range) {
    // A run that has not moved yet is still a run. Padding a flat series keeps
    // it a line across the middle instead of a divide-by-zero.
    if (minValue === maxValue) {
      const padding = Math.abs(minValue) > 0 ? Math.abs(minValue) * 0.1 : 1;
      minValue -= padding;
      maxValue += padding;
    }
    if (minValue > 0) minValue = 0;
  }
  const spanX = maxStep - minStep;
  const spanY = maxValue - minValue || 1;
  const x = (step) => spanX === 0 ? 0 : (step - minStep) / spanX * width;
  const y = (value) => height - (value - minValue) / spanY * height;
  // Metrics whose units have nothing to do with each other -- a learning rate
  // of 4e-5 beside a gradient norm of 1.2 -- are each scaled to their own
  // range. On a shared axis the smaller one is a flat line along the bottom
  // and says nothing. The shape of each series is then honest but their
  // heights are no longer comparable, so the value axis is dropped and the
  // hover tooltip becomes the only place the real numbers are read.
  const independent = chart.scale === "independent";
  const scaleFor = (entry) => {
    if (!independent) return y;
    const own = entry.points.map((point) => point.value);
    let low = Math.min(...own);
    let high = Math.max(...own);
    if (low === high) {
      const padding = Math.abs(low) > 0 ? Math.abs(low) * 0.1 : 1;
      low -= padding;
      high += padding;
    }
    const span = high - low || 1;
    return (value) => height - (value - low) / span * height;
  };
  return {
    width, height, minStep, maxStep, minValue, maxValue, independent,
    xTicks: (spanX === 0 ? [minStep] : axisTicks(minStep, maxStep, 4, true))
      .map((step) => ({ value: step, x: x(step) })),
    yTicks: independent ? [] : axisTicks(minValue, maxValue, 4).map((value) => ({ value, y: y(value) })),
    series: chart.series.map((entry) => {
      const scale = scaleFor(entry);
      return {
        ...entry,
        coordinates: entry.points.map((point) => ({ x: x(point.step), y: scale(point.value), ...point })),
      };
    }),
  };
}

export function chartPath(coordinates) {
  return coordinates
    .map((point, index) => `${index === 0 ? "M" : "L"}${point.x.toFixed(2)} ${point.y.toFixed(2)}`)
    .join(" ");
}

// Every series' reading at the step nearest an x position in the viewBox.
//
// Plotly calls this "x unified" hover, and it is the whole point of hovering a
// training chart: the question being asked is "what did the other series do
// when this one dipped", which a per-point tooltip can never answer because
// only one point is ever under the cursor. Held here, beside the geometry, so
// the lookup can be exercised without a DOM.
//
// Series are sparse and on different cadences -- validation is only measured
// every few steps -- so a series with nothing at the chosen step is left out
// rather than reported as a zero.
export function chartHoverAt(geometry, x) {
  if (!geometry || !isNumber(x)) return null;
  let step = null;
  let nearest = Infinity;
  for (const entry of geometry.series) {
    for (const point of entry.coordinates) {
      const distance = Math.abs(point.x - x);
      if (distance < nearest) {
        nearest = distance;
        step = point.step;
      }
    }
  }
  if (step === null) return null;
  const readings = [];
  let column = 0;
  for (const entry of geometry.series) {
    const point = entry.coordinates.find((candidate) => candidate.step === step);
    if (!point) continue;
    column = point.x;
    readings.push({ name: entry.name, tone: entry.tone, value: point.value, x: point.x, y: point.y });
  }
  return readings.length ? { step, x: column, readings } : null;
}

// The headline numbers, with how far each has moved.
//
// "Is it working" is a comparison, not a value: 0.72 means nothing until it is
// 0.72 up from 0.61. The latest step carrying each metric is used rather than
// the last row, because validation is only measured every few steps.
export function runHeadline(rows = []) {
  const usable = Array.isArray(rows) ? rows.filter(isRecord) : [];
  const readings = (key) => {
    const found = [];
    usable.forEach((row, index) => {
      if (isNumber(row[key])) found.push({ step: stepOf(row, index), value: row[key] });
    });
    return found;
  };
  const headline = (label, key, format, steady = false) => {
    const found = readings(key);
    if (!found.length) return null;
    const latest = found[found.length - 1];
    const previous = found.length > 1 ? found[found.length - 2] : null;
    return {
      label, key, format, steady,
      value: latest.value,
      step: latest.step,
      delta: previous && !steady ? latest.value - previous.value : null,
    };
  };
  return [
    headline("Validation reward", "rle_harness/validation_mean_reward", "reward"),
    headline("Validation success", "rle_harness/validation_success_rate", "percent"),
    headline("Train reward", "env/all/reward/total", "reward"),
    headline("Mixed groups", "env/all/by_group/frac_mixed", "percent"),
    headline("Grad norm", "skyrl.ai/grad_norm", "reward"),
    // Progress and elapsed are states, not trends: "+2.1% vs previous" on a
    // clock that only ever counts up is noise, so they carry no delta.
    headline("Progress", "progress/done_frac", "percent", true),
    headline("Elapsed", "training_duration_s", "duration", true),
  ].filter(Boolean);
}

// A run is a collapse when one update destroys the policy, which is the failure
// this dashboard exists to make obvious. It is worth naming on the page rather
// than leaving the user to infer it from a line that went down.
export function runWarnings(rows = []) {
  const charts = runCharts(rows, RUN_CHARTS);
  const series = (chartID, name) => charts.find((chart) => chart.id === chartID)
    ?.series.find((entry) => entry.name === name)?.points ?? [];
  const warnings = [];

  const train = series("reward", "Train");
  if (train.length > 1) {
    const peak = train.reduce((best, point) => point.value > best.value ? point : best, train[0]);
    const latest = train[train.length - 1];
    // Relative, so it holds whatever the environment's reward scale is.
    if (peak.value > 0 && latest.value < peak.value * 0.5 && latest.step > peak.step) {
      warnings.push(`Training reward has fallen to ${latest.value.toFixed(3)} from ${peak.value.toFixed(3)}`
        + ` at step ${peak.step}. A drop this large is usually one oversized update, not slow learning:`
        + ` check the gradient norm at the step it happened and lower the learning rate or raise the batch size.`);
    }
  }

  const mixed = series("signal", "Mixed");
  const spread = series("signal", "Spread");
  // Whichever source the panel resolved to: both answer "did any group disagree",
  // and a flat zero on either means no group produced a gradient.
  const teaching = spread.length ? spread : mixed;
  if (teaching.length >= 3 && teaching.slice(-3).every((point) => point.value === 0)) {
    warnings.push("No group has produced mixed outcomes for the last three steps, so every group's advantage"
      + " is zero and no gradient is being learned from. The tasks are either all passing or all failing.");
  }

  return warnings;
}

// Learning rates are written as 1e-5 everywhere they are set, so showing
// 0.00001 makes the reader convert it back before they can compare it to the
// recipe they typed it into.
export function settingLabel(value) {
  if (!isNumber(value) || value === 0) return value;
  return Math.abs(value) < 0.001 ? value.toExponential() : value;
}

// runFacts flattens run_meta.json and config.json into the labelled rows the
// page shows. Both documents are the recipe's own, so anything unrecognised is
// left out here rather than guessed at.
export function runFacts(overview) {  if (!isRecord(overview)) return { identity: [], model: [], dataset: [], training: [] };
  const meta = isRecord(overview.meta) ? overview.meta : {};
  const config = isRecord(overview.config) ? overview.config : {};
  const builder = isRecord(config.dataset_builder) ? config.dataset_builder : {};
  const environment = isRecord(meta.environment) ? meta.environment : {};
  const model = isRecord(meta.model) ? meta.model : {};
  const dataset = isRecord(meta.dataset) ? meta.dataset : {};

  const rows = (entries) => entries.filter(([, value]) => present(value) && value !== "");
  return {
    identity: rows([
      ["Environment", environment.name],
      ["Version", environment.version],
      ["Recipe", meta.recipe],
      ["Started", meta.started_at],
    ]),
    model: rows([
      ["Base model", model.model_name],
      ["Renderer", model.renderer_name],
      ["Loom session", model.loom_session_id],
      ["Context window", isNumber(model.max_sequence_tokens)
        ? `${model.max_sequence_tokens.toLocaleString()} tokens` : null],
    ]),
    dataset: rows([
      // The recipe reports the file's row count here, not the count it trains
      // on, so the truncated number is shown beside it rather than instead.
      ["Training cases", isNumber(builder.max_train_examples)
        ? `${builder.max_train_examples} of ${dataset.training_cases ?? "?"}`
        : dataset.training_cases],
      ["Validation cases", dataset.validation_cases],
      ["Group size", builder.group_size],
      ["Groups per batch", builder.groups_per_batch],
      ["Rollouts per step", isNumber(builder.group_size) && isNumber(builder.groups_per_batch)
        ? builder.group_size * builder.groups_per_batch : null],
      ["Epochs", builder.num_epochs],
    ]),
    training: rows([
      ["Learning rate", settingLabel(config.learning_rate)],
      ["Max steps", config.max_steps],
      ["Evaluate every", config.eval_every],
      ["Save every", config.save_every],
      ["LoRA rank", config.lora_rank],
      ["Loss", config.loss_fn],
      ["Temperature", config.temperature],
      ["KL penalty", settingLabel(config.kl_penalty_coef)],
      ["Max tokens", config.max_tokens],
    ]),
  };
}

async function fetchRun(fetcher, url) {
  let result;
  try {
    result = await fetcher(url, { credentials: "same-origin", cache: "no-store",
      headers: { Accept: "application/json" } });
  } catch {
    throw new Error("Could not reach the local monitor. Check that the monitor command is still running, then try again.");
  }
  // 404 is how the monitor says this job was never followed on this machine,
  // which is a normal way to use it and not a failure to report.
  if (result.status === 404) return null;
  if (!result.ok) {
    throw new Error(`The local monitor returned HTTP ${result.status}. Check the monitor terminal, then try again.`);
  }
  try {
    return await result.json();
  } catch {
    throw new Error("The local monitor returned invalid JSON. Check the monitor terminal, then try again.");
  }
}

export const fetchRunOverview = (fetcher = fetch) => fetchRun(fetcher, "/api/run");
export const fetchRunMetrics = (fetcher = fetch) => fetchRun(fetcher, "/api/run/metrics");
export const fetchRunLog = (fetcher = fetch, offset = 0) =>
  fetchRun(fetcher, `/api/run/logs?offset=${encodeURIComponent(String(offset))}`);
