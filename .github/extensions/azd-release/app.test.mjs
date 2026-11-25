// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { runInNewContext } from "node:vm";
import { releaseRoadmap } from "./roadmap.mjs";
import { KNOWN_ISSUES } from "./known-issues.mjs";
import { nextStep, ACCESS_REQUIRED } from "./model.mjs";
import { packageGates } from "./packages.mjs";

test("page loads once and refreshes on demand without timers or visibility listeners", async () => {
    const elements = new Map();
    const element = (id) => {
        if (!elements.has(id)) elements.set(id, { elements: { releaseVersion: {} }, replaceChildren() {}, setAttribute() {}, reset() {} });
        return elements.get(id);
    };
    let requests = 0;
    const source = await readFile(new URL("app.js", import.meta.url), "utf8");
    runInNewContext(source, {
        document: {
            getElementById: element,
            createElement: () => ({}),
            querySelectorAll: () => [],
            addEventListener: () => assert.fail("No automatic visibility refresh should be registered"),
        },
        location: { pathname: "/token/" },
        setInterval: () => assert.fail("No timed polling should be scheduled"),
        setTimeout: () => assert.fail("No automatic refresh should be scheduled"),
        fetch: async () => {
            requests++;
            return { ok: false, json: async () => ({ error: "Offline fixture" }) };
        },
    });

    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(requests, 1);
    await element("refresh").onclick();
    assert.equal(requests, 2);
    await element("check-changelog").onclick();
    assert.equal(requests, 2, "Inactive release controls must not read live services");
});

