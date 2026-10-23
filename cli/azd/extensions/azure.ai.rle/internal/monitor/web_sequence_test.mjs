// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import test from "node:test";
import assert from "node:assert/strict";
import { sequenceData, sequencePage, SEQUENCE_BIN_LIMIT, TOKEN_PAGE_SIZE } from "./web/data.mjs";

test("missing arrays and explicit empty arrays remain distinguishable", () => {
  const missing = sequenceData({}, "tokens");
  const empty = sequenceData({ input_ids: [], loss_mask: [], logprobs: [] }, "tokens");
  assert.equal(missing.arrays.input_ids, null);
  assert.deepEqual(empty.arrays.input_ids, []);
  assert.match(missing.notices.join(" "), /not included/);
  assert.match(empty.notices.join(" "), /present but empty/);
  assert.equal(sequencePage(missing).rows.length, 0);
  assert.equal(sequencePage(empty).rows.length, 0);
});

test("unequal arrays retain all tails and never fill missing elements with zero", () => {
  const data = sequenceData({ input_ids: [100], loss_mask: [0, 1], logprobs: [0, -.25, -3] }, "tokens");
  const { rows } = sequencePage(data);
  assert.equal(data.length, 3);
  assert.match(data.notices.join(" "), /lengths differ/);
  assert.equal(rows[1].token, undefined);
  assert.equal(rows[2].mask, undefined);
  assert.equal(rows[2].logprob, -3);
  assert.equal(rows[2].score, "unknown");
});

test("zero placeholders under mask zero are excluded, never treated as certainty", () => {
  const { rows } = sequencePage(sequenceData({
    input_ids: [0, 8, 9, 10], loss_mask: [0, 1, 0, 1], logprobs: [0, 0, -.5, -.125],
  }, "tokens"));
  assert.deepEqual(rows.map((row) => row.score), ["excluded", "scored", "excluded", "scored"]);
  assert.deepEqual(rows.map((row) => row.logprob), [0, 0, -.5, -.125]);
  assert.equal(rows[0].token, 0);
  assert.equal(rows[0].mask, 0);
});

test("nonfinite, null, strings, and absent logprobs never become real zeroes", () => {
  const { rows } = sequencePage(sequenceData({
    loss_mask: [1, 1, 1, 1, 1, 1], logprobs: [NaN, Infinity, null, "0", undefined, 0],
  }, "tokens"));
  assert.deepEqual(rows.map((row) => row.score), ["missing", "missing", "missing", "missing", "missing", "scored"]);
  assert.equal(rows[2].logprob, null);
  assert.equal(rows[4].logprob, undefined);
});

test("a log probability cannot be scored without a valid target mask", () => {
  const { rows } = sequencePage(sequenceData({ loss_mask: [2, "1", null], logprobs: [-1, -2, -3, -4] }, "tokens"));
  assert.equal(rows.every((row) => row.score === "unknown"), true);
});

test("mask target selection is independent of sequence role and trainability", () => {
  for (const role of ["agent", "auxiliary", "discarded"]) {
    const data = sequenceData({ role, trainable: false, loss_mask: [1], logprobs: [-.1] }, "tokens");
    assert.equal(sequencePage(data).rows[0].score, "scored");
  }
});

test("metadata capture is disclosed without inventing arrays or hiding supplied values", () => {
  const absent = sequenceData({}, "metadata");
  assert.match(absent.notices.join(" "), /not "tokens"/);
  assert.equal(absent.length, 0);
  const supplied = sequenceData({ input_ids: [0] }, "metadata");
  assert.equal(sequencePage(supplied).rows[0].token, 0);
  assert.match(supplied.notices.join(" "), /not "tokens"/);
});

test("100k token arrays render only a page while preserving absolute positions", () => {
  const input_ids = Array.from({ length: 100001 }, (_, index) => index + 800);
  const data = sequenceData({ input_ids }, "tokens");
  const first = sequencePage(data);
  assert.equal(first.rows.length, TOKEN_PAGE_SIZE);
  assert.equal(first.rows[0].position, 0);
  const second = sequencePage(data, 1);
  assert.equal(second.rows[0].position, 100);
  assert.equal(second.rows[0].token, 900);
  const last = sequencePage(data, 99999);
  assert.equal(last.rows.length, 1);
  assert.equal(last.rows[0].position, 100000);
  assert.equal(last.pageCount, 1001);
  assert.equal(sequencePage(data, -1).page, 0);
});

