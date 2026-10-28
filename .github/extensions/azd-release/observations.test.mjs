// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import test from "node:test";
import assert from "node:assert/strict";
import { ReleaseService } from "./release-service.mjs";
import { ReleaseError } from "./cli.mjs";
import { block, releaseState, PRIVATE_REPO } from "./model.mjs";
import {
    observationComment, observationUpdate, readObservations, observationView, mergeObservations, OBSERVATION_MAX_AGE,
} from "./observations.mjs";
import { packageGates, releaseAssetNames } from "./packages.mjs";
import { homebrewFixture } from "./homebrew-fixture.mjs";

const source = "a".repeat(40);
const metadata = {
    available: true, cliVersion: "1.2.3", msiVersion: "1.2.400", isPublishingGa: true,
    submitWingetPackage: true, submitChocoPackage: true, submitBrewFormula: true,
};
const issue = () => ({
    number: 42, state: "open", user: { login: "driver" }, labels: [{ name: "azd-release" }],
    body: block("state", { schema: 1, version: "1.2.3", changelogPr: 100 }),
});
const runEvent = () => ({
    id: 1, user: { login: "driver" }, body: block("event", { schema: 1, type: "run", runId: 200 }),
});
const run = () => ({ id: 200, source, status: "inProgress", result: null, metadata: { ...metadata } });
const packageResult = (at = new Date().toISOString()) => ({
    runId: 200, source, cliVersion: "1.2.3", msiVersion: "1.2.400", checkedAt: at,
    gates: packageGates("1.2.3", "1.2.400").map((gate) => ({ ...gate, status: "ready" })),
});

function fixture() {
    const tracking = issue();
    const comments = [runEvent()];
    const writes = [];
    let login = "driver";
    let azureReads = 0;
    const make = () => {
        const service = new ReleaseService(async (file, args) => {
            if (file === "gh") {
                assert.match(args[1], /azure-dev-pr\/issues\/42\/comments/);
                return [structuredClone(comments)];
            }
            assert.equal(file, "winget");
            return args[0] === "source" ? "" : "Found azd [Microsoft.Azd]\nVersion\n-------\n1.2.400";
        });
        service.githubAccess = async () => ({ login });
        service.listIssues = async () => [{ number: 42, title: "[azd-release] 1.2.3", state: tracking.state }];
        service.gh = async (path, method = "GET", data) => {
            if (path === `${PRIVATE_REPO}/issues/42`) return structuredClone(tracking);
            if (path === `${PRIVATE_REPO}/issues/42/comments` && method === "POST") {
                writes.push(data.body);
                const comment = { id: comments.length + 1, user: { login }, body: data.body };
                comments.push(comment);
                return comment;
            }
            if (path === "Azure/azure-dev/pulls/100") return {
                state: "closed", merged: true, base: { ref: "main", repo: { full_name: "Azure/azure-dev" } },
            };
            if (path.includes("homebrew-azd/contents/")) {
                const text = homebrewFixture(path.includes("/Casks/"));
                return { encoding: "base64", content: Buffer.from(text).toString("base64") };
            }
            if (path.includes("/releases/tags/")) return {
                tag_name: "azure-dev-cli_1.2.3", draft: false, prerelease: false,
                assets: releaseAssetNames("1.2.3").map((name) => ({ name, size: 42 })),
            };
            assert.fail(`Unexpected ${method} ${path}`);
        };
        service.azureAccess = async () => { azureReads++; return { definitionId: 9 }; };
        service.build = async () => ({ id: 200, sourceVersion: source, status: "inProgress", result: null });
        service.metadata = async () => ({ ...metadata });
        service.az = async () => ({ records: [
            { id: "stage", identifier: "PublishCLI", type: "Stage", state: "inProgress", name: "DO NOT STORE raw log / credential" },
            { id: "job", parentId: "stage", identifier: "Publish_Brew", type: "Job", state: "completed", result: "failed", log: { id: 7 } },
            { id: "task", parentId: "job", type: "Task", state: "completed", result: "failed", name: "DO NOT STORE signed URL" },
        ] });
        service.wingetPrs = async () => ({ expectedVersion: "1.2.400", prs: [{ number: 3, state: "closed", merged: true }] });
        service.versionPrs = async () => ({ prs: [{ number: 4, state: "open", merged: false }] });
        service.read = async (_url, method) => method === "HEAD" ? new Headers({ "content-length": "42" }) :
            `<entry><title>azd</title><m:properties><d:Version>1.2.400</d:Version><d:IsApproved>true</d:IsApproved>` +
            "<d:PackageValidationResultStatus>Passing</d:PackageValidationResultStatus>" +
            "<d:PackageTestResultStatus>Passing</d:PackageTestResultStatus><d:PackageSubmittedStatus>Ready</d:PackageSubmittedStatus></m:properties></entry>";
        return service;
    };
    return { tracking, comments, writes, make, login: (value) => { login = value; }, azureReads: () => azureReads };
}

