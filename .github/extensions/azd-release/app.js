// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

const $ = (id) => document.getElementById(id);
const token = location.pathname.split("/")[1];
let snapshot;
let preview;
let working = false;
let refreshing = false;

function node(tag, text, className) {
    const result = document.createElement(tag);
    if (text !== undefined) result.textContent = text;
    if (className) result.className = className;
    return result;
}

function link(text, url) {
    const result = node("a", text);
    result.href = url;
    result.target = "_blank";
    result.rel = "noreferrer";
    return result;
}

function message(text, error = false) {
    $("notice").replaceChildren(node("p", text, error ? "error" : "callout"));
}

function restrict(message, detail = "") {
    snapshot = undefined;
    preview = undefined;
    $("workspace").hidden = true;
    $("access-required").hidden = false;
    $("access-message").textContent = message;
    $("access-detail").textContent = detail;
    for (const id of ["releases", "links", "roadmap", "known-issues", "changelog-requests", "release-errors", "access", "notice"]) {
        $(id).replaceChildren();
    }
    for (const id of ["title", "role", "driver", "health", "tracking-status", "next", "updated", "setup",
        "queue-preview", "queue-phrase", "queue-mode-info", "adopt-error"]) $(id).textContent = "";
    for (const id of ["create", "release", "empty", "queue-action", "link-run", "changelog-setup"]) $(id).hidden = true;
    for (const id of ["queue-dialog", "adopt-dialog"]) if ($(id).open) $(id).close();
    for (const id of ["create", "link-changelog", "adopt", "queue-confirm"]) $(id).reset();
}

