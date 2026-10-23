// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import test from "node:test";
import assert from "node:assert/strict";
import {
    block, releaseState, cleanUrl, evidenceUrl, validateRun, timelineSummary,
    pipelineHealth, publicationStatus, MILESTONES, nextStep,
} from "./model.mjs";
import { errorMessage } from "./cli.mjs";

const source = "a".repeat(40);

test("merge advances next-step guidance without enabling observer, reserved, or duplicate queues", () => {
    const snapshot = {
        release: { issueState: "open", author: "driver", changelogPr: 100, runId: null, intent: null },
        changelog: { merged: false, state: "open" },
        access: { azure: { ready: true } }, isDriver: true,
    };
    assert.equal(nextStep(snapshot).stage, "preparation");
    assert.equal(nextStep(snapshot).canQueue, false);
    snapshot.changelog = { merged: true, state: "closed" };
    assert.equal(nextStep(snapshot).stage, "queue");
    assert.equal(nextStep(snapshot).canQueue, true);
    assert.match(nextStep(snapshot).message, /Changelog PR merged/);
    snapshot.isDriver = false;
    assert.equal(nextStep(snapshot).stage, "awaiting-driver");
    assert.equal(nextStep(snapshot).canQueue, false);
    snapshot.isDriver = true;
    snapshot.access.azure.ready = false;
    assert.equal(nextStep(snapshot).stage, "azure-access");
    assert.equal(nextStep(snapshot).canQueue, false);
    snapshot.access.azure.ready = true;
    snapshot.release.intent = { token: "reserved" };
    assert.equal(nextStep(snapshot).stage, "reconcile");
    assert.equal(nextStep(snapshot).canQueue, false);
    snapshot.release.runId = 200;
    assert.equal(nextStep(snapshot).stage, "packages");
    assert.equal(nextStep(snapshot).canQueue, false);
    snapshot.pipelineHealth = "failed";
    assert.equal(nextStep(snapshot).stage, "investigation");
    snapshot.release.issueState = "closed";
    snapshot.release.closureReason = "completed";
    assert.equal(nextStep(snapshot).stage, "completed");
    assert.equal(nextStep(snapshot).canQueue, false);
});

test("unknown status and a PR closed without merging never imply ready to publish", () => {
    const snapshot = {
        release: { issueState: "open", author: "driver", changelogPr: 100 },
        access: { azure: { ready: true } }, isDriver: true,
    };
    assert.equal(nextStep(snapshot).stage, "changelog-unknown");
    assert.equal(nextStep(snapshot).canQueue, false);
    snapshot.changelog = { merged: false, state: "closed" };
    assert.match(nextStep(snapshot).message, /closed without merging/);
    assert.equal(nextStep(snapshot).canQueue, false);
});
function issue() {
    return {
        number: 42, user: { login: "driver" }, state: "open", labels: [{ name: "azd-release" }],
        body: block("state", { schema: 1, version: "1.37.0", changelogPr: 100 }),
    };
}
function event(id, data, login = "driver") {
    return { id, user: { login }, body: block("event", { schema: 1, ...data }) };
}

test("state is release-scoped and folds only driver comments in append order", () => {
    const comments = [
        event(30, { type: "run", runId: 200 }),
        event(10, { type: "run", runId: 100 }),
        event(40, { type: "run", runId: 999 }, "observer"),
        event(20, { type: "milestone", key: "build", status: "complete", evidence: "https://github.com/Azure/azure-dev/pull/100" }),
    ];
    const state = releaseState(issue(), comments);
    assert.equal(state.runId, 200);
    assert.equal(state.milestones.build.status, "complete");
    assert.equal(state.version, "1.37.0");
    assert.equal(publicationStatus(state), `1/${MILESTONES.length} milestones verified by driver`);
});

test("the first queue reservation wins across clients and survives reload", () => {
    const comments = [
        event(2, { type: "queue-intent", token: "second" }),
        event(1, { type: "queue-intent", token: "first" }),
    ];
    assert.equal(releaseState(issue(), comments).intent.token, "first");
    assert.deepEqual(releaseState(issue(), comments), releaseState(issue(), comments));
});

