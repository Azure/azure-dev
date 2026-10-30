// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { block, parseBlock, requireValue, positiveInt, version, sha, buildUrl } from "./model.mjs";
import { packageGates, summarizePackages, PACKAGE_CHECK_MAX_AGE } from "./packages.mjs";

export const OBSERVATION_MAX_AGE = 10 * 60_000;
export const OBSERVATION_SECTIONS = {
    changelog: "Changelog PR",
    run: "Pipeline status and metadata",
    timeline: "Stages and approvals",
    winget: "WinGet PR",
    version: "Version increment PR",
    packages: "Package readiness",
};
const marker = "<!-- azd-release-observation\n";
const results = [null, "succeeded", "succeededWithIssues", "partiallySucceeded", "failed", "canceled", "skipped", "abandoned", "none"];
const runStates = ["notStarted", "inProgress", "completed", "cancelling", "postponed", "notSet", "none"];
const recordTypes = ["Stage", "Phase", "Job", "Task", "Checkpoint", "Checkpoint.Approval"];
const invalidMessage = "Some saved observations were invalid or unsupported and were ignored. Refresh live data to replace them.";

function timestamp(value) {
    requireValue(typeof value === "string" && Number.isFinite(Date.parse(value)) &&
        new Date(value).toISOString() === value && Date.parse(value) <= Date.now() + 60_000,
    "Invalid observation timestamp.");
    return value;
}

function choice(value, values) {
    requireValue(values.includes(value), "Invalid observation status.");
    return value;
}

function id(value) {
    requireValue(typeof value === "string" && /^[a-z0-9_.-]{1,120}$/i.test(value), "Invalid timeline identity.");
    return value;
}

function metadata(data, releaseVersion) {
    requireValue(typeof data?.available === "boolean", "Invalid observed release metadata.");
    if (!data.available) return { available: false, message: "Release metadata was not available at the last check." };
    requireValue(version(data.cliVersion) === releaseVersion &&
        typeof data.msiVersion === "string" && /^\d+\.\d+\.\d+(?:\.\d+)?$/.test(data.msiVersion),
    "Observed metadata disagrees with the release.");
    const result = { available: true, cliVersion: releaseVersion, msiVersion: data.msiVersion };
    for (const key of ["isPublishingGa", "submitWingetPackage", "submitChocoPackage", "submitBrewFormula"]) {
        requireValue(typeof data[key] === "boolean", "Invalid observed distribution flags.");
        result[key] = data[key];
    }
    return result;
}

function prs(data, repo) {
    requireValue(Array.isArray(data.prs) && data.prs.length <= 20, "Invalid observed PR list.");
    return data.prs.map((pr) => ({
        number: positiveInt(pr.number, "PR number"),
        state: choice(pr.state, ["open", "closed"]),
        merged: choice(pr.merged, [true, false]),
        url: `https://github.com/${repo}/pull/${pr.number}`,
    }));
}

// Reconstruct display fields; never retain authored text, URLs, credentials, logs or permissions.
function project(name, data, release) {
    requireValue(data && typeof data === "object" && !Array.isArray(data), "Invalid observation data.");
    if (name === "changelog") {
        requireValue(release.changelogPr, "An observed changelog needs a linked PR.");
        return { state: choice(data.state, ["open", "closed"]), merged: choice(data.merged, [true, false]) };
    }
    requireValue(release.runId, "Observed pipeline evidence needs a linked run.");
    const source = sha(data.source);
    if (name === "run") {
        requireValue(data.id === release.runId, "Observed run does not match tracking.");
        return {
            id: release.runId, url: buildUrl(release.runId), source,
            status: choice(data.status, runStates), result: choice(data.result ?? null, results),
            metadata: metadata(data.metadata, release.version),
        };
    }
    if (name === "timeline") {
        requireValue(Array.isArray(data.records) && data.records.length <= 500, "Observed timeline is too large.");
        return { source, records: data.records.filter((record) => recordTypes.includes(record.type)).map((record) => ({
            id: id(record.id), parentId: record.parentId == null ? null : id(record.parentId),
            identifier: record.identifier == null ? null : id(record.identifier),
            type: record.type, state: choice(record.state ?? null, [null, "pending", "inProgress", "completed"]),
            result: choice(record.result ?? null, results), attempt: positiveInt(record.attempt ?? 1, "Attempt"),
        })) };
    }
    if (name === "winget" || name === "version") {
        const result = { source, prs: prs(data, name === "winget" ? "microsoft/winget-pkgs" : "Azure/azure-dev") };
        if (name === "winget") {
            requireValue(typeof data.expectedVersion === "string" && /^\d+\.\d+\.\d+(?:\.\d+)?$/.test(data.expectedVersion),
                "Invalid observed WinGet version.");
            result.expectedVersion = data.expectedVersion;
        }
        return result;
    }
    requireValue(name === "packages" && data.runId === release.runId &&
        /^\d+\.\d+\.\d+$/.test(data.cliVersion) && data.cliVersion === release.version && typeof data.msiVersion === "string" &&
        /^\d+\.\d+\.\d+(?:\.\d+)?$/.test(data.msiVersion), "Invalid observed package identity.");
    const expected = packageGates(release.version, data.msiVersion);
    requireValue(Array.isArray(data.gates) && data.gates.length === expected.length &&
        new Set(data.gates.map((gate) => gate.key)).size === expected.length &&
        expected.every((gate) => data.gates.some((item) => item.key === gate.key)), "Invalid observed package gates.");
    return summarizePackages({
        cliVersion: release.version, msiVersion: data.msiVersion, runId: release.runId, source,
        checkedAt: timestamp(data.checkedAt),
        gates: expected.map((gate) => {
            const status = choice(data.gates.find((item) => item.key === gate.key).status, ["ready", "blocked", "unknown"]);
            return { ...gate, status,
                detail: `Last observed: ${status}. Run a new package check for current, detailed diagnostics.` };
        }),
    });
}

