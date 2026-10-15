import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { runInNewContext } from 'node:vm';
import { describe, it, expect, vi } from 'vitest';

const docsTracker = 'foundry-docs-tracker';
const duplicateDetector = 'foundry-duplicate-pr-detector';
const context = { repo: { owner: 'Azure', repo: 'azure-dev' }, issue: { number: 42 }, runId: 123 };
const issuePrefix = '[docs] (PR #42) ';
const prefixExpression = '[docs] (PR #${{ github.event.pull_request.number }}) ';

/** @param {string} name @param {string} [extension] */
function readWorkflow(name, extension = '.md') {
  return readFileSync(join(__dirname, '..', '..', 'workflows', name + extension), 'utf8');
}

/** @param {string} name */
function inlineScript(name) {
  const lines = readWorkflow(name).split(/\r?\n/);
  const start = lines.findIndex(line => line.trim() === 'script: |');
  const header = lines[start];
  if (!header) throw new Error(`Missing inline script in ${name}`);
  const indentation = ' '.repeat(header.indexOf('script:') + 2);
  const script = [];
  for (const line of lines.slice(start + 1)) {
    if (line.trim() && !line.startsWith(indentation)) break;
    script.push(line.slice(indentation.length));
  }
  return script.join('\n');
}

/** @param {string} workflow @param {string} name */
function compiledJob(workflow, name) {
  const lines = readWorkflow(workflow, '.lock.yml').split(/\r?\n/);
  const start = lines.indexOf(`  ${name}:`);
  if (start < 0) throw new Error(`Missing ${name} job in ${workflow}`);
  const end = lines.findIndex((line, index) => index > start && /^  [\w-]+:$/.test(line));
  return lines.slice(start, end < 0 ? undefined : end).join('\n');
}

/** @param {string} workflow @param {object} github */
async function runStep(workflow, github) {
  const core = { info: vi.fn(), setOutput: vi.fn() };
  await runInNewContext(`(async () => {\n${inlineScript(workflow)}\n})()`, { github, context, core });
  return core;
}

describe('Foundry actor authorization', () => {
  it.each([docsTracker, duplicateDetector])('keeps %s behind repository write roles', workflow => {
    expect(readWorkflow(workflow)).toContain('roles: [admin, maintainer, write]');
    expect(readWorkflow(workflow)).toContain('checkout: false');
    const permissionCheck = compiledJob(workflow, 'pre_activation');
    expect(permissionCheck).toContain('GH_AW_REQUIRED_ROLES: "admin,maintainer,write"');
    expect(permissionCheck).toContain("if: steps.check_membership.outputs.is_team_member == 'true'");
    expect(permissionCheck).not.toContain('actions/checkout');
    expect(compiledJob(workflow, 'activation')).toContain("needs.pre_activation.outputs.activated == 'true'");
    expect(compiledJob(workflow, 'agent')).toContain('needs: activation');
    for (const job of ['detection', 'safe_outputs', 'conclusion']) {
      expect(compiledJob(workflow, job)).toContain("needs.agent.result != 'skipped'");
    }
  });
});