test("initial and repeated saved reads use only GitHub, with no public PR, Azure, package or shared writes", async () => {
    const f = fixture();
    f.login("observer");
    const service = f.make();
    service.azureAccess = service.build = service.metadata = service.read = async () => assert.fail("No live reads");
    const gh = service.gh.bind(service);
    service.gh = async (path, ...rest) => {
        assert.equal(path, `${PRIVATE_REPO}/issues/42`);
        return gh(path, ...rest);
    };
    for (let i = 0; i < 2; i++) {
        const snapshot = await service.snapshot(42);
        assert.equal(snapshot.release.runId, 200);
        assert.equal(snapshot.isDriver, false);
        assert.equal(snapshot.pipeline, undefined);
        assert.deepEqual(snapshot.access.azure, { ready: false, skipped: true });
        assert.equal(snapshot.observations.length, 0);
    }
    assert.equal(f.writes.length, 0);
});

test("driver live refresh saves compact snapshots; another machine renders them without Azure or public re-fetches", async () => {
    const f = fixture();
    const driver = await f.make().refreshLive({ issueNumber: 42 });
    assert.equal(driver.sharing, "saved");
    assert.equal(driver.pipelineHealth, "failed");
    assert.equal(f.writes.length, 1);
    assert.match(f.writes[0], /azd-release-observation/);
    assert.doesNotMatch(f.writes[0], /DO NOT STORE|signed URL|logId|access|isDriver|queue-intent/);
    assert.equal(driver.observations.length, 5);
    f.login("observer");
    const service = f.make();
    service.azureAccess = service.read = async () => assert.fail("No Azure login or public checks required");
    const gh = service.gh.bind(service);
    service.gh = async (path, ...rest) => {
        assert.equal(path, `${PRIVATE_REPO}/issues/42`, "No changelog/follow-up PR re-fetch");
        return gh(path, ...rest);
    };
    const viewer = await service.snapshot(42);
    assert.deepEqual(viewer.pipeline, driver.pipeline);
    assert.equal(viewer.changelog.merged, true);
    assert.equal(viewer.followups.version.prs[0].number, 4);
    assert.equal(viewer.roadmap[0].status, "complete");
    assert.equal(viewer.pipelineHealth, "failed");
    assert.equal(viewer.isDriver, false);
    assert.equal(viewer.access.azure.ready, false);
    assert.ok(viewer.observations.every((entry) => entry.observedBy === "driver"));
    assert.equal(f.azureReads(), 1);
    assert.equal(f.writes.length, 1);
});

test("observer live refresh is local only, including when the issue author closes or abandons tracking", async () => {
    for (const mode of ["observer", "closed", "abandoned"]) {
        const f = fixture();
        if (mode === "observer") f.login("observer");
        if (mode === "closed") f.tracking.state = "closed";
        if (mode === "abandoned") f.comments.push({
            id: 2, user: { login: "driver" }, body: block("event", { schema: 1, type: "outcome", status: "partial" }),
        });
        const snapshot = await f.make().refreshLive({ issueNumber: 42 });
        assert.equal(snapshot.sharing, "not-shared");
        assert.equal(snapshot.observationSource, "local");
        assert.equal(snapshot.pipeline.id, 200);
        assert.ok(snapshot.observations.every((entry) => entry.local));
        assert.equal(f.writes.length, 0);
        assert.equal((await f.make().snapshot(42)).pipeline, undefined);
    }
});

test("failed live reads preserve prior observations, record separate failed-attempt times and never refresh package timestamps", async () => {
    const f = fixture();
    const service = f.make();
    const first = await service.refreshLive({ issueNumber: 42 });
    await new Promise((resolve) => setTimeout(resolve, 5));
    service.azureAccess = async () => { throw new ReleaseError("cli_failed", "Azure login unavailable."); };
    const second = await service.refreshLive({ issueNumber: 42 });
    assert.deepEqual(second.pipeline, first.pipeline);
    assert.equal(second.access.azure.ready, false);
    const prior = first.observations.find((entry) => entry.key === "run");
    const failed = second.observations.find((entry) => entry.key === "run");
    assert.equal(failed.observedAt, prior.observedAt);
    assert.ok(failed.lastAttempt.at > prior.lastAttempt.at);
    assert.equal(failed.lastAttempt.failed, true);
    assert.equal(failed.stale, true);
    assert.match(second.errors[0].message, /Azure login unavailable/);
    const observer = await f.make().snapshot(42);
    assert.equal(observer.observations.find((entry) => entry.key === "run").lastAttempt.failed, true);
    assert.equal(observer.pipeline.id, 200);
    assert.doesNotMatch(f.writes[1], /Azure login unavailable/);
});

