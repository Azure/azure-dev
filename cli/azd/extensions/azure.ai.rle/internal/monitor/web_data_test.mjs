// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import test from "node:test";
import assert from "node:assert/strict";
import {
  fetchRolloutIndex, fetchRolloutStates, fetchSnapshot, mapSnapshot,
} from "./web/data.mjs";

const snapshot = (response = {}) => ({
  source: "Saved local result", saved_at: "2026-09-21T20:00:00Z",
  response: { rollout_id: "rollout-1", reward: 0.75, ...response },
});

test("distinguishes absent success, false, and true without assuming grading", () => {
  assert.equal(mapSnapshot(snapshot()).outcome, null);
  assert.equal(mapSnapshot(snapshot({ success: false })).outcome, "Task unsuccessful");
  assert.equal(mapSnapshot(snapshot({ success: true })).outcome, "Task succeeded");
  assert.equal(mapSnapshot(snapshot({ episode: { ungraded: true } })).outcome, null);
});

test("completion, positive reward, and environment correctness do not invent a task verdict", () => {
  const model = mapSnapshot(snapshot({
    reward: 1, result: { correct: true, format: true },
    episode: { termination_reason: "done", steps: [{ reward: 1, episode_done: true }] },
  }));
  assert.equal(model.outcome, null);
  assert.equal(model.episode.termination_reason, "done");
  assert.equal(model.steps[0].raw.episode_done, true);
});

test("accepts missing saved_at without inventing a timestamp", () => {
  const input = snapshot();
  delete input.saved_at;
  input.source = "Remote result";
  const model = mapSnapshot(input);
  assert.equal(model.savedAt, undefined);
  assert.equal(model.source, "Remote result");
});

test("shows only the environment recorded with the execution, without modifying the response", () => {
  const input = { ...snapshot(), environment: { name: "math_rl", version: "2.1.0" } };
  const before = JSON.stringify(input);
  const model = mapSnapshot(input);
  assert.deepEqual(model.environment, input.environment);
  assert.equal(Object.hasOwn(model.response, "environment"), false);
  assert.equal(JSON.stringify(input), before);
  assert.equal(mapSnapshot(snapshot()).environment, null);
  assert.equal(mapSnapshot({ ...snapshot(), environment: null }).environment, null);
  for (const environment of ["math_rl", [], {}, { name: "math_rl" },
    { name: "", version: "1.0.0" }, { name: "math_rl", version: 1 }]) {
    assert.throws(() => mapSnapshot({ ...snapshot(), environment }), /environment/);
  }
});

test("carries artifact warnings separately from the task verdict", () => {
  const warnings = ["Legacy export: task verdict is ambiguous."];
  const model = mapSnapshot({ ...snapshot(), warnings });
  assert.deepEqual(model.warnings, warnings);
  assert.equal(model.outcome, null);
  assert.deepEqual(mapSnapshot(snapshot()).warnings, []);
  for (const value of ["warning", [null], [42]]) {
    assert.throws(() => mapSnapshot({ ...snapshot(), warnings: value }), /warnings/);
  }
});

test("rejects malformed saved_at when explicitly present", () => {
  for (const saved_at of [null, undefined, "", 0, "not-a-date", "2026-09-21"]) {
    assert.throws(() => mapSnapshot({ ...snapshot(), saved_at }), /saved_at must be an RFC 3339 timestamp/);
  }
});

test("preserves missing collections and stats, including explicit empty collections", () => {
  const missing = mapSnapshot(snapshot());
  assert.equal(missing.steps, null);
  assert.equal(missing.turns, null);
  assert.equal(missing.stats.n_turns, undefined);
  const empty = mapSnapshot(snapshot({ episode: { steps: [] }, rollout: { turns: [], stats: { n_turns: 0 } } }));
  assert.deepEqual(empty.steps, []);
  assert.deepEqual(empty.turns, []);
  assert.equal(empty.stats.n_turns, 0);
});

test("preserves signed rewards, repeated capture IDs, and many-to-many joins", () => {
  const model = mapSnapshot(snapshot({
    episode: { steps: [
      { capture_node_id: "a", reward: 2 }, { capture_node_id: "a", reward: -0.5 },
      { capture_node_id: "b", reward: 0 },
    ] },
    rollout: { turns: [{ node_id: "a", index: 0 }, { node_id: "a", index: 4 }, { node_id: "other" }] },
  }));
  assert.deepEqual(model.steps.map((step) => step.reward), [2, -0.5, 0]);
  assert.deepEqual(model.steps.map((step) => step.turnPositions), [[0, 1], [0, 1], []]);
  assert.deepEqual(model.turns.map((turn) => turn.stepNumbers), [[1, 2], [1, 2], []]);
  assert.deepEqual(model.turns.map((turn) => turn.label), ["Turn 1", "Turn 2", "Turn 3"]);
  assert.deepEqual(model.turns.map((turn) => turn.raw.index), [0, 4, undefined]);
});