describe('Foundry docs eligibility', () => {
  const unrelatedFiles = [{ filename: 'README.md' }];
  const trackingIssue = { number: 501, title: issuePrefix + 'Document the new flag', state: 'open' };

  it.each([
    ['current extension changes', [{ filename: 'cli/azd/extensions/azure.ai.routines/commands.go' }], [], true],
    ['a rename out of an extension', [{
      filename: 'cli/azd/pkg/moved.go',
      previous_filename: 'cli/azd/extensions/azure.ai.routines/commands.go',
    }], [], true],
    ['an existing open issue after full rescope', unrelatedFiles, [trackingIssue], true],
    ['an existing closed issue', unrelatedFiles, [{ ...trackingIssue, state: 'closed' }], true],
    ['another PR issue', unrelatedFiles, [{ ...trackingIssue, title: '[docs] (PR #43) Other feature' }], false],
    ['a longer PR number', unrelatedFiles, [{ ...trackingIssue, title: '[docs] (PR #420) Other feature' }], false],
    ['a generic docs issue', unrelatedFiles, [{ ...trackingIssue, title: '[docs] Unrelated feature' }], false],
    ['a PR with a matching title', unrelatedFiles, [{ ...trackingIssue, pull_request: {} }], false],
    ['an unrelated PR without tracking', unrelatedFiles, [], false],
    ['a different extension family', [{ filename: 'cli/azd/extensions/azure.appservice/commands.go' }], [], false],
  ])('handles %s', async (_description, files, issues, eligible) => {
    const listFiles = vi.fn();
    const listForRepo = vi.fn();
    const github = {
      rest: { pulls: { listFiles }, issues: { listForRepo } },
      paginate: vi.fn().mockResolvedValueOnce(files).mockResolvedValueOnce(issues),
    };
    const core = await runStep(docsTracker, github);
    expect(core.setOutput).toHaveBeenCalledWith('eligible', String(eligible));
    expect(github.paginate).toHaveBeenCalledWith(listFiles, {
      ...context.repo, pull_number: 42, per_page: 100,
    });
    if (github.paginate.mock.calls.length > 1) {
      expect(github.paginate).toHaveBeenCalledWith(listForRepo, {
        ...context.repo, state: 'all', labels: 'area/public-docs', per_page: 100,
      });
    }
  });

  it('propagates metadata API failures instead of silently treating the PR as ineligible', async () => {
    const github = {
      rest: { pulls: { listFiles: vi.fn() } },
      paginate: vi.fn().mockRejectedValue(new Error('Metadata request failed')),
    };
    await expect(runStep(docsTracker, github)).rejects.toThrow('Metadata request failed');
  });

  it('filters lifecycle events before activation without an event-level path filter', () => {
    const workflow = readWorkflow(docsTracker, '.lock.yml');
    expect(workflow).not.toMatch(/^    paths:/m);
    expect(workflow).toContain('      - synchronize');
    expect(workflow).toContain('      - closed');
    expect(compiledJob(docsTracker, 'pre_activation'))
      .toContain('docs_eligible: ${{ steps.docs_scope.outputs.eligible }}');
    expect(compiledJob(docsTracker, 'activation'))
      .toContain("needs.pre_activation.outputs.docs_eligible == 'true'");
  });

  it('enforces the same PR-specific prefix for creation, comments, and labels', () => {
    const job = compiledJob(docsTracker, 'safe_outputs');
    const encoded = /^\s+GH_AW_SAFE_OUTPUTS_HANDLER_CONFIG: (".*")$/m.exec(job)?.[1];
    if (!encoded) throw new Error('Missing safe-output handler configuration');
    const config = JSON.parse(JSON.parse(encoded));
    expect(config.create_issue).toMatchObject({
      title_prefix: prefixExpression,
      deduplicate_by_title: true,
      labels: ['area/public-docs'],
      max: 1,
    });
    for (const handler of [config.add_comment, config.add_labels]) {
      expect(handler).toMatchObject({
        required_title_prefix: prefixExpression,
        required_labels: ['area/public-docs'],
      });
    }
  });
});

describe('Foundry duplicate lookback', () => {
  it.each([
    ['2026-10-15T16:00:00Z', '2026-10-15', '2026-10-01'],
    ['2026-01-05T12:00:00Z', '2026-01-05', '2025-12-22'],
    ['2024-03-05T12:00:00Z', '2024-03-05', '2024-02-20'],
    ['2026-10-15T00:30:00+02:00', '2026-10-14', '2026-09-30'],
  ])('uses the workflow start time %s', async (startedAt, runDate, cutoffDate) => {
    const getWorkflowRun = vi.fn().mockResolvedValue({ data: { run_started_at: startedAt } });
    const core = await runStep(duplicateDetector, { rest: { actions: { getWorkflowRun } } });
    expect(getWorkflowRun).toHaveBeenCalledWith({ ...context.repo, run_id: 123 });
    expect(core.setOutput).toHaveBeenCalledWith('run_date', runDate);
    expect(core.setOutput).toHaveBeenCalledWith('cutoff_date', cutoffDate);
  });

  it.each([undefined, null, '', 'invalid'])('rejects an invalid run timestamp: %s', async startedAt => {
    const getWorkflowRun = vi.fn().mockResolvedValue({ data: { run_started_at: startedAt } });
    await expect(runStep(duplicateDetector, { rest: { actions: { getWorkflowRun } } }))
      .rejects.toThrow('Workflow run has no valid start time.');
  });

  it('passes the computed dates into the prompt and search query', () => {
    const workflow = readWorkflow(duplicateDetector);
    expect(workflow).not.toContain('github.event.pull_request.created_at');
    expect(workflow).toContain('${{ needs.pre_activation.outputs.run_date }}');
    expect(workflow).toContain('merged:>=${{ needs.pre_activation.outputs.cutoff_date }}');
    const permissionCheck = compiledJob(duplicateDetector, 'pre_activation');
    expect(permissionCheck).toContain('run_date: ${{ steps.lookback.outputs.run_date }}');
    expect(permissionCheck).toContain('cutoff_date: ${{ steps.lookback.outputs.cutoff_date }}');
  });
});
