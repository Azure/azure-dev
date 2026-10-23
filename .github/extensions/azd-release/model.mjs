// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { ReleaseError } from "./cli.mjs";

export const PRIVATE_REPO = "Azure/azure-dev-pr";
export const ACCESS_REQUIRED = `This canvas requires access to the private ${PRIVATE_REPO} repository to create or follow azd core releases. Sign in with an authorized GitHub account and refresh.`;
export const PUBLIC_REPO = "Azure/azure-dev";
export const LABEL = "azd-release";
export const ORG = "https://dev.azure.com/azure-sdk";
export const PROJECT = "internal";
export const PIPELINE = "azure-dev - cli";
export const RUNBOOK = "https://github.com/Azure/azure-dev/pull/10566";
export const MILESTONES = [
    ["preparation", "Changelog and version preparation"],
    ["build", "Validation and build"],
    ["sign", "Signing and release artifacts"],
    ["publication", "GitHub, module tag, and storage publication"],
    ["channels", "Distribution channels"],
    ["followups", "Version increment and documentation PRs"],
    ["markers", "Final version markers"],
];

export function requireValue(condition, message, code = "validation") {
    if (!condition) throw new ReleaseError(code, message);
}

export function positiveInt(value, name) {
    requireValue(Number.isSafeInteger(value) && value > 0, `${name} must be a positive integer.`);
    return value;
}

export function version(value) {
    requireValue(typeof value === "string" && /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/.test(value) &&
        value.length <= 80, "Enter a CLI version such as 1.37.0 or 1.37.0-beta.1.");
    return value;
}

export function sha(value) {
    requireValue(typeof value === "string" && /^[a-f0-9]{40}$/i.test(value), "Expected a full source commit SHA.");
    return value;
}

export function cleanUrl(value) {
    try {
        const url = new URL(value);
        requireValue(url.protocol === "https:", "Evidence must use HTTPS.");
        url.username = "";
        url.password = "";
        url.search = "";
        url.hash = "";
        return url.href;
    } catch {
        throw new ReleaseError("validation", "Enter a valid HTTPS evidence URL.");
    }
}

