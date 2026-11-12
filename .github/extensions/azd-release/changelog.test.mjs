// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import test from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, rm, readdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { ChangelogWorkflow } from "./changelog.mjs";

async function fixture(t) {
    const workspacePath = await mkdtemp(join(tmpdir(), "azd-changelog-test-"));
    t.after(() => rm(workspacePath, { recursive: true, force: true }));
    const sent = [];
    const workflow = new ChangelogWorkflow({
        workspacePath, send: async (message) => { sent.push(message); },
    }, async () => []);
    return { workflow, sent, workspacePath };
}

test("current-session request retains skill approvals and does not authorize publishing", async (t) => {
    const { workflow, sent } = await fixture(t);
    await workflow.start({ releaseVersion: "1.37.0", mode: "current" });
    assert.equal(sent.length, 1);
    assert.equal(sent[0].mode, "immediate");
    assert.match(sent[0].prompt, /changelog-generation skill/);
    assert.match(sent[0].prompt, /review\/approval/);
    assert.match(sent[0].prompt, /THIS session/);
    assert.match(sent[0].prompt, /Do not merge/);
    assert.match(sent[0].prompt, /Release tracking already exists/);
    assert.match(sent[0].prompt, /azd-release-changelog:[a-f0-9-]{36}/);
    await assert.rejects(workflow.start({ releaseVersion: "1.37.0", mode: "current" }), /already requested/);
    assert.equal(sent.length, 1);
});

test("new-session selection explicitly requests an isolated app session and preserves approval steps", async (t) => {
    const { workflow, sent } = await fixture(t);
    await workflow.start({ releaseVersion: "1.37.0", mode: "new" });
    assert.match(sent[0].prompt, /create_session/);
    assert.match(sent[0].prompt, /base_branch unset/);
    assert.match(sent[0].prompt, /interactive mode/);
    assert.match(sent[0].prompt, /child session link/);
    assert.match(sent[0].prompt, /do not silently fall back/);
    assert.match(sent[0].prompt, /changelog-generation skill/);
});

test("requests survive provider reload and discovery returns only their exact PR and version", async (t) => {
    const { workflow, workspacePath } = await fixture(t);
    await workflow.start({ releaseVersion: "1.37.0", mode: "new" });
    const directory = workflow.directory();
    const saved = JSON.parse(await readFile(join(directory, (await readdir(directory))[0]), "utf8"));
    const marker = `<!-- azd-release-changelog:${saved.id} -->`;
    const commands = [];
    const reloaded = new ChangelogWorkflow({ workspacePath }, async (_file, args) => {
        commands.push(args);
        if (args[0] === "pr") return [
            { number: 101, body: marker, baseRefName: "main", state: "CLOSED", headRefOid: "a".repeat(40) },
            { number: 102, body: marker, baseRefName: "other", state: "OPEN", headRefOid: "a".repeat(40) },
            { number: 103, body: "unrelated", baseRefName: "main", state: "OPEN", headRefOid: "a".repeat(40) },
            { number: 104, body: marker, baseRefName: "main", state: "OPEN", headRefOid: "a".repeat(40) },
        ];
        return "1.37.0";
    });
    const [status] = await reloaded.status();
    assert.equal(status.status, "ready");
    assert.equal(status.pr, 104);
    assert.equal(status.url, "https://github.com/Azure/azure-dev/pull/104");
    assert.ok(commands[0].includes(`"azd-release-changelog:${saved.id}" in:body`));
    await assert.rejects(reloaded.start({ releaseVersion: "1.37.0", mode: "new" }), /already requested/);
});

test("ambiguous or wrong-version PRs produce explicit errors without filling a PR number", async (t) => {
    const { workflow } = await fixture(t);
    await workflow.start({ releaseVersion: "1.37.0", mode: "current" });
    const saved = JSON.parse(await readFile(join(workflow.directory(), "1.37.0.json"), "utf8"));
    const pr = {
        number: 104, body: `<!-- azd-release-changelog:${saved.id} -->`,
        baseRefName: "main", state: "OPEN", headRefOid: "a".repeat(40),
    };
    workflow.run = async (_file, args) => args[0] === "pr" ? [pr, { ...pr, number: 105 }] : "1.38.0";
    let [status] = await workflow.status();
    assert.equal(status.status, "error");
    assert.match(status.message, /Multiple PRs/);
    assert.equal(status.pr, undefined);
    workflow.run = async (_file, args) => args[0] === "pr" ? [pr] : "1.38.0";
    [status] = await workflow.status();
    assert.equal(status.status, "error");
    assert.match(status.message, /differs/);
    assert.equal(status.pr, undefined);
});

test("invalid inputs and unavailable session storage never enqueue work", async (t) => {
    const { workflow, sent } = await fixture(t);
    await assert.rejects(workflow.start({ releaseVersion: "../escape", mode: "current" }), /CLI version/);
    await assert.rejects(workflow.start({ releaseVersion: "1.37.0", mode: "ignored" }), /Choose/);
    const unavailable = new ChangelogWorkflow({ send: () => { throw new Error("Must not send"); } });
    await assert.rejects(unavailable.start({ releaseVersion: "1.37.0", mode: "current" }), /storage is unavailable/);
    assert.equal(sent.length, 0);
});

test("uncertain delivery is persistent and never silently sends duplicate requests", async (t) => {
    const { workflow } = await fixture(t);
    workflow.session.send = async () => { throw new Error("timeout"); };
    await assert.rejects(workflow.start({ releaseVersion: "1.37.0", mode: "current" }), /delivery could not be confirmed/);
    await assert.rejects(workflow.start({ releaseVersion: "1.37.0", mode: "current" }), /already requested/);
});

test("version-scoped discovery does not show or fetch other releases' requests", async (t) => {
    const { workflow } = await fixture(t);
    await workflow.start({ releaseVersion: "1.37.0", mode: "new" });
    await workflow.start({ releaseVersion: "1.37.1", mode: "new" });
    let searches = 0;
    workflow.run = async () => { searches++; return []; };
    const requests = await workflow.status("1.37.1");
    assert.deepEqual(requests.map((request) => request.version), ["1.37.1"]);
    assert.equal(searches, 1);
    assert.deepEqual(await workflow.status("1.38.0"), []);
    assert.equal(searches, 1);
    await assert.rejects(workflow.status("../invalid"), /CLI version/);
});

test("saved views read local preparation requests without searching GitHub or revalidating public PRs", async (t) => {
    const { workflow } = await fixture(t);
    await workflow.start({ releaseVersion: "1.2.3", mode: "new" });
    workflow.run = async () => assert.fail("Saved views must not query public PR status");
    const requests = await workflow.status("1.2.3", { live: false });
    assert.equal(requests.length, 1);
    assert.equal(requests[0].status, "waiting");
    assert.match(requests[0].message, /Refresh live data/);
});
