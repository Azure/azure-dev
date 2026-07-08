const { isDeepStrictEqual } = require('node:util');

// GitHub Actions entry point.
module.exports = run;

// Test-only helpers exposed on the action entry point.
module.exports.forTests = {
  getRegistryJson,
  isApprovedByCoreTeam,
  isAllowedRegistryJsonUpdate,
  isCreatedByCoreTeam,
  coreExtensionApprovers,
  diffRegistry,
}

/**
 * Users that, when they approve, bypass any checks in this file.
 */
function coreExtensionApprovers() {
  return new Set([
    "hemarina",
    "JeffreyCA",
    "RickWinter",
    // TODO: bring me back from the dead!
    // "richardpark-msft",
    "tg-msft",
    "vhvb1989",
  ]);
}

const REGISTRY_JSON_PATH = 'cli/azd/extensions/registry.json';

// GitHub action types

/**
 * @typedef {typeof import('@actions/github').context} Context
 * @typedef {ReturnType<typeof import('@actions/github').getOctokit>} Octokit
 * @typedef {typeof import('@actions/core')} Core
 *  
 * Response item types inferred from Octokit methods.
 * @typedef {Awaited<ReturnType<Octokit['rest']['pulls']['listReviews']>>['data'][number]} Review
 * @typedef {Awaited<ReturnType<Octokit['rest']['pulls']['listFiles']>>['data'][number]} PullRequestFile
 */

// registry.json's types

/**
 * @typedef {object} Provider
 * @property {string} name
 * @property {string} type
 * @property {string} [description]
 *
 * @typedef {object} ExtensionVersion
 * @property {string} version
 * @property {string[]} [capabilities]
 * @property {Provider[]} [providers]
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

/**
 * @typedef {NonNullable<Context['payload']['pull_request']>} PullRequest
 */

/**
 * @param {{ github: Octokit, context: Context, core: Core, coreTeam?: Set<string>, registryBaseRef?: string }} args
 */
async function run({ github: octokit, context, core, coreTeam = coreExtensionApprovers(), registryBaseRef }) {
  try {
    assertHasPullRequest(context);
    const baseRef = registryBaseRef ?? context.payload.pull_request['base']?.sha ?? 'main';

    // no extra checks needed if a core team member authored the PR.
    if (isCreatedByCoreTeam({ context, core, coreTeam })) {
      core.info(`PR was created by a core team, no further checks needed`)
      return;
    }

    // no extra checks needed if a core team member has already approved it.
    if (await isApprovedByCoreTeam({ octokit, context, core, coreTeam })) {
      core.info(`PR was approved by a core team member, no further checks needed`)
      return;
    }

    // Non-registry file changes require core-team review.
    const changedFileReviewReasons = await getChangedFileReviewReasons({
      octokit,
      context,
    });

    // Simple release-only registry changes can proceed without core-team review.
    const registryReviewReasons = await isAllowedRegistryJsonUpdate({
      octokit,
      context,
      registryBaseRef: baseRef,
    });

    const reviewReasons = changedFileReviewReasons.concat(registryReviewReasons);

    if (reviewReasons.length === 0) {
      core.info(`PR registry changes do not require core team review (no changes in capabilities, providers)`)
      return;
    }

    core.setFailed(
      "PR changes the extension registry in a way that requires core team review:\n" +
      reviewReasons.map((r) => `- ${r}`).join("\n") +
      "\n\nTo fix:\n" +
      `1. Have any core team member review and approve this PR. Core team members: (${[...coreExtensionApprovers()].join(", ")})\n` +
      `2. After approval, re-run this build step so it'll re-evaluate the PR - no commits or pushes needed.`
    );
  } catch (err) {
    core.setFailed(`Internal failure in script: ${err instanceof Error ? err.message : err}`);
  }
}

/**
 * @param {{ octokit: Octokit, context: Context, core: Core, coreTeam: Set<string> }} args
 * @returns {Promise<boolean>} true if it is approved, false otherwise.
 */
