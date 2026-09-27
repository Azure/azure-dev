// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import test from "node:test";
import assert from "node:assert/strict";
import {
  chartGeometry, chartPath, groupSignal, runCharts, runFacts, runHeadline, runWarnings, settingLabel,
  withGroupSignal, RUN_CHARTS,
} from "./web/data.mjs";

const rewardChart = RUN_CHARTS.find((chart) => chart.id === "reward");

// The run that prompted this view: one oversized update destroyed the policy at
// step 1 and it never recovered. Reproduced here so the charts keep showing it.
const collapsedRun = [
  { step: 0, "env/all/reward/total": 0.725, "rle_harness/validation_mean_reward": 0.818, "skyrl.ai/grad_norm": 609.9 },
  { step: 1, "env/all/reward/total": 0.059, "rle_harness/validation_mean_reward": 0.045, "skyrl.ai/grad_norm": 0 },
  { step: 2, "env/all/reward/total": 0.051, "rle_harness/validation_mean_reward": 0.038, "skyrl.ai/grad_norm": 43.9 },
  { step: 3, "env/all/reward/total": 0.045, "rle_harness/validation_mean_reward": 0.332, "skyrl.ai/grad_norm": 15.3 },
];

// Validation runs every few steps, not every step, so its series is shorter
// than the training one and the two cannot be stored as aligned columns.
test("each metric is its own series, so a metric measured less often is not padded or dropped", () => {
  const charts = runCharts([
    { step: 0, "env/all/reward/total": 0.7, "rle_harness/validation_mean_reward": 0.8 },
    { step: 1, "env/all/reward/total": 0.6 },
    { step: 2, "env/all/reward/total": 0.9, "rle_harness/validation_mean_reward": 0.85 },
  ], [rewardChart]);

  const [train, validation] = charts[0].series;
  assert.deepEqual(train.points.map((point) => point.step), [0, 1, 2]);
  assert.deepEqual(validation.points.map((point) => point.step), [0, 2]);
});

// A metric the recipe never emitted is not a metric that was zero: drawing it
// flat along the axis would invent a reading that was never taken.
test("a series with no readings is left out rather than drawn at zero", () => {
  const charts = runCharts([{ step: 0, "env/all/reward/total": 0.7 }], [rewardChart]);
  assert.deepEqual(charts[0].series.map((entry) => entry.name), ["Train"]);

  assert.deepEqual(runCharts([{ step: 0, other: 1 }], [rewardChart]), []);
});

test("non-finite and non-numeric readings are not plotted", () => {
  const rows = [
    { step: 0, "env/all/reward/total": 0.5 },
    { step: 1, "env/all/reward/total": null },
    { step: 2, "env/all/reward/total": NaN },
    { step: 3, "env/all/reward/total": Infinity },
    { step: 4, "env/all/reward/total": "0.9" },
    { step: 5, "env/all/reward/total": 0.8 },
  ];
  const [series] = runCharts(rows, [rewardChart])[0].series;
  assert.deepEqual(series.points, [{ step: 0, value: 0.5 }, { step: 5, value: 0.8 }]);
});

test("the plotted area spans the steps and values actually recorded", () => {
  const [chart] = runCharts(collapsedRun, [rewardChart]);
  const geometry = chartGeometry(chart, 400, 160);

  assert.equal(geometry.minStep, 0);
  assert.equal(geometry.maxStep, 3);
  assert.equal(geometry.minValue, 0, "a run with only positive rewards is measured from zero");
  assert.equal(geometry.maxValue, 0.818);

  const [train] = geometry.series;
  assert.equal(train.coordinates[0].x, 0);
  assert.equal(train.coordinates[train.coordinates.length - 1].x, 400);
  // Reward fell almost to the floor, so the last point must sit near it.
  assert.ok(train.coordinates[train.coordinates.length - 1].y > 150);
});

