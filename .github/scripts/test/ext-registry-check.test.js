import { execFileSync } from 'node:child_process';
import { describe, it, expect, vi } from 'vitest';
import run from '../ext-registry-check.js';

/**
 * @typedef {typeof import('@actions/github').context} Context
 * @typedef {ReturnType<typeof import('@actions/github').getOctokit>} Octokit
 * @typedef {typeof import('@actions/core')} Core
 *
 * @typedef {object} Provider
 * @property {string} name
 * @property {string} type
 * @property {string} [description]
 *
 * @typedef {object} ExtensionVersion
 * @property {string} version
 * @property {string[]} [capabilities]
 * @property {Provider[]} [providers]
 * @property {Record<string, unknown>} [artifacts]
 * @property {string} [usage]
 *
 * @typedef {object} Extension
 * @property {string} id
 * @property {string} [namespace]
 * @property {string} [displayName]
 * @property {string} [description]
 * @property {ExtensionVersion[]} versions
 *
 * @typedef {object} RegistryJson
 * @property {Extension[]} extensions
 */

const {
  diffRegistry,
  isAllowedRegistryJsonUpdate,
} = run.forTests;

/**
 * @param {object} [opts]
 * @param {string[]} [opts.capabilities]
 * @param {{ name: string, type: string, description?: string }[]} [opts.providers]
 * @param {string} [opts.version]
 * @returns {ExtensionVersion}
 */
function version({ version = '1.0.0', capabilities = ['custom-commands'], providers = [{ name: 'p', type: 'service-target' }] } = {}) {
  return { version, capabilities, providers, artifacts: {} };
}

/**
 * @param {object} [opts]
 * @param {string} [opts.id]
 * @param {ExtensionVersion[]} [opts.versions]
 * @returns {Extension}
 */
function extension({ id = 'ext.one', versions = [version()] } = {}) {
  return { id, namespace: 'ns', displayName: 'Ext One', description: 'desc', versions };
}

/**
 * @param {Extension[]} extensions
 * @returns {RegistryJson}
 */
function registry(extensions) {
  return { extensions };
}

