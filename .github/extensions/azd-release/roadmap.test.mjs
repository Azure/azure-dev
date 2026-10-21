// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import test from "node:test";
import assert from "node:assert/strict";
import { currentRecords, releaseRoadmap } from "./roadmap.mjs";
import { timelineSummary } from "./model.mjs";

const stage = (identifier, state = "pending", result = null, attempt = 1, id = identifier) =>
    ({ id, identifier, type: "Stage", state, result, attempt });
const approval = (parent, state = "inProgress", result = null, attempt = 1) => [
    { id: `${parent}-check-${attempt}`, parentId: parent, identifier: "Checkpoint", type: "Checkpoint", attempt },
    { id: `${parent}-approve-${attempt}`, parentId: `${parent}-check-${attempt}`,
        identifier: `${parent}-approve-${attempt}`, type: "Checkpoint.Approval", state, result, attempt },
];
const fixture = (timeline = []) => ({
    release: { runId: 200, changelogPr: 100 },
    changelog: { merged: true, state: "closed" },
    pipeline: { status: "inProgress", timeline, metadata: { available: true, msiVersion: "1.37.100", submitWingetPackage: true } },
});
const statuses = (snapshot) => releaseRoadmap(snapshot).map((step) => step.status);

test("five stops stay gray without evidence; changelog needs an actual merge", () => {
    const snapshot = fixture();
    delete snapshot.changelog;
    delete snapshot.pipeline;
    assert.deepEqual(statuses(snapshot), ["pending", "pending", "pending", "pending", "pending"]);
    snapshot.changelog = { state: "closed", merged: false };
    assert.equal(statuses(snapshot)[0], "failed");
    snapshot.changelog.merged = true;
    assert.equal(statuses(snapshot)[0], "complete");
    assert.equal(releaseRoadmap(snapshot).length, 5);
});

test("only run-scoped publishing approval checkpoints advance CI, not queued stages or unrelated checks", () => {
    for (const records of [
        [stage("BuildAndTest", "completed", "succeeded"), stage("PublishCLI")],
        [stage("Sign"), ...approval("Sign")],
        [stage("PublishCLI", "inProgress"), { id: "check", parentId: "PublishCLI", type: "Checkpoint.ProductionReadinessCheck", state: "inProgress" }],
        [...approval("different-run-stage")],
    ]) assert.equal(statuses(fixture(records))[1], "pending");
    for (const publishing of ["PublishCLI", "PublishInstallers"]) {
        const snapshot = fixture([stage(publishing), ...approval(publishing)]);
        assert.equal(statuses(snapshot)[1], "complete");
        assert.equal(statuses(snapshot)[2], "pending");
        assert.match(releaseRoadmap(snapshot)[1].detail, /first publishing approval/);
    }
});

test("publishing reaches green only at version.txt approval or evidence of passing it", () => {
    const snapshot = fixture([stage("PublishCLI", "completed", "succeeded"), stage("PublishVersionTxt")]);
    assert.deepEqual(statuses(snapshot).slice(0, 3), ["complete", "complete", "pending"]);
    snapshot.pipeline.timeline.push(...approval("PublishVersionTxt", "pending"));
    assert.equal(statuses(snapshot)[2], "complete");
    assert.match(releaseRoadmap(snapshot)[2].detail, /version.txt approval/);
    snapshot.pipeline.timeline = [
        stage("PublishVersionTxt", "completed", "succeeded"),
    ];
    assert.deepEqual(statuses(snapshot).slice(0, 3), ["complete", "complete", "complete"]);
    snapshot.pipeline.timeline = [
        stage("PublishVersionTxt", "inProgress"),
        { id: "upload", parentId: "PublishVersionTxt", identifier: "PublishVersionTxt.Upload_Version_Txt", type: "Job", state: "inProgress" },
    ];
    assert.equal(statuses(snapshot)[2], "complete");
});

test("failures color the relevant stage red without treating skipped dependents as failures", () => {
    assert.deepEqual(statuses(fixture([
        stage("BuildAndTest", "completed", "failed"), stage("PublishCLI", "completed", "skipped"),
    ])).slice(1, 3), ["failed", "pending"]);
    assert.deepEqual(statuses(fixture([
        stage("BuildAndTest", "completed", "succeeded"),
        stage("PublishCLI", "completed", "failed"), ...approval("PublishCLI", "completed", "succeeded"),
    ])).slice(1, 3), ["complete", "failed"]);
    assert.equal(statuses(fixture([
        stage("PublishVersionTxt", "completed", "canceled"), ...approval("PublishVersionTxt", "completed", "canceled"),
    ]))[2], "failed");
});