// A rate cannot be read without knowing it is a rate: 0.75 shown full-height
// would look like success, not three quarters.
test("a chart with a declared range keeps it instead of fitting to the data", () => {
  const success = RUN_CHARTS.find((chart) => chart.id === "success");
  const [chart] = runCharts([
    { step: 0, "env/all/rle_harness/task_success": 0.5 },
    { step: 1, "env/all/rle_harness/task_success": 0.6 },
  ], [success]);
  const geometry = chartGeometry(chart, 400, 160);

  assert.deepEqual([geometry.minValue, geometry.maxValue], [0, 1]);
  assert.equal(geometry.series[0].coordinates[0].y, 80);
});

test("a run that has not moved is a flat line rather than a division by zero", () => {
  const [chart] = runCharts([
    { step: 0, "env/all/reward/total": 0.4 },
    { step: 1, "env/all/reward/total": 0.4 },
  ], [rewardChart]);
  const geometry = chartGeometry(chart, 400, 160);

  for (const point of geometry.series[0].coordinates) {
    assert.ok(Number.isFinite(point.x) && Number.isFinite(point.y), `not finite: ${JSON.stringify(point)}`);
  }
  assert.equal(geometry.series[0].coordinates[0].y, geometry.series[0].coordinates[1].y);
});

// A run is opened while it is going, so its first step is on the page alone.
test("a single step is placed at the left edge, where a run that just started belongs", () => {
  const [chart] = runCharts([{ step: 0, "env/all/reward/total": 0.4 }], [rewardChart]);
  const geometry = chartGeometry(chart, 400, 160);

  assert.equal(geometry.series[0].coordinates.length, 1);
  assert.equal(geometry.series[0].coordinates[0].x, 0);
});

test("no readings at all yields no geometry rather than an empty plot", () => {
  assert.equal(chartGeometry({ series: [] }), null);
  assert.equal(chartGeometry({ series: [{ points: [] }] }), null);
});

test("a path is drawn as one move followed by lines", () => {
  const path = chartPath([{ x: 0, y: 10 }, { x: 200.126, y: 5.4 }, { x: 400, y: 0 }]);
  assert.equal(path, "M0.00 10.00 L200.13 5.40 L400.00 0.00");
  assert.equal(chartPath([]), "");
});

// "Is it working" is a comparison. A value with no previous reading beside it
// cannot answer it.
test("headline metrics carry their change against the previous reading of the same metric", () => {
  const headline = runHeadline(collapsedRun);
  const trainReward = headline.find((entry) => entry.label === "Train reward");

  assert.equal(trainReward.value, 0.045);
  assert.equal(trainReward.step, 3);
  assert.ok(Math.abs(trainReward.delta - -0.006) < 1e-9, `delta was ${trainReward.delta}`);
});

test("a metric with one reading has a value but no change to report", () => {
  const [entry] = runHeadline([{ step: 0, "env/all/reward/total": 0.5 }]);
  assert.equal(entry.value, 0.5);
  assert.equal(entry.delta, null);
});

test("the change compares the last two readings of a metric, not the last two steps", () => {
  const headline = runHeadline([
    { step: 0, "rle_harness/validation_mean_reward": 0.4 },
    { step: 1 },
    { step: 2, "rle_harness/validation_mean_reward": 0.9 },
  ]);
  const validation = headline.find((entry) => entry.label === "Validation reward");
  assert.equal(validation.step, 2);
  assert.ok(Math.abs(validation.delta - 0.5) < 1e-9, `delta was ${validation.delta}`);
});

test("metrics the run never reported produce no headline card", () => {
  assert.deepEqual(runHeadline([{ step: 0, unrelated: 3 }]), []);
  assert.deepEqual(runHeadline([]), []);
  assert.deepEqual(runHeadline(null), []);
});

// The failure this view exists to make obvious.
test("a collapse is named rather than left to be inferred from the chart", () => {
  const [warning] = runWarnings(collapsedRun);
  assert.match(warning, /fallen to 0\.045 from 0\.725/);
  assert.match(warning, /step 0/);
  assert.match(warning, /learning rate|batch size/);
});

test("a run that is climbing raises nothing", () => {
  assert.deepEqual(runWarnings([
    { step: 0, "env/all/reward/total": 0.40, "env/all/by_group/frac_mixed": 1 },
    { step: 1, "env/all/reward/total": 0.55, "env/all/by_group/frac_mixed": 1 },
    { step: 2, "env/all/reward/total": 0.71, "env/all/by_group/frac_mixed": 0.8 },
  ]), []);
});

