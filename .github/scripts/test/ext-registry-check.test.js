import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, it, expect, vi } from 'vitest';
import run from '../src/ext-registry-check.js';

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
 * @property {Object<string, { url: string }>} [artifacts]
 * @property {string} [usage]
 *
 * @typedef {object} Extension
 * @property {string} id
 * @property {string} [namespace]
 * @property {string} [displayName]
 * @property {string} [description]
 * @property {string[]} [tags]
 * @property {ExtensionVersion[]} versions
 *
 * @typedef {object} RegistryJson
 * @property {Extension[]} extensions
 */

const {
  diffRegistry,
  getCoreReviewers,
  isAllowedRegistryJsonUpdate,
  REGISTRY_JSON_PATHS,
} = run.forTests;

const PROD_REGISTRY_PATH = 'cli/azd/extensions/registry.json';
const DEV_REGISTRY_PATH = 'cli/azd/extensions/registry.dev.json';
const FIG_SPEC_SNAPSHOT_PATH = 'cli/azd/cmd/testdata/TestFigSpec.ts';
const REGISTRY_PATH_LIST = `${PROD_REGISTRY_PATH}, ${DEV_REGISTRY_PATH}`;

// The three commits the fixtures model: the PR head from the event payload, the base branch
// tip the merge preview was built from, and GitHub's synthetic merge commit.
const HEAD_SHA = 'abc123';
const MERGE_BASE_SHA = 'current-base';
const MERGE_RESULT_SHA = 'merge-result';

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
    expect(reasons).toContainEqual(expect.stringContaining('added: lifecycle-events'));
  });

  it('lists added and removed capabilities in review reasons', () => {
    const base = registry([extension({ versions: [version({ version: '1.0.0', capabilities: ['custom-commands', 'lifecycle-events'] })] })]);
    const pr = registry([
      extension({
        versions: [
          version({ version: '1.0.0', capabilities: ['custom-commands', 'lifecycle-events'] }),
          version({ version: '1.1.0', capabilities: ['resource-group'] }),
        ],
      }),
    ]);
    const reasons = diffRegistry(base, pr);

    expect(reasons).toContainEqual(expect.stringContaining('added: resource-group'));
    expect(reasons).toContainEqual(expect.stringContaining('removed: custom-commands, lifecycle-events'));
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
    expect(reasons).toContainEqual(expect.stringContaining('added: p (host)'));
    expect(reasons).toContainEqual(expect.stringContaining('removed: p (service-target)'));
  });

  it('lists added and removed providers in review reasons', () => {
    const base = registry([extension({
      versions: [version({
        version: '1.0.0', providers: [
          { name: 'p', type: 'service-target' },
          { name: 'old', type: 'host' },
        ]
      })]
    })]);
    const pr = registry([
      extension({
        versions: [
          version({
            version: '1.0.0', providers: [
              { name: 'p', type: 'service-target' },
              { name: 'old', type: 'host' },
            ]
          }),
          version({
            version: '1.1.0', providers: [
              { name: 'p', type: 'service-target' },
              { name: 'new', type: 'host' },
            ]
          }),
        ],
      }),
    ]);
    const reasons = diffRegistry(base, pr);

    expect(reasons).toContainEqual(expect.stringContaining('added: new (host)'));
    expect(reasons).toContainEqual(expect.stringContaining('removed: old (host)'));
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

  it('approves a GA release after beta releases for the same version', () => {
    const base = registry([
      extension({
        id: 'azure.ai.agents',
        versions: [
          version({ version: '1.0.0-beta.2', capabilities: ['custom-commands'] }),
          version({ version: '1.0.0-beta.3', capabilities: ['custom-commands'] }),
          version({ version: '1.0.0-beta.4', capabilities: ['custom-commands'] }),
        ],
      }),
    ]);
    const pr = registry([
      extension({
        id: 'azure.ai.agents',
        versions: [
          version({ version: '1.0.0-beta.2', capabilities: ['custom-commands'] }),
          version({ version: '1.0.0-beta.3', capabilities: ['custom-commands'] }),
          version({ version: '1.0.0-beta.4', capabilities: ['custom-commands'] }),
          version({ version: '1.0.0', capabilities: ['custom-commands'] }),
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
    expect(reasons).toContainEqual(expect.stringContaining('changes metadata that requires core review'));
    expect(reasons).toContainEqual(expect.stringContaining('namespace'));
  });

  it('approves extension tag changes', () => {
    const base = registry([{ ...extension({ id: 'ext.one' }), tags: ['ai'] }]);
    const pr = registry([{ ...extension({ id: 'ext.one' }), tags: ['ai', 'foundry'] }]);
    expect(diffRegistry(base, pr)).toEqual([]);
  });

  it('fails when any non-allowlisted extension metadata changes', () => {
    const base = registry([extension({ id: 'ext.one' })]);
    const pr = registry([/** @type {Extension} */ ({ ...extension({ id: 'ext.one' }), platform: 'windows' })]);
    const reasons = diffRegistry(base, pr);
    expect(reasons).not.toEqual([]);
    expect(reasons).toContainEqual(expect.stringContaining('changes metadata that requires core review'));
    expect(reasons).toContainEqual(expect.stringContaining('platform'));
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

  it('throws when the PR registry has duplicate extension ids', () => {
    const base = registry([extension({ id: 'ext.one' })]);
    const pr = registry([extension({ id: 'ext.one' }), extension({ id: 'ext.one' })]);
    expect(() => diffRegistry(base, pr)).toThrow('duplicate key: ext.one');
  });

  it('throws when the base registry has duplicate extension ids', () => {
    const base = registry([extension({ id: 'ext.one' }), extension({ id: 'ext.one' })]);
    const pr = registry([extension({ id: 'ext.one' })]);
    expect(() => diffRegistry(base, pr)).toThrow('duplicate key: ext.one');
  });

  it('throws when an extension has duplicate version entries', () => {
    const base = registry([extension({ id: 'ext.one', versions: [version({ version: '1.0.0' })] })]);
    const pr = registry([
      extension({ id: 'ext.one', versions: [version({ version: '1.0.0' }), version({ version: '1.0.0' })] }),
    ]);
    expect(() => diffRegistry(base, pr)).toThrow('duplicate key: 1.0.0');
  });

  it('fails when a new release artifact URL is hosted outside the azure-dev releases location', () => {
    const base = registry([extension({ versions: [version({ version: '1.0.0' })] })]);
    const pr = registry([
      extension({
        versions: [
          version({ version: '1.0.0' }),
          { ...version({ version: '1.1.0' }), artifacts: { 'linux/amd64': { url: 'https://evil.example.com/x.zip' } } },
        ],
      }),
    ]);
    const reasons = diffRegistry(base, pr);
    expect(reasons).not.toEqual([]);
    expect(reasons).toContainEqual(expect.stringContaining('has a URL outside https://github.com/Azure/azure-dev/releases/download/'));
  });

  it('fails when a new release artifact URL resolves outside the azure-dev releases location', () => {
    const base = registry([extension({ versions: [version({ version: '1.0.0' })] })]);
    const pr = registry([
      extension({
        versions: [
          version({ version: '1.0.0' }),
          { ...version({ version: '1.1.0' }), artifacts: { 'linux/amd64': { url: 'https://github.com/Azure/azure-dev/releases/../../../attacker/repo/releases/download/v1/x.zip' } } },
        ],
      }),
    ]);
    const reasons = diffRegistry(base, pr);
    expect(reasons).not.toEqual([]);
    expect(reasons).toContainEqual(expect.stringContaining('has a URL outside https://github.com/Azure/azure-dev/releases/download/'));
  });

  it('fails when a new release artifact URL uses encoded paths (like %2f, etc..) to go outside the azure-dev releases location', () => {
    const base = registry([extension({ versions: [version({ version: '1.0.0' })] })]);
    const pr = registry([
      extension({
        versions: [
          version({ version: '1.0.0' }),
          { ...version({ version: '1.1.0' }), artifacts: { 'linux/amd64': { url: 'https://github.com/Azure/azure-dev/releases/download/..%2f..%2fattacker/repo/releases/download/v1/x.zip' } } },
        ],
      }),
    ]);
    const reasons = diffRegistry(base, pr);
    expect(reasons).not.toEqual([]);
    expect(reasons).toContainEqual(expect.stringContaining('has a URL outside https://github.com/Azure/azure-dev/releases/download/'));
  });

  it('fails when a new release artifact URL contains any percent-encoded characters', () => {
    const base = registry([extension({ versions: [version({ version: '1.0.0' })] })]);
    const pr = registry([
      extension({
        versions: [
          version({ version: '1.0.0' }),
          { ...version({ version: '1.1.0' }), artifacts: { 'linux/amd64': { url: 'https://github.com/Azure/azure-dev/releases/download/ext_1.1.0/file%41.zip' } } },
        ],
      }),
    ]);
    const reasons = diffRegistry(base, pr);
    expect(reasons).not.toEqual([]);
    expect(reasons).toContainEqual(expect.stringContaining('has a URL outside https://github.com/Azure/azure-dev/releases/download/'));
  });

  it('approves a new release whose artifact URLs are hosted under the azure-dev releases location', () => {
    const base = registry([extension({ versions: [version({ version: '1.0.0' })] })]);
    const pr = registry([
      extension({
        versions: [
          version({ version: '1.0.0' }),
          { ...version({ version: '1.1.0' }), artifacts: { 'linux/amd64': { url: 'https://github.com/Azure/azure-dev/releases/download/ext_1.1.0/x.zip' } } },
        ],
      }),
    ]);
    expect(diffRegistry(base, pr)).toEqual([]);
  });

  it('throws when a new release artifact is missing a URL', () => {
    const base = registry([extension({ versions: [version({ version: '1.0.0' })] })]);
    const pr = registry([
      extension({
        versions: [
          version({ version: '1.0.0' }),
          { ...version({ version: '1.1.0' }), artifacts: { 'linux/amd64': /** @type {any} */ ({ entryPoint: 'x' }) } },
        ],
      }),
    ]);
    expect(() => diffRegistry(base, pr)).toThrow("artifact 'linux/amd64' has no string URL (got undefined)");
  });

  it('fails when a registry-level metadata field changes', () => {
    const base = { ...registry([extension()]), schemaVersion: '1.0' };
    const pr = { ...registry([extension()]), schemaVersion: '2.0' };
    const reasons = diffRegistry(base, pr);
    expect(reasons).not.toEqual([]);
    expect(reasons).toContainEqual(expect.stringContaining('changes top-level metadata that requires core review'));
    expect(reasons).toContainEqual(expect.stringContaining('schemaVersion'));
  });

  it('fails when a new registry-level metadata field is added', () => {
    const base = registry([extension()]);
    const pr = { ...registry([extension()]), schemaVersion: '1.0' };
    const reasons = diffRegistry(base, pr);
    expect(reasons).not.toEqual([]);
    expect(reasons).toContainEqual(expect.stringContaining('changes top-level metadata that requires core review'));
    expect(reasons).toContainEqual(expect.stringContaining('schemaVersion'));
  });

  it('approves an identical registry that carries top-level metadata', () => {
    const base = { ...registry([extension()]), schemaVersion: '1.0' };
    const pr = { ...registry([extension()]), schemaVersion: '1.0' };
    expect(diffRegistry(base, pr)).toEqual([]);
  });

  it('fails when more than one new release is added at once', () => {
    const base = registry([extension({ versions: [version({ version: '1.0.0' })] })]);
    const pr = registry([
      extension({
        versions: [
          version({ version: '1.0.0' }),
          version({ version: '1.1.0' }),
          version({ version: '1.2.0' }),
        ],
      }),
    ]);
    const reasons = diffRegistry(base, pr);
    expect(reasons).not.toEqual([]);
    expect(reasons).toContainEqual(expect.stringContaining('adds 2 new releases (1.1.0, 1.2.0)'));
    expect(reasons).toContainEqual(expect.stringContaining('only a single new release may be added without core review'));
  });

  it('fails when the new release is older than the current latest release', () => {
    const base = registry([extension({ versions: [version({ version: '2.0.0' })] })]);
    const pr = registry([
      extension({ versions: [version({ version: '2.0.0' }), version({ version: '1.9.0' })] }),
    ]);
    const reasons = diffRegistry(base, pr);
    expect(reasons).not.toEqual([]);
    expect(reasons).toContainEqual(expect.stringContaining("release '1.9.0' is not newer than the current latest release '2.0.0'"));
  });
});

describe('isAllowedRegistryJsonUpdate', () => {
  it('loads the base and proposed registry.json and applies the registry policy', async () => {
    const base = registry([extension({ id: 'ext.one' })]);
    const pr = registry([{ ...extension({ id: 'ext.one' }), namespace: 'other' }]);
    const octokit = createRegistryOctokit({ base, pr });
    const context = createRegistryContext();

    await expect(isAllowedRegistryJsonUpdate({
      octokit,
      context,
      registryPath: PROD_REGISTRY_PATH,
      registryComparisonRefs: {
        baseRef: 'main',
        proposedRef: MERGE_RESULT_SHA,
      },
    })).resolves.toContainEqual(expect.stringContaining('changes metadata that requires core review'));
    expect(octokit.rest.repos.getContent).toHaveBeenCalledWith(expect.objectContaining({
      owner: 'Azure',
      repo: 'azure-dev',
      ref: 'main',
    }));
    expect(octokit.rest.repos.getContent).toHaveBeenCalledWith(expect.objectContaining({
      owner: 'Azure',
      repo: 'azure-dev',
      ref: MERGE_RESULT_SHA,
    }));
  });

  it('approves a release update when the base has an unrelated concurrently merged release', async () => {
    const base = registry([
      extension({
        id: 'ext.one',
        versions: [version({ version: '1.0.0' })],
      }),
      extension({
        id: 'ext.concurrent',
        versions: [version({ version: '2.0.0' })],
      }),
    ]);
    const merged = registry([
      extension({
        id: 'ext.one',
        versions: [
          version({ version: '1.0.0' }),
          version({ version: '1.1.0' }),
        ],
      }),
      extension({
        id: 'ext.concurrent',
        versions: [version({ version: '2.0.0' })],
      }),
    ]);
    const staleHead = registry([
      extension({
        id: 'ext.one',
        versions: [
          version({ version: '1.0.0' }),
          version({ version: '1.1.0' }),
        ],
      }),
    ]);
    const octokit = createRegistryOctokit({ base, pr: merged, head: staleHead });

    await expect(isAllowedRegistryJsonUpdate({
      octokit,
      context: createRegistryContext(),
      registryPath: PROD_REGISTRY_PATH,
      registryComparisonRefs: {
        baseRef: MERGE_BASE_SHA,
        proposedRef: MERGE_RESULT_SHA,
      },
    })).resolves.toEqual([]);
    expect(octokit.rest.repos.getContent).toHaveBeenCalledWith(expect.objectContaining({
      ref: MERGE_BASE_SHA,
    }));
    expect(octokit.rest.repos.getContent).toHaveBeenCalledWith(expect.objectContaining({
      ref: MERGE_RESULT_SHA,
    }));
  });

  it('requires review when the synthetic merge result removes a published release', async () => {
    const base = registry([
      extension({
        id: 'ext.one',
        versions: [
          version({ version: '1.0.0' }),
          version({ version: '1.1.0' }),
        ],
      }),
    ]);
    const merged = registry([
      extension({
        id: 'ext.one',
        versions: [version({ version: '1.0.0' })],
      }),
    ]);
    const octokit = createRegistryOctokit({ base, pr: merged });

    await expect(isAllowedRegistryJsonUpdate({
      octokit,
      context: createRegistryContext(),
      registryPath: PROD_REGISTRY_PATH,
      registryComparisonRefs: {
        baseRef: 'main',
        proposedRef: MERGE_RESULT_SHA,
      },
    })).resolves.toContainEqual(
      expect.stringContaining("release '1.1.0' was removed; published releases are immutable"),
    );
  });

  it('can load the base registry from a supplied commit-ish', async () => {
    const base = registry([extension({ id: 'ext.one' })]);
    const pr = registry([{ ...extension({ id: 'ext.one' }), namespace: 'other' }]);
    const octokit = createRegistryOctokit({ base, pr });
    const context = createRegistryContext();

    await expect(isAllowedRegistryJsonUpdate({
      octokit,
      context,
      registryPath: PROD_REGISTRY_PATH,
      registryComparisonRefs: {
        baseRef: 'base-before-pr',
        proposedRef: MERGE_RESULT_SHA,
      },
    })).resolves.toContainEqual(expect.stringContaining('changes metadata that requires core review'));
    expect(octokit.rest.repos.getContent).toHaveBeenCalledWith(expect.objectContaining({
      owner: 'Azure',
      repo: 'azure-dev',
      ref: 'base-before-pr',
    }));
  });

  it('loads and validates registry.dev.json when requested', async () => {
    const base = registry([extension({ id: 'ext.one' })]);
    const pr = registry([{ ...extension({ id: 'ext.one' }), namespace: 'other' }]);
    const octokit = createRegistryOctokit({ base, pr });

    await expect(isAllowedRegistryJsonUpdate({
      octokit,
      context: createRegistryContext(),
      registryPath: DEV_REGISTRY_PATH,
      registryComparisonRefs: {
        baseRef: 'main',
        proposedRef: MERGE_RESULT_SHA,
      },
    })).resolves.toContainEqual(expect.stringContaining('changes metadata that requires core review'));
    expect(octokit.rest.repos.getContent).toHaveBeenCalledWith(expect.objectContaining({
      path: 'cli/azd/extensions/registry.dev.json',
    }));
  });

  it('requires review when an existing release changes capabilities', async () => {
    const base = registry([extension({ id: 'ext.one', versions: [version({ capabilities: ['custom-commands'] })] })]);
    const pr = registry([extension({ id: 'ext.one', versions: [version({ capabilities: ['lifecycle-events'] })] })]);
    const octokit = createRegistryOctokit({ base, pr });

    const reasons = await isAllowedRegistryJsonUpdate({
      octokit,
      context: createRegistryContext(),
      registryPath: PROD_REGISTRY_PATH,
      registryComparisonRefs: {
        baseRef: 'main',
        proposedRef: MERGE_RESULT_SHA,
      },
    });

    expect(reasons).toContainEqual(expect.stringContaining('changes capabilities'));
  });
});

describe('run', () => {
  it('fails fast when an empty core review team is injected', async () => {
    const core = createNoopCore();

    await run({
      github: createRegistryOctokit({ base: registry([]), pr: registry([]) }),
      context: createRegistryContext(),
      core,
      coreTeam: new Set(),
    });

    expect(core.setFailed).toHaveBeenCalledWith(expect.stringContaining('Invalid parameter - coreteam must be populated'));
  });

  it('uses the hardcoded registry maintainer list when no core review team is injected', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      base: registry([extension()]),
      pr: registry([extension()]),
      files: [
        { filename: 'cli/azd/extensions/registry.json' },
        { filename: 'cli/azd/extensions/README.md' },
      ],
    });
    const context = createRegistryContext({ author: 'tg-msft' });

    await run({
      github: octokit,
      context,
      core,
    });

    expect(core.setFailed).not.toHaveBeenCalled();
    expect(octokit.paginate).not.toHaveBeenCalledWith(octokit.rest.pulls.listFiles, expect.anything());
  });

  it('allows a simple registry-only PR without core review', async () => {
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

  it('allows a simple registry.dev.json-only PR without core review', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      base: registry([extension({ versions: [version({ version: '1.0.0' })] })]),
      pr: registry([extension({ versions: [version({ version: '1.0.0' }), version({ version: '1.1.0' })] })]),
      files: [{ filename: 'cli/azd/extensions/registry.dev.json' }],
    });

    await run({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).not.toHaveBeenCalled();
    expect(octokit.rest.repos.getContent).toHaveBeenCalledWith(expect.objectContaining({
      path: 'cli/azd/extensions/registry.dev.json',
    }));
  });

  it('allows simple updates to both registries without core review', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      base: registry([extension({ versions: [version({ version: '1.0.0' })] })]),
      pr: registry([extension({ versions: [version({ version: '1.0.0' }), version({ version: '1.1.0' })] })]),
      files: [
        { filename: 'cli/azd/extensions/registry.json' },
        { filename: 'cli/azd/extensions/registry.dev.json' },
      ],
    });

    await run({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).not.toHaveBeenCalled();
    expect(octokit.rest.repos.getContent).toHaveBeenCalledWith(expect.objectContaining({
      path: 'cli/azd/extensions/registry.json',
    }));
    expect(octokit.rest.repos.getContent).toHaveBeenCalledWith(expect.objectContaining({
      path: 'cli/azd/extensions/registry.dev.json',
    }));
  });

  it('allows a simple registry PR that also updates the TestFigSpec snapshot', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      base: registry([extension({ versions: [version({ version: '1.0.0' })] })]),
      pr: registry([extension({ versions: [version({ version: '1.0.0' }), version({ version: '1.1.0' })] })]),
      files: [
        { filename: PROD_REGISTRY_PATH },
        { filename: FIG_SPEC_SNAPSHOT_PATH, status: 'modified' },
      ],
    });

    await run({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).not.toHaveBeenCalled();
  });

  it('requires review when a registry.dev.json-only PR updates the TestFigSpec snapshot', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      base: registry([extension({ versions: [version({ version: '1.0.0' })] })]),
      pr: registry([extension({ versions: [version({ version: '1.0.0' }), version({ version: '1.1.0' })] })]),
      files: [
        { filename: DEV_REGISTRY_PATH },
        { filename: FIG_SPEC_SNAPSHOT_PATH, status: 'modified' },
      ],
    });

    await run({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).toHaveBeenCalledWith(
      expect.stringContaining(`files outside the extension registries (${REGISTRY_PATH_LIST})`));
    expect(core.setFailed).toHaveBeenCalledWith(
      expect.stringContaining(FIG_SPEC_SNAPSHOT_PATH));
  });

  it('requires review for other non-registry files alongside TestFigSpec', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      base: registry([extension()]),
      pr: registry([extension()]),
      files: [
        { filename: PROD_REGISTRY_PATH },
        { filename: FIG_SPEC_SNAPSHOT_PATH, status: 'modified' },
        { filename: 'cli/azd/extensions/README.md' },
      ],
    });

    await run({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).toHaveBeenCalledWith(
      expect.stringContaining(`files outside the extension registries (${REGISTRY_PATH_LIST})`));
    expect(core.setFailed).toHaveBeenCalledWith(expect.stringContaining('cli/azd/extensions/README.md'));
    expect(core.setFailed).toHaveBeenCalledWith(
      expect.not.stringContaining(FIG_SPEC_SNAPSHOT_PATH));
  });

  it('requires review when the TestFigSpec snapshot is deleted', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      base: registry([extension({ versions: [version({ version: '1.0.0' })] })]),
      pr: registry([extension({ versions: [version({ version: '1.0.0' }), version({ version: '1.1.0' })] })]),
      files: [
        { filename: PROD_REGISTRY_PATH },
        { filename: FIG_SPEC_SNAPSHOT_PATH, status: 'removed' },
      ],
    });

    await run({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).toHaveBeenCalledWith(
      expect.stringContaining(`files outside the extension registries (${REGISTRY_PATH_LIST})`));
    expect(core.setFailed).toHaveBeenCalledWith(
      expect.stringContaining(FIG_SPEC_SNAPSHOT_PATH));
  });

  it('requires review when the TestFigSpec snapshot is renamed', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      base: registry([extension({ versions: [version({ version: '1.0.0' })] })]),
      pr: registry([extension({ versions: [version({ version: '1.0.0' }), version({ version: '1.1.0' })] })]),
      files: [
        { filename: PROD_REGISTRY_PATH },
        {
          filename: FIG_SPEC_SNAPSHOT_PATH,
          previous_filename: 'cli/azd/cmd/testdata/TestFigSpec.old.ts',
          status: 'renamed',
        },
      ],
    });

    await run({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).toHaveBeenCalledWith(
      expect.stringContaining(`files outside the extension registries (${REGISTRY_PATH_LIST})`));
    expect(core.setFailed).toHaveBeenCalledWith(
      expect.stringContaining(FIG_SPEC_SNAPSHOT_PATH));
  });

  it('skips changed-file review when a registry maintainer authored the PR', async () => {
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

  it('skips changed-file review when a registry maintainer approved the current head commit', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      base: registry([extension()]),
      pr: registry([extension()]),
      files: [
        { filename: 'cli/azd/extensions/registry.json' },
        { filename: 'cli/azd/extensions/README.md' },
      ],
      reviews: [
        { user: { login: 'core-member' }, state: 'APPROVED', commit_id: 'abc123' },
      ],
    });

    await run({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).not.toHaveBeenCalled();
    expect(octokit.paginate).not.toHaveBeenCalledWith(octokit.rest.pulls.listFiles, expect.anything());
    expect(octokit.rest.repos.getContent).not.toHaveBeenCalled();
  });

  it('requires review when a registry maintainer approval is for an older head commit', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      base: registry([extension()]),
      pr: registry([extension()]),
      files: [
        { filename: 'cli/azd/extensions/registry.json' },
        { filename: 'cli/azd/extensions/README.md' },
      ],
      reviews: [
        { user: { login: 'core-member' }, state: 'APPROVED', commit_id: 'older-commit' },
      ],
    });

    await run({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).toHaveBeenCalledWith(
      expect.stringContaining(`files outside the extension registries (${REGISTRY_PATH_LIST})`));
    expect(octokit.paginate).toHaveBeenCalledWith(octokit.rest.pulls.listFiles, expect.objectContaining({
      pull_number: 1,
    }));
  });

  it('skips changed-file review when a maintainer approved the head commit and later only commented', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      base: registry([extension()]),
      pr: registry([extension()]),
      files: [
        { filename: 'cli/azd/extensions/registry.json' },
        { filename: 'cli/azd/extensions/README.md' },
      ],
      // A trailing COMMENTED review is not a verdict and must not shadow the earlier approval.
      reviews: [
        { user: { login: 'core-member' }, state: 'APPROVED', commit_id: 'abc123' },
        { user: { login: 'core-member' }, state: 'COMMENTED', commit_id: 'abc123' },
      ],
    });

    await run({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).not.toHaveBeenCalled();
    expect(octokit.paginate).not.toHaveBeenCalledWith(octokit.rest.pulls.listFiles, expect.anything());
    expect(octokit.rest.repos.getContent).not.toHaveBeenCalled();
  });

  it('requires review when a maintainer approved the head commit and later requested changes', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      base: registry([extension()]),
      pr: registry([extension()]),
      files: [
        { filename: 'cli/azd/extensions/registry.json' },
        { filename: 'cli/azd/extensions/README.md' },
      ],
      reviews: [
        // basically, the user approved it, but then (on the same commit), requested changes. 
        // the ordering here will be correct, but we need to make sure we note that they are NOT approved.
        { user: { login: 'core-member' }, state: 'APPROVED', commit_id: 'abc123' },
        { user: { login: 'core-member' }, state: 'CHANGES_REQUESTED', commit_id: 'abc123' },
      ],
    });

    await run({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).toHaveBeenCalledWith(
      expect.stringContaining(`files outside the extension registries (${REGISTRY_PATH_LIST})`));
    expect(octokit.paginate).toHaveBeenCalledWith(octokit.rest.pulls.listFiles, expect.objectContaining({
      pull_number: 1,
    }));
  });

  it('skips changed-file review when a maintainer requested changes and then approved the head commit', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      base: registry([extension()]),
      pr: registry([extension()]),
      files: [
        { filename: 'cli/azd/extensions/registry.json' },
        { filename: 'cli/azd/extensions/README.md' },
      ],
      // Latest decisive review is the approval, so the change is bypassed.
      reviews: [
        { user: { login: 'core-member' }, state: 'CHANGES_REQUESTED', commit_id: 'abc123' },
        { user: { login: 'core-member' }, state: 'APPROVED', commit_id: 'abc123' },
      ],
    });

    await run({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).not.toHaveBeenCalled();
    expect(octokit.paginate).not.toHaveBeenCalledWith(octokit.rest.pulls.listFiles, expect.anything());
    expect(octokit.rest.repos.getContent).not.toHaveBeenCalled();
  });

  it('uses the merge preview base as the registry comparison base by default', async () => {
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
      ref: MERGE_BASE_SHA,
    }));
    expect(octokit.rest.repos.getContent).toHaveBeenCalledWith(expect.objectContaining({
      owner: 'Azure',
      repo: 'azure-dev',
      ref: MERGE_RESULT_SHA,
    }));
  });

  it('fails closed when GitHub returns a merge preview for an older PR head', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      base: registry([extension()]),
      pr: registry([extension()]),
      mergeHeadShas: ['older-head'],
    });

    await runWithoutRetryDelay({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).toHaveBeenCalledWith(
      expect.stringContaining(`GitHub's merge preview is stale for the current PR head`),
    );
    expect(octokit.rest.repos.getContent).not.toHaveBeenCalled();
  });

  it('retries until GitHub returns a merge preview for the current PR head', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      base: registry([extension()]),
      pr: registry([extension()]),
      mergeHeadShas: ['older-head', HEAD_SHA],
    });

    await runWithoutRetryDelay({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).not.toHaveBeenCalled();
    expect(octokit.rest.repos.getCommit).toHaveBeenCalledTimes(2);
  });

  it('reports how to recover when GitHub has no merge preview', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      base: registry([extension()]),
      pr: registry([extension()]),
      mergeError: new Error('Not Found'),
    });

    await runWithoutRetryDelay({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).toHaveBeenCalledWith(
      expect.stringContaining('Resolve any merge conflicts or re-run the check'),
    );
    expect(octokit.rest.repos.getContent).not.toHaveBeenCalled();
  });

  it('requires review when the PR changes files outside the extension registries', async () => {
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

    expect(core.setFailed).toHaveBeenCalledWith(
      expect.stringContaining(`files outside the extension registries (${REGISTRY_PATH_LIST})`));
    expect(core.setFailed).toHaveBeenCalledWith(expect.stringContaining('cli/azd/extensions/README.md'));
  });

  it('requires review when a file is renamed into a registry path', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      base: registry([extension()]),
      pr: registry([extension()]),
      files: [
        { filename: PROD_REGISTRY_PATH, previous_filename: 'cli/azd/extensions/registry.old.json', status: 'renamed' },
      ],
    });

    await run({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).toHaveBeenCalledWith(expect.stringContaining('renames extension registry files'));
    expect(core.setFailed).toHaveBeenCalledWith(
      expect.stringContaining(`cli/azd/extensions/registry.old.json -> ${PROD_REGISTRY_PATH}`));
  });

  it('requires review when a registry file is renamed away', async () => {
    const core = createNoopCore();
    // A registry renamed away no longer exists at either registry path, so no fixture is
    // configured - the mock throws if the policy tries to load one anyway.
    const octokit = createRegistryOctokit({
      registries: {},
      files: [
        { filename: 'cli/azd/extensions/registry.old.json', previous_filename: DEV_REGISTRY_PATH, status: 'renamed' },
      ],
    });

    await run({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).toHaveBeenCalledWith(expect.stringContaining('renames extension registry files'));
    expect(core.setFailed).toHaveBeenCalledWith(
      expect.stringContaining(`${DEV_REGISTRY_PATH} -> cli/azd/extensions/registry.old.json`));
    // The renamed registry must be reported as a rename, not as an unrelated outside file.
    expect(core.setFailed).not.toHaveBeenCalledWith(
      expect.stringContaining('files outside the extension registries'));
  });

  it('requires review with a policy message when a registry file is deleted', async () => {
    const core = createNoopCore();
    // A deleted registry has nothing to fetch at the PR head, so no fixture is configured -
    // the mock throws if the policy tries to load it anyway.
    const octokit = createRegistryOctokit({
      registries: {},
      files: [{ filename: DEV_REGISTRY_PATH, status: 'removed' }],
    });

    await run({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).toHaveBeenCalledWith(expect.stringContaining('deletes extension registry files'));
    expect(core.setFailed).toHaveBeenCalledWith(expect.stringContaining(DEV_REGISTRY_PATH));
    // A deletion must surface as policy guidance, not as an unhandled 404 from the registry fetch.
    expect(core.setFailed).not.toHaveBeenCalledWith(expect.stringContaining('Internal failure in script'));
    expect(octokit.rest.repos.getContent).not.toHaveBeenCalled();
  });

  it('evaluates each registry independently and names only the one that requires review', async () => {
    const core = createNoopCore();
    const octokit = createRegistryOctokit({
      registries: {
        // Prod registry: a simple, policy-valid new release.
        [PROD_REGISTRY_PATH]: {
          base: registry([extension({ versions: [version({ version: '1.0.0' })] })]),
          pr: registry([extension({ versions: [version({ version: '1.0.0' }), version({ version: '1.1.0' })] })]),
        },
        // Dev registry: a new release that changes capabilities, which requires core review.
        [DEV_REGISTRY_PATH]: {
          base: registry([extension({ versions: [version({ version: '1.0.0', capabilities: ['custom-commands'] })] })]),
          pr: registry([
            extension({
              versions: [
                version({ version: '1.0.0', capabilities: ['custom-commands'] }),
                version({ version: '1.1.0', capabilities: ['custom-commands', 'lifecycle-events'] }),
              ],
            }),
          ]),
        },
      },
      files: [
        { filename: PROD_REGISTRY_PATH },
        { filename: DEV_REGISTRY_PATH },
      ],
    });

    await run({
      github: octokit,
      context: createRegistryContext(),
      core,
      coreTeam: new Set(['core-member']),
    });

    expect(core.setFailed).toHaveBeenCalledWith(expect.stringContaining(`${DEV_REGISTRY_PATH}: `));
    expect(core.setFailed).not.toHaveBeenCalledWith(expect.stringContaining(`${PROD_REGISTRY_PATH}: `));
  });
});