describe('diffRegistry', () => {
  it('approves an identical registry (no changes)', () => {
    const base = registry([extension()]);
    const pr = registry([extension()]);
    expect(diffRegistry(base, pr)).toEqual([]);
  });

  it('approves adding a new release with the same capabilities and providers', () => {
    const base = registry([extension({ versions: [version({ version: '1.0.0' })] })]);
    const pr = registry([
      extension({ versions: [version({ version: '1.0.0' }), version({ version: '1.1.0' })] }),
    ]);
    expect(diffRegistry(base, pr)).toEqual([]);
  });

  it('approves a new release when only a provider description changes (cosmetic)', () => {
    const base = registry([
      extension({ versions: [version({ version: '1.0.0', providers: [{ name: 'p', type: 'service-target', description: 'a' }] })] }),
    ]);
    const pr = registry([
      extension({
        versions: [
          version({ version: '1.0.0', providers: [{ name: 'p', type: 'service-target', description: 'a' }] }),
          version({ version: '1.1.0', providers: [{ name: 'p', type: 'service-target', description: 'b (reworded)' }] }),
        ],
      }),
    ]);
    expect(diffRegistry(base, pr)).toEqual([]);
  });

  it('approves extension display metadata changes', () => {
    const base = registry([extension({ id: 'ext.one' })]);
    const pr = registry([{ ...extension({ id: 'ext.one' }), displayName: 'Renamed', description: 'Updated copy' }]);
    expect(diffRegistry(base, pr)).toEqual([]);
  });

  it('fails when a brand new extension is added', () => {
    const base = registry([extension({ id: 'ext.one' })]);
    const pr = registry([extension({ id: 'ext.one' }), extension({ id: 'ext.two' })]);
    const reasons = diffRegistry(base, pr);
    expect(reasons).not.toEqual([]);
    expect(reasons).toContainEqual(expect.stringContaining("'ext.two' is new"));
  });

  it('fails when an existing extension is removed', () => {
    const base = registry([extension({ id: 'ext.one' }), extension({ id: 'ext.two' })]);
    const pr = registry([extension({ id: 'ext.one' })]);
    const reasons = diffRegistry(base, pr);
    expect(reasons).not.toEqual([]);
    expect(reasons).toContainEqual(expect.stringContaining("'ext.two' was removed"));
  });

  it('fails when a new release changes capabilities', () => {
    const base = registry([extension({ versions: [version({ version: '1.0.0', capabilities: ['custom-commands'] })] })]);
    const pr = registry([
      extension({
        versions: [
          version({ version: '1.0.0', capabilities: ['custom-commands'] }),
          version({ version: '1.1.0', capabilities: ['custom-commands', 'lifecycle-events'] }),
        ],
      }),
    ]);
    const reasons = diffRegistry(base, pr);
    expect(reasons).not.toEqual([]);
    expect(reasons).toContainEqual(expect.stringContaining('changes capabilities'));
  });

  it('fails when a new release changes providers (name or type)', () => {
    const base = registry([extension({ versions: [version({ version: '1.0.0', providers: [{ name: 'p', type: 'service-target' }] })] })]);
    const pr = registry([
      extension({
        versions: [
          version({ version: '1.0.0', providers: [{ name: 'p', type: 'service-target' }] }),
          version({ version: '1.1.0', providers: [{ name: 'p', type: 'host' }] }),
        ],
      }),
    ]);
    const reasons = diffRegistry(base, pr);
    expect(reasons).not.toEqual([]);
    expect(reasons).toContainEqual(expect.stringContaining('changes providers'));
  });

  it('uses the latest semver release as the baseline for new release capability checks', () => {
    const base = registry([
      extension({
        versions: [
          version({ version: '2.0.0', capabilities: ['custom-commands', 'lifecycle-events'] }),
          version({ version: '1.9.0', capabilities: ['custom-commands'] }),
        ],
      }),
    ]);
    const pr = registry([
      extension({
        versions: [
          version({ version: '2.0.0', capabilities: ['custom-commands', 'lifecycle-events'] }),
          version({ version: '1.9.0', capabilities: ['custom-commands'] }),
          version({ version: '2.1.0', capabilities: ['custom-commands', 'lifecycle-events'] }),
        ],
      }),
    ]);

    expect(diffRegistry(base, pr)).toEqual([]);
  });

  it('treats registry versions as oldest-to-newest semver order, including prerelease labels', () => {
    const base = registry([
      extension({
        id: 'azure.ai.agents',
        versions: [
          version({ version: '0.1.9-preview', capabilities: ['custom-commands'] }),
          version({ version: '0.1.10-preview', capabilities: ['custom-commands', 'lifecycle-events'] }),
        ],
      }),
      extension({
        id: 'microsoft.foundry',
        versions: [
          version({ version: '1.0.0-beta.2', capabilities: ['custom-commands'] }),
          version({ version: '1.0.0-beta.3', capabilities: ['custom-commands', 'lifecycle-events'] }),
        ],
      }),
    ]);
    const pr = registry([
      extension({
        id: 'azure.ai.agents',
        versions: [
          version({ version: '0.1.9-preview', capabilities: ['custom-commands'] }),
          version({ version: '0.1.10-preview', capabilities: ['custom-commands', 'lifecycle-events'] }),
          version({ version: '0.1.11-preview', capabilities: ['custom-commands', 'lifecycle-events'] }),
        ],
      }),
      extension({
        id: 'microsoft.foundry',
        versions: [
          version({ version: '1.0.0-beta.2', capabilities: ['custom-commands'] }),
          version({ version: '1.0.0-beta.3', capabilities: ['custom-commands', 'lifecycle-events'] }),
          version({ version: '1.0.0-beta.4', capabilities: ['custom-commands', 'lifecycle-events'] }),
        ],
      }),
    ]);

    expect(diffRegistry(base, pr)).toEqual([]);
  });

  it('fails when an already-published release is modified', () => {
    const base = registry([extension({ versions: [version({ version: '1.0.0', capabilities: ['custom-commands'] })] })]);
    const pr = registry([extension({ versions: [version({ version: '1.0.0', capabilities: ['something-else'] })] })]);
    const reasons = diffRegistry(base, pr);
    expect(reasons).not.toEqual([]);
    expect(reasons).toContainEqual(expect.stringContaining("release '1.0.0' was modified"));
  });

  it('fails when an already-published release changes providers', () => {
    const base = registry([extension({ versions: [version({ version: '1.0.0', providers: [{ name: 'p', type: 'service-target' }] })] })]);
    const pr = registry([extension({ versions: [version({ version: '1.0.0', providers: [{ name: 'p', type: 'host' }] })] })]);
    const reasons = diffRegistry(base, pr);
    expect(reasons).not.toEqual([]);
    expect(reasons).toContainEqual(expect.stringContaining("release '1.0.0' changes providers"));
  });

  it('fails when an already-published release is removed', () => {
    const base = registry([
      extension({ versions: [version({ version: '1.0.0' }), version({ version: '1.1.0' })] }),
    ]);
    const pr = registry([extension({ versions: [version({ version: '1.1.0' })] })]);
    const reasons = diffRegistry(base, pr);
    expect(reasons).not.toEqual([]);
    expect(reasons).toContainEqual(expect.stringContaining("release '1.0.0' was removed"));
  });

  it('fails when an extension namespace changes', () => {
    const base = registry([extension({ id: 'ext.one' })]);
    const pr = registry([{ ...extension({ id: 'ext.one' }), namespace: 'other' }]);
    const reasons = diffRegistry(base, pr);
    expect(reasons).not.toEqual([]);
    expect(reasons).toContainEqual(expect.stringContaining('namespace changed'));
  });

  it('fails when an existing release metadata field changes', () => {
    const base = registry([extension({ versions: [version({ version: '1.0.0' })] })]);
    const pr = registry([
      extension({
        versions: [{ ...version({ version: '1.0.0' }), usage: 'azd ext <command>' }],
      }),
    ]);
    const reasons = diffRegistry(base, pr);

    expect(reasons).not.toEqual([]);
    expect(reasons).toContainEqual(expect.stringContaining("release '1.0.0' was modified"));
  });
});

