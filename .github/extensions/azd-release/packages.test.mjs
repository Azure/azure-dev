// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import test from "node:test";
import assert from "node:assert/strict";
import {
    chocolateyReadiness, homebrewReadiness, releaseAssetNames, readPackageReadiness,
    currentPackageReadiness, PACKAGE_CHECK_MAX_AGE, publicPackageRead,
} from "./packages.mjs";
import { homebrewFixture as brew } from "./homebrew-fixture.mjs";

const cliVersion = "1.2.3";
const msiVersion = "1.2.400";
const metadata = { cliVersion, msiVersion };
const xml = (values = {}) => `<entry><title type="text">${values.Id ?? "azd"}</title><m:properties>${Object.entries({
    Version: msiVersion, IsApproved: "true", PackageValidationResultStatus: "Passing",
    PackageTestResultStatus: "Passing", PackageSubmittedStatus: "Ready", ...values,
}).map(([key, value]) => `<d:${key} m:type="Edm.String">${value}</d:${key}>`).join("")}</m:properties></entry>`;
function fixture() {
    const calls = [];
    return {
        calls, metadata,
        run: async (file, args) => {
            assert.equal(file, "winget");
            calls.push(args);
            return args[0] === "source" ? "" : `Found azd [Microsoft.Azd]\nVersion\n-------\n${msiVersion}`;
        },
        gh: async (path) => {
            calls.push(path);
            if (path.includes("/contents/")) return {
                encoding: "base64", content: Buffer.from(brew(path.includes("/Casks/"))).toString("base64"),
            };
            return { tag_name: `azure-dev-cli_${cliVersion}`, draft: false, prerelease: false,
                assets: releaseAssetNames(cliVersion).map((name) => ({ name, size: 42 })) };
        },
        read: async (url, method) => {
            calls.push([url, method]);
            return method === "HEAD" ? new Headers({ "content-length": "42" }) : xml();
        },
    };
}

test("Chocolatey requires exact identity, approval, passing validation and passing verification", () => {
    assert.equal(chocolateyReadiness(xml(), msiVersion).status, "ready");
    for (const values of [
        { IsApproved: "false" }, { PackageValidationResultStatus: "Failing" },
        { PackageTestResultStatus: "Failing", PackageSubmittedStatus: "Waiting" },
        { PackageTestResultStatus: "Pending" },
    ]) assert.equal(chocolateyReadiness(xml(values), msiVersion).status, "blocked");
    assert.match(chocolateyReadiness(xml({ PackageTestResultStatus: "Failing" }), msiVersion).detail, /account owner/);
    for (const input of [
        xml({ Version: "1.2.3" }), xml({ Id: "other" }), xml({ IsApproved: "TRUE" }),
        xml().replace(/<d:IsApproved[\s\S]*?<\/d:IsApproved>/, ""),
        xml().replace("</m:properties>", "<d:IsApproved>true</d:IsApproved></m:properties>"),
        `<!DOCTYPE entry>${xml()}`, xml() + xml(), "<html>Service unavailable</html>",
    ]) assert.throws(() => chocolateyReadiness(input, msiVersion));
});

test("Homebrew checks exact versions and all expected architecture download definitions without executing Ruby", () => {
    for (const cask of [true, false]) {
        assert.equal(homebrewReadiness(brew(cask), cliVersion, cask).status, "ready");
        assert.equal(homebrewReadiness(brew(cask, "1.2.2"), cliVersion, cask).status, "blocked");
        assert.equal(homebrewReadiness(brew(cask, "1.2.4"), cliVersion, cask).status, "blocked");
        assert.throws(() => homebrewReadiness(`${brew(cask)}\nversion "1.2.4"`, cliVersion, cask), /ambiguous/);
        const unsafe = brew(cask).replace("https://github.com/", "https://user:secret@example.test/?sig=secret#");
        assert.throws(() => homebrewReadiness(unsafe, cliVersion, cask), /expected versioned downloads/);
        try { homebrewReadiness(unsafe, cliVersion, cask); }
        catch (error) { assert.doesNotMatch(error.message, /user|secret|sig=|example\.test/); }
        assert.throws(() => homebrewReadiness(brew(cask).replace(/^sha256.*$/m, ""), cliVersion, cask));
    }
});