describe('getCoreReviewers', () => {
  it('returns the hardcoded registry maintainer logins', () => {
    const core = createNoopCore();

    const reviewers = getCoreReviewers({ core });

    expect(reviewers).toBeInstanceOf(Set);
    expect(reviewers.size).toBeGreaterThan(0);
    expect(reviewers.has('tg-msft')).toBe(true);
    expect(core.info).toHaveBeenCalledWith(expect.stringContaining(`Loaded ${reviewers.size} registry maintainer(s)`));
  });
});

describe('REGISTRY_JSON_PATHS', () => {
  // The ext-registry-check workflow detects registry changes before it checks out the
  // repo, so it can't require this script and keeps its own copy of the path list.
  // A path added here but missed there fails open: the gated steps skip and the
  // required check reports green without the policy ever running.
  it('matches the inline registryPaths list in ext-registry-check.yml', () => {
    const workflowPath = join(__dirname, '..', '..', 'workflows', 'ext-registry-check.yml');
    const workflow = readFileSync(workflowPath, 'utf8');

    const setLiteral = /const registryPaths = new Set\(\[([^\]]*)\]\)/.exec(workflow);
    // Fail loudly rather than silently stop guarding if the literal is renamed or restructured.
    expect(setLiteral, 'could not find the `registryPaths` Set literal in ext-registry-check.yml').not.toBeNull();

    const workflowPaths = [...(setLiteral?.[1] ?? '').matchAll(/'([^']+)'/g)].map((match) => String(match[1]));
    expect(workflowPaths.length).toBeGreaterThan(0);
    expect(workflowPaths.sort()).toEqual([...REGISTRY_JSON_PATHS].sort());
  });
});

