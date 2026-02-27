import * as core from "@actions/core";
import * as github from "@actions/github";
import { Octokit } from "@octokit/rest";
import { getInputs, parseRepoFullName } from "./inputs";
import { processPr } from "./processor";
import { GITHUB_PAGE_SIZE } from "./constants";

/** Resolve which PRs to process based on the configured mode. */
async function resolvePrNumbers(
  mode: string,
  prNumber: number | undefined,
  prList: number[] | undefined,
  sourceRepo: string,
  sourceOctokit: Octokit,
): Promise<number[]> {
  switch (mode) {
    case "auto": {
      const pr = github.context.payload.pull_request;
      if (!pr) {
        core.setFailed("No pull_request in event payload. Use mode=single/all_open/list for manual triggers.");
        return [];
      }
      return [pr.number as number];
    }
    case "single": {
      if (!prNumber) {
        core.setFailed("mode=single requires pr-number input");
        return [];
      }
      return [prNumber];
    }
    case "all_open": {
      const [owner, repo] = parseRepoFullName(sourceRepo);
      core.info("Fetching all open PRs targeting main...");
      const prs = await sourceOctokit.paginate(sourceOctokit.pulls.list, {
        owner,
        repo,
        state: "open",
        base: "main",
        per_page: GITHUB_PAGE_SIZE,
      });
      core.info(`Found ${prs.length} open PRs`);
      return prs.map((pr) => pr.number);
    }
    case "list": {
      if (!prList || prList.length === 0) {
        core.setFailed("mode=list requires pr-list input");
        return [];
      }
      return prList;
    }
    default:
      core.setFailed(`Unknown mode: ${mode}`);
      return [];
  }
}

async function run(): Promise<void> {
  try {
    const inputs = getInputs();

    const sourceOctokit = new Octokit({ auth: inputs.githubToken });
    const docsOctokit = inputs.docsRepoToken
      ? new Octokit({ auth: inputs.docsRepoToken })
      : null;

    const prNumbers = await resolvePrNumbers(
      inputs.mode, inputs.prNumber, inputs.prList, inputs.sourceRepo, sourceOctokit,
    );

    for (const prNum of prNumbers) {
      try {
        await processPr(sourceOctokit, docsOctokit, inputs, prNum);
      } catch (error) {
        core.error(`Failed to process PR #${prNum}: ${error}`);
        if (prNumbers.length === 1) throw error;
      }
    }

    core.info(`Processed ${prNumbers.length} PR(s)`);
  } catch (error) {
    core.setFailed(`Action failed: ${error instanceof Error ? error.message : String(error)}`);
  }
}

run();