test("new stage attempts invalidate earlier approvals and failures, retaining separate stage checkpoints", () => {
    const records = [
        stage("PublishCLI", "completed", "failed", 1, "old"),
        ...approval("old", "completed", "succeeded"),
        stage("PublishCLI", "pending", null, 2, "new"),
        { id: "stale-check", parentId: "new", type: "Checkpoint", identifier: "Checkpoint", attempt: 1 },
        { id: "stale-approval", parentId: "stale-check", type: "Checkpoint.Approval", state: "inProgress", attempt: 1 },
        { id: "old-phase", parentId: "new", identifier: "PublishCLI.Publish_Release", type: "Phase", attempt: 1 },
        { id: "old-job", parentId: "old-phase", identifier: "PublishCLI.Publish_Release.__default", type: "Job", state: "completed", result: "succeeded", attempt: 1 },
        stage("PublishInstallers"), ...approval("PublishInstallers"),
    ];
    const current = currentRecords(timelineSummary(records));
    assert.ok(!current.some((record) => ["old", "old-check-1", "old-approve-1", "stale-approval", "old-phase", "old-job"].includes(record.id)));
    assert.ok(current.some((record) => record.id === "PublishInstallers-check-1"));
    assert.equal(statuses(fixture(current))[1], "complete");
    assert.equal(statuses(fixture(current.filter((record) =>
        record.id !== "PublishInstallers-approve-1")))[1], "pending");
});

test("merged WinGet PRs alone stay pending; only catalog availability completes that stop", () => {
    const snapshot = fixture();
    const pr = (number, state, merged = false) => ({ number, state, merged, url: `https://github.com/microsoft/winget-pkgs/pull/${number}` });
    snapshot.followups = {
        winget: { expectedVersion: "1.37.100", prs: [pr(1, "closed"), pr(2, "closed", true)] },
        version: { prs: [pr(3, "open")] },
    };
    assert.deepEqual(statuses(snapshot).slice(3), ["pending", "pending"]);
    assert.match(releaseRoadmap(snapshot)[3].warning, /local WinGet catalog.*Merging the PR is not enough/);
    assert.match(releaseRoadmap(snapshot)[3].detail, /Expected package version \(WinGet PR\): 1\.37\.100/);
    snapshot.followups.winget.catalog = {
        expectedVersion: "1.37.100", latestVersion: "1.36.100", available: false,
        checkedAt: "2026-10-21T06:00:00Z",
    };
    assert.equal(statuses(snapshot)[3], "pending");
    assert.match(releaseRoadmap(snapshot)[3].detail, /Latest local catalog version: 1\.36\.100/);
    snapshot.followups.winget.catalog.latestVersion = "1.37.100";
    snapshot.followups.winget.catalog.available = true;
    assert.equal(statuses(snapshot)[3], "complete");
    assert.equal(releaseRoadmap(snapshot)[3].checkedAt, "2026-10-21T06:00:00Z");
    snapshot.followups.winget.catalog.expectedVersion = "1.36.100";
    assert.equal(statuses(snapshot)[3], "pending");
    snapshot.followups.version.prs[0] = pr(3, "closed");
    assert.equal(statuses(snapshot)[4], "failed");
    snapshot.followups.winget = { error: "Could not check WinGet PR: access unavailable" };
    assert.equal(statuses(snapshot)[3], "pending");
    assert.match(releaseRoadmap(snapshot)[3].detail, /access unavailable/);
    snapshot.pipeline.metadata.submitWingetPackage = false;
    assert.equal(statuses(snapshot)[3], "pending");
    assert.match(releaseRoadmap(snapshot)[3].detail, /disabled/);
});

