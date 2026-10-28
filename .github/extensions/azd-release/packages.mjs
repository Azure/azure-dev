// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { errorMessage, ReleaseError } from "./cli.mjs";
import { requireValue, version } from "./model.mjs";
import { readWingetCatalog } from "./winget.mjs";

export const PACKAGE_CHECK_MAX_AGE = 10 * 60_000;
const artifactsBase = "https://azuresdkartifacts.z5.web.core.windows.net/azd/standalone/release";
const packageVersion = /^\d+\.\d+\.\d+(?:\.\d+)?$/;

export function packageGates(cliVersion, msiVersion) {
    return [
        { key: "winget", name: "WinGet", expectedVersion: msiVersion,
            url: "https://github.com/microsoft/winget-pkgs/tree/master/manifests/m/Microsoft/Azd" },
        { key: "choco", name: "Chocolatey", expectedVersion: msiVersion,
            url: `https://community.chocolatey.org/packages/azd${msiVersion ? `/${msiVersion}` : ""}` },
        { key: "brew-cask", name: "Homebrew cask", expectedVersion: cliVersion,
            url: "https://github.com/Azure/homebrew-azd/blob/main/Casks/azd.rb" },
        { key: "brew-formula", name: "Homebrew formula", expectedVersion: cliVersion,
            url: "https://github.com/Azure/homebrew-azd/blob/main/Formula/azd.rb" },
        { key: "artifacts", name: "Standalone, MSI, DEB and RPM", expectedVersion: cliVersion,
            url: `https://github.com/Azure/azure-dev/releases${cliVersion ? `/tag/azure-dev-cli_${cliVersion}` : ""}` },
    ];
}

export async function publicPackageRead(url, method = "GET") {
    let response;
    try {
        response = await fetch(url, {
            method, redirect: "manual", signal: AbortSignal.timeout(20_000),
            headers: { Accept: "application/atom+xml" },
        });
        requireValue(response.ok, `Public package endpoint returned HTTP ${response.status}.`);
        if (method === "HEAD") return response.headers;
        const chunks = [];
        let size = 0;
        for await (const chunk of response.body) {
            size += chunk.length;
            requireValue(size <= 128_000, "Public package response exceeded the size limit.");
            chunks.push(chunk);
        }
        return Buffer.concat(chunks).toString("utf8");
    } catch (error) {
        if (error instanceof ReleaseError) throw error;
        throw new ReleaseError("package_read_failed",
            "Public package endpoint could not be read or timed out. Check service availability and retry the check.");
    }
}

export function chocolateyReadiness(xml, expectedVersion) {
    requireValue(typeof xml === "string" && !/<!DOCTYPE|<!ENTITY/i.test(xml),
        "Chocolatey returned unsupported package metadata.");
    const properties = [...xml.matchAll(/<m:properties(?:\s[^>]*)?>([\s\S]*?)<\/m:properties>/g)];
    requireValue(properties.length === 1, "Chocolatey returned missing or ambiguous package metadata.");
    const value = (name) => {
        const matches = [...properties[0][1].matchAll(
            new RegExp(`<d:${name}(?:\\s[^>]*)?>([^<]*)</d:${name}>`, "g"))];
        requireValue(matches.length === 1, `Chocolatey metadata is missing or has ambiguous ${name}.`);
        return matches[0][1].trim();
    };
    // NuGet's Atom representation carries package ID in the entry title, not d:Id.
    const titles = [...xml.matchAll(/<title(?:\s[^>]*)?>([^<]*)<\/title>/g)];
    requireValue(titles.length === 1 && titles[0][1] === "azd" && value("Version") === expectedVersion,
        "Chocolatey metadata does not identify the expected azd package version.");
    const approved = value("IsApproved");
    const validation = value("PackageValidationResultStatus");
    const verification = value("PackageTestResultStatus");
    const moderation = value("PackageSubmittedStatus");
    requireValue(["true", "false"].includes(approved) &&
        [validation, verification, moderation].every((text) => /^[A-Za-z ]{1,60}$/.test(text)),
    "Chocolatey returned invalid approval or moderation fields.");
    const ready = approved === "true" && validation === "Passing" && verification === "Passing";
    return {
        status: ready ? "ready" : "blocked",
        detail: `Approved: ${approved === "true" ? "yes" : "no"}; validation: ${validation}; ` +
            `verification: ${verification}; moderation: ${moderation}.` +
            (ready ? "" : " Coordinate unresolved verification or approval with the package-account owner / Chocolatey operators; the release driver may not control that account."),
    };
}