test("missing rewards remain unknown without affecting other steps", () => {
  const model = mapSnapshot(snapshot({ episode: { steps: [{ reward: 1 }, {}, { reward: 3 }] } }));
  assert.deepEqual(model.steps.map((step) => step.reward), [1, null, 3]);
});

test("one-step snapshots retain their exact result and raw graph data", () => {
  const input = snapshot({ result: { content: "<script>alert('not markup')</script>" },
    episode: { steps: [{ reward: -1, episode_done: true }] },
    rollout: { capture_level: "tokens", turns: [{ n_prompt: 0, n_tools: 5 }], validation: [] } });
  const before = JSON.stringify(input);
  const model = mapSnapshot(input);
  assert.equal(model.response, input.response);
  assert.equal(model.steps[0].reward, -1);
  assert.equal(model.turns[0].prompt, 0);
  assert.equal(model.turns[0].sampled, null);
  assert.deepEqual(model.steps[0].turnPositions, []);
  assert.equal(JSON.stringify(input), before);
});

test("token counts are exposed only at tokens capture level", () => {
  for (const level of [undefined, "full", "metadata", "tokens"]) {
    const model = mapSnapshot(snapshot({ rollout: { capture_level: level, turns: [{ n_prompt: 12, n_sampled: 4 }] } }));
    assert.equal(model.turns[0].prompt, level === "tokens" ? 12 : null);
    assert.equal(model.turns[0].sampled, level === "tokens" ? 4 : null);
  }
});

test("maps final response and captured model conversation without changing the response", () => {
  const input = snapshot({
    final_response: "<think>Private reasoning</think>\nThe final answer",
    rollout: { turns: [
      {
        request_messages: [{ role: "system", content: "Instructions" }, { role: "user", content: "Question" }],
        response_message: {
          role: "assistant",
          content: "Calling a tool",
          tool_calls: [{ function: { name: "run_code", arguments: "{}" } }],
        },
      },
      {
        request_messages: [{ role: "tool", content: "Result" }],
        response_message: { role: "assistant", content: "Answer", tool_calls: [{ name: "explain_result" }] },
      },
    ] },
  });
  const before = JSON.stringify(input);
  const model = mapSnapshot(input);
  assert.equal(model.finalResponse, input.response.final_response);
  assert.equal(model.finalResponsePreview, "The final answer");
  assert.equal(model.finalResponseReasoningHidden, true);
  assert.equal(model.finalResponseExpandable, true);
  assert.equal(model.hasConversation, true);
  assert.deepEqual(model.turns[0].requestMessages, input.response.rollout.turns[0].request_messages);
  assert.equal(model.turns[1].responseMessage.content, "Answer");
  assert.deepEqual(model.turns[0].flow, ["system", "user", "assistant"]);
  assert.deepEqual(model.turns[1].flow, ["tool", "assistant"]);
  assert.equal(model.toolActivity.count, 2);
  assert.deepEqual(model.toolActivity.names, ["run_code", "explain_result"]);
  assert.equal(JSON.stringify(input), before);
  assert.equal(mapSnapshot(snapshot()).finalResponse, null);
  assert.equal(mapSnapshot(snapshot()).finalResponsePreview, null);
  assert.equal(mapSnapshot(snapshot()).hasConversation, false);
});

test("keeps short final responses expanded and does not strip incomplete reasoning tags", () => {
  const short = mapSnapshot(snapshot({ final_response: "A concise answer." }));
  assert.equal(short.finalResponsePreview, "A concise answer.");
  assert.equal(short.finalResponseReasoningHidden, false);
  assert.equal(short.finalResponseExpandable, false);

  const incomplete = mapSnapshot(snapshot({ final_response: "<think>Still generating" }));
  assert.equal(incomplete.finalResponsePreview, "<think>Still generating");
  assert.equal(incomplete.finalResponseReasoningHidden, false);
});