test("tracking can precede changelog preparation and driver linking is shared, not panel-local", () => {
    const tracking = { ...issue(), body: block("state", { schema: 1, version: "1.37.1", changelogPr: null }) };
    const initial = releaseState(tracking, []);
    assert.equal(initial.changelogPr, null);
    assert.equal(nextStep({ release: initial, isDriver: true }).stage, "changelog");
    assert.equal(nextStep({ release: initial, isDriver: true }).canQueue, false);
    assert.match(nextStep({ release: initial, isDriver: false }).message, /Waiting for @driver/);
    const linked = event(1, { type: "changelog", changelogPr: 10661 });
    assert.equal(releaseState(tracking, [linked]).changelogPr, 10661);
    assert.deepEqual(releaseState(tracking, [linked]), releaseState(tracking, [linked]));
    assert.equal(releaseState(tracking, [{ ...linked, user: { login: "observer" } }]).changelogPr, null);
    assert.throws(() => releaseState(tracking, [event(1, { type: "changelog", changelogPr: 0 })]), /positive integer/);
    assert.throws(() => releaseState(tracking, [linked, event(2, { type: "changelog", changelogPr: 10662 })]), /different changelog/);
    assert.throws(() => releaseState(tracking, [event(1, { type: "run", runId: 200 })]), /linked changelog/);
});

test("unknown/malformed state fails closed rather than erasing progress", () => {
    assert.throws(() => releaseState({ ...issue(), body: "not state" }, []), /Missing/);
    assert.throws(() => releaseState({ ...issue(), body: issue().body + issue().body }, []), /ambiguous/);
    assert.throws(() => releaseState(issue(), [event(1, { type: "future-feature" })]), /Unknown/);
    assert.throws(() => releaseState(issue(), [event(1, { type: "milestone", key: "unknown", status: "complete" })]), /Invalid/);
    assert.throws(() => releaseState({ ...issue(), labels: [] }, []), /labeled/);
});

test("credential-bearing URLs are never disclosed in evidence or timeline output", () => {
    const sensitive = "https://user:password@dev.azure.com/azure-sdk/internal/_build/results?buildId=123&sig=secret#private";
    assert.equal(cleanUrl(sensitive), "https://dev.azure.com/azure-sdk/internal/_build/results");
    const canonical = evidenceUrl(sensitive);
    assert.equal(canonical, "https://dev.azure.com/azure-sdk/internal/_build/results?buildId=123");
    const comments = [event(1, { type: "milestone", key: "build", status: "complete", evidence: sensitive })];
    const rendered = JSON.stringify({
        state: releaseState(issue(), comments),
        timeline: timelineSummary([{ type: "Job", name: `Download ${sensitive}`, state: "completed" }]),
    });
    for (const secret of ["user:", "password", "sig=", "secret", "#private"]) assert.ok(!rendered.includes(secret));
    assert.throws(() => evidenceUrl("https://evil.example/logs"), /Use a GitHub/);
    assert.throws(() => evidenceUrl("javascript:alert(1)"), /HTTPS/);
    assert.ok(!errorMessage(new Error(sensitive)).includes("password"));
});

test("run identity requires publishing intent, repo, branch, reason, definition, and full SHA", () => {
    const build = {
        definition: { id: 9 }, repository: { name: "Azure/azure-dev" },
        sourceBranch: "refs/heads/main", reason: "manual", sourceVersion: source,
        templateParameters: { DoPublish: true },
    };
    validateRun(build, 9);
    validateRun({ ...build, templateParameters: '{"DoPublish":"true"}' }, 9);
    validateRun({ ...build, repository: { id: "Azure/azure-dev", type: "GitHub" } }, 9);
    for (const change of [
        { definition: { id: 8 } }, { repository: { name: "other/repo" } }, { sourceBranch: "refs/heads/feature" },
        { reason: "individualCI" }, { templateParameters: { DoPublish: false } }, { sourceVersion: "short" },
        { repository: { id: "Azure/azure-dev", type: "TfsGit" } },
        { repository: { name: "other/repo", id: "Azure/azure-dev", type: "GitHub" } },
        { repository: { id: "other/repo", type: "GitHub" } },
    ]) assert.throws(() => validateRun({ ...build, ...change }, 9));
});