describe('isAllowedRegistryJsonUpdate', () => {
  it('loads main and PR registry.json and applies the registry policy', async () => {
    const base = registry([extension({ id: 'ext.one' })]);
    const pr = registry([{ ...extension({ id: 'ext.one' }), namespace: 'other' }]);
    const octokit = createRegistryOctokit({ base, pr });
    const context = createRegistryContext();

    await expect(isAllowedRegistryJsonUpdate({ octokit, context })).resolves.toContainEqual(expect.stringContaining('namespace changed'));
    expect(octokit.rest.repos.getContent).toHaveBeenCalledWith(expect.objectContaining({
      owner: 'Azure',
      repo: 'azure-dev',
      ref: 'main',
    }));
    expect(octokit.rest.repos.getContent).toHaveBeenCalledWith(expect.objectContaining({
      owner: 'fork-owner',
      repo: 'azure-dev-fork',
      ref: 'abc123',
    }));
  });

  it('can load the base registry from a supplied commit-ish', async () => {
    const base = registry([extension({ id: 'ext.one' })]);
    const pr = registry([{ ...extension({ id: 'ext.one' }), namespace: 'other' }]);
    const octokit = createRegistryOctokit({ base, pr });
    const context = createRegistryContext();

    await expect(isAllowedRegistryJsonUpdate({
      octokit,
      context,
      registryBaseRef: 'base-before-pr',
    })).resolves.toContainEqual(expect.stringContaining('namespace changed'));
    expect(octokit.rest.repos.getContent).toHaveBeenCalledWith(expect.objectContaining({
      owner: 'Azure',
      repo: 'azure-dev',
      ref: 'base-before-pr',
    }));
  });

  it('requires review when an existing release changes capabilities', async () => {
    const base = registry([extension({ id: 'ext.one', versions: [version({ capabilities: ['custom-commands'] })] })]);
    const pr = registry([extension({ id: 'ext.one', versions: [version({ capabilities: ['lifecycle-events'] })] })]);
    const octokit = createRegistryOctokit({ base, pr });

    const reasons = await isAllowedRegistryJsonUpdate({
      octokit,
      context: createRegistryContext(),
    });

    expect(reasons).toContainEqual(expect.stringContaining('changes capabilities'));
  });
});