export function evidenceUrl(value) {
    const result = cleanUrl(value);
    const url = new URL(result);
    requireValue(
        (url.hostname === "github.com" && /^\/(?:Azure|microsoft)\//i.test(url.pathname)) ||
        (url.hostname === "dev.azure.com" && url.pathname.startsWith("/azure-sdk/internal/")) ||
        url.hostname === "azuresdkartifacts.z5.web.core.windows.net" ||
        url.hostname === "community.chocolatey.org",
        "Use a GitHub, internal Azure DevOps, public artifact, or Chocolatey evidence URL.");
    if (url.hostname === "dev.azure.com" && url.pathname === "/azure-sdk/internal/_build/results") {
        const id = new URL(value).searchParams.get("buildId");
        requireValue(/^[1-9]\d*$/.test(id ?? ""), "Build evidence needs a numeric buildId.");
        return buildUrl(positiveInt(Number(id), "Build ID"));
    }
    return result;
}

export function buildUrl(id) {
    return `${ORG}/${PROJECT}/_build/results?buildId=${positiveInt(id, "Build ID")}`;
}

export function block(kind, value) {
    return `<!-- azd-release-${kind}\n${JSON.stringify(value)}\n-->`;
}

export function parseBlock(body, kind) {
    const marker = `<!-- azd-release-${kind}\n`;
    const start = body?.indexOf(marker) ?? -1;
    requireValue(start >= 0 && body.indexOf(marker, start + marker.length) < 0,
        `Missing or ambiguous release ${kind} block.`);
    const end = body.indexOf("\n-->", start);
    requireValue(end >= 0, `Unterminated release ${kind} block.`);
    try { return JSON.parse(body.slice(start + marker.length, end)); }
    catch { throw new ReleaseError("invalid_state", `Invalid JSON in release ${kind} block.`); }
}

export function releaseState(issue, comments) {
    requireValue(!issue.pull_request && issue.labels.some((l) => l.name === LABEL),
        "This is not a labeled release tracking issue.");
    const state = parseBlock(issue.body, "state");
    requireValue(state.schema === 1, "Unsupported release state schema.");
    version(state.version);
    if (state.changelogPr !== null) positiveInt(state.changelogPr, "Changelog PR");
    let changelogPr = state.changelogPr;
    const milestones = Object.fromEntries(MILESTONES.map(([key]) => [key, null]));
    let runId = null;
    let intent = null;
    let outcome = null;
    for (const comment of [...comments].sort((a, b) => a.id - b.id)) {
        if (comment.user.login.toLowerCase() !== issue.user.login.toLowerCase() ||
            !comment.body.includes("<!-- azd-release-event\n")) continue;
        const event = parseBlock(comment.body, "event");
        requireValue(event.schema === 1, "Unsupported release event schema.");
        if (event.type === "changelog") {
            const linked = positiveInt(event.changelogPr, "Changelog PR");
            requireValue(changelogPr === null || changelogPr === linked, "A different changelog PR is already linked.");
            changelogPr = linked;
        } else if (event.type === "queue-intent") {
            requireValue(typeof event.token === "string", "Invalid queue reservation.");
            intent ??= { ...event, commentId: comment.id };
        } else if (event.type === "run") {
            runId = positiveInt(event.runId, "Build ID");
        } else if (event.type === "milestone") {
            requireValue(Object.hasOwn(milestones, event.key) &&
                ["complete", "blocked", "not-applicable"].includes(event.status),
            "Invalid milestone event.");
            milestones[event.key] = { status: event.status, evidence: evidenceUrl(event.evidence) };
        } else if (event.type === "outcome") {
            requireValue(event.status === "partial", "Unsupported release outcome.");
            requireValue(event.resolution === undefined || event.resolution === "mitigated-errors",
                "Unsupported partial-release resolution.");
            const successorVersion = event.successorVersion === undefined ? null : version(event.successorVersion);
            requireValue(successorVersion !== state.version, "The successor must be a different release version.");
            requireValue(event.resolution !== "mitigated-errors" || successorVersion === null,
                "A mitigated release cannot also specify an abandonment successor.");
            outcome = { status: event.status, successorVersion };
            if (event.resolution) outcome.resolution = event.resolution;
        } else {
            throw new ReleaseError("invalid_state", "Unknown release event; update the extension before writing.");
        }
    }
    requireValue(changelogPr !== null || (!runId && !intent), "Publishing requires a linked changelog PR.");
    return {
        version: state.version, changelogPr, runId, intent, milestones, outcome,
        issueNumber: issue.number, title: `[azd-release] ${state.version}`, issueState: issue.state,
        closureReason: issue.state_reason ?? null,
        author: issue.user.login, url: `https://github.com/${PRIVATE_REPO}/issues/${issue.number}`,
    };
}

export function validateRun(build, definitionId) {
    let parameters = build.templateParameters;
    if (typeof parameters === "string") {
        try { parameters = JSON.parse(parameters); }
        catch { throw new ReleaseError("invalid_run", "Run template parameters are not valid JSON."); }
    }
    const repository = build.repository;
    const repositoryName = repository?.name ??
        (repository?.type?.toLowerCase() === "github" ? repository.id : undefined);
    requireValue(build.definition?.id === definitionId &&
        repositoryName?.toLowerCase() === PUBLIC_REPO.toLowerCase() &&
        build.sourceBranch === "refs/heads/main" && build.reason?.toLowerCase() === "manual" &&
        (parameters?.DoPublish === true || parameters?.DoPublish === "true"),
    "Run must be the core pipeline, Azure/azure-dev, main, manual, and DoPublish=true.");
    sha(build.sourceVersion);
}

export function timelineSummary(records = []) {
    return records.filter((r) => ["Stage", "Phase", "Job"].includes(r.type) ||
        r.type?.startsWith("Checkpoint") || r.result === "failed")
        .map((r) => ({
            id: r.id, parentId: r.parentId, identifier: r.identifier,
            name: String(r.name ?? "").replace(/https?:\/\/[^\s)]+/g, (url) => cleanUrl(url)),
            type: r.type, state: r.state, result: r.result ?? null,
            attempt: r.attempt ?? 1, logId: r.log?.id ?? null,
            previousAttempts: (r.previousAttempts ?? []).map((a) => ({
                attempt: a.attempt, timelineId: a.timelineId, recordId: a.recordId,
            })),
        }));
}