test("warnings, failed attempts, and product attestation remain distinct", () => {
    assert.equal(pipelineHealth({ result: "succeeded" }, [{ result: "succeededWithIssues" }]), "warnings");
    assert.equal(pipelineHealth({ result: "failed" }, []), "failed");
    const state = releaseState({ ...issue(), state: "closed", state_reason: "completed" }, []);
    assert.equal(publicationStatus(state), "driver recorded completion");
    assert.equal(pipelineHealth({ result: "failed" }, []), "failed");
    const timeline = timelineSummary([{
        id: "job", type: "Job", name: "Publish", result: "failed", attempt: 2,
        previousAttempts: [{ attempt: 1, timelineId: "old", recordId: "prior", secret: "not copied" }],
    }]);
    assert.deepEqual(timeline[0].previousAttempts, [{ attempt: 1, timelineId: "old", recordId: "prior" }]);
});

test("partial abandonment survives reconstruction without completing milestones or offering recovery", () => {
    const comments = [
        event(1, { type: "queue-intent", token: "original" }),
        event(2, { type: "run", runId: 200 }),
        event(3, { type: "outcome", status: "partial", successorVersion: "1.37.1" }),
    ];
    for (const state of ["open", "closed"]) {
        const release = releaseState({ ...issue(), state, state_reason: "not_planned" }, comments);
        assert.equal(release.runId, 200);
        assert.equal(release.intent.token, "original");
        assert.equal(release.outcome.successorVersion, "1.37.1");
        assert.ok(Object.values(release.milestones).every((milestone) => milestone === null));
        assert.equal(publicationStatus(release), "partially published; abandoned");
        const next = nextStep({ release, pipelineHealth: "failed", isDriver: true });
        assert.equal(next.stage, "partial");
        assert.equal(next.canQueue, false);
        assert.match(next.message, /do not retry.*1\.37\.1/);
        assert.deepEqual(releaseState({ ...issue(), state, state_reason: "not_planned" }, comments), release);
    }
    const observer = comments.map((comment) => ({ ...comment, user: { login: "observer" } }));
    assert.equal(releaseState(issue(), observer).outcome, null);
});

test("unknown closure outcomes and invalid abandonment events never imply successful completion", () => {
    for (const state_reason of ["not_planned", null]) {
        const release = releaseState({ ...issue(), state: "closed", state_reason }, []);
        assert.equal(nextStep({ release }).stage, "closed");
        assert.equal(nextStep({ release }).canQueue, false);
        assert.equal(publicationStatus(release), "closed without verified completion");
    }
    for (const data of [
        { status: "complete" }, { status: "partial", successorVersion: "../invalid" },
        { status: "partial", successorVersion: "1.37.0" }, { status: "partial", successorVersion: null },
        { status: "partial", resolution: "success" },
        { status: "partial", resolution: "mitigated-errors", successorVersion: "1.37.1" },
    ]) assert.throws(() => releaseState(issue(), [event(1, { type: "outcome", ...data })]));
    assert.equal(releaseState(issue(), [event(1, { type: "outcome", status: "partial" })]).outcome.successorVersion, null);
});

test("mitigated partial closure preserves pipeline failures and does not attest pending manual recovery", () => {
    const comments = [
        event(1, { type: "run", runId: 200 }),
        event(2, { type: "outcome", status: "partial", resolution: "mitigated-errors" }),
    ];
    for (const state of ["open", "closed"]) {
        const release = releaseState({ ...issue(), state, state_reason: "completed" }, comments);
        assert.equal(release.outcome.resolution, "mitigated-errors");
        assert.equal(publicationStatus(release), "partially completed; mitigated errors");
        assert.equal(pipelineHealth({ result: "failed" }, []), "failed");
        assert.ok(Object.values(release.milestones).every((milestone) => milestone === null));
        const next = nextStep({ release, pipelineHealth: "failed", isDriver: true });
        assert.equal(next.canQueue, false);
        assert.match(next.message, /pending follow-ups/);
        assert.doesNotMatch(next.message, /abandoned/);
    }
    assert.equal(releaseState(issue(), comments.map((comment) =>
        ({ ...comment, user: { login: "observer" } }))).outcome, null);
});
