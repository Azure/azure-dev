import * as core from "@actions/core";
import OpenAI from "openai";
import type { ClassifiedChange, DocEntry, AnalysisResult, DocImpact } from "./types";
import { buildDocManifest } from "./docs-inventory";
import {
  GITHUB_MODELS_ENDPOINT,
  AI_MODEL,
  AI_TEMPERATURE,
  AI_MAX_TOKENS,
  MAX_PR_BODY_CHARS,
  MAX_DIFF_PROMPT_CHARS,
  MAX_MANIFEST_PROMPT_CHARS,
} from "./constants";

/** Create an OpenAI client configured for GitHub Models. */
export function createAIClient(token: string): OpenAI {
  return new OpenAI({
    baseURL: GITHUB_MODELS_ENDPOINT,
    apiKey: token,
  });
}

/** Analyze PR changes against doc inventory to identify impacted documentation. */
export async function analyzeDocImpact(
  client: OpenAI,
  prTitle: string,
  prBody: string | null,
  diffSummary: string,
  classifiedChanges: ClassifiedChange[],
  docInventory: DocEntry[],
  sourceRepo?: string,
  docsRepo?: string,
): Promise<AnalysisResult> {
  const manifest = buildDocManifest(docInventory);

  const changesSummary = classifiedChanges
    .filter((c) => c.category !== "test" && c.category !== "docs")
    .map((c) => `- ${c.category}: ${c.summary} (${c.files.map((f) => f.path).join(", ")})`)
    .join("\n");

  const systemPrompt = `You are a documentation impact analyzer for the Azure Developer CLI (azd) project.
Your job is to determine which documentation files need to be created, updated, or deleted based on code changes in a pull request.

You MUST respond with valid JSON matching this schema:
{
  "impacts": [
    {
      "repo": "owner/repo",
      "path": "path/to/doc.md",
      "action": "create" | "update" | "delete",
      "reason": "Brief explanation of why this doc is impacted",
      "suggestedChanges": "Description of what should change in the doc",
      "priority": "high" | "medium" | "low"
    }
  ],
  "summary": "Overall summary of documentation impact",
  "noImpact": false
}

If no documentation changes are needed, return:
{
  "impacts": [],
  "summary": "No documentation changes needed because ...",
  "noImpact": true
}

Guidelines:
- API changes (new commands, flags, parameters) = high priority doc updates
- Behavior changes = medium-high priority
- Config/schema changes = medium priority
- Internal refactors with no user-facing change = likely no impact
- Bug fixes = low priority unless they change documented behavior
- Consider both in-repo docs (Azure/azure-dev) and external docs (MicrosoftDocs/azure-dev-docs-pr)
- Be specific about what needs to change in each doc
- Don't flag docs that are unrelated to the changes
- For new features, consider if new docs should be created`;

  const userPrompt = `## Pull Request
Title: ${prTitle}
${prBody ? `Description: ${prBody.slice(0, MAX_PR_BODY_CHARS)}` : ""}

## Classified Changes
${changesSummary}

## Diff Summary
${diffSummary.slice(0, MAX_DIFF_PROMPT_CHARS)}

## Documentation Inventory
${manifest.slice(0, MAX_MANIFEST_PROMPT_CHARS)}

Analyze the changes and determine which documentation files are impacted. Respond with JSON only.`;

  try {
    const response = await client.chat.completions.create({
      model: AI_MODEL,
      messages: [
        { role: "system", content: systemPrompt },
        { role: "user", content: userPrompt },
      ],
      temperature: AI_TEMPERATURE,
      max_tokens: AI_MAX_TOKENS,
      response_format: { type: "json_object" },
    });

    const content = response.choices[0]?.message?.content;
    if (!content) {
      return { impacts: [], summary: "AI analysis returned empty response", noImpact: true };
    }

    const parsed = JSON.parse(content) as RawAnalysisResult;
    return validateResult(parsed, sourceRepo, docsRepo);
  } catch (error) {
    core.error(`AI analysis failed: ${error}`);
    return {
      impacts: [],
      summary: `AI analysis failed: ${error instanceof Error ? error.message : String(error)}`,
      noImpact: true,
    };
  }
}

/** Raw AI response impact format (flat structure). */
interface RawImpact {
  repo: string;
  path: string;
  action: string;
  reason: string;
  suggestedChanges?: string;
  priority: string;
}

interface RawAnalysisResult {
  impacts: RawImpact[];
  summary: string;
  noImpact: boolean;
}

/** Validate and normalize the AI response from flat format to our DocImpact type. */
function validateResult(
  raw: RawAnalysisResult,
  sourceRepo?: string,
  docsRepo?: string,
): AnalysisResult {
  if (!Array.isArray(raw.impacts)) {
    raw.impacts = [];
  }

  const knownRepos = [sourceRepo, docsRepo].filter(Boolean) as string[];

  const validImpacts: DocImpact[] = raw.impacts
    .filter((impact) => {
      return (
        impact.repo &&
        impact.path &&
        ["create", "update", "delete"].includes(impact.action) &&
        ["high", "medium", "low"].includes(impact.priority) &&
        typeof impact.reason === "string"
      );
    })
    .map((impact) => {
      if (knownRepos.length > 0 && !knownRepos.includes(impact.repo)) {
        core.warning(
          `AI returned unknown repo "${impact.repo}" for doc "${impact.path}". ` +
          `Expected one of: ${knownRepos.join(", ")}`,
        );
      }
      return {
        doc: {
          repo: impact.repo,
          path: impact.path,
          title: impact.path.split("/").pop()?.replace(/\.md$/, "") || impact.path,
          topics: [],
        },
        action: impact.action as DocImpact["action"],
        reason: impact.reason,
        suggestedChanges: impact.suggestedChanges,
        priority: impact.priority as DocImpact["priority"],
      };
    });

  const noImpact = validImpacts.length === 0;
  return {
    impacts: validImpacts,
    summary: raw.summary || (noImpact ? "No documentation changes needed" : `${validImpacts.length} doc(s) impacted`),
    noImpact,
  };
}