export function pipelineHealth(build, timeline) {
    if (!build) return "not linked";
    if (timeline.some((r) => r.result === "failed") || build.result === "failed") return "failed";
    if (build.result === "partiallySucceeded" || timeline.some((r) => r.result === "succeededWithIssues")) return "warnings";
    return build.result ?? build.status ?? "unknown";
}

export function publicationStatus(release) {
    if (release.outcome?.status === "partial") return release.outcome.resolution === "mitigated-errors"
        ? "partially completed; mitigated errors" : "partially published; abandoned";
    if (release.issueState === "closed") return release.closureReason === "completed"
        ? "driver recorded completion" : "closed without verified completion";
    const recorded = Object.values(release.milestones).filter((m) => m?.status === "complete").length;
    return `${recorded}/${MILESTONES.length} milestones verified by driver`;
}

export function nextStep(snapshot) {
    const release = snapshot.release;
    if (!release) return { stage: "tracking", message: "Create release tracking to follow the changelog and publishing run.", canQueue: false };
    const step = (stage, message, canQueue = false) => ({ stage, message, canQueue });
    if (release.outcome?.status === "partial") {
        if (release.outcome.resolution === "mitigated-errors") {
            return step("partial", "Tracking closed as partially completed with mitigated errors. " +
                "Pipeline health and unfinished steps remain unchanged. Read the private issue for manual recovery and pending follow-ups; " +
                "closure is not proof that those actions completed. No further release actions are available here.");
        }
        return step("partial", "This irregular release was abandoned with partial publication. Existing artifacts are retained; " +
            "do not retry this run or advance its remaining approvals." +
            (release.outcome.successorVersion ? ` The planned successor is azd ${release.outcome.successorVersion}, tracked as a new release.` : ""));
    }
    if (release.issueState === "closed") {
        return release.closureReason === "completed"
            ? step("completed", "The driver recorded completion. Live pipeline health remains separate from that attestation.")
            : step("closed", "Tracking was closed without verified completion. Read the private issue for the final disposition; closure is not proof of successful publication.");
    }
    if (release.runId) {
        return snapshot.pipelineHealth === "failed"
            ? step("investigation", "A pipeline failure needs investigation. Analyze it before considering any retry or recovery.")
            : step("packages", "Check every package-readiness gate before approving version.txt or announcing a new version. " +
                "Pipeline approvals and final publication verification remain separate.");
    }
    if (release.intent) {
        return step("reconcile", "Queue intent is reserved, but no run is linked. Inspect Azure DevOps and link the existing run. Do not queue again.");
    }
    if (!release.changelogPr) {
        const request = snapshot.changelogRequests?.find((request) => request.version === release.version);
        return step("changelog", !snapshot.isDriver
            ? `Tracking is ready. Waiting for @${release.author} to prepare or link the changelog PR.`
            : request?.status === "ready"
                ? `Changelog PR #${request.pr} is ready. Link this PR to the tracked release; do not prepare another changelog.`
                : request ? "Changelog preparation was already requested. Follow its conversation and any reported discovery errors, or link the existing PR."
                    : "Tracking is ready. Prepare the changelog with the agent, or link an existing changelog PR for this version.");
    }
    if (!snapshot.changelog) {
        return step("changelog-unknown", "No saved changelog PR observation is available. Select Refresh live data to check it before publishing.");
    }
    if (!snapshot.changelog.merged) {
        return step("preparation", snapshot.changelog.state === "closed"
            ? "The changelog PR was closed without merging. Resolve the changelog PR before publishing."
            : "Next: review and merge the changelog PR. Tracking is ready; publishing is not.");
    }
    if (!snapshot.isDriver) {
        return step("awaiting-driver", `Changelog PR merged. Waiting for @${release.author} to preview and queue publishing, or link an existing run.`);
    }
    if (!snapshot.access.azure.ready) {
        return step("azure-access", "The changelog was observed merged. Select Refresh live data with Azure DevOps access to preview publishing or link an existing run.");
    }
    return step("queue", "Changelog PR merged. Next: choose the test recording mode and preview the publishing pipeline. If a publishing run already exists, link it instead.", true);
}
