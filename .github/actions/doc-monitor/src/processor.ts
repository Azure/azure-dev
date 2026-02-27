/** Core PR processing logic for the doc-monitor action. */

import * as core from "@actions/core";
import { Octokit } from "@octokit/rest";
import type { ActionInputs, FileDiff, TrackingState } from "./types";
import { parseRepoFullName } from "./inputs";
import { getPrInfo, getPrFiles, classifyChanges, buildDiffSummary } from "./diff";
import { buildDocInventory } from "./docs-inventory";
import { createAIClient, analyzeDocImpact } from "./analyze";
import { createOrUpdateDocPr, closeCompanionPrs } from "./pr-manager";
import { updateTrackingComment } from "./comment-tracker";

/** Process a single PR: analyze diff, determine doc impact, create companion PRs. */
export async function processPr(
  sourceOctokit: Octokit,
  docsOctokit: Octokit,
  inputs: ActionInputs,
  prNumber: number,
): Promise<void> {
  const [sourceOwner, sourceRepo] = parseRepoFullName(inputs.sourceRepo);
  const [docsOwner, docsRepo] = parseRepoFullName(inputs.docsRepo);

  core.info(`Processing PR #${prNumber} in ${inputs.sourceRepo}`);

  const prInfo = await getPrInfo(sourceOctokit, sourceOwner, sourceRepo, prNumber);
  core.info(`PR: "${prInfo.title}" (${prInfo.state})`);

  // Handle closed-without-merge: clean up companion PRs
  if (prInfo.state === "closed" && !prInfo.merged) {
    await handleClosedPr(sourceOctokit, docsOctokit, sourceOwner, sourceRepo, docsOwner, docsRepo, prNumber, !!inputs.docsRepoToken);
    return;
  }

  const files = await getPrFiles(sourceOctokit, sourceOwner, sourceRepo, prNumber);
  core.info(`Found ${files.length} changed files`);

  // Skip if PR only touches docs (it IS a doc change)
  if (isDocOnlyPr(files)) {
    core.info("PR only contains documentation changes — skipping analysis");
    await postNoImpact(
      sourceOctokit, sourceOwner, sourceRepo, prNumber,
      "This PR contains only documentation changes — no additional doc updates needed.",
    );
    return;
  }

  const classifiedChanges = classifyChanges(files);
  const diffSummary = buildDiffSummary(files);

  // Build doc inventories — docsOctokit always has a valid token
  // (docs-repo-token for write access, or GITHUB_TOKEN fallback for public repo reads)
  core.info("Building documentation inventory...");
  const inRepoDocs = await buildDocInventory(sourceOctokit, sourceOwner, sourceRepo, [
    "cli/azd/docs", "cli/azd/extensions", "ext", "README.md", "CONTRIBUTING.md",
  ]);
  const externalDocs = await buildDocInventory(docsOctokit, docsOwner, docsRepo, ["articles/azure-developer-cli"]);
  core.info(`Doc inventory: ${inRepoDocs.length} in-repo, ${externalDocs.length} external`);

  // AI analysis
  core.info("Running AI analysis...");
  const aiClient = createAIClient(inputs.githubToken);
  const analysisResult = await analyzeDocImpact(
    aiClient, prInfo.title, prInfo.body, diffSummary, classifiedChanges, [...inRepoDocs, ...externalDocs],
  );
  core.info(`Analysis: ${analysisResult.summary}`);
  core.info(`Impacts: ${analysisResult.impacts.length} doc(s) affected`);

  // Build tracking state
  const state: TrackingState = { sourcePr: prNumber, lastUpdated: new Date().toISOString(), analysisResult };

  // Create/update companion PRs if there are impacts
  if (!analysisResult.noImpact) {
    const inRepoImpacts = analysisResult.impacts.filter((i) => i.doc.repo === inputs.sourceRepo);
    const externalImpacts = analysisResult.impacts.filter((i) => i.doc.repo === inputs.docsRepo);

    if (inRepoImpacts.length > 0) {
      core.info(`Creating/updating in-repo doc PR (${inRepoImpacts.length} impacts)...`);
      state.inRepoPr = await createOrUpdateDocPr(
        sourceOctokit, sourceOwner, sourceRepo, prNumber, prInfo.htmlUrl,
        inRepoImpacts, inputs.docsAssignees,
      );
      core.info(`In-repo PR: ${state.inRepoPr.status} — ${state.inRepoPr.htmlUrl}`);
    }

    if (externalImpacts.length > 0) {
      if (inputs.docsRepoToken) {
        core.info(`Creating/updating external doc PR (${externalImpacts.length} impacts)...`);
        state.externalPr = await createOrUpdateDocPr(
          docsOctokit, docsOwner, docsRepo, prNumber, prInfo.htmlUrl,
          externalImpacts, inputs.docsAssignees,
        );
        core.info(`External PR: ${state.externalPr.status} — ${state.externalPr.htmlUrl}`);
      } else {
        core.warning(
          `Found ${externalImpacts.length} external doc impact(s) but docs-repo-token not set — ` +
          "skipping companion PR creation. Doc inventory scanning still works with GITHUB_TOKEN.",
        );
      }
    }
  }

  // Update tracking comment on source PR
  core.info("Updating tracking comment...");
  await updateTrackingComment(sourceOctokit, sourceOwner, sourceRepo, prNumber, state);

  core.setOutput("has-impact", !analysisResult.noImpact);
  core.setOutput("impact-count", analysisResult.impacts.length);
  core.setOutput("summary", analysisResult.summary);
  if (state.inRepoPr) core.setOutput("in-repo-pr-url", state.inRepoPr.htmlUrl);
  if (state.externalPr) core.setOutput("external-pr-url", state.externalPr.htmlUrl);
}

function isDocOnlyPr(files: FileDiff[]): boolean {
  return files.length === 0 || files.every((f) => f.path.endsWith(".md"));
}

async function handleClosedPr(
  sourceOctokit: Octokit, docsOctokit: Octokit,
  sourceOwner: string, sourceRepo: string,
  docsOwner: string, docsRepo: string,
  prNumber: number,
  canWriteDocsRepo: boolean,
): Promise<void> {
  core.info("PR closed without merge — closing companion doc PRs");
  await closeCompanionPrs(sourceOctokit, sourceOwner, sourceRepo, prNumber);
  if (canWriteDocsRepo) {
    await closeCompanionPrs(docsOctokit, docsOwner, docsRepo, prNumber);
  } else {
    core.info("Skipping external companion PR cleanup — docs-repo-token not provided");
  }
  await postNoImpact(
    sourceOctokit, sourceOwner, sourceRepo, prNumber,
    "Source PR was closed without merge. Companion doc PRs have been closed.",
  );
}

async function postNoImpact(
  octokit: Octokit, owner: string, repo: string, prNumber: number, summary: string,
): Promise<void> {
  const state: TrackingState = {
    sourcePr: prNumber,
    lastUpdated: new Date().toISOString(),
    analysisResult: { impacts: [], summary, noImpact: true },
  };
  await updateTrackingComment(octokit, owner, repo, prNumber, state);
}