test("new release creates tracking first, then prepares or links its changelog inside that release", async () => {
        const html = await readFile(new URL("index.html", import.meta.url), "utf8");
        const createMarkup = html.match(/<form id="create"[\s\S]*?<\/form>/)[0];
        assert.equal((createMarkup.match(/<input /g) ?? []).length, 1);
        assert.match(createMarkup, /name="releaseVersion"/);
        assert.match(createMarkup, />Create release</);
        assert.doesNotMatch(createMarkup, /prepare-changelog|changelogPr|changelog-mode/);
        assert.match(html, /<div id="workspace" hidden>/);
        assert.ok(html.indexOf('id="prepare-changelog"') > html.indexOf('id="release"'));
        const elements = new Map();
        const makeElement = (tag) => ({
            tag, children: [], values: {}, hidden: true, value: "", button: {},
            replaceChildren(...children) { this.children = children; },
            append(...children) { this.children.push(...children); },
            querySelector() { return this.button; }, setAttribute() {},
            reset() { this.values = {}; },
            close() { this.open = false; },
        });
        const element = (id) => {
            if (!elements.has(id)) elements.set(id, makeElement());
            return elements.get(id);
        };
        const state = {
            access: { github: { ready: true, login: "driver" }, azure: { ready: true } },
            release: null, releases: [], errors: [], checkedAt: new Date().toISOString(), isDriver: false,
        };
        const update = () => {
            state.nextStep = nextStep(state);
            state.roadmap = releaseRoadmap(state);
        };
        update();
        const actions = [];
        runInNewContext(await readFile(new URL("app.js", import.meta.url), "utf8"), {
            document: { getElementById: element, createElement: makeElement, querySelectorAll: () => [] },
            location: { pathname: "/token/" },
            FormData: class { constructor(form) { return Object.entries(form.values); } },
            fetch: async (url, options) => {
                if (url === "api/state") return { ok: true, json: async () => state };
                const data = JSON.parse(options.body);
                actions.push({ url, data });
                if (url === "api/create") {
                    assert.deepEqual(data, { releaseVersion: "1.37.1" });
                    state.release = {
                        version: "1.37.1", changelogPr: null, issueNumber: 77,
                        author: "driver", issueState: "open", runId: null, milestones: {},
                    };
                    state.releases = [{ number: 77, title: "[azd-release] 1.37.1", state: "open" }];
                    state.isDriver = true;
                } else if (url === "api/prepare-changelog") {
                    assert.deepEqual(data, { issueNumber: 77, mode: "new" });
                    state.changelogRequests = [{ version: "1.37.1", mode: "new", status: "waiting" }];
                } else if (url === "api/link-changelog") {
                    assert.deepEqual(data, { issueNumber: 77, changelogPr: 10661 });
                    state.release.changelogPr = 10661;
                    state.changelog = { state: "open", merged: false };
                } else assert.fail(`Unexpected action ${url}`);
                update();
                return { ok: true, json: async () => ({ issueNumber: 77 }) };
            },
        });
        await new Promise((resolve) => setImmediate(resolve));
        element("new").onclick();
        assert.equal(element("create").hidden, false);
        element("create").values = { releaseVersion: "1.37.1" };
        await element("create").onsubmit({ preventDefault() {}, target: element("create") });
        assert.equal(element("create").hidden, true);
        assert.equal(element("title").textContent, "azd 1.37.1");
        assert.equal(element("changelog-setup").hidden, false);
        assert.equal(element("changelog-actions").hidden, false);
        assert.equal(element("prepare-changelog").hidden, false);
        assert.equal(element("check-changelog").hidden, true);
        assert.equal(element("queue-action").hidden, true);
        assert.equal(element("link-run").hidden, true);
        assert.ok(element("links").children.every((item) => !item.href?.includes("/pull/null")));
        assert.equal(actions.length, 1, "Creating tracking must not start preparation");
        element("changelog-mode").value = "new";
        await element("prepare-changelog").onclick();
        assert.equal(element("prepare-changelog").hidden, true, "No duplicate preparation while the existing request is pending");
        state.changelogRequests[0] = {
            version: "1.37.1", mode: "new", status: "ready", pr: 10661, url: "https://github.com/Azure/azure-dev/pull/10661",
        };
        await element("refresh").onclick();
        const use = element("changelog-requests").children[0].children.find((item) => item.tag === "button");
        assert.equal(use.textContent, "Link this PR");
        await use.onclick();
        assert.equal(element("changelog-setup").hidden, true);
        assert.equal(element("prepare-changelog").disabled, true);
        assert.equal(element("check-changelog").hidden, false);
        assert.equal(element("queue-action").hidden, true, "An unmerged linked PR cannot enable publishing");
        assert.ok(element("links").children.some((item) => item.href === "https://github.com/Azure/azure-dev/pull/10661"));
        assert.equal(actions.length, 3);
        state.release.changelogPr = null;
        state.changelog = null;
        state.changelogRequests = [{ version: "1.37.0", mode: "new", status: "ready", pr: 100 }];
        state.isDriver = false;
        update();
        await element("refresh").onclick();
        assert.equal(element("changelog-actions").hidden, true);
        assert.equal(element("prepare-changelog").disabled, true);
        assert.deepEqual(element("changelog-requests").children, []);
        state.release.issueState = "closed";
        update();
        await element("refresh").onclick();
        assert.equal(element("changelog-setup").hidden, true);
        element("queue-dialog").open = true;
        element("adopt-dialog").open = true;
        element("queue-confirm").values = { confirmation: "Queue azd 1.37.1" };
        element("queue-preview").textContent = "previous source metadata";
        state.access = {
            github: { ready: false, message: ACCESS_REQUIRED },
            azure: { ready: false, skipped: true },
        };
        state.restricted = true;
        state.release = null;
        state.releases = [];
        await element("refresh").onclick();
        assert.equal(element("workspace").hidden, true);
        assert.equal(element("access-required").hidden, false);
        assert.equal(element("access-message").textContent, ACCESS_REQUIRED);
        for (const id of ["title", "role", "driver", "health", "tracking-status", "next", "queue-preview", "setup"]) {
            assert.equal(element(id).textContent, "", `${id} must not retain prior private state`);
        }
        for (const id of ["releases", "links", "roadmap", "changelog-requests", "known-issues", "observations"]) {
            assert.deepEqual(element(id).children, [], `${id} must not retain prior private state`);
        }
        assert.equal(element("queue-dialog").open, false);
        assert.equal(element("adopt-dialog").open, false);
        assert.equal(element("refresh-live").hidden, true);
        assert.deepEqual(element("queue-confirm").values, {});
        await element("create").onsubmit({ preventDefault() {}, target: element("create") });
        assert.equal(actions.length, 3, "A denied view must not dispatch actions from cached UI");
        state.access = { github: { ready: true, login: "driver" }, azure: { ready: true } };
        state.restricted = false;
        state.release = {
            version: "1.37.1", changelogPr: 10661, issueNumber: 77,
            author: "driver", issueState: "open", runId: null, milestones: {},
        };
        state.releases = [{ number: 77, title: "[azd-release] 1.37.1", state: "open" }];
        state.isDriver = true;
        update();
        await element("refresh").onclick();
        assert.equal(element("workspace").hidden, false);
        assert.equal(element("access-required").hidden, true);
        assert.equal(element("title").textContent, "azd 1.37.1");
        assert.equal(element("queue-dialog").open, false);
});

