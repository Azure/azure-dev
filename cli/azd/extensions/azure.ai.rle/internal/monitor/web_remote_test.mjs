// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import test from "node:test";
import assert from "node:assert/strict";
import {
  remoteRolloutState, remoteRunNotices, rolloutAttemptSessions, runFacts, runCharts, fetchSnapshot,
  unavailableRolloutFacts, unavailableRolloutMessage,
  fetchRolloutEvents, rolloutEventMessage,
} from "./web/data.mjs";

test("rollout events preserve backend payloads and opaque pagination tokens", async () => {
  const calls = [];
  const page = { data: [{ sequence: 7, elapsed_ms: 2300, phase: "execute", status: "failed",
    message: "<script>not markup</script>", partial_response: { reward: 0 } }], nextContinuationToken: "next" };
  const result = await fetchRolloutEvents(async (url, options) => {
    calls.push([url, options]);
    return { ok: true, json: async () => page };
  }, "abc", "opaque+/=&");
  assert.equal(result, page);
  const url = new URL(calls[0][0], "http://localhost");
  assert.equal(url.pathname, "/api/rollout/events");
  assert.equal(url.searchParams.get("id"), "abc");
  assert.equal(url.searchParams.get("continuationToken"), "opaque+/=&");
  assert.equal(calls[0][1].credentials, "same-origin");
  assert.equal(calls[0][1].cache, "no-store");
  assert.equal(rolloutEventMessage(page.data[0]), "<script>not markup</script>");
});

test("empty event pages retain their continuation token", async () => {
  const page = { data: [], nextContinuationToken: "next" };
  assert.equal(await fetchRolloutEvents(async () => ({ ok: true, json: async () => page }), "abc"), page);
  assert.equal(rolloutEventMessage({ sequence: 0, elapsed_ms: 0 }), "No message reported.");
  assert.equal(rolloutEventMessage(null), "No message reported.");
  assert.equal(rolloutEventMessage({ message: 42 }), "No message reported.");
});

test("event errors and malformed pages are not mistaken for empty history", async () => {
  await assert.rejects(fetchRolloutEvents(async () => {
    throw new Error("offline");
  }, "abc"), /Could not reach the local monitor/);
  await assert.rejects(fetchRolloutEvents(async () => ({
    ok: false, status: 502, json: async () => ({ error: "HTTP 429; operation ID event-operation" }),
  }), "abc"), /Could not load rollout events: HTTP 429; operation ID event-operation/);
  await assert.rejects(fetchRolloutEvents(async () => ({
    ok: false, status: 404, json: async () => { throw new Error("plain text"); },
  }), "abc"), /HTTP 404/);
  for (const page of [{}, { data: null }, { data: {} }, { data: [], nextContinuationToken: 42 }]) {
    await assert.rejects(fetchRolloutEvents(async () => ({ ok: true, json: async () => page }), "abc"),
      /invalid rollout events page/);
  }
});

test("remote execution state comes from metadata, not the task verdict", () => {
  assert.equal(remoteRolloutState({ status: "completed", success: false }), "completed");
  assert.equal(remoteRolloutState({ status: "running", success: true }), "running");
  assert.equal(remoteRolloutState({ success: true }), "unknown");
});

test("retry attempts are separated by each rollout's training session", () => {
  const attempts = rolloutAttemptSessions([
    { rollout_id: "first", session_id: "session-initial" },
    { rollout_id: "second", session_id: "session-initial" },
    { rollout_id: "third", session_id: "session-retry-1" },
    { rollout_id: "fourth", session_id: "session-retry-2" },
  ]);
  assert.deepEqual([...attempts], [
    ["first", "session-initial"], ["second", "session-initial"],
    ["third", "session-retry-1"], ["fourth", "session-retry-2"],
  ]);
  assert.equal(rolloutAttemptSessions([{ rollout_id: "only", session_id: "one-session" }]).size, 0);
  assert.equal(rolloutAttemptSessions([{ rollout_id: "unknown" }, { rollout_id: "retry", session_id: "known" }]).size, 0);
});

test("registered config does not require a facade run_meta file", () => {
  const facts = runFacts({ config: { model_name: "Qwen/Qwen3-32B", learning_rate: 0.0003, max_steps: 60 } });
  assert.ok(facts.model.some(([key, value]) => key === "Base model" && value === "Qwen/Qwen3-32B"));
});

test("remote notices surface pending, stale, bounded and paused states", () => {
  const notes = remoteRunNotices({
    run: { config: null, meta: { status: "running" } },
    states: { metrics: { error: "HTTP 403", updated_at: "2026-10-01T00:00:00Z" } },
    limited: true, paused: "completed",
  }).join(" ");
  assert.match(notes, /registration/);
  assert.match(notes, /HTTP 403/);
  assert.match(notes, /Last successful read/);
  assert.match(notes, /not the full run/);
  assert.match(notes, /paused after completion/);
  assert.match(remoteRunNotices({ run: { config: {} }, paused: "disabled" }).join(" "), /polling stopped/);
});

test("sparse real-service metrics keep their reported steps", () => {
  const chart = runCharts([{ step: 9, "env/all/reward/total": 0.5 }, { step: 15, "optim/lr": 0.001 }])
    .find((entry) => entry.id === "reward");
  assert.deepEqual(chart.series[0].points, [{ step: 9, value: 0.5 }]);
});

test("selected rollout fetch accepts unavailable metadata without inventing a graph", async () => {
  const calls = [];
  const payload = { unavailable: true, metadata: { status: "running" } };
  const result = await fetchSnapshot(async (url) => {
    calls.push(url);
    return { ok: true, json: async () => payload };
  }, "abc");
  assert.deepEqual(calls, ["/api/rollout?id=abc"]);
  assert.equal(result, payload);
  assert.equal(result.response, undefined);
});

test("an unavailable rollout explains its state and summarizes service metadata", () => {
  const failed = {
    rollout_id: "77ddddcf0bd44cc99e5642c3dd022e4d", job_id: "ftjob-1", environment_name: "math_rl",
    environment_version: "1.0.8", created_at_utc: "2026-10-02T22:05:18Z", status: "failed",
    latency_s: 22.1011194, checkpoint_id: "step0", session_id: "session_1a74b5e9", has_graph: false,
  };
  assert.match(unavailableRolloutMessage(failed), /failed before producing a result/);
  assert.match(unavailableRolloutMessage({ status: "running" }), /still running/);
  assert.match(unavailableRolloutMessage({ status: "completed" }), /not retained or has expired/);
  assert.match(unavailableRolloutMessage({}), /not available/);
  assert.deepEqual(unavailableRolloutFacts(failed), [
    ["State", "Failed"], ["Environment", "math_rl 1.0.8"], ["Checkpoint", "step0"], ["Latency", "22.1s"],
    ["Created", "2026-10-02T22:05:18Z"], ["Session", "session_1a74b5e9"], ["Job", "ftjob-1"],
  ]);
  assert.deepEqual(unavailableRolloutFacts({ status: "expired", reward: 0 }), [["State", "expired"], ["Reward", "0.000"]]);
});