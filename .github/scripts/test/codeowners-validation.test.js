import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { runInNewContext } from 'node:vm';
import { describe, it, expect, vi } from 'vitest';

const workflow = readFileSync(join(__dirname, '..', '..', 'workflows', 'codeowners-validation.yml'), 'utf8');
const script = /          script: \|\n((?:            .*(?:\n|$)|\n)+)/.exec(workflow)?.[1];
if (!script) {
  throw new Error('Could not find the CODEOWNERS validation script in the workflow.');
}

function fixture() {
  const github = {
    paginate: vi.fn().mockResolvedValue([{ filename: '.github/CODEOWNERS', status: 'modified' }]),
    rest: {
      pulls: { listFiles: vi.fn() },
      repos: {
        getContent: vi.fn().mockResolvedValue({ data: { type: 'file', size: 100 } }),
        codeownersErrors: vi.fn().mockResolvedValue({ data: { errors: [] } }),
      },
    },
  };
  const context = {
    repo: { owner: 'Azure', repo: 'azure-dev' },
    eventName: 'pull_request_target',
    sha: 'default-branch-commit',
    payload: {
      pull_request: {
        number: 42,
        changed_files: 1,
        head: { sha: 'head-commit', repo: { full_name: 'contributor/azure-dev' } },
        base: { sha: 'base-commit' },
      },
    },
  };
  const core = { info: vi.fn(), error: vi.fn(), setFailed: vi.fn() };
  return {
    github,
    context,
    core,
    run: () => runInNewContext(`(async () => {\n${script}\n})()`, { github, context, core }),
  };
}