test("checks every package gate and updates only the readiness view, independently of PR merge and pipeline health", async () => {
        const elements = new Map();
        const makeElement = (tag) => ({
            tag, children: [], value: "", elements: { releaseVersion: {}, changelogPr: {} },
            replaceChildren(...children) { this.children = children; },
            append(...children) { this.children.push(...children); },
            querySelector: () => ({}), setAttribute() {},
        });
        const element = (id) => {
            if (!elements.has(id)) elements.set(id, makeElement());
            return elements.get(id);
        };
        const snapshot = {
            access: { github: { ready: true, login: "observer" }, azure: { ready: true } },
            releases: [], errors: [], milestones: [], knownIssues: KNOWN_ISSUES, isDriver: false,
            checkedAt: new Date().toISOString(), nextStep: { message: "Next step", canQueue: false },
            release: { version: "1.37.0", changelogPr: 100, issueNumber: 42, author: "driver", issueState: "open", runId: 200, milestones: {} },
            changelog: { merged: true },
            followups: { winget: { expectedVersion: "1.37.100", prs: [{ number: 123, state: "open", merged: false, url: "https://github.com/microsoft/winget-pkgs/pull/123" }] } },
        };
        snapshot.roadmap = releaseRoadmap(snapshot);
        let reads = 0;
        let liveReads = 0;
        let catalogChecks = 0;
        let latestVersion = "1.36.100";
        runInNewContext(await readFile(new URL("app.js", import.meta.url), "utf8"), {
            document: { getElementById: element, createElement: makeElement, querySelectorAll: () => [] },
            location: { pathname: "/token/" },
            fetch: async (url, options) => {
                if (url === "api/refresh-live") {
                    assert.deepEqual(JSON.parse(options.body), { issueNumber: 42 });
                    liveReads++;
                    return { ok: true, json: async () => ({ ...snapshot, sharing: "not-shared", observationSource: "local" }) };
                }
                if (url === "api/check-packages") {
                    assert.equal(options.method, "POST");
                    assert.equal(options.headers["X-Azd-Release-Token"], "token");
                    assert.deepEqual(JSON.parse(options.body), { issueNumber: 42 });
                    catalogChecks++;
                    const ready = latestVersion === "1.37.100";
                    const result = {
                        cliVersion: "1.37.0", msiVersion: "1.37.100", runId: 200, source: "source",
                        ready, status: ready ? "complete" : "failed", summary: ready ? "5/5 ready." : "4/5 ready. Do not approve version.txt.",
                        checkedAt: new Date().toISOString(), gates: packageGates("1.37.0", "1.37.100").map((gate) => ({
                            ...gate, status: gate.key === "winget" && !ready ? "blocked" : "ready",
                            detail: gate.key === "winget" ? `Expected: 1.37.100; latest local catalog: ${latestVersion}.` : "Verified.",
                        })),
                    };
                    return { ok: true, json: async () => result };
                }
                assert.equal(url, "api/state");
                assert.equal(options.method, undefined);
                reads++;
                return { ok: true, json: async () => snapshot };
            },
            setInterval: () => assert.fail("No timed polling"),
        });
        await new Promise((resolve) => setImmediate(resolve));
        const stops = element("roadmap").children;
        assert.equal(stops.length, 5);
        assert.deepEqual(stops.map((stop) => stop.children.find((child) => child.tag === "h3").textContent),
            ["Changelog", "CI and signing", "Publishing", "Package readiness", "Version increment"]);
        assert.equal(stops[0].className, "roadmap-stop complete");
        assert.ok(element("links").children.some((child) => child.textContent === "Publishing build 200" &&
            child.href === "https://dev.azure.com/azure-sdk/internal/_build/results?buildId=200"));
        assert.equal(elements.has("timeline"), false);
        assert.equal(elements.has("pipeline-info"), false);
        assert.equal(element("analyze").hidden, true);
        assert.ok(stops[3].children.some((child) => child.href === snapshot.followups.winget.prs[0].url));
        assert.match(element("package-summary").textContent, /Not checked.*Do not approve/);
        assert.equal(element("package-gates").children.length, 5);
        const buttons = () => element("roadmap").children.flatMap((stop) =>
            stop.children.filter((child) => child.tag === "button"));
        assert.deepEqual(buttons().map((button) => button.textContent), ["Check version PR"]);
        assert.equal(element("check-packages").disabled, true);
        snapshot.pipeline = {
            status: "inProgress", source: "source",
            id: 200,
            metadata: { available: true, cliVersion: "1.37.0", msiVersion: "1.37.100", submitWingetPackage: true, isPublishingGa: true },
            timeline: [{
                id: "final", identifier: "PublishVersionTxt", type: "Stage", state: "completed",
                result: "succeeded", attempt: 1, previousAttempts: [],
            }],
        };
        snapshot.roadmap = releaseRoadmap(snapshot);
        await element("refresh").onclick();
        assert.deepEqual(buttons().map((button) => button.textContent), ["Check version PR"]);
        assert.equal(element("check-packages").disabled, false);
        snapshot.followups.winget.prs[0].merged = true;
        snapshot.roadmap = releaseRoadmap(snapshot);
        await element("refresh").onclick();
        assert.deepEqual(buttons().map((button) => button.textContent), ["Check version PR"]);
        assert.equal(catalogChecks, 0);
        const beforeCheckReads = reads;
        const releaseLinks = element("links").children;
        await element("check-packages").onclick();
        assert.equal(reads, beforeCheckReads, "Package checks must not refresh the full state");
        assert.equal(element("links").children, releaseLinks, "Unrelated UI stays in place");
        assert.equal(element("roadmap").children[3].className, "roadmap-stop failed");
        assert.ok(element("package-gates").children[0].children.some((child) =>
            /Expected: 1\.37\.100; latest local catalog: 1\.36\.100/.test(child.textContent)));
        latestVersion = "1.37.100";
        await element("check-packages").onclick();
        assert.equal(element("roadmap").children[3].className, "roadmap-stop complete");
        assert.deepEqual(buttons().map((button) => button.textContent), ["Check version PR"]);
        snapshot.followups.version = { prs: [{ number: 124, state: "closed", merged: true }] };
        snapshot.roadmap = releaseRoadmap(snapshot);
        await buttons()[0].onclick();
        assert.deepEqual(buttons(), []);
        assert.equal(reads, 3, "A live refresh must not immediately reload and discard its local result");
        assert.equal(liveReads, 1);
        assert.equal(catalogChecks, 2);
        for (const removed of ["controls", "evidence", "recorded-progress", "milestone-key", "complete-phrase", "product"]) {
            assert.equal(elements.has(removed), false, `${removed} should not be accessed`);
        }
        const issues = element("known-issues").children;
        assert.equal(issues.length, 2);
        for (const [index, issue] of KNOWN_ISSUES.entries()) {
            assert.ok(issues[index].children.some((child) => child.href === issue.url));
            assert.ok(issues[index].children.some((child) => child.textContent === `Workaround: ${issue.workaround}`));
            assert.ok(issues[index].children.some((child) => child.textContent === issue.caution));
        }
        snapshot.pipeline.timeline[0].result = "failed";
        snapshot.errors = [{ source: "pipeline", message: "Pipeline identity unavailable." }];
        await element("refresh").onclick();
        assert.equal(element("analyze").hidden, false);
        assert.equal(element("analyze").disabled, false);
        assert.equal(element("analysis-note").hidden, false);
        assert.equal(element("release-errors").children[0].textContent, "pipeline: Pipeline identity unavailable.");
        const priorStatuses = releaseRoadmap(snapshot).map((stop) => stop.status);
        snapshot.release.outcome = { status: "partial", successorVersion: "1.37.1" };
        snapshot.release.issueState = "closed";
        snapshot.release.closureReason = "not_planned";
        snapshot.isDriver = false;
        snapshot.releases = [{ number: 42, title: "[azd-release] 1.37.0", state: "closed", closureReason: "not_planned" }];
        snapshot.roadmap = releaseRoadmap(snapshot);
        snapshot.nextStep = nextStep(snapshot);
        await element("refresh").onclick();
        assert.equal(element("role").textContent, "Irregular release");
        assert.equal(element("tracking-status").textContent, "Partially completed - abandoned");
        assert.match(element("releases").children[0].textContent, /partial \/ abandoned/);
        assert.match(element("next").textContent, /do not retry.*1\.37\.1/);
        assert.equal(element("queue-action").hidden, true);
        assert.equal(element("queue").disabled, true);
        assert.equal(element("link-run").hidden, true);
        assert.equal(element("check-changelog").hidden, true);
        assert.deepEqual(buttons(), []);
        assert.deepEqual(snapshot.roadmap.map((stop) => stop.status), priorStatuses);
        snapshot.release.outcome = { status: "partial", resolution: "mitigated-errors" };
        snapshot.release.closureReason = "completed";
        snapshot.nextStep = nextStep(snapshot);
        await element("refresh").onclick();
        assert.equal(element("tracking-status").textContent, "Partially completed - mitigated errors");
        assert.match(element("releases").children[0].textContent, /partial \/ mitigated errors/);
        assert.match(element("next").textContent, /pending follow-ups/);
        assert.equal(element("queue-action").hidden, true);
        assert.equal(element("check-packages").hidden, true);
        assert.deepEqual(snapshot.roadmap.map((stop) => stop.status), priorStatuses);
});

