// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import test from "node:test";
import assert from "node:assert/strict";
import { startServer } from "./server.mjs";
import { ReleaseService } from "./release-service.mjs";
import { ReleaseError } from "./cli.mjs";
import { ACCESS_REQUIRED } from "./model.mjs";

test("loopback server serves assets and isolates panels without leaking state to other origins", async (t) => {
    const calls = [];
    const service = {
        requireAccess: async () => ({ login: "driver" }),
        snapshot: async (issueNumber) => ({ issueNumber }),
        refreshLive: async (data) => { calls.push(data); return { release: { issueNumber: data.issueNumber }, sharing: "saved" }; },
        milestone: async (data) => { calls.push(data); return { recorded: true }; },
        diagnosis: async () => ({ prompt: "Read-only investigation" }),
        prepareQueue: async (issueNumber, recordMode) => ({
            issueNumber, parameters: { AzureRecordMode: recordMode },
        }),
        checkPackages: async (data) => {
            calls.push(data);
            return { ready: false, gates: [{ key: "choco", status: "blocked" }] };
        },
        create: async (data) => { calls.push(data); return { issueNumber: 44 }; },
        prepareChangelog: async (data) => { calls.push(data); return { requested: true }; },
        linkChangelog: async (data) => { calls.push(data); return { changelogPr: data.changelogPr }; },
    };
    const session = { send: async (data) => { calls.push(data); } };
    const panel = await startServer(service, session, 42);
    const second = await startServer(service, session, 43);
    t.after(async () => { await panel.close(); await second.close(); });
    assert.equal(panel.server.address().address, "127.0.0.1");
    const html = await fetch(panel.url);
    assert.equal(html.status, 200);
    assert.match(await html.text(), /Release control room/);
    assert.ok(html.headers.get("content-security-policy").includes("script-src 'self'"));
    assert.equal(html.headers.get("referrer-policy"), "no-referrer");
    assert.equal((await fetch(new URL("/api/state", panel.url))).status, 404);
    assert.equal((await fetch(new URL("api/state", panel.url), { headers: { Origin: "https://evil.example" } })).status, 400);
    assert.deepEqual(await (await fetch(new URL("api/state", panel.url))).json(), { issueNumber: 42 });
    assert.deepEqual(await (await fetch(new URL("api/state", second.url))).json(), { issueNumber: 43 });
    const token = new URL(panel.url).pathname.split("/")[1];
    const post = (action, data, headers = {}) => fetch(new URL(`api/${action}`, panel.url), {
        method: "POST", headers: { "Content-Type": "application/json", "X-Azd-Release-Token": token, ...headers },
        body: JSON.stringify(data),
    });

    assert.equal((await post("milestone", { issueNumber: 42 }, { "X-Azd-Release-Token": "wrong" })).status, 400);
    assert.equal((await post("milestone", { issueNumber: 42, unsupported: true })).status, 400);
    assert.equal(calls.length, 0);
    assert.equal((await post("milestone", { issueNumber: 42 })).status, 200);
    assert.equal((await post("analyze", { issueNumber: 42 })).status, 200);
    assert.deepEqual(calls.at(-1), { prompt: "Read-only investigation", mode: "immediate" });
    const preview = await post("prepare-queue", { issueNumber: 42, recordMode: "playback" });
    assert.equal(preview.status, 200);
    assert.deepEqual(await preview.json(), { issueNumber: 42, parameters: { AzureRecordMode: "playback" } });
    assert.equal((await post("queue", { issueNumber: 42, ticket: "preview", recordMode: "live" })).status, 400);
    assert.equal((await post("check-packages", { issueNumber: 42, expectedVersion: "1.37.0" })).status, 400);
    const catalog = await post("check-packages", { issueNumber: 42 });
    assert.equal(catalog.status, 200);
    assert.deepEqual(await catalog.json(), {
        ready: false, gates: [{ key: "choco", status: "blocked" }],
    });
    assert.deepEqual(calls.at(-1), { issueNumber: 42 });
    assert.equal((await post("refresh-live", { issueNumber: 42, share: false })).status, 400);
    const live = await post("refresh-live", { issueNumber: 42 });
    assert.equal(live.status, 200);
    assert.deepEqual(await live.json(), { release: { issueNumber: 42 }, sharing: "saved" });
    assert.deepEqual(calls.at(-1), { issueNumber: 42 });
    assert.equal((await post("create", { releaseVersion: "1.37.1", changelogPr: 100 })).status, 400);
    assert.equal((await post("prepare-changelog", { releaseVersion: "1.37.1", mode: "new" })).status, 400);
    assert.equal((await post("create", { releaseVersion: "1.37.1" })).status, 200);
    assert.deepEqual(calls.at(-1), { releaseVersion: "1.37.1" });
    assert.equal(panel.selection.issueNumber, 44);
    assert.equal((await post("prepare-changelog", { issueNumber: 44, mode: "new" })).status, 200);
    assert.deepEqual(calls.at(-1), { issueNumber: 44, mode: "new" });
    assert.equal((await post("link-changelog", { issueNumber: 44, changelogPr: 10661 })).status, 200);
    assert.deepEqual(calls.at(-1), { issueNumber: 44, changelogPr: 10661 });
    assert.equal((await post("select", { issueNumber: 99 })).status, 200);
    assert.equal(panel.selection.issueNumber, 99);
    assert.equal(second.selection.issueNumber, 43);
});