test("independent follow-up failures retain their own last success without refreshing unrelated sections", async () => {
    const f = fixture();
    const service = f.make();
    const first = await service.refreshLive({ issueNumber: 42 });
    service.versionPrs = async () => { throw new Error("private diagnostic"); };
    const second = await service.refreshLive({ issueNumber: 42 });
    const a = first.observations.find((entry) => entry.key === "version");
    const b = second.observations.find((entry) => entry.key === "version");
    assert.equal(b.observedAt, a.observedAt);
    assert.equal(b.lastAttempt.failed, true);
    assert.equal(second.followups.version.prs[0].number, 4);
    assert.doesNotMatch(JSON.stringify(second) + f.writes.join(""), /private diagnostic/);
});

test("a failed snapshot write is explicit, not claimed as shared or retried automatically", async () => {
    const f = fixture();
    const service = f.make();
    let attempts = 0;
    const gh = service.gh.bind(service);
    service.gh = async (path, method, data) => {
        if (method === "POST") { attempts++; throw new ReleaseError("cli_failed", "Snapshot write failed."); }
        return gh(path, method, data);
    };
    await assert.rejects(service.refreshLive({ issueNumber: 42 }), /Snapshot write failed/);
    assert.equal(attempts, 1);
    assert.equal((await f.make().snapshot(42)).pipeline, undefined);
});

test("private access loss, account switches and tracking changes during live reads cannot save or return evidence", async () => {
    for (const scenario of ["revoked", "switched", "version", "run", "closed"]) {
        const f = fixture();
        const service = f.make();
        const read = service.metadata;
        service.metadata = async () => {
            if (scenario === "revoked") service.githubAccess = async () => { throw new Error("revoked"); };
            if (scenario === "switched") f.login("observer");
            if (scenario === "version") f.tracking.body = block("state", { schema: 1, version: "1.2.4", changelogPr: 100 });
            if (scenario === "run") f.comments.push({
                id: 2, user: { login: "driver" }, body: block("event", { schema: 1, type: "run", runId: 201 }),
            });
            if (scenario === "closed") f.tracking.state = "closed";
            return read();
        };
        await assert.rejects(service.refreshLive({ issueNumber: 42 }),
            scenario === "revoked" ? /requires access/ : scenario === "switched" ? /identity changed/ : /Tracking changed/);
        assert.equal(f.writes.length, 0, scenario);
    }
});

test("driver package checks share sanitized gates and run metadata; observers' package checks never write", async () => {
    const f = fixture();
    const service = f.make();
    const result = await service.checkPackages({ issueNumber: 42 });
    assert.equal(result.ready, true);
    assert.equal(result.shared, true);
    assert.equal(f.writes.length, 1);
    assert.doesNotMatch(f.writes[0], /latest local catalog|Approved:|permissions|password|sig=/);
    const saved = await f.make().snapshot(42);
    assert.equal(saved.pipeline.metadata.msiVersion, "1.2.400");
    assert.equal(saved.packageReadiness.ready, true);
    assert.equal(saved.pipeline.timeline.length, 0, "Package checks do not invent or refresh timeline evidence");
    assert.equal(saved.observations.find((entry) => entry.key === "packages").observedAt, result.checkedAt);
    const at = saved.packageReadiness.checkedAt;
    await service.refreshLive({ issueNumber: 42 });
    assert.equal((await f.make().snapshot(42)).packageReadiness.checkedAt, at);
    f.login("observer");
    const before = f.writes.length;
    assert.equal((await f.make().checkPackages({ issueNumber: 42 })).shared, false);
    assert.equal(f.writes.length, before);
});

test("driver package-read failure invalidates shared success while retaining historical observation provenance", async () => {
    const f = fixture();
    const service = f.make();
    await service.checkPackages({ issueNumber: 42 });
    service.azureAccess = async () => { throw new ReleaseError("cli_failed", "Azure unavailable"); };
    await assert.rejects(service.checkPackages({ issueNumber: 42 }), /Azure unavailable/);
    const saved = await f.make().snapshot(42);
    assert.equal(saved.packageReadiness.ready, false);
    assert.equal(saved.packageReadiness.stale, true);
    assert.ok(saved.packageReadiness.gates.every((gate) => gate.status === "unknown"));
    assert.equal(saved.observations.find((entry) => entry.key === "packages").lastAttempt.failed, true);
});