describe('run', () => {
  it('fails fast when an empty core team is injected', async () => {
    const core = createNoopCore();

    await run({
      github: createRegistryOctokit({ base: registry([]), pr: registry([]) }),
      context: createRegistryContext(),
      core,
      coreTeam: new Set(),
    });

    expect(core.setFailed).toHaveBeenCalledWith(expect.stringContaining('Invalid parameter - coreteam must be populated'));
  });

  it('allows a simple registry-only PR without core team review', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      base: registry([extension({ versions: [version({ version: '1.0.0' })] })]),
      pr: registry([extension({ versions: [version({ version: '1.0.0' }), version({ version: '1.1.0' })] })]),
    });

    await run({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).not.toHaveBeenCalled();
    expect(octokit.paginate).toHaveBeenCalledWith(octokit.rest.pulls.listFiles, expect.objectContaining({
      pull_number: 1,
    }));
  });

  it('skips changed-file review when a core team member authored the PR', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      base: registry([extension()]),
      pr: registry([extension()]),
      files: [
        { filename: 'cli/azd/extensions/registry.json' },
        { filename: 'cli/azd/extensions/README.md' },
      ],
    });
    const context = createRegistryContext({ author: 'core-member' });

    await run({
      github: octokit,
      context,
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).not.toHaveBeenCalled();
    expect(octokit.paginate).not.toHaveBeenCalled();
    expect(octokit.rest.repos.getContent).not.toHaveBeenCalled();
  });

  it('uses the pull request base sha as the registry comparison base by default', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({ base: registry([extension()]), pr: registry([extension()]) });
    const context = createRegistryContext();

    await run({
      github: octokit,
      context,
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).not.toHaveBeenCalled();
    expect(octokit.rest.repos.getContent).toHaveBeenCalledWith(expect.objectContaining({
      owner: 'Azure',
      repo: 'azure-dev',
      ref: 'base-before-pr',
    }));
  });

  it('requires review when the PR changes files outside registry.json', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      base: registry([extension()]),
      pr: registry([extension()]),
      files: [
        { filename: 'cli/azd/extensions/registry.json' },
        { filename: 'cli/azd/extensions/README.md' },
      ],
    });

    await run({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).toHaveBeenCalledWith(expect.stringContaining('files outside cli/azd/extensions/registry.json'));
    expect(core.setFailed).toHaveBeenCalledWith(expect.stringContaining('cli/azd/extensions/README.md'));
  });
});

/**
 * @param {{ base: RegistryJson, pr: RegistryJson, files?: { filename: string, previous_filename?: string }[] }} args
 * @returns {Octokit}
 */
function createRegistryOctokit({ base, pr, files = [{ filename: 'cli/azd/extensions/registry.json' }] }) {
  const octokit = {
    rest: {
      pulls: {
        listReviews: vi.fn(),
        listFiles: vi.fn(),
      },
      repos: {
        getContent: vi.fn(({ ref }) => Promise.resolve({
          data: JSON.stringify(ref === 'abc123' ? pr : base),
        })),
      },
    },
    paginate: vi.fn((endpoint) => {
      if (endpoint === octokit.rest.pulls.listFiles) {
        return Promise.resolve(files);
      }

      return Promise.resolve([]);
    }),
  };

  return /** @type {Octokit} */ (/** @type {unknown} */ (octokit));
}

/**
 * @param {object} [opts]
 * @param {string} [opts.author]
 * @returns {Context}
 */
function createRegistryContext({ author = 'contributor' } = {}) {
  return /** @type {Context} */ (/** @type {unknown} */ ({
    repo: { owner: 'Azure', repo: 'azure-dev' },
    payload: {
      pull_request: {
        number: 1,
        base: { sha: 'base-before-pr' },
        head: {
          sha: 'abc123',
          repo: {
            name: 'azure-dev-fork',
            owner: { login: 'fork-owner' },
          },
        },
        user: { login: author, id: 1, type: 'User' },
      },
    },
  }));
}

const LIVE_TEST_OWNER = 'Azure';
const LIVE_TEST_REPO = 'azure-dev';
const RUN_LIVE_TESTS = process.env['RUN_LIVE_TESTS'] === '1';
const liveDescribe = RUN_LIVE_TESTS ? describe : describe.skip;

if (!RUN_LIVE_TESTS) {
  process.stderr.write(
    `[live] Skipping live PR scenario test(s). ` +
    `Set RUN_LIVE_TESTS=1 to run them against ${LIVE_TEST_OWNER}/${LIVE_TEST_REPO}:\n` +
    '\n'
  );
}

function getLiveGithubToken() {
  const envToken = process.env['GH_TOKEN'] || process.env['GITHUB_TOKEN'];

  if (envToken) return envToken;

  try {
    return execFileSync('gh', ['auth', 'token'], {
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
    }).trim();
  } catch {
    return '';
  }
}

async function createLiveOctokit() {
  const token = getLiveGithubToken();

  if (!token) {
    throw new Error('[live] tests require GH_TOKEN, GITHUB_TOKEN, or `gh auth token`');
  }

  const { getOctokit } = await import('@actions/github');
  return getOctokit(token);
}

/**
 * @param {Octokit} octokit
 * @param {number} prNumber
 * @returns {Promise<Context>}
 */
async function createLiveContext(octokit, prNumber) {
  const { data: pr } = await octokit.rest.pulls.get({
    owner: LIVE_TEST_OWNER,
    repo: LIVE_TEST_REPO,
    pull_number: prNumber,
  });

  return /** @type {Context} */ (/** @type {unknown} */ ({
    repo: { owner: LIVE_TEST_OWNER, repo: LIVE_TEST_REPO },
    payload: {
      pull_request: {
        number: pr.number,
        head: {
          sha: pr.head.sha,
          repo: {
            name: pr.head.repo?.name,
            owner: {
              login: pr.head.repo?.owner?.login,
            },
          },
        },
        user: {
          id: pr.user?.id,
          type: pr.user?.type,
          login: pr.user?.login,
        },
      },
    },
  }));
}