test("real Stage -> Phase -> Job -> Task ancestry retains running jobs and failed task diagnosis", () => {
    const raw = [
        stage("BuildAndTest", "inProgress"),
        { id: "phase", parentId: "BuildAndTest", identifier: "BuildAndTest.BuildCLI", type: "Phase", state: "inProgress" },
        { id: "job", parentId: "phase", identifier: "BuildAndTest.BuildCLI.__default", type: "Job", state: "inProgress" },
        { id: "task", parentId: "job", identifier: "test", type: "Task", result: "failed", log: { id: 25 } },
    ];
    const timeline = currentRecords(timelineSummary(raw));
    assert.equal(timeline.length, 4);
    assert.equal(timeline.at(-1).logId, 25);
    assert.equal(statuses(fixture(timeline))[1], "failed");
    const publishing = [
        stage("PublishCLI", "inProgress"),
        { id: "phase", parentId: "PublishCLI", identifier: "PublishCLI.Publish_Release", type: "Phase" },
        { id: "job", parentId: "phase", identifier: "PublishCLI.Publish_Release.__default", type: "Job", state: "inProgress" },
    ];
    assert.equal(statuses(fixture(currentRecords(timelineSummary(publishing))))[1], "complete");
    const duplicateTaskNames = [...raw,
        { id: "job2", parentId: "phase", identifier: "BuildAndTest.BuildCLI.other", type: "Job", state: "completed" },
        { id: "task2", parentId: "job2", identifier: "test", type: "Task", result: "failed", log: { id: 26 } },
    ];
    assert.equal(currentRecords(timelineSummary(duplicateTaskNames)).filter((record) => record.type === "Task").length, 2);
});

test("catalog and version checks appear only at their active stop and disappear after completion or closure", () => {
    const snapshot = fixture();
    const checks = () => releaseRoadmap(snapshot).filter((step) => step.check).map((step) => step.check);
    assert.deepEqual(checks(), []);
    snapshot.pipeline.timeline = [stage("PublishCLI"), ...approval("PublishCLI")];
    assert.deepEqual(checks(), []);
    snapshot.pipeline.timeline = [stage("PublishVersionTxt"), ...approval("PublishVersionTxt")];
    assert.deepEqual(checks(), ["Check WinGet catalog"]);
    snapshot.followups = {
        winget: { expectedVersion: "1.37.100", prs: [{ number: 1, state: "closed", merged: false }] },
        version: { prs: [{ number: 2, state: "open", merged: false }] },
    };
    assert.deepEqual(checks(), ["Check WinGet catalog"]);
    snapshot.followups.winget.prs[0].merged = true;
    assert.deepEqual(checks(), ["Check WinGet catalog"]);
    snapshot.followups.winget.catalog = {
        expectedVersion: "1.37.100", latestVersion: "1.37.100", available: true,
    };
    assert.deepEqual(checks(), ["Check version PR"]);
    snapshot.followups.version.prs[0].merged = true;
    assert.deepEqual(checks(), []);
    snapshot.followups.version.prs[0].merged = false;
    snapshot.release.issueState = "closed";
    assert.deepEqual(checks(), []);
    snapshot.release.issueState = "open";
    snapshot.pipeline.timeline = [stage("PublishCLI", "completed", "failed")];
    assert.deepEqual(checks(), []);
});

test("a discovered WinGet PR allows checking the catalog while ESRP or unrelated publishing jobs are unfinished", () => {
    const snapshot = fixture([stage("PublishCLI", "inProgress")]);
    snapshot.followups = {
        winget: { expectedVersion: "1.37.100", prs: [{ number: 1, state: "closed", merged: true }] },
    };
    for (const result of [null, "failed"]) {
        snapshot.pipeline.timeline[0].result = result;
        const winget = releaseRoadmap(snapshot)[3];
        assert.equal(winget.status, "pending");
        assert.equal(winget.check, "Check WinGet catalog");
        assert.equal(winget.action, "check-winget");
    }
    snapshot.release.issueState = "closed";
    assert.equal(releaseRoadmap(snapshot)[3].check, undefined);
});

test("a run with WinGet submission disabled skips that check when reaching follow-ups", () => {
    const snapshot = fixture([stage("PublishVersionTxt"), ...approval("PublishVersionTxt")]);
    snapshot.pipeline.metadata.submitWingetPackage = false;
    assert.deepEqual(releaseRoadmap(snapshot).filter((step) => step.check).map((step) => step.check),
        ["Check version PR"]);
});
