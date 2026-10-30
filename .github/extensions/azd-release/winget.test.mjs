// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import test from "node:test";
import assert from "node:assert/strict";
import { readWingetCatalog } from "./winget.mjs";
import { ReleaseError } from "./cli.mjs";

const output = (...versions) => `Found Azure Developer CLI [Microsoft.Azd]\r\nVersion\r\n---------\r\n${versions.join("\r\n")}`;

test("catalog check refreshes the explicit source before reading latest and expected package versions", async () => {
    const calls = [];
    const catalog = await readWingetCatalog(async (file, args, options) => {
        calls.push({ file, args, options });
        return args[0] === "source" ? "Updated" : output("1.37.100", "1.36.100", "0.5.0.3");
    }, "1.37.100");
    assert.deepEqual(calls, [
        { file: "winget", args: ["source", "update", "--name", "winget", "--disable-interactivity"], options: { text: true } },
        { file: "winget", args: ["show", "--id", "Microsoft.Azd", "--exact", "--versions", "--source", "winget", "--disable-interactivity"], options: { text: true } },
    ]);
    assert.equal(catalog.expectedVersion, "1.37.100");
    assert.equal(catalog.latestVersion, "1.37.100");
    assert.equal(catalog.available, true);
    assert.ok(Number.isFinite(Date.parse(catalog.checkedAt)));
});

test("newer, older, and localized catalog responses do not substitute another version for the expected one", async () => {
    for (const [response, latest, available] of [
        [output("1.36.100", "1.35.200"), "1.36.100", false],
        [output("1.38.100", "1.36.100"), "1.38.100", false],
        [output("1.38.100", "1.37.100"), "1.38.100", true],
        ["\x1b[32mGefunden Azure Developer CLI [Microsoft.Azd]\x1b[0m\nVersion\n---------\n1.37.100", "1.37.100", true],
    ]) {
        const catalog = await readWingetCatalog(async () => response, "1.37.100");
        assert.equal(catalog.latestVersion, latest);
        assert.equal(catalog.available, available);
    }
});

test("refresh failure stops the check instead of reading a stale catalog", async () => {
    let calls = 0;
    await assert.rejects(readWingetCatalog(async () => {
        calls++;
        throw new ReleaseError("cli_failed", "WinGet source update failed.");
    }, "1.37.100"), /source update failed/);
    assert.equal(calls, 1);
});

test("unreadable output fails explicitly without exposing raw command output", async () => {
    for (const response of [
        "", "Source agreements are required.",
        output(), output("unknown"), output("1.37.100\nhttps://user:secret@example.com/?sig=secret"),
        output("1.37.100").replace("Microsoft.Azd", "Unrelated.Package"),
    ]) {
        await assert.rejects(readWingetCatalog(async () => response, "1.37.100"), (error) => {
            assert.match(error.message, /Could not/);
            assert.doesNotMatch(error.message, /secret|example\.com/);
            return true;
        });
    }
});

test("invalid expected versions are rejected before invoking any commands", async () => {
    for (const expected of [undefined, "", "1.37.0-beta.1", "1.37.100 --source other"]) {
        await assert.rejects(readWingetCatalog(async () => assert.fail("No command expected"), expected),
            /Invalid expected/);
    }
});
