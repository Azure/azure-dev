/** Low-level GitHub API helpers for branch and file operations. */

import { Octokit } from "@octokit/rest";

/** Minimal PR info returned by findExistingPr. */
export interface ExistingPrInfo {
  number: number;
  htmlUrl: string;
  state: string;
  body: string;
}

/** Check whether a branch exists in a repository. */
export async function checkBranchExists(
  octokit: Octokit,
  owner: string,
  repo: string,
  branch: string,
): Promise<boolean> {
  try {
    await octokit.git.getRef({ owner, repo, ref: `heads/${branch}` });
    return true;
  } catch {
    return false;
  }
}

/** Find an existing PR for a given head branch. Returns the newest match or null. */
export async function findExistingPr(
  octokit: Octokit,
  owner: string,
  repo: string,
  headBranch: string,
): Promise<ExistingPrInfo | null> {
  const { data: prs } = await octokit.pulls.list({
    owner,
    repo,
    head: `${owner}:${headBranch}`,
    state: "all",
    per_page: 1,
  });

  if (prs.length === 0) return null;

  return {
    number: prs[0].number,
    htmlUrl: prs[0].html_url,
    state: prs[0].state,
    body: prs[0].body || "",
  };
}

/** Create or update a single file on a branch via the GitHub Contents API. */
export async function createOrUpdateFile(
  octokit: Octokit,
  owner: string,
  repo: string,
  branch: string,
  path: string,
  content: string,
  message: string,
): Promise<void> {
  let existingSha: string | undefined;
  try {
    const { data } = await octokit.repos.getContent({ owner, repo, path, ref: branch });
    if (!Array.isArray(data) && "sha" in data) {
      existingSha = data.sha;
    }
  } catch {
    // File doesn't exist yet — will be created
  }

  await octokit.repos.createOrUpdateFileContents({
    owner,
    repo,
    path,
    message,
    content: Buffer.from(content).toString("base64"),
    branch,
    sha: existingSha,
  });
}