async function isApprovedByCoreTeam({ octokit, context, core, coreTeam }) {
  if (coreTeam == null || coreTeam.size === 0) {
    throw new Error("Invalid parameter - coreteam must be populated");
  }

  assertHasPullRequest(context);

  const reviews = await octokit.paginate(octokit.rest.pulls.listReviews, {
    ...context.repo,
    pull_number: context.payload.pull_request.number,
  });

  // users can have multiple reviews (ie, they requested changes, then they approved), so we'll 
  // make sure we get their absolutely latest review state.

  // NOTE: api docs indicate reviews always come back in chronological order, according to their docs,
  // and Map.set keeps the last entry per key - so this is "latest review per core-team user".
  /** @type {Map<string, Review['state']>} */
  const latestByUser = new Map();

  for (const review of reviews) {
    if (review.user != null && coreTeam.has(review.user.login)) {
      latestByUser.set(review.user.login, review.state);
    }
  }

  // GitHub will take care of blocking the PR if reviewers did a request-changes, for instance.
  const coreApprovals = [...latestByUser].filter(([, v]) => v === 'APPROVED').map(([k]) => k);

  if (coreApprovals != null && coreApprovals.length > 0) {
    core.info(`PR approved by member(s) of the AZD team (${coreApprovals.join(",")})`)
    return true;
  }

  return false;
}

/**
 * @param {{ context: Context, core: Core, coreTeam: Set<string> }} args
 * @returns {boolean} true if the PR author is a member of the core team, false otherwise.
 */
function isCreatedByCoreTeam({ context, core, coreTeam }) {
  if (coreTeam == null || coreTeam.size === 0) {
    throw new Error("Invalid parameter - coreteam must be populated");
  }

  assertHasPullRequest(context);

  const author = context.payload.pull_request['user']?.login;

  if (author != null && coreTeam.has(author)) {
    core.info(`PR was created by a member of the AZD team (${author})`);
    return true;
  }

  return false;
}

/**
 * Checks whether the registry update is simple enough to proceed without core-team review.
 *
 * @param {{ octokit: Octokit, context: Context, registryBaseRef?: string }} args
 * @returns {Promise<string[]>} the reasons core team review is needed; empty means the change is approved
 */
async function isAllowedRegistryJsonUpdate({ octokit, context, registryBaseRef = 'main' }) {
  assertHasPullRequest(context);
  const pr = context.payload.pull_request;

  const mainRegistry = await getRegistryJson({
    octokit,
    owner: context.repo.owner,
    repo: context.repo.repo,
    ref: registryBaseRef,
  });

  const head = pr['head'];
  const ref = head?.sha ?? head?.ref;
  if (!ref) {
    throw new Error('Unable to determine PR head ref for registry.json update check');
  }

  const prRegistry = await getRegistryJson({
    octokit,
    owner: head?.repo?.owner?.login ?? context.repo.owner,
    repo: head?.repo?.name ?? context.repo.repo,
    ref,
  });

  return diffRegistry(mainRegistry, prRegistry);
}

/**
 * Checks whether the PR changed only registry.json.
 *
 * @param {{ octokit: Octokit, context: Context }} args
 * @returns {Promise<string[]>}
 */
async function getChangedFileReviewReasons({ octokit, context }) {
  const changedFiles = await getChangedFiles({ octokit, context });
  return diffChangedFiles(changedFiles);
}

/**
 * Fetches the list of files changed by the PR.
 *
 * @param {{ octokit: Octokit, context: Context }} args
 * @returns {Promise<PullRequestFile[]>}
 */
async function getChangedFiles({ octokit, context }) {
  assertHasPullRequest(context);

  return await octokit.paginate(octokit.rest.pulls.listFiles, {
    ...context.repo,
    pull_number: context.payload.pull_request.number,
  });
}

/**
 * @param {PullRequestFile[]} changedFiles
 * @returns {string[]}
 */
function diffChangedFiles(changedFiles) {
  const unexpectedFiles = changedFiles
    .filter((file) => file.filename !== REGISTRY_JSON_PATH || file.previous_filename != null)
    .map((file) => file.filename);

  if (unexpectedFiles.length === 0) {
    return [];
  }

  return [
    `PR changes files outside ${REGISTRY_JSON_PATH}; registry auto-approval only applies to registry-only PRs: ${unexpectedFiles.join(', ')}`,
  ];
}

/**
 * Fetches and parses cli/azd/extensions/registry.json at a given ref.
 *
 * @param {{ octokit: Octokit, owner: string, repo: string, ref: string }} args
 * @returns {Promise<RegistryJson>}
 */
