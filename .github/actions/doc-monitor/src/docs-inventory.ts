import * as core from "@actions/core";
import { Octokit } from "@octokit/rest";
import type { DocEntry } from "./types";
import { MAX_RECURSION_DEPTH, MAX_TOPICS, MAX_TOPIC_LENGTH } from "./constants";

/** Glob patterns to exclude from doc inventory. */
const EXCLUDE_PATTERNS = [
  /testdata\//,
  /node_modules\//,
  /vendor\//,
  /dist\//,
  /CHANGELOG\.md$/,
  /pkg\/input\/testdata\//,
];

/** Check if a path should be excluded from doc inventory. */
function shouldExclude(path: string): boolean {
  return EXCLUDE_PATTERNS.some((p) => p.test(path));
}

/** Extract a title from markdown content (first H1 or filename). */
function extractTitle(content: string, path: string): string {
  const h1Match = content.match(/^#\s+(.+)$/m);
  if (h1Match) return h1Match[1].trim();

  const frontmatterTitle = content.match(/^title:\s*["']?(.+?)["']?\s*$/m);
  if (frontmatterTitle) return frontmatterTitle[1].trim();

  // Fall back to filename
  const parts = path.split("/");
  return parts[parts.length - 1].replace(/\.md$/, "");
}

/** Extract topic keywords from markdown content. */
function extractTopics(content: string, path: string): string[] {
  const topics: string[] = [];

  // From path segments
  const segments = path.split("/").filter((s) => s !== "." && !s.endsWith(".md"));
  topics.push(...segments.slice(0, 3));

  // From frontmatter tags
  const tagsMatch = content.match(/^tags:\s*\[(.+)\]/m);
  if (tagsMatch) {
    topics.push(...tagsMatch[1].split(",").map((t) => t.trim().replace(/["']/g, "")));
  }

  // From H2 headings
  const h2Matches = content.matchAll(/^##\s+(.+)$/gm);
  for (const match of h2Matches) {
    topics.push(match[1].trim().toLowerCase().slice(0, MAX_TOPIC_LENGTH));
  }

  return [...new Set(topics)].slice(0, MAX_TOPICS);
}

/** Build a doc inventory for a repository by scanning for markdown files. */
export async function buildDocInventory(
  octokit: Octokit,
  owner: string,
  repo: string,
  paths: string[] = [""],
): Promise<DocEntry[]> {
  const entries: DocEntry[] = [];
  const repoFullName = `${owner}/${repo}`;

  for (const searchPath of paths) {
    try {
      await collectDocs(octokit, owner, repo, searchPath, repoFullName, entries);
    } catch (error) {
      core.warning(`Could not scan ${repoFullName}/${searchPath}: ${error}`);
    }
  }

  return entries;
}

async function collectDocs(
  octokit: Octokit,
  owner: string,
  repo: string,
  path: string,
  repoFullName: string,
  entries: DocEntry[],
  depth: number = 0,
): Promise<void> {
  // Limit recursion depth to avoid API rate limits
  if (depth > MAX_RECURSION_DEPTH) return;

  try {
    const { data } = await octokit.repos.getContent({ owner, repo, path });

    if (Array.isArray(data)) {
      for (const item of data) {
        if (item.type === "dir" && !shouldExclude(item.path)) {
          await collectDocs(octokit, owner, repo, item.path, repoFullName, entries, depth + 1);
        } else if (item.type === "file" && item.name.endsWith(".md") && !shouldExclude(item.path)) {
          // Fetch file content for title/topic extraction
          try {
            const fileData = await octokit.repos.getContent({ owner, repo, path: item.path });
            if (!Array.isArray(fileData.data) && "content" in fileData.data && fileData.data.content) {
              const content = Buffer.from(fileData.data.content, "base64").toString("utf-8");
              entries.push({
                repo: repoFullName,
                path: item.path,
                title: extractTitle(content, item.path),
                topics: extractTopics(content, item.path),
              });
            }
          } catch {
            // If we can't read the file, still add it with minimal info
            entries.push({
              repo: repoFullName,
              path: item.path,
              title: item.name.replace(/\.md$/, ""),
              topics: item.path.split("/").slice(0, 3),
            });
          }
        }
      }
    }
  } catch (error) {
    core.warning(`Could not list ${repoFullName}/${path}: ${error}`);
  }
}

/** Build a compact manifest string for AI consumption. */
export function buildDocManifest(entries: DocEntry[]): string {
  const lines = entries.map(
    (e) => `[${e.repo}] ${e.path} | "${e.title}" | topics: ${e.topics.join(", ")}`,
  );
  return lines.join("\n");
}
