import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, it, expect, vi } from 'vitest';
import check from '../pr-governance-issue-check.js';

function fixture({ login = 'contributor', draft = false, labels = [] } = {}) {
  const github = {
    graphql: vi.fn().mockResolvedValue({
      repository: { pullRequest: { closingIssuesReferences: { nodes: [] } } },
    }),
    paginate: vi.fn().mockResolvedValue([]),
    rest: {
      issues: {
        listComments: vi.fn(),
        createComment: vi.fn(),
        updateComment: vi.fn(),
        deleteComment: vi.fn(),
      },
    },
  };
  const context = {
    repo: { owner: 'Azure', repo: 'azure-dev' },
    payload: {
      pull_request: {
        number: 10764,
        user: { login },
        title: 'Increment CLI version after release',
        head: { ref: 'cli-version-increment-7012057' },
        draft,
        labels,
      },
    },
  };
  const core = { setOutput: vi.fn(), setFailed: vi.fn(), warning: vi.fn() };
  return { github, context, core, run: () => check({ github, context, core }) };
}

describe('PR governance exemptions', () => {
  it.each([
    'azure-sdk-automation[bot]',
    'azure-sdk',
    'dependabot[bot]',
    'dependabot',
    'app/dependabot',
  ])('skips automated PRs authored by %s without API calls or enforcement', async (login) => {
    const { github, core, run } = fixture({ login });

    await run();

    expect(core.setOutput).toHaveBeenCalledExactlyOnceWith('skipped', 'true');
    expect(core.setFailed).not.toHaveBeenCalled();
    expect(github.graphql).not.toHaveBeenCalled();
    expect(github.paginate).not.toHaveBeenCalled();
    expect(github.rest.issues.createComment).not.toHaveBeenCalled();
  });

  it.each([
    { draft: true },
    { labels: [{ name: 'skip-governance' }] },
  ])('preserves the existing exemption for %j', async (options) => {
    const { github, core, run } = fixture(options);

    await run();

    expect(core.setOutput).toHaveBeenCalledWith('skipped', 'true');
    expect(core.setFailed).not.toHaveBeenCalled();
    expect(github.graphql).not.toHaveBeenCalled();
  });

  it.each(['contributor', 'unrelated[bot]', 'azure-sdk-automation'])(
    'still enforces linked issues for %s despite a release-like title and branch',
    async (login) => {
      const { github, core, run } = fixture({ login });

      await run();

      expect(github.graphql).toHaveBeenCalledOnce();
      expect(github.rest.issues.createComment).toHaveBeenCalledOnce();
      expect(core.setFailed).toHaveBeenCalledWith('PR must be linked to a GitHub issue.');
      expect(core.setOutput).not.toHaveBeenCalledWith('skipped', 'true');
    },
  );

  it('continues passing linked issues to the priority check for non-exempt PRs', async () => {
    const { github, core, run } = fixture();
    github.graphql.mockResolvedValue({
      repository: { pullRequest: { closingIssuesReferences: { nodes: [{ number: 123 }] } } },
    });

    await run();

    expect(core.setFailed).not.toHaveBeenCalled();
    expect(core.setOutput).toHaveBeenCalledExactlyOnceWith('issue_numbers', '[123]');
    expect(github.rest.issues.createComment).not.toHaveBeenCalled();
  });

  it('gates the priority check on the issue check exemption output', () => {
    const workflow = readFileSync(join(__dirname, '..', '..', 'workflows', 'pr-governance.yml'), 'utf8');

    expect(workflow).toContain(
      "if: steps.issue-check.outputs.skipped != 'true' && steps.issue-check.outcome == 'success'",
    );
  });
});