async function getRegistryJson({ octokit, owner, repo, ref }) {
  const { data } = await octokit.rest.repos.getContent({
    owner,
    repo,
    path: REGISTRY_JSON_PATH,
    ref,
    mediaType: {
      format: 'raw',
    },
  });

  if (typeof data !== 'string') {
    throw new Error(`Unable to load ${REGISTRY_JSON_PATH} from ${owner}/${repo}@${ref}`);
  }

  return JSON.parse(data);
}

/**
 * Diffs the base (main) registry against the registry proposed by a PR and decides
 * whether the change is safe enough to auto-approve, or whether a core team member
 * needs to review it.
 *
 * New releases can be auto-approved when they keep the previous release's
 * capabilities and providers, and only add a new release to an existing extension.
 * 
 * @param {RegistryJson} baseRegistry  registry.json as it exists on main
 * @param {RegistryJson} prRegistry    registry.json as proposed by the PR
 * @returns {string[]} the reasons core team review is needed; empty means the change is approved
 */
function diffRegistry(baseRegistry, prRegistry) {
  /** @type {string[]} */
  const reasons = [];

  const baseExtensions = new Map((baseRegistry.extensions ?? []).map((e) => [e.id, e]));
  const prExtensions = new Map((prRegistry.extensions ?? []).map((e) => [e.id, e]));

  // brand new extensions can't be auto-approved.
  for (const id of prExtensions.keys()) {
    if (!baseExtensions.has(id)) {
      reasons.push(`extension '${id}' is new; new extensions cannot be auto-approved`);
    }
  }

  // removing an existing extension can't be auto-approved.
  for (const id of baseExtensions.keys()) {
    if (!prExtensions.has(id)) {
      reasons.push(`extension '${id}' was removed; removing extensions cannot be auto-approved`);
    }
  }

  for (const [id, prExtension] of prExtensions) {
    const baseExtension = baseExtensions.get(id);

    if (baseExtension == null) {
      continue; // already reported as a new extension above
    }

    if (baseExtension.namespace !== prExtension.namespace) {
      reasons.push(`extension '${id}' namespace changed; namespace changes cannot be auto-approved`);
    }

    const baseVersions = new Map((baseExtension.versions ?? []).map((v) => [v.version, v]));
    const prVersions = new Map((prExtension.versions ?? []).map((v) => [v.version, v]));

    reasons.push(...diffPublishedReleases(id, baseVersions, prVersions));
    reasons.push(...diffNewReleases(id, baseExtension.versions ?? [], baseVersions, prVersions));
  }

  return reasons;
}

/**
 * @param {string} id
 * @param {Map<string, ExtensionVersion>} baseVersions
 * @param {Map<string, ExtensionVersion>} prVersions
 * @returns {string[]}
 */
function diffPublishedReleases(id, baseVersions, prVersions) {
  /** @type {string[]} */
  const reasons = [];

  for (const [version, baseVersion] of baseVersions) {
    const prVersion = prVersions.get(version);
    if (prVersion == null) {
      reasons.push(`extension '${id}' release '${version}' was removed; published releases are immutable`);
      continue;
    }

    if (!sameCapabilities(baseVersion, prVersion)) {
      reasons.push(`extension '${id}' release '${version}' changes capabilities; published capability declarations require core review`);
    }

    if (!sameProviders(baseVersion, prVersion)) {
      reasons.push(`extension '${id}' release '${version}' changes providers; published provider declarations require core review`);
    }

    if (!isDeepStrictEqual(baseVersion, prVersion)) {
      reasons.push(`extension '${id}' release '${version}' was modified; published releases are immutable`);
    }
  }

  return reasons;
}

/**
 * @param {string} id
 * @param {ExtensionVersion[]} baseVersionList
 * @param {Map<string, ExtensionVersion>} baseVersions
 * @param {Map<string, ExtensionVersion>} prVersions
 * @returns {string[]}
 */
function diffNewReleases(id, baseVersionList, baseVersions, prVersions) {
  /** @type {string[]} */
  const reasons = [];
  const previousRelease = latestVersionBySemver(baseVersionList);

  for (const [version, prVersion] of prVersions) {
    if (baseVersions.has(version)) {
      continue;
    }

    if (previousRelease == null) {
      reasons.push(`extension '${id}' release '${version}' has no previous release to compare against`);
      continue;
    }

    if (!sameCapabilities(previousRelease, prVersion)) {
      reasons.push(
        `extension '${id}' release '${version}' changes capabilities from the previous release '${previousRelease.version}'`,
      );
    }

    if (!sameProviders(previousRelease, prVersion)) {
      reasons.push(
        `extension '${id}' release '${version}' changes providers from the previous release '${previousRelease.version}'`,
      );
    }
  }

  return reasons;
}

