// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import test from "node:test";
import assert from "node:assert/strict";
import { bootstrapSession, fetchSnapshot, mapSnapshot, SessionRequiredError, unlockSession } from "./web/data.mjs";

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

test("malformed responses fail explicitly instead of rendering fabricated values", () => {
  const invalid = [
    null, {}, { ...snapshot(), saved_at: "yesterday" }, { ...snapshot(), source: null },
    snapshot({ rollout_id: "" }), snapshot({ reward: "0.5" }), snapshot({ reward: Infinity }),
    snapshot({ success: null }), snapshot({ success: "false" }), snapshot({ rollout: [] }),
    snapshot({ episode: { steps: [null] } }), snapshot({ episode: { steps: [{ reward: "1" }] } }),
    snapshot({ rollout: { turns: [{ n_tools: -1 }] } }), snapshot({ rollout: { validation: {} } }),
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

test("bootstrap strips the token fragment before exchanging it for the session cookie", async () => {
  const events = [];
  const location = { hash: "#token=secret%2Bvalue", pathname: "/", search: "?view=saved" };
  const history = { replaceState: (...args) => events.push(["replace", ...args]) };
  await bootstrapSession(location, history, async (url, options) => {
    assert.deepEqual(events, [["replace", null, "", "/?view=saved"]]);
    assert.equal(url, "/session");
    assert.equal(options.method, "POST");
    assert.equal(options.headers.Authorization, "Bearer secret+value");
    assert.equal(options.credentials, "same-origin");
    assert.equal(options.cache, "no-store");
    events.push(["post"]);
    return { status: 204 };
  });
  assert.equal(events.length, 2);
});

test("reloads without a token reuse the cookie without a session exchange", async () => {
  for (const hash of ["", "#raw"]) {
    await bootstrapSession({ hash }, {
      replaceState: () => assert.fail("A non-token fragment should be preserved"),
    }, async () => assert.fail("No token should mean no POST"));
  }
});

test("empty token fragments are removed but do not send an empty bearer credential", async () => {
  let removed = false;
  await bootstrapSession({ hash: "#token=", pathname: "/", search: "" }, {
    replaceState: () => { removed = true; },
  }, async () => assert.fail("An empty token should not be exchanged"));
  assert.equal(removed, true);
});

test("bootstrap failures give reopen guidance without exposing credentials or server content", async () => {
  const token = "private-test-token";
  for (const fetcher of [
    async () => ({ status: 401 }),
    async () => ({ status: 403 }),
    async () => ({ status: 500 }),
    async () => ({ status: 200 }),
    async () => { throw new Error(token); },
  ]) {
    let removed = false;
    await assert.rejects(bootstrapSession({ hash: `#token=${token}`, pathname: "/", search: "" }, {
      replaceState: () => { removed = true; },
    }, fetcher), (error) => {
      assert.equal(removed, true);
      assert.match(error.message, /reopen.*link from the CLI/i);
      assert.ok(!error.message.includes(token));
      return true;
    });
  }
});

test("API authentication failures tell users to reopen the CLI link", async () => {
  for (const status of [401, 403]) {
    await assert.rejects(fetchSnapshot(async () => ({ ok: false, status })),
      (error) => {
        assert.ok(error instanceof SessionRequiredError);
        assert.match(error.message, /Reopen the monitor link from the CLI/);
        return true;
      });
  }
});

test("manual access-code exchange uses only the authorization header", async () => {
  await unlockSession("manual-test-code", async (url, options) => {
    assert.equal(url, "/session");
    assert.equal(options.method, "POST");
    assert.deepEqual(options.headers, { Authorization: "Bearer manual-test-code" });
    assert.equal(options.body, undefined);
    assert.equal(options.credentials, "same-origin");
    return { status: 204 };
  });
});

test("empty manual codes never cause a request", async () => {
  for (const token of ["", "  ", null, undefined]) {
    await assert.rejects(unlockSession(token, async () => assert.fail("Do not submit an empty code")),
      /Enter the local access code/);
  }
});

test("rejected manual codes produce a distinct locked state without echoing the code", async () => {
  await assert.rejects(unlockSession("never-display-this-code", async () => ({ status: 401 })), (error) => {
    assert.ok(error instanceof SessionRequiredError);
    assert.ok(!error.message.includes("never-display-this-code"));
    return true;
  });
});