async function request(action, data) {
    const response = await fetch(`api/${action}`, data === undefined ? {} : {
        method: "POST", headers: { "Content-Type": "application/json", "X-Azd-Release-Token": token },
        body: JSON.stringify(data),
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error);
    return result;
}

async function refresh() {
    if (refreshing || working) return;
    refreshing = true;
    $("content").hidden = true;
    $("content").setAttribute("aria-busy", "true");
    $("loading").hidden = false;
    $("refresh").disabled = true;
    $("refresh").textContent = "Refreshing...";
    $("check-changelog").disabled = true;
    document.querySelectorAll("#roadmap button").forEach((button) => { button.disabled = true; });
    try {
        snapshot = await request("state");
        render();
    } catch (error) {
        restrict("Access could not be verified. This canvas requires access to the private Azure/azure-dev-pr repository to create or follow azd releases. Refresh to try again.");
        message(error.message, true);
        $("updated").textContent = "Refresh failed. Previously displayed evidence is stale.";
    } finally {
        refreshing = false;
        $("loading").hidden = true;
        $("content").hidden = false;
        $("content").setAttribute("aria-busy", "false");
        $("refresh").disabled = false;
        $("refresh").textContent = "Refresh";
        $("check-changelog").disabled = false;
        document.querySelectorAll("#roadmap button").forEach((button) => { button.disabled = false; });
    }
}

async function act(action, data) {
    if (working || refreshing || !snapshot?.access.github.ready || snapshot.restricted) return;
    working = true;
    document.querySelectorAll("button").forEach((button) => { button.disabled = true; });
    try {
        const result = await request(action, data);
        message(action === "analyze" ? "Read-only analysis requested in this conversation." :
            action === "check-winget" ? "Local WinGet catalog refreshed and checked." :
                action === "create" ? "Release tracking created. Prepare or link its changelog inside the release." : "Action completed.");
        return result;
    } catch (error) {
        message(error.message, true);
        if (action === "adopt") {
            $("adopt-error").textContent = error.message;
            $("adopt-error").hidden = false;
        }
    }
    finally {
        working = false;
        document.querySelectorAll("button").forEach((button) => { button.disabled = false; });
        await refresh();
    }
}

function render() {
    const s = snapshot;
    if (s.restricted || !s.access.github.ready) {
        restrict(s.access.github.message, s.access.github.detail);
        return;
    }
    $("workspace").hidden = false;
    $("access-required").hidden = true;
    $("access-message").textContent = "";
    $("access-detail").textContent = "";
    $("access").replaceChildren(...["github", "azure"].map((key) => {
        const access = s.access[key];
        return node("span", `${key === "github" ? "GitHub" : "Azure DevOps"}: ${access.ready ? "connected" : "action needed"}`,
            `badge ${access.ready ? "good" : "bad"}`);
    }));
    const accessErrors = Object.values(s.access).filter((a) => !a.ready).map((a) => a.message);
    $("setup").textContent = accessErrors.join(" ") || `Signed in as ${s.access.github.login}. Access is checked on every refresh.`;
    $("release-errors").replaceChildren(...s.errors.filter((error) => error.source !== "changelog preparation")
        .map((error) => node("p", `${error.source}: ${error.message}`, "error")));
    $("runbook").href = s.runbook;
    $("releases").replaceChildren(...s.releases.map((issue) => {
        const state = issue.number === s.release?.issueNumber && s.release.outcome?.status === "partial"
            ? "partial / abandoned" : issue.closureReason === "not_planned" ? "closed / not completed" : issue.state;
        const option = node("option", `#${issue.number} ${issue.title} (${state})`);
        option.value = issue.number;
        option.selected = issue.number === s.release?.issueNumber;
        return option;
    }));
    $("releases").disabled = !s.releases.length;
    $("new").disabled = !s.access.github.ready;
    $("changelog-setup").hidden = true;
    $("changelog-requests").replaceChildren();
    $("empty").hidden = !s.access.github.ready || Boolean(s.release);
    $("release").hidden = !s.release;
    if (!s.release) return;
    const r = s.release;
    const partial = r.outcome?.status === "partial";
    const needsChangelog = !r.changelogPr && !partial && r.issueState === "open";
    $("changelog-setup").hidden = !needsChangelog;
    $("changelog-actions").hidden = !s.isDriver;
    const requests = (s.changelogRequests ?? []).filter((request) => request.version === r.version);
    const preparationErrors = s.errors.filter((error) => error.source === "changelog preparation");
    $("prepare-changelog").hidden = requests.length > 0;
    $("prepare-changelog").disabled = !needsChangelog || !s.isDriver || preparationErrors.length > 0;
    $("changelog-mode-label").hidden = requests.length > 0;
    $("link-changelog").querySelector("button").disabled = !needsChangelog || !s.isDriver;
    if (needsChangelog) {
        for (const request of requests) {
            const row = node("p");
            row.append(node("span", `${request.version} (${request.mode === "new" ? "new isolated session" : "this session"}): `));
            if (request.status === "ready") {
                row.append(link(`PR #${request.pr}`, request.url));
                if (s.isDriver) {
                    const use = node("button", "Link this PR");
                    use.type = "button";
                    use.onclick = () => act("link-changelog", { issueNumber: r.issueNumber, changelogPr: request.pr });
                    row.append(" ", use);
                }
            } else {
                row.append(node("span", request.message ??
                    "Preparation already requested. Follow the agent conversation for progress and approvals, then refresh to discover its PR."));
            }
            $("changelog-requests").append(row);
        }
        for (const error of preparationErrors) $("changelog-requests").append(node("p", error.message, "error"));
    }
    $("check-changelog").hidden = !r.changelogPr || partial || Boolean(r.runId) || r.issueState === "closed";
    $("title").textContent = `azd ${r.version}`;
    $("role").textContent = partial ? "Irregular release" : r.issueState === "closed" ? "Closed tracking" :
        s.isDriver ? "Release driver" : "Read-only observer";
    $("driver").textContent = `Driven by @${r.author} | Private issue #${r.issueNumber}`;
    $("links").replaceChildren(link("Private tracking issue", r.url));
    if (r.changelogPr) $("links").append(
        link(`Changelog PR (${s.changelog?.merged ? "merged" : s.changelog?.state ?? "unknown"})`,
            `https://github.com/Azure/azure-dev/pull/${r.changelogPr}`));
    if (r.runId) $("links").append(link(`Publishing build ${r.runId}`,
        `https://dev.azure.com/azure-sdk/internal/_build/results?buildId=${r.runId}`));
    $("tracking-status").textContent = partial ? "Partially completed - abandoned" : r.issueState === "closed"
        ? r.closureReason === "completed" ? "Closed - completion recorded" : "Closed without verified completion"
        : "In progress";
    $("health").textContent = s.pipelineHealth;
    $("health").className = s.pipelineHealth === "failed" ? "bad" : "";
    $("next").textContent = s.nextStep.message;
    $("queue-action").hidden = !s.nextStep.canQueue;
    $("link-run").hidden = !r.changelogPr || !s.isDriver || !s.access.azure.ready || Boolean(r.runId);
    $("updated").textContent = `Live read: ${new Date(s.checkedAt).toLocaleString()}. Refresh on demand to check for changes; automatic polling is off.`;
    $("roadmap").replaceChildren(...(s.roadmap ?? []).map((step, index) => {
        const row = node("li", undefined, `roadmap-stop ${step.status}`);
        const circle = node("span", step.status === "complete" ? "\u2713" : step.status === "failed" ? "!" : String(index + 1), "roadmap-circle");
        circle.setAttribute("aria-hidden", "true");
        row.append(circle, node("h3", step.title),
            node("span", step.status === "complete" ? "Reached" : step.status === "failed" ? "Needs attention" : "Pending", "roadmap-status"),
            node("p", step.detail, "muted"));
        for (const item of step.links) row.append(link(item.label, item.url));
        if (step.checkedAt) {
            row.append(node("p", `Catalog checked locally: ${new Date(step.checkedAt).toLocaleString()}.`, "muted"));
        }
        if (step.warning) row.append(node("p", step.warning, "roadmap-warning"));
        if (step.check) {
            const check = node("button", step.check);
            check.type = "button";
            check.onclick = step.action
                ? () => act(step.action, { issueNumber: r.issueNumber })
                : refresh;
            row.append(check);
        }
        return row;
    }));
    $("known-issues").replaceChildren(...(s.knownIssues ?? []).map((issue) => {
        const item = node("article", undefined, "known-issue");
        item.append(node("h3", issue.title),
            node("p", `${issue.stage} | Observed in azd ${issue.observedIn}`, "muted"),
            node("p", issue.symptom),
            node("p", `Workaround: ${issue.workaround}`),
            node("p", issue.caution, "callout"),
            link(`Issue #${issue.number} - full details and workaround`, issue.url));
        return item;
    }));
    const pipeline = s.pipeline;
    const canAnalyze = pipeline?.timeline.some((record) => record.result === "failed") ?? false;
    $("analyze").disabled = !canAnalyze;
    $("analyze").hidden = !canAnalyze;
    $("analysis-note").hidden = !canAnalyze;
    $("queue").disabled = !s.nextStep.canQueue;
    $("record-mode").disabled = $("queue").disabled;
    $("adopt").querySelector("button").disabled = !r.changelogPr || !s.isDriver || !s.access.azure.ready || Boolean(r.runId);
}

$("refresh").onclick = refresh;
$("check-changelog").onclick = async () => {
    $("check-changelog").textContent = "Checking changelog PR...";
    try { await refresh(); }
    finally { $("check-changelog").textContent = "Check changelog PR"; }
};
$("new").onclick = () => { $("create").hidden = !$("create").hidden; };
$("releases").onchange = () => act("select", { issueNumber: Number($("releases").value) });
$("prepare-changelog").onclick = async () => {
    const result = await act("prepare-changelog", {
        issueNumber: snapshot.release.issueNumber, mode: $("changelog-mode").value,
    });
    if (result) message("Changelog preparation requested. Follow the agent conversation for approvals and, if selected, the new session link.");
};
$("create").onsubmit = async (event) => {
    event.preventDefault();
    const form = Object.fromEntries(new FormData(event.target));
    const result = await act("create", form);
    if (result) { event.target.reset(); event.target.hidden = true; }
};
$("link-changelog").onsubmit = async (event) => {
    event.preventDefault();
    const data = Object.fromEntries(new FormData(event.target));
    data.issueNumber = snapshot.release.issueNumber;
    data.changelogPr = Number(data.changelogPr);
    if (await act("link-changelog", data)) event.target.reset();
};
$("adopt").onsubmit = async (event) => {
    event.preventDefault();
    $("adopt-error").hidden = true;
    const data = Object.fromEntries(new FormData(event.target));
    data.issueNumber = snapshot.release.issueNumber;
    data.runId = Number(data.runId);
    if (await act("adopt", data)) $("adopt-dialog").close();
};
$("queue").onclick = async () => {
    preview = await act("prepare-queue", {
        issueNumber: snapshot.release.issueNumber, recordMode: $("record-mode").value,
    });
    if (!preview) return;
    $("queue-mode-info").textContent = preview.parameters.AzureRecordMode === "playback"
        ? "Playback selected: tests use recorded responses instead of live validation. This still publishes a real release."
        : "Live selected: tests run against real Azure resources. This publishes a real release.";
    $("queue-preview").textContent = JSON.stringify({
        organization: preview.organization, project: preview.project, definitionId: preview.definitionId,
        repository: preview.repository, branch: preview.branch, source: preview.source, parameters: preview.parameters,
    }, null, 2);
    $("queue-phrase").textContent = `Queue azd ${preview.version}`;
    $("queue-confirm").reset();
    $("queue-dialog").showModal();
};
$("queue-cancel").onclick = () => $("queue-dialog").close();
$("link-run").onclick = () => {
    $("adopt-error").hidden = true;
    $("adopt").reset();
    $("adopt-dialog").showModal();
};
$("adopt-cancel").onclick = () => $("adopt-dialog").close();
$("queue-confirm").onsubmit = async (event) => {
    event.preventDefault();
    const data = Object.fromEntries(new FormData(event.target));
    data.issueNumber = snapshot.release.issueNumber;
    data.ticket = preview.ticket;
    $("queue-dialog").close();
    await act("queue", data);
};
$("analyze").onclick = () => {
    if (confirm("Start a read-only agent investigation in this conversation? Private run identifiers will be included, but raw logs will not.")) {
        act("analyze", { issueNumber: snapshot.release.issueNumber });
    }
};
refresh();