// Ordinary step-to-step noise is not a collapse, and warning on it would make
// the warning worth ignoring.
test("a dip that stays above half the best reward is not called a collapse", () => {
  assert.deepEqual(runWarnings([
    { step: 0, "env/all/reward/total": 0.8 },
    { step: 1, "env/all/reward/total": 0.5 },
  ]), []);
});

test("groups that all agree for three steps are reported as no gradient to learn from", () => {
  const warnings = runWarnings([
    { step: 0, "env/all/by_group/frac_mixed": 0 },
    { step: 1, "env/all/by_group/frac_mixed": 0 },
    { step: 2, "env/all/by_group/frac_mixed": 0 },
  ]);
  assert.equal(warnings.length, 1);
  assert.match(warnings[0], /advantage/);
});

// The recipe reports the dataset file's row count, not the number it trains on.
// Showing one without the other has misled a reader of this data before.
test("the training case count shows what is trained on as well as what was available", () => {
  const facts = runFacts({
    meta: { dataset: { training_cases: 804, validation_cases: 4 } },
    config: { dataset_builder: { max_train_examples: 32, group_size: 8, groups_per_batch: 4 } },
  });
  assert.deepEqual(facts.dataset.find(([label]) => label === "Training cases"), ["Training cases", "32 of 804"]);
  assert.deepEqual(facts.dataset.find(([label]) => label === "Rollouts per step"), ["Rollouts per step", 32]);
});

test("the file's row count stands alone when the run did not truncate it", () => {
  const facts = runFacts({ meta: { dataset: { training_cases: 804 } }, config: {} });
  assert.deepEqual(facts.dataset.find(([label]) => label === "Training cases"), ["Training cases", 804]);
  assert.equal(facts.dataset.find(([label]) => label === "Rollouts per step"), undefined);
});

// Hyperparameters that are legitimately zero must still be shown: a KL penalty
// of 0 is a decision, and hiding it hides the decision.
test("zero-valued settings are reported, absent ones are not", () => {
  const facts = runFacts({ meta: {}, config: { kl_penalty_coef: 0, learning_rate: 1e-5 } });
  const labels = facts.training.map(([label]) => label);
  assert.deepEqual(labels, ["Learning rate", "KL penalty"]);
});

test("an overview with no documents yet still yields every group, empty", () => {
  for (const value of [null, undefined, {}, { meta: null, config: null }]) {
    const facts = runFacts(value);
    assert.deepEqual(
      [facts.identity, facts.model, facts.dataset, facts.training].map((group) => group.length),
      [0, 0, 0, 0],
    );
  }
});

// A learning rate is written as 1e-5 in every recipe and doc that sets it.
test("small settings keep the exponential form they were written in", () => {
  assert.equal(settingLabel(1e-5), "1e-5");
  assert.equal(settingLabel(4e-5), "4e-5");
  assert.equal(settingLabel(0.001), 0.001, "values at the threshold stay decimal");
  assert.equal(settingLabel(0), 0, "zero is not 0e+0");
  assert.equal(settingLabel(32), 32);
  assert.equal(settingLabel("importance_sampling"), "importance_sampling");
});

// The environment counts a group as teaching something when its rewards are not
// all exactly equal. That is true of a pass/fail reward and never true of a
// weighted score, so on most runs the reported series read 1/0/0 for every step
// and the panel answers nothing. These derive the same question from the
// rollouts, where a spread of zero still means a group taught nothing.
const rollout = (task_id, checkpoint_id, reward, extra = {}) =>
  ({ task_id, checkpoint_id, reward, split: "train", ...extra });
// A standard deviation is a square root, so these compare to the precision the
// chart draws at rather than to the bit.
const close = (actual, expected) => assert.ok(Math.abs(actual - expected) < 1e-9,
  `expected ${actual} to be about ${expected}`);