test("page omits the detailed pipeline section but retains failure analysis near the release summary", async () => {
    const html = await readFile(new URL("index.html", import.meta.url), "utf8");
    assert.doesNotMatch(html, /Live pipeline|id="timeline"|id="pipeline-info"/);
    assert.match(html, /id="analyze"[^>]*hidden disabled/);
    assert.doesNotMatch(html, /Independent public evidence|Driver actions|Driver-recorded verification|id="controls"|id="milestone"|id="complete"/);
    assert.match(html, /Known previous issues/);
    assert.match(html, /historical references, not a diagnosis/);
    assert.match(html, /<dialog id="adopt-dialog"/);
});

test("queue previews survive only a successful refresh for the same authorized driver and active release", async () => {
    const source = await readFile(new URL("app.js", import.meta.url), "utf8");
    for (const change of ["none", "denied", "network", "observer", "account", "issue", "version", "closed", "run", "intent", "partial"]) {
        const elements = new Map();
        const makeElement = () => ({
            children: [], elements: { releaseVersion: {} }, open: false,
            replaceChildren(...children) { this.children = children; },
            append(...children) { this.children.push(...children); },
            querySelector: () => ({}), setAttribute() {}, reset() {},
            showModal() { this.open = true; }, close() { this.open = false; },
        });
        const element = (id) => {
            if (!elements.has(id)) elements.set(id, makeElement());
            return elements.get(id);
        };
        const state = {
            access: { github: { ready: true, login: "driver" }, azure: { ready: true } },
            releases: [], errors: [], isDriver: true,
            release: { version: "1.2.3", changelogPr: 100, issueNumber: 42, author: "driver",
                issueState: "open", runId: null, milestones: {} },
            nextStep: { message: "Ready to preview", canQueue: true },
        };
        let reads = 0;
        const context = {
            document: { getElementById: element, createElement: makeElement, querySelectorAll: () => [] },
            location: { pathname: "/token/" },
            fetch: async (url) => {
                if (url === "api/prepare-queue") return { ok: true, json: async () => ({
                    version: "1.2.3", source: "a".repeat(40), ticket: "preview",
                    parameters: { AzureRecordMode: "live" },
                }) };
                assert.equal(url, "api/state");
                if (++reads === 2) {
                    if (change === "network") throw new Error("Offline");
                    if (change === "denied") {
                        state.access.github.ready = false;
                        state.restricted = true;
                        state.release = null;
                    }
                    if (change === "observer") state.isDriver = false;
                    if (change === "account") state.access.github.login = "another-driver";
                    if (change === "issue") state.release.issueNumber = 43;
                    if (change === "version") state.release.version = "1.2.4";
                    if (change === "closed") state.release.issueState = "closed";
                    if (change === "run") state.release.runId = 200;
                    if (change === "intent") state.release.intent = { token: "reserved" };
                    if (change === "partial") state.release.outcome = { status: "partial" };
                }
                return { ok: true, json: async () => structuredClone(state) };
            },
        };
        runInNewContext(source, context);
        await new Promise((resolve) => setImmediate(resolve));
        await element("queue").onclick();
        assert.equal(reads, 2);
        assert.equal(element("queue-dialog").open, change === "none", change);
        assert.equal(Boolean(runInNewContext("preview", context)), change === "none", change);
        if (["denied", "network"].includes(change)) {
            assert.equal(element("queue-preview").textContent, "", change);
            assert.equal(element("workspace").hidden, true, change);
        }
    }
});