/**
 * @param {ExtensionVersion} a
 * @param {ExtensionVersion} b
 * @returns {boolean}
 */
function sameCapabilities(a, b) {
  return isDeepStrictEqual((a.capabilities ?? []).sort(), (b.capabilities ?? []).sort());
}

/**
 * @param {ExtensionVersion} a
 * @param {ExtensionVersion} b
 * @returns {boolean}
 */
function sameProviders(a, b) {
  return isDeepStrictEqual(providerIdentities(a.providers ?? []), providerIdentities(b.providers ?? []));
}

/**
 * Reduces providers to their behavioral identity (name + type), sorted, so that a
 * cosmetic description tweak doesn't force a core-team review, while any change to
 * what the extension actually registers does.
 *
 * @param {Provider[]} providers
 * @returns {{ name: string, type: string }[]}
 */
function providerIdentities(providers) {
  return providers
    .map((p) => ({ name: p.name, type: p.type }))
    .sort((x, y) => x.name.localeCompare(y.name) || x.type.localeCompare(y.type));
}

/**
 * @param {ExtensionVersion[]} versions
 * @returns {ExtensionVersion | undefined}
 */
function latestVersionBySemver(versions) {
  if (versions.length === 0) {
    return undefined;
  }

  return versions.reduce((latest, candidate) =>
    compareSemver(candidate.version, latest.version) > 0 ? candidate : latest
  );
}

/**
 * @param {string} a
 * @param {string} b
 * @returns {number}
 */
function compareSemver(a, b) {
  const parsedA = parseSemver(a);
  const parsedB = parseSemver(b);

  for (const key of /** @type {const} */ (['major', 'minor', 'patch'])) {
    if (parsedA[key] !== parsedB[key]) {
      return parsedA[key] < parsedB[key] ? -1 : 1;
    }
  }

  return comparePrerelease(parsedA.prerelease, parsedB.prerelease);
}

/**
 * @param {string} version
 * @returns {{ major: number, minor: number, patch: number, prerelease: string }}
 */
function parseSemver(version) {
  const withoutBuild = version.split('+')[0] ?? '';
  const coreAndPrerelease = withoutBuild.split('-');
  const core = coreAndPrerelease[0] ?? '';
  const prerelease = coreAndPrerelease[1] ?? '';
  const [major = 0, minor = 0, patch = 0] = core.split('.').map((n) => Number.parseInt(n, 10) || 0);

  return { major, minor, patch, prerelease };
}

/**
 * @param {string} a
 * @param {string} b
 * @returns {number}
 */
function comparePrerelease(a, b) {
  if (a === b) {
    return 0;
  }
  if (a === '') {
    return 1;
  }
  if (b === '') {
    return -1;
  }

  const aFields = a.split('.');
  const bFields = b.split('.');
  const fieldCount = Math.max(aFields.length, bFields.length);

  for (let i = 0; i < fieldCount; i++) {
    const aField = aFields[i];
    const bField = bFields[i];

    if (aField === undefined) {
      return -1;
    }
    if (bField === undefined) {
      return 1;
    }

    const aNumeric = /^\d+$/.test(aField);
    const bNumeric = /^\d+$/.test(bField);

    if (aNumeric && bNumeric) {
      const diff = Number.parseInt(aField, 10) - Number.parseInt(bField, 10);
      if (diff !== 0) {
        return diff < 0 ? -1 : 1;
      }
    } else if (aNumeric) {
      return -1;
    } else if (bNumeric) {
      return 1;
    } else if (aField !== bField) {
      return aField < bField ? -1 : 1;
    }
  }

  return 0;
}

/**
 * Asserts that we're being invoked for a pull request (and is also a typeguard)
 * 
 * @param {Context} context
 * @returns {asserts context is Context & { payload: { pull_request: PullRequest } }}
 */
function assertHasPullRequest(context) {
  if (context.payload.pull_request == null) {
    throw new Error('No pull_request found in event payload. Workflow targeting should only target pull requests.');
  }
}


