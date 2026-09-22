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

// Leave the parsed response unchanged; derive only display values, never missing metrics.
export function mapSnapshot(snapshot) {
  requireValue(isRecord(snapshot), "expected an object");
  const response = snapshot.response;
  requireValue(isRecord(response), "response must be an object");
  requireValue(isString(response.rollout_id) && response.rollout_id.trim().length > 0, "response.rollout_id is required");
  requireValue(isNumber(response.reward), "response.reward must be a finite number");
  if (Object.hasOwn(response, "success")) requireValue(isBoolean(response.success), "response.success must be a boolean");
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
  const mappedTurns = turns?.map((turn, position) => ({
    raw: turn, position, label: `Turn ${position + 1}`,
    stepNumbers: stepNumbersByTurn.get(position) ?? [],
    prompt: graph.capture_level === "tokens" ? turn.n_prompt ?? null : null,
    sampled: graph.capture_level === "tokens" ? turn.n_sampled ?? null : null,
  })) ?? null;

  return { response, source: snapshot.source, savedAt: snapshot.saved_at, environment, warnings: snapshot.warnings ?? [],
    episode, graph, stats,
    steps: mappedSteps, turns: mappedTurns, tokensCaptured: graph.capture_level === "tokens",
    outcome: response.success === true ? "Task succeeded" : response.success === false ? "Task unsuccessful" : null };
}

export class SessionRequiredError extends Error {
  constructor(message) {
    super(message);
    this.name = "SessionRequiredError";
  }
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

export async function bootstrapSession(location, history, fetcher = fetch) {
  const parameters = new URLSearchParams(location.hash.slice(1));
  const token = parameters.get("token");
  if (parameters.has("token")) {
    history.replaceState(null, "", location.pathname + location.search);
  }
  if (!token) return;
  await unlockSession(token, fetcher);
}

export async function unlockSession(token, fetcher = fetch) {
  if (typeof token !== "string" || !token.trim()) {
    throw new Error("Enter the local access code printed by the monitor command.");
  }
  let result;
  try {
    result = await fetcher("/session", {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
      credentials: "same-origin",
      cache: "no-store",
    });
  } catch {
    throw new Error("Could not establish the local monitor session. Check that the monitor is running and reopen its link from the CLI.");
  }
  if (result.status === 401 || result.status === 403) {
    throw new SessionRequiredError(
      `Session authentication failed (HTTP ${result.status}). Reopen the monitor link from the CLI to start a new session.`,
    );
  }
  if (result.status !== 204) {
    throw new Error(`Session authentication failed (HTTP ${result.status}). Reopen the monitor link from the CLI to start a new session.`);
  }
}

export async function fetchSnapshot(fetcher = fetch) {
  let result;
  try {
    result = await fetcher("/api/rollout", { credentials: "same-origin", cache: "no-store",
      headers: { Accept: "application/json" } });
  } catch {
    throw new Error("Could not reach the local monitor. Check that the monitor command is still running, then try again.");
  }
  if (result.status === 401 || result.status === 403) {
    throw new SessionRequiredError(
      `Session authentication failed (HTTP ${result.status}). Reopen the monitor link from the CLI to access this snapshot.`,
    );
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