test("a package recheck clears previous success immediately; failed reads and access loss cannot retain a green view", async () => {
    for (const denied of [false, true]) {
        const elements = new Map();
        const makeElement = (tag) => ({
            tag, children: [], elements: { releaseVersion: {} },
            replaceChildren(...children) { this.children = children; },
            append(...children) { this.children.push(...children); },
            querySelector: () => ({}), setAttribute() {}, reset() {}, close() { this.open = false; },
        });
        const element = (id) => {
            if (!elements.has(id)) elements.set(id, makeElement());
            return elements.get(id);
        };
        const state = {
            access: { github: { ready: true, login: "observer" }, azure: { ready: true } },
            release: { version: "1.2.3", runId: 200, issueNumber: 42, author: "driver", issueState: "open", milestones: {} },
            releases: [], errors: [], nextStep: { message: "Review readiness", canQueue: false },
            pipeline: { id: 200, source: "a".repeat(40), timeline: [],
                metadata: { available: true, isPublishingGa: true, cliVersion: "1.2.3", msiVersion: "1.2.400" } },
            packageReadiness: {
                cliVersion: "1.2.3", msiVersion: "1.2.400", runId: 200, source: "a".repeat(40),
                checkedAt: new Date().toISOString(), status: "complete", ready: true, summary: "5/5 ready.",
                gates: packageGates("1.2.3", "1.2.400").map((gate) => ({ ...gate, status: "ready" })),
            },
        };
        state.roadmap = releaseRoadmap(state);
        let complete;
        let stateReads = 0;
        runInNewContext(await readFile(new URL("app.js", import.meta.url), "utf8"), {
            document: { getElementById: element, createElement: makeElement, querySelectorAll: () => [] },
            location: { pathname: "/token/" },
            fetch: async (url) => {
                if (url === "api/state") { stateReads++; return { ok: true, json: async () => state }; }
                assert.equal(url, "api/check-packages");
                return new Promise((resolve) => { complete = resolve; });
            },
        });
        await new Promise((resolve) => setImmediate(resolve));
        assert.equal(element("roadmap").children[3].className, "roadmap-stop complete");
        const pending = element("check-packages").onclick();
        assert.equal(element("roadmap").children[3].className, "roadmap-stop pending");
        assert.match(element("package-summary").textContent, /Checking/);
        assert.equal(element("check-packages").disabled, true);
        complete({ ok: false, status: denied ? 403 : 400, json: async () => ({
            error: denied ? ACCESS_REQUIRED : "Pipeline could not be read.", code: denied ? "access_required" : "request_failed",
        }) });
        await pending;
        assert.equal(stateReads, 1, "No full canvas refresh after a package check");
        if (denied) {
            assert.equal(element("workspace").hidden, true);
            assert.deepEqual(element("package-gates").children, []);
            assert.equal(element("package-summary").textContent, "");
            assert.equal(element("check-packages").disabled, true);
        } else {
            assert.match(element("package-summary").textContent, /Not checked/);
            assert.equal(element("roadmap").children[3].className, "roadmap-stop pending");
            assert.ok(element("package-gates").children.every((card) => card.className.includes("unknown")));
            assert.equal(element("check-packages").disabled, false);
        }
    }
});

