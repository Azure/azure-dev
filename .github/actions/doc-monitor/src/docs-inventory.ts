import * as core from "@actions/core";
import { Octokit } from "@octokit/rest";
import type { DocEntry } from "./types";
import { MAX_RECURSION_DEPTH, MAX_TOPICS, MAX_TOPIC_LENGTH, MAX_CONTENT_FETCHES, MAX_CONTENT_SIZE_BYTES } from "./constants";

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

/** Strip HTML tags, markdown links/images, and control characters from text. */
function sanitizeText(value: string): string {
  return value
    .replace(/<[^>]*>/g, "")
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, "")
    .replace(/[\x00-\x08\x0B\x0C\x0E-\x1F]/g, "");
}

/** Extract a title from markdown content (first H1 or filename). */
function extractTitle(content: string, path: string): string {
  const h1Match = content.match(/^#\s+(.+)$/m);
  if (h1Match) return sanitizeText(h1Match[1].trim());

  const frontmatterTitle = content.match(/^title:\s*["']?(.+?)["']?\s*$/m);
  if (frontmatterTitle) return sanitizeText(frontmatterTitle[1].trim());

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
    topics.push(...tagsMatch[1].split(",").map((t) => sanitizeText(t.trim().replace(/["']/g, ""))));
  }

  // From H2 headings
  const h2Matches = content.matchAll(/^##\s+(.+)$/gm);
  for (const match of h2Matches) {
    topics.push(sanitizeText(match[1].trim().toLowerCase()).slice(0, MAX_TOPIC_LENGTH));
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
  const repoFullName = `${owner}/${repo}`;

  try {
    return await collectDocsViaTree(octokit, owner, repo, repoFullName, paths);
  } catch (error) {
    core.warning(`Tree API failed for ${repoFullName}, falling back to recursive listing: ${error}`);
    const entries: DocEntry[] = [];
    for (const searchPath of paths) {
      try {
        await collectDocsRecursive(octokit, owner, repo, searchPath, repoFullName, entries);
      } catch (err) {
        core.warning(`Could not scan ${repoFullName}/${searchPath}: ${err}`);
      }
    }
    return entries;
  }
}

/** Single-call tree-based inventory (eliminates N+1). */
async function collectDocsViaTree(
  octokit: Octokit,
  owner: string,
  repo: string,
  repoFullName: string,
  filterPaths: string[],
): Promise<DocEntry[]> {
  const { data } = await octokit.git.getTree({ owner, repo, tree_sha: "HEAD", recursive: "1" });

  const mdFiles = data.tree.filter((item) => {
    if (item.type !== "blob" || !item.path?.endsWith(".md")) return false;
    if (shouldExclude(item.path)) return false;
    if (filterPaths.length === 1 && filterPaths[0] === "") return true;
    return filterPaths.some((p) => item.path!.startsWith(p));
  });

  const entries: DocEntry[] = [];
  let contentFetches = 0;

  // Batch content fetches using blob SHAs from the tree response (avoids N+1 getContent calls)
  const filesToFetch = mdFiles.filter(() => contentFetches++ < MAX_CONTENT_FETCHES);
  const remaining = mdFiles.slice(filesToFetch.length);

  const CONCURRENCY_LIMIT = 10;
  for (let i = 0; i < filesToFetch.length; i += CONCURRENCY_LIMIT) {
    const batch = filesToFetch.slice(i, i + CONCURRENCY_LIMIT);
    const results = await Promise.all(
      batch.map(async (file) => {
        const filePath = file.path!;
        try {
          const { data: blob } = await octokit.git.getBlob({ owner, repo, file_sha: file.sha! });
          if ((blob.size ?? 0) > MAX_CONTENT_SIZE_BYTES) {
            // Skip oversized files — use path-based fallback
            const name = filePath.split("/").pop() ?? filePath;
            return {
              repo: repoFullName, path: filePath,
              title: name.replace(/\.md$/, ""), topics: filePath.split("/").slice(0, 3),
            } as DocEntry;
          }
          const content = Buffer.from(blob.content, "base64").toString("utf-8");
          return {
            repo: repoFullName,
            path: filePath,
            title: extractTitle(content, filePath),
            topics: extractTopics(content, filePath),
          } as DocEntry;
        } catch {
          // Fall through to path-based entry
          const name = filePath.split("/").pop() ?? filePath;
          return {
            repo: repoFullName,
            path: filePath,
            title: name.replace(/\.md$/, ""),
            topics: filePath.split("/").slice(0, 3),
          } as DocEntry;
        }
      }),
    );
    entries.push(...results);
  }

  // Path-based fallback for files beyond the content fetch limit
  for (const file of remaining) {
    const filePath = file.path!;
    const name = filePath.split("/").pop() ?? filePath;
    entries.push({
      repo: repoFullName,
      path: filePath,
      title: name.replace(/\.md$/, ""),
      topics: filePath.split("/").slice(0, 3),
    });
  }

  return entries;
}

async function collectDocsRecursive(
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
          await collectDocsRecursive(octokit, owner, repo, item.path, repoFullName, entries, depth + 1);
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
