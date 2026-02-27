import { Octokit } from "@octokit/rest";
import type { FileDiff, PrInfo, ClassifiedChange, ChangeCategory } from "./types";
import { MAX_DIFF_SUMMARY_CHARS, MAX_PATCH_CHARS, GITHUB_PAGE_SIZE } from "./constants";

/** Fetch PR metadata. */
export async function getPrInfo(
  octokit: Octokit,
  owner: string,
  repo: string,
  prNumber: number,
): Promise<PrInfo> {
  const { data } = await octokit.pulls.get({ owner, repo, pull_number: prNumber });
  return {
    number: data.number,
    title: data.title,
    body: data.body,
    baseBranch: data.base.ref,
    headBranch: data.head.ref,
    state: data.state,
    merged: data.merged,
    htmlUrl: data.html_url,
  };
}

/** Fetch the list of files changed in a PR. */
export async function getPrFiles(
  octokit: Octokit,
  owner: string,
  repo: string,
  prNumber: number,
): Promise<FileDiff[]> {
  const files: FileDiff[] = [];
  for await (const response of octokit.paginate.iterator(octokit.pulls.listFiles, {
    owner,
    repo,
    pull_number: prNumber,
    per_page: GITHUB_PAGE_SIZE,
  })) {
    for (const file of response.data) {
      files.push({
        path: file.filename,
        status: mapStatus(file.status),
        previousPath: file.previous_filename,
        additions: file.additions,
        deletions: file.deletions,
        patch: file.patch,
      });
    }
  }
  return files;
}

function mapStatus(status: string): FileDiff["status"] {
  switch (status) {
    case "added":
      return "added";
    case "removed":
      return "deleted";
    case "renamed":
      return "renamed";
    default:
      return "modified";
  }
}

/** Area classification patterns. */
const AREA_PATTERNS: { pattern: RegExp; category: ChangeCategory }[] = [
  { pattern: /^cli\/azd\/internal\/cmd\//, category: "api" },
  { pattern: /^cli\/azd\/pkg\//, category: "behavior" },
  { pattern: /^cli\/azd\/internal\//, category: "behavior" },
  { pattern: /^cli\/azd\/extensions\//, category: "feature" },
  { pattern: /^schemas\//, category: "config" },
  { pattern: /^eng\//, category: "infra" },
  { pattern: /^ext\//, category: "feature" },
  { pattern: /\.md$/, category: "docs" },
  { pattern: /(_test\.go|_test\.ts|\.test\.)/, category: "test" },
  { pattern: /^\.github\//, category: "infra" },
];

/** Classify a file into a change category. */
function classifyFile(path: string): ChangeCategory {
  for (const { pattern, category } of AREA_PATTERNS) {
    if (pattern.test(path)) return category;
  }
  return "other";
}

/** Group files into classified changes. */
export function classifyChanges(files: FileDiff[]): ClassifiedChange[] {
  const groups = new Map<ChangeCategory, FileDiff[]>();
  for (const file of files) {
    const cat = classifyFile(file.path);
    if (!groups.has(cat)) groups.set(cat, []);
    groups.get(cat)!.push(file);
  }

  return Array.from(groups.entries()).map(([category, groupFiles]) => ({
    files: groupFiles,
    category,
    summary: `${groupFiles.length} file(s) in ${category}`,
  }));
}

/** Build a compact diff summary for AI consumption, respecting token limits. */
export function buildDiffSummary(files: FileDiff[], maxChars: number = MAX_DIFF_SUMMARY_CHARS): string {
  const lines: string[] = [];
  let currentLen = 0;
  let filesProcessed = 0;

  for (const file of files) {
    const header = `--- ${file.status}: ${file.path} (+${file.additions}/-${file.deletions})`;
    if (currentLen + header.length > maxChars) {
      lines.push(`\n... truncated (${files.length - filesProcessed} more files)`);
      break;
    }
    lines.push(header);
    currentLen += header.length;
    filesProcessed++;

    if (file.patch) {
      const patchTruncated =
        file.patch.length > MAX_PATCH_CHARS
          ? file.patch.slice(0, MAX_PATCH_CHARS) + "\n... (patch truncated)"
          : file.patch;
      if (currentLen + patchTruncated.length > maxChars) {
        lines.push("  (patch omitted for size)");
        currentLen += 30;
      } else {
        lines.push(patchTruncated);
        currentLen += patchTruncated.length;
      }
    }
  }

  return lines.join("\n");
}