test("changelog check disappears after linking a build or completing tracking", async () => {
        const elements = new Map();
        const makeElement = () => ({
            value: "", elements: { releaseVersion: {}, changelogPr: {} },
            replaceChildren() {}, append() {}, querySelector: () => ({}), setAttribute() {},
            showModal() { this.open = true; }, close() { this.open = false; }, reset() {},
        });
        const element = (id) => {
            if (!elements.has(id)) elements.set(id, makeElement());
            return elements.get(id);
        };
        const snapshot = {
            access: { github: { ready: true, login: "driver" }, azure: { ready: true } },
            releases: [], errors: [], milestones: [], isDriver: true,
            checkedAt: new Date().toISOString(),
            nextStep: { message: "Next step", canQueue: false },
            release: {
                version: "1.37.0", changelogPr: 10629, issueNumber: 42,
                author: "driver", issueState: "open", runId: null, milestones: {},
            },
        };
        const source = await readFile(new URL("app.js", import.meta.url), "utf8");
        runInNewContext(source, {
            document: { getElementById: element, createElement: makeElement, querySelectorAll: () => [] },
            location: { pathname: "/token/" },
            fetch: async () => ({ ok: true, json: async () => snapshot }),
        });
        await new Promise((resolve) => setImmediate(resolve));
        assert.equal(element("check-changelog").hidden, false);
        assert.equal(element("link-run").hidden, false);
        element("link-run").onclick();
        assert.equal(element("adopt-dialog").open, true);
        element("adopt-cancel").onclick();
        assert.equal(element("adopt-dialog").open, false);
        snapshot.release.runId = 200;
        await element("refresh").onclick();
        assert.equal(element("check-changelog").hidden, true);
        assert.equal(element("link-run").hidden, true);
        snapshot.release.runId = null;
        await element("refresh").onclick();
        assert.equal(element("check-changelog").hidden, false);
        snapshot.release.issueState = "closed";
        await element("refresh").onclick();
        assert.equal(element("check-changelog").hidden, true);
});