describe('CODEOWNERS validation workflow', () => {
  it('uses the trusted target trigger instead of a PR-controlled workflow', () => {
    expect(workflow).toMatch(/^on:\n  pull_request_target:\n/m);
    expect(workflow).not.toMatch(/^  pull_request:/m);
  });

  it('succeeds without validating unrelated changes', async () => {
    const { github, core, run } = fixture();
    github.paginate.mockResolvedValue([{ filename: 'README.md' }]);

    await run();

    expect(core.info).toHaveBeenCalledWith('No CODEOWNERS changes.');
    expect(github.rest.repos.getContent).not.toHaveBeenCalled();
    expect(github.rest.repos.codeownersErrors).not.toHaveBeenCalled();
    expect(core.setFailed).not.toHaveBeenCalled();
  });

  it.each([
    { filename: '.github/CODEOWNERS', status: 'modified' },
    { filename: 'CODEOWNERS', status: 'added' },
    { filename: 'docs/CODEOWNERS', status: 'modified' },
    { filename: '.github/CODEOWNERS', status: 'removed' },
    { filename: 'archived-owners', previous_filename: '.github/CODEOWNERS', status: 'renamed' },
    { filename: '.github/CODEOWNERS', previous_filename: 'owners', status: 'renamed' },
  ])('validates $status changes involving $filename', async (file) => {
    const { github, context, core, run } = fixture();
    github.paginate.mockResolvedValue([file]);

    await run();

    expect(github.paginate).toHaveBeenCalledWith(github.rest.pulls.listFiles, {
      ...context.repo, pull_number: 42, per_page: 100,
    });
    expect(github.rest.repos.getContent).toHaveBeenCalledExactlyOnceWith({
      ...context.repo, ref: 'head-commit', path: '.github/CODEOWNERS',
    });
    expect(github.rest.repos.codeownersErrors).toHaveBeenCalledExactlyOnceWith({
      ...context.repo, ref: 'head-commit',
    });
    expect(core.info).toHaveBeenCalledWith('No CODEOWNERS errors reported by GitHub.');
    expect(core.setFailed).not.toHaveBeenCalled();
  });

  it('does not report an incomplete changed-file list as an unrelated PR', async () => {
    const { github, context, run } = fixture();
    context.payload.pull_request.changed_files = 3001;
    github.paginate.mockResolvedValue(Array.from({ length: 3000 }, (_, i) => ({ filename: `file-${i}` })));

    await expect(run()).rejects.toThrow('incomplete changed-file list');
    expect(github.rest.repos.codeownersErrors).not.toHaveBeenCalled();
  });

  it('checks the size of the first supported file, skipping missing paths and directories', async () => {
    const { github, core, run } = fixture();
    github.paginate.mockResolvedValue([{ filename: '.github/CODEOWNERS', status: 'removed' }]);
    github.rest.repos.getContent
      .mockRejectedValueOnce({ status: 404 })
      .mockResolvedValueOnce({ data: [] })
      .mockResolvedValueOnce({ data: { type: 'file', size: 3 * 1024 * 1024 + 1 } });

    await run();

    expect(github.rest.repos.getContent.mock.calls.map(([request]) => request.path)).toEqual([
      '.github/CODEOWNERS', 'CODEOWNERS', 'docs/CODEOWNERS',
    ]);
    expect(core.setFailed).toHaveBeenCalledWith("docs/CODEOWNERS exceeds GitHub's 3 MiB limit and cannot be validated.");
  });

  it('rejects oversized files even when the native API reports no errors', async () => {
    const { github, core, run } = fixture();
    github.rest.repos.getContent.mockResolvedValue({ data: { type: 'file', size: 3 * 1024 * 1024 + 1 } });

    await run();

    expect(github.rest.repos.codeownersErrors).toHaveBeenCalledOnce();
    expect(core.setFailed).toHaveBeenCalledWith(expect.stringContaining("exceeds GitHub's 3 MiB limit"));
    expect(core.info).not.toHaveBeenCalled();
  });

  it('allows a CODEOWNERS file exactly at the native size limit', async () => {
    const { github, core, run } = fixture();
    github.rest.repos.getContent.mockResolvedValue({ data: { type: 'file', size: 3 * 1024 * 1024 } });

    await run();

    expect(github.rest.repos.codeownersErrors).toHaveBeenCalledOnce();
    expect(core.setFailed).not.toHaveBeenCalled();
  });

  it('annotates every GitHub error with its path, line, column and kind, then fails', async () => {
    const { github, core, run } = fixture();
    const errors = [
      { path: '.github/CODEOWNERS', line: 2, column: 1, kind: 'Invalid pattern', message: 'Invalid pattern on line 2.' },
      { path: '.github/CODEOWNERS', line: 7, column: 12, kind: 'Unknown owner', message: 'Confirm the owner has write access.' },
    ];
    github.rest.repos.codeownersErrors.mockResolvedValue({ data: { errors } });

    await run();

    expect(core.error).toHaveBeenCalledTimes(2);
    expect(core.error).toHaveBeenNthCalledWith(1, 'Invalid pattern on line 2.', {
      file: '.github/CODEOWNERS', startLine: 2, startColumn: 1, title: 'Invalid pattern',
    });
    expect(core.error).toHaveBeenNthCalledWith(
      2,
      'Confirm the owner has write access.\n\n' +
      'Owners who need write access should request membership in @Azure/azure-dev-write.',
      { file: '.github/CODEOWNERS', startLine: 7, startColumn: 12, title: 'Unknown owner' },
    );
    expect(core.setFailed).toHaveBeenCalledWith(
      'GitHub reported 2 CODEOWNERS error(s). Fix the annotated entries.',
    );
    expect(github.rest.repos.getContent).not.toHaveBeenCalled();
    expect(core.info).not.toHaveBeenCalled();
  });

  it('propagates native validation failures, including a missing CODEOWNERS file', async () => {
    const { github, run } = fixture();
    const error = Object.assign(new Error('Not Found'), { status: 404 });
    github.rest.repos.codeownersErrors.mockRejectedValue(error);

    await expect(run()).rejects.toBe(error);
    expect(github.rest.repos.getContent).not.toHaveBeenCalled();
  });

  it('propagates content API failures other than missing files', async () => {
    const { github, run } = fixture();
    const error = Object.assign(new Error('Content request failed'), { status: 403 });
    github.rest.repos.getContent.mockRejectedValue(error);

    await expect(run()).rejects.toBe(error);
  });

  it('propagates failures while listing changed files', async () => {
    const { github, run } = fixture();
    const error = new Error('Could not list changed files');
    github.paginate.mockRejectedValue(error);

    await expect(run()).rejects.toBe(error);
    expect(github.rest.repos.getContent).not.toHaveBeenCalled();
  });

  it('rejects an unexpected validation response rather than reporting success', async () => {
    const { github, run } = fixture();
    github.rest.repos.codeownersErrors.mockResolvedValue({ data: {} });

    await expect(run()).rejects.toThrow('invalid CODEOWNERS validation response');
  });

  it('does not report success if the file size cannot be checked', async () => {
    const { github, run } = fixture();
    github.rest.repos.getContent.mockRejectedValue({ status: 404 });

    await expect(run()).rejects.toThrow('Could not read CODEOWNERS to verify the file size.');
  });
});