test("malformed responses fail explicitly instead of rendering fabricated values", () => {
  const invalid = [
    null, {}, { ...snapshot(), saved_at: "yesterday" }, { ...snapshot(), source: null },
    snapshot({ rollout_id: "" }), snapshot({ reward: "0.5" }), snapshot({ reward: Infinity }),
    snapshot({ success: null }), snapshot({ success: "false" }), snapshot({ final_response: 42 }),
    snapshot({ rollout: [] }),
    snapshot({ episode: { steps: [null] } }), snapshot({ episode: { steps: [{ reward: "1" }] } }),
    snapshot({ rollout: { turns: [{ n_tools: -1 }] } }), snapshot({ rollout: { validation: {} } }),
    snapshot({ rollout: { turns: [{ request_messages: [null] }] } }),
    snapshot({ rollout: { turns: [{ response_message: "answer" }] } }),
    snapshot({ rollout: { stats: { n_turns: 1.2 } } }),
  ];
  for (const input of invalid) assert.throws(() => mapSnapshot(input), /Invalid snapshot/);
});

test("fetches once from the same-origin API with cookies and no cache", async () => {
  let calls = 0;
  const expected = snapshot();
  const received = await fetchSnapshot(async (url, options) => {
    calls++;
    assert.equal(url, "/api/rollout");
    assert.equal(options.credentials, "same-origin");
    assert.equal(options.cache, "no-store");
    return { ok: true, json: async () => expected };
  });
  assert.equal(received, expected);
  assert.equal(calls, 1);
});

test("handles HTTP, transport and invalid JSON failures without displaying response bodies", async () => {
  await assert.rejects(fetchSnapshot(async () => ({ ok: false, status: 403 })), /HTTP 403/);
  await assert.rejects(fetchSnapshot(async () => { throw new Error("sensitive network detail"); }), /Could not reach/);
  await assert.rejects(fetchSnapshot(async () => ({
    ok: true, json: async () => { throw new Error("sensitive body"); },
  })), /invalid JSON/);
});


test("requests a named rollout from the job's set", async () => {
  const expected = snapshot();
  const received = await fetchSnapshot(async (url) => {
    assert.equal(url, "/api/rollout?id=abc%2Fdef");
    return { ok: true, json: async () => expected };
  }, "abc/def");
  assert.equal(received, expected);
});

test("reads a missing index as a single saved rollout rather than an error", async () => {
  assert.equal(await fetchRolloutIndex(async () => ({ ok: false, status: 404 })), null);
});

test("returns the recorded index for a training job", async () => {
  const expected = { job_id: "ftjob-1", data: [{ rollout_id: "a" }] };
  const received = await fetchRolloutIndex(async (url, options) => {
    assert.equal(url, "/api/rollouts");
    assert.equal(options.credentials, "same-origin");
    assert.equal(options.cache, "no-store");
    return { ok: true, json: async () => expected };
  });
  assert.equal(received, expected);
});

test("index transport and HTTP failures stay distinguishable", async () => {
  await assert.rejects(fetchRolloutIndex(async () => ({ ok: false, status: 500 })), /HTTP 500/);
  await assert.rejects(fetchRolloutIndex(async () => { throw new Error("sensitive"); }), /Could not reach/);
});

test("returns the execution states the monitor has classified so far", async () => {
  const expected = {
    job_id: "ftjob-1", known: 2, total: 9,
    data: { a: { state: "completed" }, b: { state: "failed", error: "openai.APITimeoutError" } },
  };
  const received = await fetchRolloutStates(async (url, options) => {
    assert.equal(url, "/api/rollouts/states");
    assert.equal(options.credentials, "same-origin");
    assert.equal(options.cache, "no-store");
    return { ok: true, json: async () => expected };
  });
  assert.equal(received, expected);
});

// The column is an addition to the list, not a precondition for it: a monitor
// that cannot answer must leave the rollouts showing rather than fail the page.
test("an unavailable state endpoint is reported as absent, not as an error", async () => {
  for (const responder of [
    async () => ({ ok: false, status: 404 }),
    async () => ({ ok: false, status: 500 }),
    async () => { throw new Error("offline"); },
    async () => ({ ok: true, json: async () => { throw new Error("invalid JSON"); } }),
  ]) {
    assert.equal(await fetchRolloutStates(responder), null);
  }
});

test("polls from the last rollout it holds, so a poll costs only what is new", async () => {
  const received = await fetchRolloutIndex(async (url) => {
    assert.equal(url, "/api/rollouts?after=rollout-9");
    return { ok: true, json: async () => ({ job_id: "ftjob-1", data: [] }) };
  }, "rollout-9");
  assert.deepEqual(received.data, []);
});

test("a rollout id is escaped into the poll query rather than concatenated", async () => {
  await fetchRolloutIndex(async (url) => {
    assert.equal(url, "/api/rollouts?after=a%26b%3Dc");
    return { ok: true, json: async () => ({ job_id: "ftjob-1", data: [] }) };
  }, "a&b=c");
});
