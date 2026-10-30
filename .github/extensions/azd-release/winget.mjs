// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { requireValue } from "./model.mjs";

const packageVersion = /^\d+\.\d+\.\d+(?:\.\d+)?$/;

export async function readWingetCatalog(run, expectedVersion) {
    requireValue(typeof expectedVersion === "string" && expectedVersion.length <= 40 &&
        packageVersion.test(expectedVersion), "Invalid expected WinGet package version.");
    await run("winget", ["source", "update", "--name", "winget", "--disable-interactivity"], { text: true });
    const output = await run("winget", [
        "show", "--id", "Microsoft.Azd", "--exact", "--versions", "--source", "winget", "--disable-interactivity",
    ], { text: true });
    const lines = output.replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, "").split(/\r?\n/).map((line) => line.trim());
    const separator = lines.findIndex((line) => /^-{3,}$/.test(line));
    requireValue(separator > 0 && lines.slice(0, separator).some((line) => line.includes("[Microsoft.Azd]")),
        "Could not identify Microsoft.Azd in the local WinGet catalog response. Check WinGet in a terminal.");
    const versions = lines.slice(separator + 1).filter(Boolean);
    requireValue(versions.length > 0 && versions.every((value) => packageVersion.test(value)),
        "Could not read the available WinGet package versions. Check WinGet in a terminal.");
    return {
        expectedVersion, latestVersion: versions[0], available: versions.includes(expectedVersion),
        checkedAt: new Date().toISOString(),
    };
}
