/** Markdown body builders for companion documentation PRs. */

import type { DocImpact } from "./types";

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
    lines.push(`- **Reason**: ${impact.reason}`);
    if (impact.suggestedChanges) {
      lines.push(`- **Suggested changes**: ${impact.suggestedChanges}`);
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
      lines.push(`- **${item.action}** \`${item.doc.path}\` - ${item.reason}`);
      if (item.suggestedChanges) {
        lines.push(`  > ${item.suggestedChanges}`);
      }
    }
    lines.push(``);
  }

  lines.push(`---`);
  lines.push(`_This PR is maintained by the doc-monitor workflow. Human edits are preserved on rebase._`);

  return lines.join("\n");
}
