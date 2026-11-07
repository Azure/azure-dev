// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { spawn } from "node:child_process";

export class ReleaseError extends Error {
    constructor(code, message) {
        super(message);
        this.code = code;
    }
}

export function errorMessage(error) {
    return error instanceof ReleaseError
        ? error.message
        : "The operation failed. Check service availability and release state before retrying the read.";
}

export async function cli(file, args, { input, text = false } = {}) {
    const azureOnWindows = process.platform === "win32" && file === "az";
    // Azure CLI is commonly az.cmd on Windows. Pass arguments as data, not shell source.
    const command = azureOnWindows ? "powershell.exe" : file;
    const argv = azureOnWindows ? [
        "-NoProfile", "-NonInteractive", "-Command",
        "$ErrorActionPreference='Stop'; $r=[Console]::In.ReadToEnd()|ConvertFrom-Json; " +
        "$a=@($r.args); & $r.file @a; exit $LASTEXITCODE",
    ] : args;
    return new Promise((resolve, reject) => {
        const child = spawn(command, argv, {
            shell: false, windowsHide: true,
            env: {
                ...process.env, GH_PROMPT_DISABLED: "1", GH_PAGER: "cat",
                ...(file === "gh" ? { GH_HOST: "github.com" } : {}),
                AZURE_EXTENSION_USE_DYNAMIC_INSTALL: "no", AZURE_CORE_ONLY_SHOW_ERRORS: "true",
            },
            stdio: ["pipe", "pipe", "pipe"],
        });
        let stdout = "";
        let stderr = "";
        let overflow = false;
        const timer = setTimeout(() => child.kill(), 90_000);
        child.stdout.on("data", (data) => {
            stdout += data;
            if (stdout.length > 8_000_000) { overflow = true; child.kill(); }
        });
        child.stderr.on("data", (data) => {
            if (stderr.length < 32_000) stderr += data;
        });
        child.on("error", () => {
            clearTimeout(timer);
            reject(new ReleaseError("cli_unavailable", `${file} could not start. Install it and reopen the app.`));
        });
        child.on("close", (code) => {
            clearTimeout(timer);
            if (code !== 0 || overflow) {
                const notFound = /\bHTTP 404\b|\bHTTP 404:/.test(stderr);
                reject(new ReleaseError(notFound ? "not_found" : "cli_failed",
                    `${file} ${args.slice(0, 2).join(" ")} failed${code === null ? " or timed out" : ` (exit ${code})`}. ` +
                    (file === "az"
                        ? "Check Azure CLI installation, az login, the azure-devops extension, and internal project access."
                        : file === "winget"
                            ? "Check WinGet installation, network access, and source agreements in a terminal. " +
                                "The canvas does not accept agreements automatically."
                            : "Check gh auth login, repository access, and your network connection.")));
                return;
            }
            try { resolve(text ? stdout.trim() : JSON.parse(stdout)); }
            catch { reject(new ReleaseError("invalid_response", `${file} returned an invalid JSON response.`)); }
        });
        child.stdin.on("error", () => { /* Spawn/close reports the command failure. */ });
        child.stdin.end(azureOnWindows ? JSON.stringify({ file, args }) : input);
    });
}