export function homebrewReadiness(content, cliVersion, cask) {
    requireValue(typeof content === "string" && content.length <= 128_000,
        "Homebrew definition could not be read.");
    const versions = [...content.matchAll(/^\s*version\s+"([^"]+)"\s*$/gm)];
    requireValue(versions.length === 1 && /^\d+\.\d+\.\d+$/.test(versions[0][1]),
        "Homebrew definition has an unsupported or ambiguous version.");
    const actual = versions[0][1];
    if (actual !== cliVersion) return { status: "blocked", detail: `Published version: ${actual}; expected: ${cliVersion}.` };
    const urls = [...content.matchAll(/^\s*url\s+"([^"]+)"\s*$/gm)].map((match) => match[1]);
    const prefix = "https://github.com/Azure/azure-dev/releases/download/azure-dev-cli_";
    const expected = cask
        ? [`${prefix}#{version}/azd-darwin-#{arch}.zip`, `${prefix}#{version}/azd-linux-#{arch}.tar.gz`]
        : ["darwin-amd64.zip", "darwin-arm64.zip", "linux-amd64.tar.gz", "linux-arm64.tar.gz"]
            .map((name) => `${prefix}${cliVersion}/azd-${name}`);
    // Recognize the tap's generated download stanza without evaluating Ruby.
    const lines = content.split(/\r?\n/).map((line) => line.trim())
        .filter((line) => line && !line.startsWith("#"));
    const downloads = cask ? [
        `version "${cliVersion}"`,
        /^sha256 arm: "[a-f0-9]{64}",$/,
        /^intel: "[a-f0-9]{64}",$/,
        /^arm64_linux: "[a-f0-9]{64}",$/,
        /^x86_64_linux: "[a-f0-9]{64}"$/,
        "on_macos do", `url "${expected[0]}"`, 'binary "azd-darwin-#{arch}", target: "azd"', "end",
        "on_linux do", `url "${expected[1]}"`, 'binary "azd-linux-#{arch}", target: "azd"', "end",
    ] : [
        "if OS.mac?",
        "if Hardware::CPU.intel?", `url "${expected[0]}"`, /^sha256 "[a-f0-9]{64}"$/,
        "elsif Hardware::CPU.arm?", `url "${expected[1]}"`, /^sha256 "[a-f0-9]{64}"$/, "end",
        "elsif OS.linux?",
        "if Hardware::CPU.intel?", `url "${expected[2]}"`, /^sha256 "[a-f0-9]{64}"$/,
        "elsif Hardware::CPU.arm?", `url "${expected[3]}"`, /^sha256 "[a-f0-9]{64}"$/, "end",
        "end", `version "${cliVersion}"`,
    ];
    const start = lines.indexOf(downloads[0]);
    const activeDownloads = start >= 0 && downloads.every((expectedLine, index) => {
        const line = lines[start + index];
        return typeof expectedLine === "string" ? line === expectedLine : expectedLine.test(line ?? "");
    });
    requireValue(urls.length === expected.length && expected.every((url) => urls.includes(url)) &&
        !lines.some((line) => /^=begin\b|^=end\b/.test(line)) && activeDownloads,
    "Homebrew definition does not contain the expected versioned downloads and architecture checksums. Inspect the tap.");
    return { status: "ready", detail: `Published definition references ${cliVersion} for macOS and Linux architectures.` };
}

export function releaseAssetNames(cliVersion) {
    return [
        "azd-darwin-amd64.zip", "azd-darwin-arm64.zip",
        "azd-linux-amd64.tar.gz", "azd-linux-arm64.tar.gz",
        "azd-windows-amd64.msi", "azd-windows-amd64.zip", "azd-windows-arm64-alpha.zip",
        `azd_${cliVersion}_amd64.deb`, `azd_${cliVersion}_arm64.deb`,
        `azd-${cliVersion}-1.x86_64.rpm`, `azd-${cliVersion}-1.aarch64.rpm`,
    ];
}

