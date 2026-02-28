/** Centralized constants for the doc-monitor action. */

// ─── AI configuration ───────────────────────────────────────────────
export const GITHUB_MODELS_ENDPOINT = "https://models.github.ai/inference";
export const AI_MODEL = "openai/gpt-4o";
export const AI_TEMPERATURE = 0.1;
export const AI_MAX_TOKENS = 4096;

// ─── Token / size limits for AI prompts ─────────────────────────────
export const MAX_DIFF_SUMMARY_CHARS = 60_000;
export const MAX_PATCH_CHARS = 2_000;
export const MAX_PR_BODY_CHARS = 2_000;
export const MAX_DIFF_PROMPT_CHARS = 40_000;
export const MAX_MANIFEST_PROMPT_CHARS = 20_000;

// ─── Doc inventory ──────────────────────────────────────────────────
export const MAX_RECURSION_DEPTH = 5;
export const MAX_TOPICS = 10;
export const MAX_TOPIC_LENGTH = 40;
export const MAX_CONTENT_FETCHES = 50;
export const MAX_CONTENT_SIZE_BYTES = 50_000;

// ─── Batch processing ───────────────────────────────────────────────
export const MAX_PRS_PER_RUN = 20;

// ─── AI output limits ───────────────────────────────────────────────
export const MAX_REASON_LENGTH = 200;
export const MAX_SUMMARY_LENGTH = 500;
export const MAX_IMPACTS = 15;

// ─── GitHub API ─────────────────────────────────────────────────────
export const GITHUB_PAGE_SIZE = 100;

// ─── PR management ──────────────────────────────────────────────────
export const DOC_BRANCH_PREFIX = "docs/pr-";
export const BOT_COMMIT_PREFIX = "[doc-monitor]";

// ─── Comment tracking ───────────────────────────────────────────────
export const COMMENT_MARKER = "<!-- doc-monitor-tracking -->";

// ─── Default configuration ──────────────────────────────────────────
export const DEFAULT_SOURCE_REPO = "Azure/azure-dev";
export const DEFAULT_DOCS_REPO = "MicrosoftDocs/azure-dev-docs-pr";
export const DEFAULT_BRANCH = "main";

// ─── Valid action modes ─────────────────────────────────────────────
export const VALID_MODES = ["auto", "single", "all_open", "list"] as const;