export function observationUpdate(release, name, data, at = new Date().toISOString()) {
    requireValue(Object.hasOwn(OBSERVATION_SECTIONS, name), "Unknown observation section.");
    return { at: timestamp(at), ...(data === undefined ? { failed: true } : { data: project(name, data, release) }) };
}

export function mergeObservations(previous, updates, author) {
    const sections = structuredClone(previous);
    for (const [name, update] of Object.entries(updates)) {
        const prior = sections[name];
        if (prior && Date.parse(prior.lastAttempt.at) > Date.parse(update.at)) continue;
        sections[name] = {
            ...(prior ?? {}),
            ...(update.failed ? {} : { data: update.data, observedAt: update.at, observedBy: author }),
            lastAttempt: { at: update.at, by: author, failed: update.failed === true },
        };
    }
    return sections;
}

export function readObservations(issue, comments, release) {
    let sections = {};
    const warnings = [];
    for (const comment of [...comments].sort((a, b) => a.id - b.id)) {
        if (comment.user.login.toLowerCase() !== issue.user.login.toLowerCase() || !comment.body.includes(marker)) continue;
        try {
            requireValue(comment.body.length <= 60_000, "Observed comment is too large.");
            const saved = parseBlock(comment.body, "observation");
            requireValue(saved.schema === 1, "Unsupported observation schema.");
            if (saved.issueNumber !== issue.number || saved.version !== release.version ||
                saved.changelogPr !== release.changelogPr) continue;
            requireValue(saved.sections && typeof saved.sections === "object" && !Array.isArray(saved.sections) &&
                Object.keys(saved.sections).every((name) => Object.hasOwn(OBSERVATION_SECTIONS, name)),
            "Invalid observation sections.");
            const updates = {};
            for (const [name, update] of Object.entries(saved.sections)) {
                if (name !== "changelog" && saved.runId !== release.runId) continue;
                requireValue(update && (update.failed === true ? !Object.hasOwn(update, "data") :
                    !Object.hasOwn(update, "failed") && Object.hasOwn(update, "data")), "Invalid observation update.");
                updates[name] = observationUpdate(release, name, update.failed ? undefined : update.data, update.at);
            }
            sections = mergeObservations(sections, updates, comment.user.login);
        } catch { if (!warnings.length) warnings.push(invalidMessage); }
    }
    return { sections, warnings };
}

export function observationComment(release, updates) {
    const sections = Object.fromEntries(Object.entries(updates).map(([name, update]) =>
        [name, observationUpdate(release, name, update.failed ? undefined : update.data, update.at)]));
    const value = {
        schema: 1, issueNumber: release.issueNumber, version: release.version,
        changelogPr: release.changelogPr, runId: release.runId, sections,
    };
    const body = [
        "## Saved release observations",
        "Display-only snapshots for teammates. These are not milestone attestations, live authorization, or permission to publish.",
        ...Object.entries(sections).map(([name, update]) =>
            `- ${OBSERVATION_SECTIONS[name]}: ${update.failed ? "read failed; prior observation retained" : "observed"} at ${update.at}`),
        block("observation", value),
    ].join("\n\n");
    requireValue(body.length <= 60_000, "Observations exceed the GitHub comment limit; no snapshot was saved.");
    return body;
}

export function observationView(sections, release, now = Date.now()) {
    const result = { observations: [], observationSource: "shared" };
    const run = sections.run?.data;
    for (const [name, entry] of Object.entries(sections)) {
        if (name !== "changelog" && name !== "run" && entry.data?.source !== run?.source && entry.data) continue;
        result.observations.push({
            key: name, name: OBSERVATION_SECTIONS[name], observedAt: entry.observedAt ?? null,
            observedBy: entry.observedBy ?? null, lastAttempt: entry.lastAttempt,
            stale: !entry.data || entry.lastAttempt.failed ||
                now - Date.parse(entry.observedAt) > (name === "packages" ? PACKAGE_CHECK_MAX_AGE : OBSERVATION_MAX_AGE),
        });
        if (!entry.data) continue;
        if (name === "changelog") result.changelog = entry.data;
        if (name === "run") result.pipeline = { ...entry.data, timeline: [] };
        if (name === "winget" || name === "version") {
            result.followups ??= {};
            result.followups[name] = entry.data;
        }
        if (name === "packages") result.packageReadiness = entry.data;
    }
    if (result.pipeline && sections.timeline?.data?.source === run.source) {
        result.pipeline.timeline = sections.timeline.data.records;
    }
    if (result.packageReadiness && (!run?.metadata.available ||
        result.packageReadiness.msiVersion !== run.metadata.msiVersion)) delete result.packageReadiness;
    if (result.followups?.winget && result.followups.winget.expectedVersion !== run?.metadata.msiVersion) {
        delete result.followups.winget;
        result.observations = result.observations.filter((entry) => entry.key !== "winget");
    }
    if (result.packageReadiness && sections.packages.lastAttempt.failed) {
        result.packageReadiness = summarizePackages({
            ...result.packageReadiness, stale: true,
            gates: result.packageReadiness.gates.map((gate) => ({
                ...gate, status: "unknown", detail: "The last package check failed. Previous observations are historical, not readiness.",
            })),
        });
    }
    // The release identity and action permissions always come from freshly authenticated tracking.
    if (!release.runId) delete result.pipeline;
    return result;
}
