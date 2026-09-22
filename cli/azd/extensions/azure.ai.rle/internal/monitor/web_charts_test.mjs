// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import test from "node:test";
import assert from "node:assert/strict";
import { chartScales, mapSnapshot, rewardGeometry } from "./web/data.mjs";

test("signed step rewards share an absolute scale and start at the 400px viewBox midpoint", () => {
  const steps = [{ reward: -2 }, { reward: 1 }, { reward: 0 }, {}];
  const max = chartScales(steps).reward;
  assert.equal(max, 2);
  assert.deepEqual(rewardGeometry(-2, max), { x: 0, width: 200, zero: false, negative: true });
  assert.deepEqual(rewardGeometry(1, max), { x: 200, width: 100, zero: false, negative: false });
  assert.deepEqual(rewardGeometry(0, max), { x: 200, width: 0, zero: true, negative: false });
  assert.equal(rewardGeometry(undefined, max), null);
});

test("zero rewards remain explicit points while missing and invalid rewards have no geometry", () => {
  assert.deepEqual(rewardGeometry(0, 0), { x: 200, width: 0, zero: true, negative: false });
  for (const value of [undefined, null, NaN, Infinity, "0"]) assert.equal(rewardGeometry(value, 0), null);
  assert.equal(chartScales([{}, { reward: null }, { reward: 0 }]).reward, 0);
});

test("extreme finite signed rewards never overflow SVG coordinates", () => {
  const max = Number.MAX_VALUE;
  for (const value of [-max, max, max / 2, -max / 2]) {
    const geometry = rewardGeometry(value, max);
    assert.ok(Number.isFinite(geometry.x));
    assert.ok(Number.isFinite(geometry.width));
    assert.ok(geometry.x >= 0 && geometry.x + geometry.width <= 400);
  }
  assert.equal(rewardGeometry(max / 2, max).width, 100);
});

test("prompt and sampled counts share one scale across calls and chart pages", () => {
  const turns = Array.from({ length: 101 }, (_, index) => ({
    prompt: index === 100 ? 800 : 0, sampled: index === 3 ? 400 : null,
  }));
  assert.equal(chartScales([], turns).tokens, 800);
  assert.equal(turns[0].prompt, 0);
  assert.equal(turns[0].sampled, null);
});

test("chart inputs preserve token zeros, missing counts, and step/call joins", () => {
  const response = { rollout_id: "chart-test", reward: 0,
    episode: { steps: [{ capture_node_id: "a", reward: -1 }, { reward: 0 }, {}] },
    rollout: { capture_level: "tokens", turns: [
      { node_id: "a", n_prompt: 0, n_sampled: 12 },
      { node_id: "a", n_prompt: 25 },
    ] } };
  const model = mapSnapshot({ source: "test", response });
  assert.deepEqual(model.turns.map((turn) => [turn.prompt, turn.sampled]), [[0, 12], [25, null]]);
  assert.deepEqual(model.steps.map((step) => step.reward), [-1, 0, null]);
  assert.deepEqual(model.steps[0].turnPositions, [0, 1]);
  assert.deepEqual(model.turns.map((turn) => turn.stepNumbers), [[1], [1]]);
  assert.deepEqual(chartScales(model.steps, model.turns), { reward: 1, tokens: 25 });
  response.rollout.capture_level = "metadata";
  const metadata = mapSnapshot({ source: "test", response });
  assert.equal(metadata.turns.every((turn) => turn.prompt === null && turn.sampled === null), true);
  assert.equal(chartScales(metadata.steps, metadata.turns).tokens, 0);
});
