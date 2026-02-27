/** Input parsing and validation for the doc-monitor action. */

import * as core from "@actions/core";
import type { ActionInputs } from "./types";
import { DEFAULT_SOURCE_REPO, DEFAULT_DOCS_REPO, VALID_MODES } from "./constants";

/** Parse and validate action inputs. */
export function getInputs(): ActionInputs {
  const mode = core.getInput("mode") || "auto";
  if (!isValidMode(mode)) {
    throw new Error(`Invalid mode "${mode}". Must be one of: ${VALID_MODES.join(", ")}`);
  }

  const prNumberRaw = core.getInput("pr-number");
  const prNumber = prNumberRaw ? parseInt(prNumberRaw, 10) : undefined;
  if (prNumberRaw && (!prNumber || prNumber <= 0)) {
    throw new Error(`Invalid pr-number "${prNumberRaw}". Must be a positive integer.`);
  }

  const prListRaw = core.getInput("pr-list");
  const prList = prListRaw
    ? prListRaw
        .split(",")
        .map((n) => parseInt(n.trim(), 10))
        .filter((n) => n > 0)
    : undefined;

  const sourceRepo = core.getInput("source-repo") || DEFAULT_SOURCE_REPO;
  const docsRepo = core.getInput("docs-repo") || DEFAULT_DOCS_REPO;
  parseRepoFullName(sourceRepo);
  parseRepoFullName(docsRepo);

  return {
    githubToken: core.getInput("github-token", { required: true }),
    docsRepoToken: core.getInput("docs-repo-token", { required: true }),
    mode,
    prNumber,
    prList,
    docsAssignees: core
      .getInput("docs-assignees")
      .split(",")
      .map((a) => a.trim())
      .filter(Boolean),
    sourceRepo,
    docsRepo,
  };
}

function isValidMode(mode: string): mode is ActionInputs["mode"] {
  return (VALID_MODES as readonly string[]).includes(mode);
}

/** Parse "owner/repo" into [owner, repo], throwing on invalid format. */
export function parseRepoFullName(fullName: string): [owner: string, repo: string] {
  const parts = fullName.split("/");
  if (parts.length !== 2 || !parts[0] || !parts[1]) {
    throw new Error(`Invalid repository format "${fullName}". Expected "owner/repo".`);
  }
  return [parts[0], parts[1]];
}