test("refresh hides content behind a loading indicator until success or failure and prevents duplicate reads", async () => {
    const elements = new Map();
    const makeElement = () => ({
        children: [], attributes: {}, elements: { releaseVersion: {} },
        replaceChildren(...children) { this.children = children; },
        setAttribute(name, value) { this.attributes[name] = value; },
        reset() {},
    });
    const element = (id) => {
        if (!elements.has(id)) elements.set(id, makeElement());
        return elements.get(id);
    };
    let complete;
    let requests = 0;
    runInNewContext(await readFile(new URL("app.js", import.meta.url), "utf8"), {
        document: { getElementById: element, createElement: makeElement, querySelectorAll: () => [] },
        location: { pathname: "/token/" },
        fetch: () => {
            requests++;
            return new Promise((resolve) => { complete = resolve; });
        },
    });
    const assertLoading = (loading) => {
        assert.equal(element("content").hidden, loading);
        assert.equal(element("content").attributes["aria-busy"], String(loading));
        assert.equal(element("loading").hidden, !loading);
        assert.equal(element("refresh").disabled, loading);
    };
    assertLoading(true);
    await element("refresh").onclick();
    assert.equal(requests, 1);
    complete({ ok: true, json: async () => ({
        access: { github: { ready: true, login: "driver" }, azure: { ready: true } },
        errors: [], releases: [], release: null,
    }) });
    await new Promise((resolve) => setImmediate(resolve));
    assertLoading(false);
    const refresh = element("refresh").onclick();
    assertLoading(true);
    assert.equal(requests, 2);
    complete({ ok: false, json: async () => ({ error: "Network unavailable" }) });
    await refresh;
    assertLoading(false);
    assert.equal(element("notice").children[0].textContent, "Network unavailable");
    assert.match(element("updated").textContent, /Refresh failed/);
});

test("page keeps the header outside loading content and provides sticky styling and reduced-motion spinner support", async () => {
    const [html, css] = await Promise.all([
        readFile(new URL("index.html", import.meta.url), "utf8"),
        readFile(new URL("style.css", import.meta.url), "utf8"),
    ]);
    assert.match(html, /<header>[\s\S]*id="refresh"[\s\S]*<\/header>[\s\S]*id="loading"[\s\S]*<main id="content" hidden aria-busy="true">/);
    assert.match(html, /id="loading"[^>]*role="status"/);
    assert.match(css, /header\s*\{[^}]*position:\s*sticky;[^}]*top:\s*0;[^}]*z-index:/);
    assert.match(css, /\.spinner\s*\{[^}]*animation:\s*spin/);
    assert.match(css, /prefers-reduced-motion:\s*reduce/);
});