async function artifactReadiness(gh, read, cliVersion) {
    const tag = `azure-dev-cli_${cliVersion}`;
    const release = await gh(`Azure/azure-dev/releases/tags/${tag}`);
    requireValue(release.tag_name === tag && release.draft === false && release.prerelease === false &&
        Array.isArray(release.assets), "The expected public GA GitHub release could not be verified.");
    const assets = releaseAssetNames(cliVersion);
    const missing = assets.filter((name) => !release.assets.some((asset) => asset.name === name));
    if (missing.length) return { status: "blocked", detail: `Missing GitHub release assets: ${missing.join(", ")}.` };
    const failures = await Promise.all(assets.map(async (name) => {
        const matching = release.assets.filter((asset) => asset.name === name);
        requireValue(matching.length === 1 && Number.isSafeInteger(matching[0].size) && matching[0].size > 0,
            "GitHub release asset sizes or identities could not be verified.");
        try {
            const headers = await read(`${artifactsBase}/${cliVersion}/${name}`, "HEAD");
            return Number(headers.get("content-length")) === matching[0].size ? null : `${name}: size differs`;
        } catch (error) { return `${name}: ${errorMessage(error)}`; }
    }));
    const unavailable = failures.filter(Boolean);
    return unavailable.length ? { status: "unknown", detail: `Public artifact check incomplete: ${unavailable.join("; ")}.` }
        : { status: "ready", detail: `All ${assets.length} expected GitHub release assets exist; versioned public storage responds with matching sizes. Downloads were not installed or checksum-tested.` };
}

export async function readPackageReadiness({ run, gh, read = publicPackageRead, metadata }) {
    const checkedAt = new Date().toISOString();
    const cliVersion = version(metadata.cliVersion);
    const msiVersion = metadata.msiVersion;
    requireValue(/^\d+\.\d+\.\d+$/.test(cliVersion) && packageVersion.test(msiVersion),
        "Package readiness needs valid GA CLI and MSI/package versions.");
    const actions = [
        async () => {
            const catalog = await readWingetCatalog(run, msiVersion);
            return { status: catalog.available ? "ready" : "blocked",
                detail: `Expected: ${msiVersion}; latest local catalog: ${catalog.latestVersion}. ` +
                    (catalog.available ? "The exact version is listed." : "The exact version is not listed.") };
        },
        async () => chocolateyReadiness(await read(
            `https://community.chocolatey.org/api/v2/Packages(Id='azd',Version='${msiVersion}')`), msiVersion),
        ...[true, false].map((cask) => async () => {
            const file = await gh(`Azure/homebrew-azd/contents/${cask ? "Casks" : "Formula"}/azd.rb?ref=main`);
            requireValue(file.encoding === "base64" && typeof file.content === "string" && file.content.length <= 180_000,
                "Homebrew definition could not be read from the public tap.");
            return homebrewReadiness(Buffer.from(file.content, "base64").toString("utf8"), cliVersion, cask);
        }),
        () => artifactReadiness(gh, read, cliVersion),
    ];
    const gates = await Promise.all(packageGates(cliVersion, msiVersion).map(async (gate, index) => {
        try { return { ...gate, ...await actions[index]() }; }
        catch (error) { return { ...gate, status: "unknown", detail: errorMessage(error) }; }
    }));
    return summarizePackages({ cliVersion, msiVersion, checkedAt, gates });
}

export function summarizePackages(observation) {
    const readyCount = observation.gates.filter((gate) => gate.status === "ready").length;
    const ready = readyCount === observation.gates.length;
    return { ...observation, ready,
        status: ready ? "complete" : observation.gates.some((gate) => gate.status === "blocked") ? "failed" : "pending",
        summary: `${readyCount}/${observation.gates.length} package-readiness gates ready. ` +
            (ready ? "Review pipeline health and release policy separately before version.txt approval."
                : "Do not approve version.txt or advertise this version while any gate is blocked or unknown."),
    };
}

export function currentPackageReadiness(observation, pipeline, now = Date.now()) {
    if (!observation || !pipeline?.metadata?.available || pipeline.metadata.isPublishingGa !== true ||
        observation.runId !== pipeline.id ||
        observation.source !== pipeline.source || observation.cliVersion !== pipeline.metadata.cliVersion ||
        observation.msiVersion !== pipeline.metadata.msiVersion) return undefined;
    const age = now - Date.parse(observation.checkedAt);
    if (age >= 0 && age <= PACKAGE_CHECK_MAX_AGE) return observation;
    return summarizePackages({ ...observation, stale: true, gates: observation.gates.map((gate) =>
        ({ ...gate, status: "unknown", detail: "Previous check expired. Check package readiness again." })) });
}