test("page boundaries expose exact values and do not mutate the sequence", () => {
  const sequence = { input_ids: [0, 25, 100], loss_mask: [0, 1, 1], logprobs: [0, -1.23456789123, -2] };
  const before = JSON.stringify(sequence);
  const page = sequencePage(sequenceData(sequence, "tokens"), 1, 2);
  assert.equal(page.start, 2);
  assert.equal(page.end, 3);
  assert.equal(page.rows[0].logprob, -2);
  assert.equal(sequencePage(sequenceData(sequence, "tokens")).rows[1].logprob, -1.23456789123);
  assert.equal(JSON.stringify(sequence), before);
});

test("whole-sequence overview counts actual masks, not reported totals or prompt length", () => {
  const data = sequenceData({
    input_ids: [11, 12, 13, 14, 15], loss_mask: [0, 1, 1, 0, null],
    logprobs: [-99, 0, -0.000001, 0, -100], prompt_len: 900, n_trainable: 900,
  }, "tokens");
  assert.deepEqual(data.positions, { target: [1, 2], excluded: [0, 3], unknown: [4] });
  assert.equal(data.scored, 2);
  assert.equal(data.minLogprob, -0.000001);
  assert.equal(data.maxLogprob, 0);
  assert.equal(data.bins.reduce((sum, bin) => sum + bin.scored, 0), 2);
});

test("filtered pages retain original positions and exact recorded zeros", () => {
  const data = sequenceData({ input_ids: [8, 9, 10, 11, 12], loss_mask: [0, 1, 0, 1, 2],
    logprobs: [0, 0, 0, -.01, 0] }, "tokens");
  assert.deepEqual(sequencePage(data, 0, 1, "target").rows.map(r => r.position), [1]);
  const next = sequencePage(data, 1, 1, "target");
  assert.equal(next.rows[0].position, 3);
  assert.equal(next.total, 2);
  assert.equal(sequencePage(data, 0, 1, "target").rows[0].logprob, 0);
  assert.deepEqual(sequencePage(data, 0, 100, "excluded").rows.map(r => r.position), [0, 2]);
  assert.deepEqual(sequencePage(data, 0, 100, "unknown").rows.map(r => r.position), [4]);
  assert.throws(() => sequencePage(data, 0, 100, "bad-filter"), /unknown token filter/);
});

test("large overviews use bounded exact range bins without dropping tails or extrema", () => {
  const length = 100001;
  const data = sequenceData({
    input_ids: Array(length).fill(12),
    loss_mask: Array.from({ length }, (_, i) => i < 136 ? 0 : 1),
    logprobs: Array.from({ length }, (_, i) => i === length - 1 ? -7.5 : 0),
  }, "tokens");
  assert.ok(data.bins.length <= SEQUENCE_BIN_LIMIT);
  assert.equal(data.bins[0].start, 0);
  assert.equal(data.bins.at(-1).end, length);
  assert.equal(data.positions.target[0], 136);
  assert.equal(data.positions.excluded.length, 136);
  assert.equal(data.scored, length - 136);
  assert.equal(data.minLogprob, -7.5);
  assert.equal(data.maxLogprob, 0);
  assert.equal(data.bins.at(-1).min, -7.5);
  assert.equal(data.bins.reduce((sum, bin) => sum + bin.target + bin.excluded + bin.unknown, 0), length);
});

test("unavailable and invalid scores produce no invented plot values", () => {
  const data = sequenceData({ loss_mask: [1, 1, 1, 1, 0], logprobs: [null, 2, "0", Infinity, -30] }, "tokens");
  assert.equal(data.scored, 0);
  assert.equal(data.minLogprob, null);
  assert.equal(data.maxLogprob, null);
  assert.equal(data.bins.every(bin => bin.min === null && bin.max === null), true);
  assert.match(data.notices.join(" "), /4 target position/);
  assert.equal(sequencePage(data).rows[1].score, "missing");
  const empty = sequenceData({}, "tokens");
  assert.deepEqual(empty.bins, []);
  assert.equal(sequencePage(empty, 10, 100, "target").total, 0);
});

test("reported count conflicts and invalid arrays are disclosed", () => {
  const data = sequenceData({ n_trainable: 50, loss_mask: [1, 0], input_ids: [1.25, -2], logprobs: {} }, "tokens");
  assert.match(data.notices.join(" "), /not an array/);
  assert.match(data.notices.join(" "), /Reported target count differs/);
  assert.match(data.notices.join(" "), /2 token ID/);
});