test("saved views work without Azure, label provenance, and use an explicit live action without discarding its result", async () => {
    for (const isDriver of [true, false]) {
        const elements = new Map();
        const makeElement = () => ({
            children: [], elements: { releaseVersion: {} },
            replaceChildren(...children) { this.children = children; }, append(...children) { this.children.push(...children); },
            querySelector: () => ({}), setAttribute() {}, reset() {},
        });
        const element = (id) => {
            if (!elements.has(id)) elements.set(id, makeElement());
            return elements.get(id);
        };
        const at = new Date(Date.now() - 11 * 60_000).toISOString();
        const state = {
            access: { github: { ready: true, login: isDriver ? "driver" : "observer" }, azure: { ready: false, skipped: true } },
            release: { issueNumber: 42, version: "1.2.3", changelogPr: 100, runId: 200, author: "driver", issueState: "open", milestones: {} },
            releases: [], errors: [], isDriver, checkedAt: new Date().toISOString(),
            pipeline: { id: 200, source: "a".repeat(40), timeline: [],
                metadata: { available: true, isPublishingGa: true, cliVersion: "1.2.3", msiVersion: "1.2.400" } },
            nextStep: { message: "Last observed progress", canQueue: false },
            observations: [{ key: "run", name: "Pipeline status and metadata", observedAt: at, observedBy: "driver",
                stale: true, lastAttempt: { at, failed: false } }],
        };
        state.roadmap = releaseRoadmap(state);
        let reads = 0;
        let liveReads = 0;
        runInNewContext(await readFile(new URL("app.js", import.meta.url), "utf8"), {
            document: { getElementById: element, createElement: makeElement, querySelectorAll: () => [] },
            location: { pathname: "/token/" },
            fetch: async (url, options) => {
                if (url === "api/state") {
                    reads++;
                    return { ok: true, json: async () => structuredClone(state) };
                }
                assert.equal(url, "api/refresh-live");
                assert.equal(options.method, "POST");
                assert.deepEqual(JSON.parse(options.body), { issueNumber: 42 });
                liveReads++;
                return { ok: true, json: async () => ({
                    ...structuredClone(state), access: { ...state.access, azure: { ready: true } },
                    sharing: isDriver ? "saved" : "not-shared", observationSource: isDriver ? "shared" : "local",
                    observations: [{ ...state.observations[0], stale: false, local: !isDriver }],
                }) };
            },
        });
        await new Promise((resolve) => setImmediate(resolve));
        assert.equal(reads, 1);
        assert.equal(liveReads, 0);
        assert.equal(element("refresh-live").disabled, false);
        assert.equal(element("refresh-live").textContent, isDriver ? "Refresh live data & share" : "Refresh live data (local)");
        assert.ok(element("access").children.some((badge) => badge.textContent === "Azure DevOps: not queried" && !/\bbad\b/.test(badge.className)));
        assert.match(element("setup").textContent, /do not require Azure login/);
        assert.match(element("observations").children[0].textContent, /by @driver.*shared, stale/);
        assert.match(element("updated").textContent, /Some observations are stale/);
        assert.equal(element("check-packages").disabled, true);
        await element("refresh-live").onclick();
        assert.equal(liveReads, 1);
        assert.equal(reads, 1);
        assert.equal(element("check-packages").disabled, false);
        assert.match(element("notice").children[0].textContent, isDriver ? /saved for teammates/ : /locally/);
        assert.match(element("observations").children[0].textContent, isDriver ? /shared/ : /local only/);
        await element("refresh").onclick();
        assert.equal(reads, 2);
        assert.equal(liveReads, 1);
        assert.equal(element("check-packages").disabled, true);
    }
});

test("failed live refresh never claims fresh verification; revoked access clears saved private evidence", async () => {
    for (const denied of [false, true]) {
        const elements = new Map();
        const makeElement = () => ({
            children: [], elements: { releaseVersion: {} },
            replaceChildren(...children) { this.children = children; }, append(...children) { this.children.push(...children); },
            querySelector: () => ({}), setAttribute() {}, reset() {}, close() {},
        });
        const element = (id) => {
            if (!elements.has(id)) elements.set(id, makeElement());
            return elements.get(id);
        };
        const at = new Date().toISOString();
        const state = {
            access: { github: { ready: true, login: "driver" }, azure: { ready: false, skipped: true } },
            release: { issueNumber: 42, version: "1.2.3", runId: 200, author: "driver", issueState: "open", milestones: {} },
            releases: [], errors: [], isDriver: true, checkedAt: at,
            nextStep: { message: "Saved progress", canQueue: false },
            observations: [{ key: "run", name: "Pipeline", observedAt: at, observedBy: "driver", stale: false }],
        };
        state.roadmap = releaseRoadmap(state);
        let reads = 0;
        runInNewContext(await readFile(new URL("app.js", import.meta.url), "utf8"), {
            document: { getElementById: element, createElement: makeElement, querySelectorAll: () => [] },
            location: { pathname: "/token/" },
            fetch: async (url) => {
                if (url === "api/state") { reads++; return { ok: true, json: async () => state }; }
                assert.equal(url, "api/refresh-live");
                return { ok: false, status: denied ? 403 : 400, json: async () => ({
                    error: denied ? ACCESS_REQUIRED : "Snapshot could not be shared.", code: denied ? "access_required" : "request_failed",
                }) };
            },
        });
        await new Promise((resolve) => setImmediate(resolve));
        await element("refresh-live").onclick();
        assert.equal(reads, 1, "Do not overwrite the failure with a successful saved-state reload");
        if (denied) {
            assert.equal(element("workspace").hidden, true);
            assert.equal(element("refresh-live").hidden, true);
            assert.deepEqual(element("observations").children, []);
            assert.equal(element("observation-note").textContent, "");
        } else {
            assert.match(element("updated").textContent, /previous view, not a fresh verification/);
            assert.match(element("observations").children[0].textContent, /stale/);
            assert.equal(element("title").textContent, "azd 1.2.3");
            assert.match(element("notice").children[0].textContent, /could not be shared/);
        }
    }
});