test("Homebrew requires active checksums in each expected platform and architecture branch", () => {
    for (const cask of [true, false]) {
        const content = brew(cask);
        const commented = content.replace(/^(sha256.*|(?:intel|arm64_linux|x86_64_linux):.*)$/gm, "# $1");
        assert.throws(() => homebrewReadiness(commented, cliVersion, cask), /architecture checksums/);
        const missing = content.replace(/^sha256.*$/m, "# checksum removed");
        assert.throws(() => homebrewReadiness(`${missing}\n# "${"a".repeat(64)}"`, cliVersion, cask));
        assert.throws(() => homebrewReadiness(`=begin\n${content}\n=end`, cliVersion, cask));
        assert.equal(homebrewReadiness(content.replace(/\n/g, "\n  # generated definition\n  "),
            cliVersion, cask).status, "ready");
    }
    for (const change of [
        (content) => content.replace("arm64_linux:", "arm:"),
        (content) => content.replace("x86_64_linux:", "other:"),
        (content) => content.replace("on_linux do", "on_macos do"),
    ]) assert.throws(() => homebrewReadiness(change(brew(true)), cliVersion, true));
    for (const change of [
        (content) => content.replace("elsif OS.linux?", "elsif OS.mac?"),
        (content) => content.replace("elsif Hardware::CPU.arm?", "elsif Hardware::CPU.intel?"),
        (content) => content.replace(`sha256 "${"a".repeat(64)}"`, `"${"a".repeat(64)}"`),
        (content) => content.replace(/darwin-(amd64|arm64)\.zip/g,
            (_match, arch) => `darwin-${arch === "amd64" ? "arm64" : "amd64"}.zip`),
    ]) assert.throws(() => homebrewReadiness(change(brew(false)), cliVersion, false));
});

test("all five gates must pass, with exact public artifact names and sizes and no installs or publishing", async () => {
    const f = fixture();
    const result = await readPackageReadiness(f);
    assert.equal(result.ready, true);
    assert.equal(result.status, "complete");
    assert.equal(result.gates.length, 5);
    assert.equal(f.calls.filter((call) => Array.isArray(call) && call[1] === "HEAD").length, 11);
    assert.ok(f.calls.some((call) => typeof call === "string" && call.includes("homebrew-azd/contents/Casks/azd.rb?ref=main")));
    assert.deepEqual(f.calls[0], ["source", "update", "--name", "winget", "--disable-interactivity"]);
    assert.match(result.summary, /5\/5.*separately/);
});