// these tests do some read-only checking against real PRs in GitHub. Use this is
// if you're just not sure if we're doing the right kind of mocking above and need
// to try against the real deal, with a real octokit instance.
liveDescribe('[live] registry diff PR scenarios', () => {
  /**
   * Returns the base-branch commit to compare against for the live PR sample.
    * Live samples are intentionally limited to closed-unmerged PRs and
    * squash-merged PRs.
   *
   * @param {Octokit} octokit
   * @param {number} prNumber
   * @returns {Promise<string>}
   */
  async function getLiveRegistryBaseRef(octokit, prNumber) {
    const { data: pr } = await octokit.rest.pulls.get({
      owner: LIVE_TEST_OWNER,
      repo: LIVE_TEST_REPO,
      pull_number: prNumber,
    });

    if (pr.state !== 'closed') {
      throw new Error(`Live PR sample ${prNumber} must be closed or merged`);
    }

    if (pr.merged_at == null) {
      if (!pr.base.sha) {
        throw new Error(`Unable to determine the base commit for PR ${prNumber}`);
      }

      return pr.base.sha;
    }

    if (!pr.merge_commit_sha) {
      throw new Error(`Unable to determine the squash merge commit for PR ${prNumber}`);
    }

    const { data: mergeCommit } = await octokit.rest.repos.getCommit({
      owner: LIVE_TEST_OWNER,
      repo: LIVE_TEST_REPO,
      ref: pr.merge_commit_sha,
    });

    if (mergeCommit.parents.length !== 1) {
      throw new Error(`Live PR sample ${prNumber} must be squash-merged`);
    }

    const parent = mergeCommit.parents[0];
    if (!parent?.sha) {
      throw new Error(`Unable to determine the base commit before PR ${prNumber}`);
    }

    return parent.sha;
  }


  /**
   * @param {{ number: number, noReviewRequired: boolean, coreTeam?: Set<string> }} sample
   */
  async function runTestAgainstLivePr(sample) {
    const octokit = await createLiveOctokit();
    const context = await createLiveContext(octokit, sample.number);
    const registryBaseRef = await getLiveRegistryBaseRef(octokit, sample.number);
    const core = createNoopCore();

    if (sample.coreTeam) {
      await run({ github: octokit, context, core, coreTeam: sample.coreTeam, registryBaseRef });
    } else {
      await run({ github: octokit, context, core, registryBaseRef });
    }

    if (sample.noReviewRequired) {
      expect(core.setFailed).not.toHaveBeenCalled();
    } else {
      expect(core.setFailed).toHaveBeenCalledWith(expect.stringContaining('requires core team review'));
    }
  }

  // NOTE: some of these are just PRs, not even release PRs, but they have the right metadata.

  describe("core approval bypass", () => {
    // https://github.com/Azure/azure-dev/pull/9027
    it('[live] PR 9027 => core team member is the author', async () => {
      await runTestAgainstLivePr({ number: 9027, noReviewRequired: true });
    }, 90_000);

    // https://github.com/Azure/azure-dev/pull/8958
    it('[live] PR 8958 => core team member approved', async () => {
      await runTestAgainstLivePr({ number: 8958, noReviewRequired: true });
    }, 90_000);
  })

  // https://github.com/Azure/azure-dev/pull/8620
  it('[live] PR 8620 => no review required: registry diff allows unchanged extension declarations', async () => {
    await runTestAgainstLivePr({ number: 8620, noReviewRequired: true });
  }, 90_000);

  // https://github.com/Azure/azure-dev/pull/8958
  it('[live] PR 8958 => approval required because some registry metadata change without core approval', async () => {
    await runTestAgainstLivePr({ number: 8958, noReviewRequired: false, coreTeam: new Set(['the fakest developer ever']) });
  }, 90_000);

  // https://github.com/Azure/azure-dev/pull/8972
  it('[live] PR 8972 => core team approval required because the PR changes another file', async () => {
    await runTestAgainstLivePr({ number: 8972, noReviewRequired: false });
  }, 90_000);
});

/** @returns {Core} */
function createNoopCore() {
  const core = {
    info: vi.fn(),
    warning: vi.fn(),
    /** @param {string} message */
    setFailed: vi.fn(),
  };

  return /** @type {Core} */ (/** @type {unknown} */ (core));
}