test("a group's learning signal is measured as spread, not as exact disagreement", () => {
  const [row] = groupSignal([
    rollout("alpha", "step0", 0.4), rollout("alpha", "step0", 0.6),
    rollout("beta", "step0", 0.5), rollout("beta", "step0", 0.5),
  ]);
  assert.equal(row.step, 0);
  // alpha deviates 0.1 either side of 0.5; beta not at all.
  close(row["derived/by_group/reward_sd"], 0.05);
  assert.equal(row["derived/by_group/frac_all_good"], 0.5);
  assert.equal(row["derived/by_group/frac_all_bad"], 0);
});

test("a group that agreed on a low reward is too hard rather than too easy", () => {
  const [row] = groupSignal([rollout("alpha", "step0", 0.2), rollout("alpha", "step0", 0.2)]);
  assert.equal(row["derived/by_group/frac_all_bad"], 1);
  assert.equal(row["derived/by_group/frac_all_good"], 0);
});

// Training rollouts leave `step` unset and name the checkpoint they were sampled
// from. Bucketing them by it reproduces the environment's own per-step mean, which
// is how the mapping was confirmed against a live run.
test("training rollouts are bucketed by the checkpoint they were sampled from", () => {
  const rows = groupSignal([
    rollout("alpha", "step0", 0.2), rollout("alpha", "step0", 0.8),
    rollout("alpha", "1", 0.5), rollout("alpha", "1", 0.5),
    rollout("alpha", "2", 0.1), rollout("alpha", "2", 0.3),
  ]);
  assert.deepEqual(rows.map((row) => row.step), [0, 1, 2]);
  rows.map((row) => row["derived/by_group/reward_sd"]).forEach((sd, index) => close(sd, [0.3, 0, 0.1][index]));
});

// Advantage is computed over the groups a step trains on. Validation rollouts
// are not grouped, and counting them would report a spread nothing learns from.
test("validation rollouts are not counted as learning signal", () => {
  assert.deepEqual(groupSignal([
    { task_id: "alpha", step: 0, reward: 0.2, split: "validation" },
    { task_id: "alpha", step: 0, reward: 0.9, split: "validation" },
  ]), []);
});

test("a group with a single rollout has no spread to report", () => {
  assert.deepEqual(groupSignal([rollout("alpha", "step0", 0.4)]), []);
});

test("derived measurements join the step they belong to and leave the rest alone", () => {
  const rows = withGroupSignal(
    [{ step: 0, "env/all/reward/total": 0.5 }, { step: 1, "env/all/reward/total": 0.6 }],
    [rollout("alpha", "step0", 0.4), rollout("alpha", "step0", 0.6)],
  );
  close(rows[0]["derived/by_group/reward_sd"], 0.1);
  assert.equal(rows[0]["env/all/reward/total"], 0.5);
  assert.equal("derived/by_group/reward_sd" in rows[1], false);
});

test("metrics rows are returned unchanged when no rollouts have been loaded", () => {
  const rows = [{ step: 0, "env/all/reward/total": 0.5 }];
  assert.deepEqual(withGroupSignal(rows, []), rows);
});

const signalChart = RUN_CHARTS.find((chart) => chart.id === "signal");

test("the learning signal panel prefers the derived spread over the reported fractions", () => {
  const [chart] = runCharts([{
    step: 0,
    "derived/by_group/reward_sd": 0.17,
    "env/all/by_group/frac_mixed": 1,
  }], [signalChart]);
  assert.deepEqual(chart.series.map((series) => series.name), ["Spread"]);
});

// A monitor serving a run it cannot list rollouts for still has the environment's
// own answer, and a weaker answer beats an empty panel.
test("the learning signal panel falls back to the reported fractions when nothing was derived", () => {
  const [chart] = runCharts([{ step: 0, "env/all/by_group/frac_mixed": 0.8 }], [signalChart]);
  assert.deepEqual(chart.series.map((series) => series.name), ["Mixed"]);
});

test("three steps with no spread in any group are reported as no gradient to learn from", () => {
  const warnings = runWarnings([
    { step: 0, "derived/by_group/reward_sd": 0 },
    { step: 1, "derived/by_group/reward_sd": 0 },
    { step: 2, "derived/by_group/reward_sd": 0 },
  ]);
  assert.equal(warnings.length, 1);
  assert.match(warnings[0], /advantage/);
});