test("package failures are independent; missing tools, service failures, malformed responses and mismatched sizes fail closed", async () => {
    const f = fixture();
    f.run = async () => { throw new Error("https://user:password@host.test/?sig=token#secret"); };
    f.read = async (_url, method) => method === "HEAD" ? new Headers({ "content-length": "41" })
        : xml({ IsApproved: "false", PackageTestResultStatus: "Failing", PackageSubmittedStatus: "Waiting" });
    const result = await readPackageReadiness(f);
    assert.equal(result.ready, false);
    assert.deepEqual(result.gates.map((gate) => gate.status), ["unknown", "blocked", "ready", "ready", "unknown"]);
    assert.match(result.gates[4].detail, /size differs/);
    assert.doesNotMatch(JSON.stringify(result), /password|sig=token|#secret|host\.test/);
    assert.match(result.summary, /Do not approve/);
    f.read = async () => { throw new Error("HTTP 503 response with secret"); };
    const unavailable = await readPackageReadiness(f);
    assert.equal(unavailable.gates[1].status, "unknown");
    assert.doesNotMatch(JSON.stringify(unavailable), /secret/);
});

test("missing GitHub assets do not become ready from storage alone", async () => {
    const f = fixture();
    f.gh = async (path) => path.includes("/contents/")
        ? { encoding: "base64", content: Buffer.from(brew(path.includes("/Casks/"))).toString("base64") }
        : { tag_name: `azure-dev-cli_${cliVersion}`, draft: false, prerelease: false, assets: [] };
    const result = await readPackageReadiness(f);
    assert.equal(result.gates[4].status, "blocked");
    assert.match(result.gates[4].detail, /Missing GitHub release assets/);
    assert.equal(f.calls.filter((call) => Array.isArray(call) && call[1] === "HEAD").length, 0);
});

test("draft, prerelease, wrong-tag and ambiguous-asset releases cannot satisfy the artifact gate", async () => {
    for (const change of [
        (release) => { release.draft = true; },
        (release) => { release.prerelease = true; },
        (release) => { release.tag_name = "azure-dev-cli_1.2.4"; },
        (release) => { release.assets.push(release.assets[0]); },
        (release) => { release.assets[0].size = 0; },
    ]) {
        const f = fixture();
        const gh = f.gh;
        f.gh = async (path) => {
            const result = await gh(path);
            if (path.includes("/releases/")) change(result);
            return result;
        };
        const result = await readPackageReadiness(f);
        assert.equal(result.ready, false);
        assert.equal(result.gates[4].status, "unknown");
    }
});

test("observations are tied to run, source, both versions, and a ten-minute freshness limit", async () => {
    const observation = { ...await readPackageReadiness(fixture()), runId: 42, source: "a".repeat(40) };
    const pipeline = { id: 42, source: observation.source, metadata: { available: true, isPublishingGa: true, ...metadata } };
    const checked = Date.parse(observation.checkedAt);
    assert.equal(currentPackageReadiness(observation, pipeline, checked + PACKAGE_CHECK_MAX_AGE).ready, true);
    const expired = currentPackageReadiness(observation, pipeline, checked + PACKAGE_CHECK_MAX_AGE + 1);
    assert.equal(expired.ready, false);
    assert.equal(expired.stale, true);
    assert.ok(expired.gates.every((gate) => gate.status === "unknown"));
    assert.equal(currentPackageReadiness(observation, pipeline, checked - 1).ready, false);
    for (const changed of [
        { ...pipeline, id: 43 }, { ...pipeline, source: "b".repeat(40) },
        { ...pipeline, metadata: { ...pipeline.metadata, cliVersion: "1.2.4" } },
        { ...pipeline, metadata: { ...pipeline.metadata, msiVersion: "1.2.500" } },
        { ...pipeline, metadata: { ...pipeline.metadata, available: false } },
        { ...pipeline, metadata: { ...pipeline.metadata, isPublishingGa: false } },
    ]) assert.equal(currentPackageReadiness(observation, changed, checked), undefined);
});

test("public read rejects redirects, oversized XML and raw network diagnostics without disclosure", async (t) => {
    const original = globalThis.fetch;
    t.after(() => { globalThis.fetch = original; });
    globalThis.fetch = async (_url, options) => {
        assert.equal(options.redirect, "manual");
        return new Response("", { status: 302, headers: { location: "https://user:secret@host/?sig=secret" } });
    };
    await assert.rejects(publicPackageRead("https://community.chocolatey.org/"), /HTTP 302/);
    globalThis.fetch = async () => new Response("x".repeat(128_001));
    await assert.rejects(publicPackageRead("https://community.chocolatey.org/"), /size limit/);
    globalThis.fetch = async () => { throw new Error("https://user:secret@host/?sig=secret#fragment"); };
    await assert.rejects(publicPackageRead("https://community.chocolatey.org/"), (error) => {
        assert.doesNotMatch(error.message, /user|secret|sig=|fragment/);
        return true;
    });
});
