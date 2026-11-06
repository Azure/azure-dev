// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { buildUrl } from "./model.mjs";
import { currentPackageReadiness, packageGates } from "./packages.mjs";

export function currentRecords(records) {
    const latest = new Map();
    for (const record of records) {
        const key = `${record.type}:${record.parentId ?? ""}:${record.identifier ?? record.id}`;
        const prior = latest.get(key);
        if (!prior || (record.attempt ?? 1) >= (prior.attempt ?? 1)) latest.set(key, record);
    }
    const selected = new Map([...latest.values()].map((record) => [record.id, record]));
    const isCurrent = (record, seen = new Set()) => {
        if (!record.parentId) return true;
        if (seen.has(record.id)) return false;
        seen.add(record.id);
        const parent = selected.get(record.parentId);
        if (!parent || !isCurrent(parent, seen)) return false;
        // A retried stage must not inherit earlier approvals or job progress.
        return !["Phase", "Job", "Checkpoint", "Checkpoint.Approval"].includes(record.type) ||
            (record.attempt ?? 1) >= (parent.attempt ?? 1);
    };
    return [...selected.values()].filter((record) => isCurrent(record));
}

export function releaseRoadmap(snapshot) {
    const { release, changelog, pipeline, followups = {} } = snapshot;
    const records = currentRecords(pipeline?.timeline ?? []);
    const byId = new Map(records.map((record) => [record.id, record]));
    const stageOf = (record) => {
        while (record && record.type !== "Stage") record = byId.get(record.parentId);
        return record?.identifier;
    };
    const stageRecords = (ids) => records.filter((record) => ids.includes(stageOf(record)));
    const failed = (items) => items.some((record) => ["failed", "canceled", "abandoned"].includes(record.result));
    const successful = (record) => ["succeeded", "succeededWithIssues"].includes(record.result);
    const started = (record) => record.state === "inProgress" ||
        (record.state === "completed" && successful(record));
    const waiting = (items) => pipeline?.status === "inProgress" && items.some((record) =>
        record.type === "Checkpoint.Approval" && !record.result &&
        ["inProgress", "pending"].includes(record.state));
    const publishStages = ["PublishCLI", "PublishInstallers"];
    const publish = stageRecords(publishStages);
    const finalGate = stageRecords(["PublishVersionTxt"]);
    const ci = records.filter((record) =>
        ![...publishStages, "PublishVersionTxt", "PublishIntegration"].includes(stageOf(record)));
    const progressed = (items) => items.some((record) =>
        (record.type === "Job" && started(record)) || (record.type === "Stage" && successful(record)));
    const finalReached = waiting(finalGate) || progressed(finalGate);
    const publishingStarted = progressed(publish);
    const ciReached = finalReached || publishingStarted || waiting(publish) ||
        publish.some((record) => record.type === "Checkpoint.Approval" && successful(record));
    const buildLink = release?.runId ? [{ label: "Open pipeline", url: buildUrl(release.runId) }] : [];
    const step = (key, title, status, detail, links = [], extra = {}) =>
        ({ key, title, status, detail, links, ...extra });
    const prStep = (key, title, evidence, missing, extra = {}) => {
        const prs = evidence?.prs ?? [];
        const merged = prs.filter((pr) => pr.merged);
        const open = prs.filter((pr) => pr.state === "open" && !pr.merged);
        const status = merged.length ? "complete" : prs.length && !open.length ? "failed" : "pending";
        const detail = evidence?.error ?? (merged.length ? "PR merged." : open.length
            ? "Review, approve, and merge the PR in GitHub." : prs.length
                ? "PR closed without merging. Resolve or replace it." : missing);
        return step(key, title, status, detail,
            prs.map((pr) => ({ label: `PR #${pr.number} (${pr.merged ? "merged" : pr.state})`, url: pr.url })), extra);
    };
    const readiness = currentPackageReadiness(snapshot.packageReadiness, pipeline);
    const packages = step("packages", "Package readiness", readiness?.status ?? "pending",
        readiness?.summary ?? "Check all distribution gates below before version.txt approval. A merged PR or successful package submission is not enough.",
        (followups.winget?.prs ?? []).map((pr) => ({
            label: `WinGet PR #${pr.number} (${pr.merged ? "merged" : pr.state})`, url: pr.url,
        })), {
            gates: readiness?.gates ?? packageGates(release?.version ?? "", pipeline?.metadata?.msiVersion),
        });
    const result = [
        step("changelog", "Changelog", changelog?.merged ? "complete" :
            changelog?.state === "closed" ? "failed" : "pending",
        changelog?.merged ? "Changelog PR merged." : changelog?.state === "closed"
            ? "PR closed without merging." : release?.changelogPr ? "Review and merge the changelog PR." :
                "Prepare or link the changelog PR from this tracked release.",
        release?.changelogPr ? [{ label: `PR #${release.changelogPr}`, url: `https://github.com/Azure/azure-dev/pull/${release.changelogPr}` }] : []),
        step("ci", "CI and signing", failed(ci) || (!ciReached && failed(publish)) ? "failed" :
            ciReached ? "complete" : "pending",
        waiting(publish) ? "CI reached the first publishing approval. Review it in Azure DevOps." :
            ciReached ? "Pipeline passed the first publishing approval." :
                pipeline ? "Waiting for validation, tests, signing, and the first publishing approval." :
                    "Waiting for verified pipeline evidence.", buildLink),
        step("publishing", "Publishing", failed(publish) || failed(finalGate) ? "failed" :
            finalReached ? "complete" : "pending",
        waiting(finalGate) ? "Release publishing reached the version.txt approval." :
            finalReached ? "Pipeline passed the version.txt approval." :
                "Waiting for release publishing to reach the final version.txt approval.", buildLink),
        packages,
        prStep("version", "Version increment", followups.version,
            release?.runId ? "Waiting for the pipeline's version-increment PR." : "Link a publishing run to find its version-increment PR."),
    ];
    if (release?.issueState !== "closed" && release?.outcome?.status !== "partial") {
        if (release?.runId && result[4].status !== "complete") {
            result[4].check = "Check version PR";
            result[4].action = "refresh-live";
        }
    }
    for (const item of result) {
        if (item.status === "failed" && ["ci", "publishing"].includes(item.key)) {
            item.detail = "A stage or approval failed or was canceled. Inspect the pipeline before continuing.";
        }
    }
    return result;
}
