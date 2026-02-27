import * as core from "@actions/core";
import { Octokit } from "@octokit/rest";
import type { CompanionPr, DocImpact } from "./types";
import { DOC_BRANCH_PREFIX, BOT_COMMIT_PREFIX, DEFAULT_BRANCH } from "./constants";
import { checkBranchExists, findExistingPr, createOrUpdateFile } from "./github-utils";
import { buildDocPrSummary, buildPrBody } from "./pr-body";

/** Get the branch name for a companion doc PR. */
export function getDocBranchName(sourcePrNumber: number): string {
  return `${DOC_BRANCH_PREFIX}${sourcePrNumber}`;
}

/** Create or update a companion doc PR in a target repo. */
export async function createOrUpdateDocPr(
  octokit: Octokit,
  targetOwner: string,
  targetRepo: string,
  sourcePrNumber: number,
  sourcePrUrl: string,
  impacts: DocImpact[],
  assignees: string[],
  defaultBranch: string = DEFAULT_BRANCH,
): Promise<CompanionPr> {
  const branch = getDocBranchName(sourcePrNumber);
  const repoFullName = `${targetOwner}/${targetRepo}`;

  try {
    // Ensure the doc branch exists
    if (!(await checkBranchExists(octokit, targetOwner, targetRepo, branch))) {
      const { data: ref } = await octokit.git.getRef({
        owner: targetOwner,
        repo: targetRepo,
        ref: `heads/${defaultBranch}`,
      });
      await octokit.git.createRef({
        owner: targetOwner,
        repo: targetRepo,
        ref: `refs/heads/${branch}`,
        sha: ref.object.sha,
      });
    }

    // Commit an analysis summary file to the branch
    const summaryContent = buildDocPrSummary(sourcePrNumber, sourcePrUrl, impacts);
    await createOrUpdateFile(
      octokit, targetOwner, targetRepo, branch,
      `.doc-monitor/pr-${sourcePrNumber}-analysis.md`,
      summaryContent,
      `${BOT_COMMIT_PREFIX} Documentation impact analysis for PR #${sourcePrNumber}`,
    );

    // Create or update the PR
    const existingPr = await findExistingPr(octokit, targetOwner, targetRepo, branch);
    if (existingPr) {
      await octokit.pulls.update({
        owner: targetOwner,
        repo: targetRepo,
        pull_number: existingPr.number,
        body: buildPrBody(sourcePrNumber, sourcePrUrl, impacts),
      });
      return {
        repo: repoFullName, number: existingPr.number, branch,
        htmlUrl: existingPr.htmlUrl, status: "updated",
      };
    }

    const { data: newPr } = await octokit.pulls.create({
      owner: targetOwner,
      repo: targetRepo,
      title: `[docs] Update documentation for azure-dev PR #${sourcePrNumber}`,
      body: buildPrBody(sourcePrNumber, sourcePrUrl, impacts),
      head: branch,
      base: defaultBranch,
    });

    await tryAssignPr(octokit, targetOwner, targetRepo, newPr.number, assignees);

    return {
      repo: repoFullName, number: newPr.number, branch,
      htmlUrl: newPr.html_url, status: "created",
    };
  } catch (error) {
    const msg = error instanceof Error ? error.message : String(error);
    core.error(`Failed to create/update doc PR in ${repoFullName}: ${msg}`);
    return { repo: repoFullName, number: 0, branch, htmlUrl: "", status: "error", message: msg };
  }
}

/** Close companion doc PRs and delete branches when source PR is closed without merge. */
export async function closeCompanionPrs(
  octokit: Octokit,
  targetOwner: string,
  targetRepo: string,
  sourcePrNumber: number,
): Promise<void> {
  const branch = getDocBranchName(sourcePrNumber);
  const existingPr = await findExistingPr(octokit, targetOwner, targetRepo, branch);

  if (!existingPr || existingPr.state !== "open") return;

  await octokit.pulls.update({
    owner: targetOwner,
    repo: targetRepo,
    pull_number: existingPr.number,
    state: "closed",
    body:
      existingPr.body +
      `\n\n---\n_Closed automatically: source PR #${sourcePrNumber} was closed without merge._`,
  });

  try {
    await octokit.git.deleteRef({ owner: targetOwner, repo: targetRepo, ref: `heads/${branch}` });
  } catch {
    core.warning(`Could not delete branch ${branch} in ${targetOwner}/${targetRepo}`);
  }
}

/** Best-effort assignee assignment — warns on failure rather than throwing. */
async function tryAssignPr(
  octokit: Octokit,
  owner: string,
  repo: string,
  prNumber: number,
  assignees: string[],
): Promise<void> {
  if (assignees.length === 0) return;
  try {
    await octokit.issues.addAssignees({ owner, repo, issue_number: prNumber, assignees });
  } catch (err) {
    core.warning(`Could not assign ${assignees.join(", ")} to PR #${prNumber}: ${err}`);
  }
}