test("local package provenance survives a local live read, but cannot cross a GitHub account switch", async () => {
    const f = fixture();
    f.login("observer");
    const service = f.make();
    const checked = await service.checkPackages({ issueNumber: 42 });
    const first = await service.refreshLive({ issueNumber: 42 });
    const receipt = first.observations.find((entry) => entry.key === "packages");
    assert.equal(receipt.observedAt, checked.checkedAt);
    assert.equal(receipt.observedBy, "observer");
    assert.equal(receipt.local, true);
    assert.equal(first.packageReadiness.ready, true);
    f.login("another-observer");
    const next = await service.refreshLive({ issueNumber: 42 });
    assert.equal(next.packageReadiness, undefined);
    assert.equal(next.observations.some((entry) => entry.key === "packages"), false);
    assert.equal(f.writes.length, 0);
});

test("package snapshot save failure is attempted once and cannot retain a local success", async () => {
    const f = fixture();
    const service = f.make();
    const gh = service.gh.bind(service);
    let attempts = 0;
    service.gh = async (path, method, data) => {
        if (method === "POST") { attempts++; throw new ReleaseError("cli_failed", "Snapshot not saved."); }
        return gh(path, method, data);
    };
    await assert.rejects(service.checkPackages({ issueNumber: 42 }), /Snapshot not saved/);
    assert.equal(attempts, 1, "Do not automatically write a second failure receipt after an ambiguous POST");
    assert.equal(service.packageChecks.size, 0);
});

test("shared package readiness expires after ten minutes and live pipeline reads cannot renew it", async () => {
    const f = fixture();
    const at = new Date(Date.now() - OBSERVATION_MAX_AGE - 1000).toISOString();
    const data = cached({ run: run(), packages: packageResult(at) }, at);
    f.comments.push(data.comment);
    const snapshot = await f.make().snapshot(42);
    assert.equal(snapshot.packageReadiness.ready, false);
    assert.equal(snapshot.packageReadiness.stale, true);
    assert.ok(snapshot.packageReadiness.gates.every((gate) => gate.status === "unknown"));
    assert.equal(snapshot.observations.find((entry) => entry.key === "packages").stale, true);
    await f.make().refreshLive({ issueNumber: 42 });
    const after = await f.make().snapshot(42);
    assert.equal(after.packageReadiness.checkedAt, at);
    assert.equal(after.packageReadiness.ready, false);
});

test("cached success never grants observers actions or substitutes for live analysis authentication", async () => {
    const f = fixture();
    await f.make().refreshLive({ issueNumber: 42 });
    f.login("observer");
    const service = f.make();
    service.azureAccess = async () => { throw new ReleaseError("cli_failed", "Fresh Azure access required"); };
    assert.equal((await service.snapshot(42)).pipeline.id, 200);
    await assert.rejects(service.prepareQueue(42), /read-only/);
    await assert.rejects(service.diagnosis(42), /Fresh Azure access required/);
    assert.equal(f.writes.length, 1);
});