test("private-repository denial blocks every HTTP action without Azure, tracking reads, or agent delivery", async (t) => {
    const service = new ReleaseService(async () => assert.fail("No downstream CLI calls after access denial"));
    service.githubAccess = async () => { throw new ReleaseError("not_found", "No private repository access"); };
    service.load = async () => assert.fail("No tracking data reads after access denial");
    const panel = await startServer(service, { send: async () => assert.fail("No agent requests after access denial") }, 42);
    t.after(() => panel.close());
    const response = await fetch(new URL("api/state", panel.url));
    assert.equal(response.status, 200);
    const state = await response.json();
    assert.equal(state.restricted, true);
    assert.equal(state.release, null);
    assert.deepEqual(state.releases, []);
    assert.equal(state.access.azure.skipped, true);
    const token = new URL(panel.url).pathname.split("/")[1];
    const requests = [
        ["create", { releaseVersion: "1.37.1" }],
        ["select", { issueNumber: 99 }],
        ["prepare-changelog", { issueNumber: 42, mode: "new" }],
        ["link-changelog", { issueNumber: 42, changelogPr: 100 }],
        ["adopt", { issueNumber: 42, runId: 200 }],
        ["prepare-queue", { issueNumber: 42, recordMode: "live" }],
        ["queue", { issueNumber: 42, ticket: "preview", confirmation: "Queue azd 1.37.1" }],
        ["milestone", { issueNumber: 42, key: "build", status: "complete", evidence: "https://github.com/Azure/azure-dev/pull/100" }],
        ["complete", { issueNumber: 42, confirmation: "Complete azd 1.37.1" }],
        ["check-packages", { issueNumber: 42 }],
        ["refresh-live", { issueNumber: 42 }],
        ["analyze", { issueNumber: 42 }],
    ];
    for (const [action, data] of requests) {
        service.tickets.set("preview", { issueNumber: 42, expires: Date.now() + 60_000 });
        const result = await fetch(new URL(`api/${action}`, panel.url), {
            method: "POST",
            headers: { "Content-Type": "application/json", "X-Azd-Release-Token": token },
            body: JSON.stringify(data),
        });
        assert.equal(result.status, 403, action);
        assert.deepEqual(await result.json(), { error: ACCESS_REQUIRED, code: "access_required" });
    }
    assert.equal(panel.selection.issueNumber, 42);
});