/**
 * @param {{
 *   base?: RegistryJson,
 *   pr?: RegistryJson,
 *   head?: RegistryJson,
 *   registries?: Object<string, { base: RegistryJson, pr: RegistryJson }>,
 *   files?: { filename: string, previous_filename?: string, status?: string }[],
 *   reviews?: { user: { login: string }, state: string, commit_id: string }[],
 *   mergeHeadShas?: string[],
 *   mergeError?: Error,
 * }} args
 * @returns {Octokit}
 */
function createRegistryOctokit({
  base,
  pr,
  head = pr,
  registries,
  files = [{ filename: PROD_REGISTRY_PATH }],
  reviews = [],
  mergeHeadShas = [HEAD_SHA],
  mergeError,
}) {
  let mergeAttempt = 0;
  // Per-path fixtures let a test drive each registry independently. When they're not
  // supplied, every registry path shares the same base/pr content.
  /** @type {Record<string, {
   *   base?: RegistryJson | undefined,
   *   pr?: RegistryJson | undefined,
   *   head?: RegistryJson | undefined,
   * }>} */
  const registriesByPath = registries ?? {
    [PROD_REGISTRY_PATH]: { base, pr, head },
    [DEV_REGISTRY_PATH]: { base, pr, head },
  };

  const octokit = {
    rest: {
      pulls: {
        listReviews: vi.fn(),
        listFiles: vi.fn(),
      },
      repos: {
        getCommit: vi.fn(() => {
          if (mergeError) {
            return Promise.reject(mergeError);
          }

          // Each call walks one step further into `mergeHeadShas`, sticking on the last
          // entry, so a test can model a merge preview that refreshes between attempts.
          const headParentSha = mergeHeadShas[Math.min(mergeAttempt++, mergeHeadShas.length - 1)];

          return Promise.resolve({
            data: {
              sha: MERGE_RESULT_SHA,
              parents: [{ sha: MERGE_BASE_SHA }, { sha: headParentSha }],
            },
          });
        }),
        getContent: vi.fn(({ path, ref }) => {
          const fixture = registriesByPath[path];
          if (fixture == null) {
            throw new Error(`No registry fixture configured for ${path}`);
          }

          // Three distinct states: the base branch tip, the (possibly stale) PR head, and
          // the merge result the check is supposed to evaluate.
          const registryForRef = ref === MERGE_RESULT_SHA
            ? fixture.pr
            : ref === HEAD_SHA
              ? fixture.head
              : fixture.base;

          return Promise.resolve({
            data: JSON.stringify(registryForRef),
          });
        }),
      },
    },
    paginate: vi.fn((endpoint) => {
      if (endpoint === octokit.rest.pulls.listFiles) {
        return Promise.resolve(files);
      }

      if (endpoint === octokit.rest.pulls.listReviews) {
        return Promise.resolve(reviews);
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
          sha: HEAD_SHA,
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

/**
 * Runs the check under fake timers so the merge-preview retry backoff resolves immediately.
 *
 * @param {Parameters<typeof run>[0]} args
 */
async function runWithoutRetryDelay(args) {
  vi.useFakeTimers();

  try {
    const runPromise = run(args);
    await vi.runAllTimersAsync();
    await runPromise;
  } finally {
    vi.useRealTimers();
  }
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
 * @param {{ owner?: string, repo?: string }} [target]
 * @returns {Promise<Context>}
 */
async function createLiveContext(octokit, prNumber, { owner = LIVE_TEST_OWNER, repo = LIVE_TEST_REPO } = {}) {
  const { data: pr } = await octokit.rest.pulls.get({
    owner,
    repo,
    pull_number: prNumber,
  });

  return /** @type {Context} */ (/** @type {unknown} */ ({
    repo: { owner, repo },
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
   * Returns the registry comparison refs for the live PR sample.
   * Live samples are intentionally limited to closed-unmerged PRs and
   * squash-merged PRs.
   *
   * @param {Octokit} octokit
   * @param {number} prNumber
   * @returns {Promise<{
   *   baseRef: string,
   *   proposedRef: string,
   * }>}
   */
  async function getLiveRegistryComparisonRefs(octokit, prNumber) {
    const { data: pr } = await octokit.rest.pulls.get({
      owner: LIVE_TEST_OWNER,
      repo: LIVE_TEST_REPO,
      pull_number: prNumber,
    });

    if (pr.state !== 'closed') {
      throw new Error(`Live PR sample ${prNumber} must be closed or merged`);
    }

    if (pr.merged_at == null) {
      if (!pr.base.sha || !pr.head.sha) {
        throw new Error(`Unable to determine the comparison refs for PR ${prNumber}`);
      }

      return {
        baseRef: pr.base.sha,
        proposedRef: pr.head.sha,
      };
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

    return {
      baseRef: parent.sha,
      proposedRef: pr.merge_commit_sha,
    };
  }


  /**
   * @param {{ number: number, noReviewRequired: boolean, coreTeam?: Set<string> }} sample
   */
  async function runTestAgainstLivePr(sample) {
    const octokit = await createLiveOctokit();
    const context = await createLiveContext(octokit, sample.number);
    const registryComparisonRefs = await getLiveRegistryComparisonRefs(octokit, sample.number);
    const core = createNoopCore();

    if (sample.coreTeam) {
      await run({ github: octokit, context, core, coreTeam: sample.coreTeam, registryComparisonRefs });
    } else {
      await run({ github: octokit, context, core, registryComparisonRefs });
    }

    if (sample.noReviewRequired) {
      expect(core.setFailed).not.toHaveBeenCalled();
    } else {
      expect(core.setFailed).toHaveBeenCalledWith(expect.stringContaining('Core review required'));
    }
  }

  // NOTE: some of these are just PRs, not even release PRs, but they have the right metadata.

  describe("core approval bypass", () => {
    // https://github.com/Azure/azure-dev/pull/9027
    it('[live] PR 9027 => core reviewer is the author', async () => {
      await runTestAgainstLivePr({ number: 9027, noReviewRequired: true });
    }, 90_000);

    // https://github.com/Azure/azure-dev/pull/8958
    it('[live] PR 8958 => core reviewer approved', async () => {
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
  it('[live] PR 8972 => no review required for an allowed TestFigSpec snapshot update', async () => {
    await runTestAgainstLivePr({ number: 8972, noReviewRequired: true });
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