test("analysis fetches fresh failure references and does not publish snapshots", async () => {
    const f = fixture();
    const service = f.make();
    const result = await service.diagnosis(42);
    assert.match(result.prompt, /logId":7/);
    assert.doesNotMatch(result.prompt, /DO NOT STORE/);
    assert.equal(f.writes.length, 0);
});

function cached(updates, at = new Date().toISOString()) {
    const tracking = issue();
    const release = releaseState(tracking, [runEvent()]);
    const comment = {
        id: 2, user: { login: "driver" },
        body: observationComment(release, Object.fromEntries(Object.entries(updates).map(([name, data]) =>
            [name, observationUpdate(release, name, data, at)]))),
    };
    return { tracking, release, comment };
}

test("snapshot projection discards credentials, signed URLs, logs, authored instructions and user-specific permissions", () => {
    const sensitive = "https://LEAK_USER:LEAK_PASSWORD@example.test/file?sig=LEAK_SIGNATURE#LEAK_FRAGMENT";
    const data = cached({
        run: { ...run(), token: sensitive, access: { azure: { ready: true } }, isDriver: true },
        timeline: { source, records: [
            { id: "stage", type: "Stage", name: sensitive, identifier: "PublishCLI", logId: 9, previousAttempts: [{ secret: sensitive }] },
        ] },
        winget: { source, expectedVersion: "1.2.400", prs: [{ number: 3, state: "closed", merged: true, url: sensitive, body: sensitive }] },
        packages: { ...packageResult(), gates: packageResult().gates.map((gate) => ({ ...gate, url: sensitive, detail: sensitive })) },
    });
    const sections = readObservations(data.tracking, [data.comment], data.release);
    const view = observationView(sections.sections, data.release);
    assert.doesNotMatch(JSON.stringify(view) + data.comment.body, /LEAK_|sig=|logId|previousAttempts|"access"|"isDriver"/);
    assert.equal(view.followups.winget.prs[0].url, "https://github.com/microsoft/winget-pkgs/pull/3");
});

test("unsupported, oversized, ambiguous and malformed driver snapshots warn but cannot erase the last good view", () => {
    const data = cached({ run: run() });
    for (const body of [
        block("observation", { schema: 99 }), `${data.comment.body}\n${data.comment.body}`,
        "<!-- azd-release-observation\nnot json\n-->", data.comment.body + " ".repeat(60_000),
    ]) {
        const parsed = readObservations(data.tracking, [data.comment, { ...data.comment, id: 3, body }], data.release);
        assert.equal(parsed.sections.run.data.id, 200);
        assert.equal(parsed.warnings.length, 1);
    }
});

test("observer-authored, wrong-release, wrong-PR and wrong-run snapshots cannot supply pipeline evidence", () => {
    const data = cached({ run: run() });
    const outsider = { ...data.comment, user: { login: "observer" } };
    assert.deepEqual(readObservations(data.tracking, [outsider], data.release).sections, {});
    for (const change of [{ issueNumber: 43 }, { version: "1.2.4" }, { changelogPr: 101 }, { runId: 201 }]) {
        const state = { schema: 1, ...data.release, ...change, sections: { run: observationUpdate(data.release, "run", run()) } };
        const comment = { ...data.comment, body: block("observation", state) };
        assert.deepEqual(readObservations(data.tracking, [comment], data.release).sections, {});
    }
});

test("slow older writes cannot replace newer section observations or newer failure markers", () => {
    const recent = new Date(Date.now() - 1000).toISOString();
    const older = new Date(Date.now() - 60_000).toISOString();
    const data = cached({ run: run() }, recent);
    const old = cached({ run: { ...run(), result: "succeeded", status: "completed" } }, older);
    const parsed = readObservations(data.tracking, [data.comment, { ...old.comment, id: 3 }], data.release);
    assert.equal(parsed.sections.run.data.result, null);
    const failed = mergeObservations(parsed.sections, { run: observationUpdate(data.release, "run", undefined, recent) }, "driver");
    const merged = mergeObservations(failed, { run: observationUpdate(data.release, "run", run(), older) }, "driver");
    assert.equal(merged.run.lastAttempt.failed, true);
});

test("source changes discard unrelated timeline and package observations; stale data never renews readiness", () => {
    const data = cached({ run: run(), timeline: { source, records: [] }, packages: packageResult() });
    const parsed = readObservations(data.tracking, [data.comment], data.release);
    const changed = mergeObservations(parsed.sections, {
        run: observationUpdate(data.release, "run", { ...run(), source: "b".repeat(40) }),
    }, "driver");
    const view = observationView(changed, data.release);
    assert.equal(view.packageReadiness, undefined);
    assert.deepEqual(view.pipeline.timeline, []);
    assert.ok(!view.observations.some((entry) => entry.key === "timeline"));
    const old = cached({ run: run() }, new Date(Date.now() - OBSERVATION_MAX_AGE - 1000).toISOString());
    const oldView = observationView(readObservations(old.tracking, [old.comment], old.release).sections, old.release);
    assert.equal(oldView.observations[0].stale, true);
});

test("observation timestamps, statuses, metadata and gate identities are validated", () => {
    const release = releaseState(issue(), [runEvent()]);
    for (const [name, value] of [
        ["run", { ...run(), status: "unknown-status" }],
        ["run", { ...run(), id: 201 }],
        ["run", { ...run(), metadata: { ...metadata, cliVersion: "1.2.4" } }],
        ["run", { ...run(), metadata: { ...metadata, submitChocoPackage: "true" } }],
        ["packages", { ...packageResult(), gates: [] }],
        ["packages", { ...packageResult(), msiVersion: "other" }],
    ]) assert.throws(() => observationUpdate(release, name, value));
    for (const at of ["yesterday", new Date(Date.now() + 120_000).toISOString()]) {
        assert.throws(() => observationUpdate(release, "run", run(), at), /timestamp/);
    }
});
