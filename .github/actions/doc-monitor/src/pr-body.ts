/** Markdown body builders for companion documentation PRs. */

import type { DocImpact } from "./types";

/** Strip HTML/markdown injection from AI-generated text before embedding in PR bodies. */
function sanitizeForMarkdown(value: string): string {
  return value
    .replace(/<[^>]*>/g, "")           // strip HTML tags
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, "")  // remove markdown images
    .replace(/[\x00-\x08\x0B\x0C\x0E-\x1F]/g, ""); // strip control chars
}

/** Build a summary markdown file for the doc analysis commit. */
export function buildDocPrSummary(
  sourcePrNumber: number,
  sourcePrUrl: string,
  impacts: DocImpact[],
): string {
  const lines = [
    `# Documentation Impact Analysis`,
    ``,
    `Source PR: [#${sourcePrNumber}](${sourcePrUrl})`,
    `Generated: ${new Date().toISOString()}`,
    ``,
    `## Impacted Documents`,
    ``,
  ];

  for (const impact of impacts) {
    lines.push(`### ${impact.action.toUpperCase()}: ${impact.doc.path}`);
    lines.push(`- **Priority**: ${impact.priority}`);
    lines.push(`- **Reason**: ${sanitizeForMarkdown(impact.reason)}`);
    if (impact.suggestedChanges) {
      lines.push(`- **Suggested changes**: ${sanitizeForMarkdown(impact.suggestedChanges)}`);
    }
    lines.push(``);
  }

  return lines.join("\n");
}

/** Build the PR body for a companion doc PR. */
export function buildPrBody(
  sourcePrNumber: number,
  sourcePrUrl: string,
  impacts: DocImpact[],
): string {
  const lines = [
    `## Documentation Update for azure-dev PR #${sourcePrNumber}`,
    ``,
    `This PR was automatically created by the **doc-monitor** workflow to track documentation changes needed for [PR #${sourcePrNumber}](${sourcePrUrl}).`,
    ``,
    `### Impacted Documents`,
    ``,
  ];

  const grouped = { high: [] as DocImpact[], medium: [] as DocImpact[], low: [] as DocImpact[] };
  for (const impact of impacts) grouped[impact.priority].push(impact);

  for (const [priority, items] of Object.entries(grouped)) {
    if (items.length === 0) continue;
    lines.push(`#### ${priority.charAt(0).toUpperCase() + priority.slice(1)} Priority`);
    for (const item of items) {
      lines.push(`- **${item.action}** \`${item.doc.path}\` - ${sanitizeForMarkdown(item.reason)}`);
      if (item.suggestedChanges) {
        lines.push(`  > ${sanitizeForMarkdown(item.suggestedChanges)}`);
      }
    }
    lines.push(``);
  }

  lines.push(`---`);
  lines.push(`_This PR is maintained by the doc-monitor workflow. Human edits are preserved on rebase._`);

  return lines.join("\n");
}
