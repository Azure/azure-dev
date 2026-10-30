// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { mkdir, readFile, readdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { randomUUID } from "node:crypto";
import { cli, errorMessage, ReleaseError } from "./cli.mjs";
import { PUBLIC_REPO, requireValue, version, sha } from "./model.mjs";

export class ChangelogWorkflow {
    constructor(session, run = cli) {
        this.session = session;
        this.run = run;
    }

    directory() {
        requireValue(this.session.workspacePath, "Session artifact storage is unavailable; use the changelog skill directly.");
        return join(this.session.workspacePath, "files", "azd-release-changelogs");
    }

    async start({ releaseVersion, mode }) {
        version(releaseVersion);
        requireValue(["current", "new"].includes(mode), "Choose this session or a new isolated session.");
        const directory = this.directory();
        await mkdir(directory, { recursive: true });
        const request = {
            schema: 1, id: randomUUID(), version: releaseVersion, mode,
            createdAt: new Date().toISOString(),
        };
        const path = join(directory, `${releaseVersion}.json`);
        try { await writeFile(path, JSON.stringify(request), { flag: "wx" }); }
        catch (error) {
            requireValue(error.code !== "EEXIST",
                "Changelog preparation was already requested for this version. Follow the existing agent conversation; do not start a duplicate.");
            throw error;
        }
        // Persist before delivery: if send times out, do not silently launch duplicate work.
        try { await this.session.send({ prompt: this.prompt(request), mode: "immediate" }); }
        catch {
            throw new ReleaseError("changelog_delivery_uncertain",
                "Changelog request was saved, but agent delivery could not be confirmed. Check this conversation before starting duplicate work.");
        }
        return { requested: true, version: releaseVersion, mode };
    }

    prompt(request) {
        const task = [
            `The user requested preparation of the azd CORE changelog and a changelog PR for version ${request.version} from the release canvas.`,
            "Invoke the changelog-generation skill and follow its review/approval steps; do not skip its user decisions.",
            "After approval, commit the changelog/version changes and create the changelog pull request targeting Azure/azure-dev main.",
            "Use a ready-for-review PR unless a draft requirement in your existing instructions overrides this default.",
            `Include this exact public, non-sensitive correlation marker in the PR body: <!-- azd-release-changelog:${request.id} -->`,
            "Do not include private tracking issue references or diagnostics in the public PR.",
            "Do not merge the PR, create a release tracking issue, queue pipelines, or publish anything.",
            "Return the PR link. Release tracking already exists; the canvas will discover the PR so the driver can link it to that release.",
        ].join("\n");
        if (request.mode === "current") {
            return `${task}\nThe user chose THIS session. Work here and preserve unrelated changes.`;
        }
        return [
            "The user explicitly chose a NEW ISOLATED SESSION for changelog preparation, not this checkout.",
            "Invoke the orchestrate skill and use create_session with an isolated worktree in the current project,",
            "leaving base_branch unset so it starts from the project default. Do not run the changelog work in this session.",
            "Use interactive mode so the child can ask for the skill's required approvals. Use the following standalone kickoff request:",
            task,
            "Give the user the child session link so they can review and answer its questions.",
            "If child-session creation is unavailable, report that limitation; do not silently fall back to this session.",
        ].join("\n");
    }

    async status(releaseVersion, { live = true } = {}) {
        if (releaseVersion !== undefined) version(releaseVersion);
        const directory = this.directory();
        let names;
        try { names = await readdir(directory); }
        catch (error) {
            if (error.code === "ENOENT") return [];
            throw error;
        }
        const requests = [];
        for (const name of names.filter((name) => releaseVersion === undefined
            ? name.endsWith(".json") : name === `${releaseVersion}.json`)) {
            const request = JSON.parse(await readFile(join(directory, name), "utf8"));
            version(request.version);
            requireValue(request.schema === 1 && /^[a-f0-9-]{36}$/.test(request.id), "Invalid saved changelog request.");
            requireValue(releaseVersion === undefined || request.version === releaseVersion,
                "The saved changelog request does not match the selected release.");
            const result = { version: request.version, mode: request.mode, createdAt: request.createdAt, status: "waiting" };
            if (!live) {
                result.message = "Preparation already requested. Follow the agent conversation, then select Refresh live data to discover its PR.";
                requests.push(result);
                continue;
            }
            try {
                const marker = `<!-- azd-release-changelog:${request.id} -->`;
                const prs = await this.run("gh", [
                    "pr", "list", "--repo", PUBLIC_REPO, "--state", "all",
                    "--search", `"azd-release-changelog:${request.id}" in:body`,
                    "--limit", "10", "--json", "number,body,baseRefName,state,headRefOid",
                ]);
                const matches = prs.filter((pr) => pr.body?.includes(marker) && pr.baseRefName === "main" &&
                    ["OPEN", "MERGED"].includes(pr.state));
                requireValue(matches.length <= 1, "Multiple PRs match this changelog request; select the intended PR manually.");
                if (matches.length === 1) {
                    const pr = matches[0];
                    const source = sha(pr.headRefOid);
                    const actual = await this.run("gh", [
                        "api", `repos/${PUBLIC_REPO}/contents/cli/version.txt?ref=${source}`,
                        "-H", "Accept: application/vnd.github.raw+json",
                    ], { text: true });
                    requireValue(actual.trim() === request.version, "The discovered PR's CLI version differs from the requested version.");
                    result.status = "ready";
                    result.pr = pr.number;
                    result.url = `https://github.com/${PUBLIC_REPO}/pull/${pr.number}`;
                }
            } catch (error) {
                result.status = "error";
                result.message = errorMessage(error);
            }
            requests.push(result);
        }
        return requests.sort((a, b) => b.createdAt.localeCompare(a.createdAt));
    }
}
