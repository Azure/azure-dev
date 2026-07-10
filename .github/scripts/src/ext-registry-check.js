// This script runs as part of the .github/workflows/ext-registry-check.yml. It lets the core azd team ensure that complicated
// registry updates (changes to capabilities, or providers) and other assorted changes always fall under core team review, while
// simple changes (simple version bump, no changes to important fields) can go by with just a simple approval from any developer.
const { isDeepStrictEqual } = require('node:util');

// GitHub Actions entry point.
module.exports = run;

// Test-only helpers exposed on the action entry point.
module.exports.forTests = {
  getRegistryJson,
  getCoreReviewers,
  isApprovedByCoreTeam,
  isAllowedRegistryJsonUpdate,
  isCreatedByCoreTeam,
  diffRegistry,
}

const REGISTRY_JSON_PATH = 'cli/azd/extensions/registry.json';

// We only allow URLs that point to our GitHub releases page.
// NOTE: this script is only for production registry.json - nightlies go to a non-releases spot, etc...
const ALLOWED_ARTIFACT_URL_ORIGIN = 'https://github.com';
const ALLOWED_ARTIFACT_URL_PATH_PREFIX = '/Azure/azure-dev/releases/download/';
const ALLOWED_ARTIFACT_URL_PREFIX = `${ALLOWED_ARTIFACT_URL_ORIGIN}${ALLOWED_ARTIFACT_URL_PATH_PREFIX}`;

// Extension-level fields that may change without core review, since they're cosmetic. 
// Everything else on an extension object (aside from `versions`, which has its own release 
// rules) must be identical between the base and PR registries. Note, we're not trying to 
// understand those other fields, just preserve the status quo. 
const ALLOWED_EXTENSION_METADATA_CHANGES = new Set(['displayName', 'description', 'tags']);

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
 * @typedef {object} Artifact
 * @property {string} url
 *
 * @typedef {object} ExtensionVersion
 * @property {string} version
 * @property {string[]} [capabilities]
 * @property {Provider[]} [providers]
 * @property {Object<string, Artifact>} [artifacts]
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
 * @property {string} [schemaVersion]
 * @property {Extension[]} extensions
 */

/**
 * @typedef {NonNullable<Context['payload']['pull_request']>} PullRequest
 */

/**
 * @param {{ github: Octokit, context: Context, core: Core, coreTeam?: Set<string>, registryBaseRef?: string }} args
 */
async function run({ github: octokit, context, core, coreTeam, registryBaseRef }) {
  try {
    assertHasPullRequest(context);
    const baseRef = registryBaseRef ?? context.payload.pull_request['base']?.sha ?? 'main';
    const coreReviewers = coreTeam ?? getCoreReviewers({ core });

    // no extra checks needed if a registry maintainer authored the PR.
    if (isCreatedByCoreTeam({ context, core, coreTeam: coreReviewers })) {
      core.info(`PR was created by a registry maintainer, no further checks needed`)
      return;
    }

    // no extra checks needed if a registry maintainer has already approved it.
    if (await isApprovedByCoreTeam({ octokit, context, core, coreTeam: coreReviewers })) {
      core.info(`PR was approved by a registry maintainer, no further checks needed`)
      return;
    }

    // Non-registry file changes require core review.
    const changedFileReviewReasons = await getChangedFileReviewReasons({
      octokit,
      context,
    });

    // Simple release-only registry changes can proceed without core review.
    const registryReviewReasons = await isAllowedRegistryJsonUpdate({
      octokit,
      context,
      registryBaseRef: baseRef,
    });

    const reviewReasons = changedFileReviewReasons.concat(registryReviewReasons);

    if (reviewReasons.length === 0) {
      core.info(`PR registry changes do not require core review (no changes in capabilities, providers)`)
      return;
    }

    core.setFailed(
      "Core review required for this extension registry change:\n" +
      reviewReasons.map((r) => `- ${r}`).join("\n") +
      "\n\nTo fix:\n" +
      `1. Have one of these registry maintainers review and approve this PR: ${[...coreReviewers].join(', ')}.\n` +
      `2. After approval, re-run this build step so it'll re-evaluate the PR - no commits or pushes needed.`
    );
  } catch (err) {
    core.setFailed(`Internal failure in script: ${err instanceof Error ? err.message : err}`);
  }
}

/**
 * Registry maintainers whose PRs skip the extra checks and whose approval
 * clears a PR for merge.
 *
 * This is intentionally a hard-coded list: the workflow's GITHUB_TOKEN can't read
 * organization team membership (@Azure/azure-dev-extregistry-maintain), and the
 * membership is fairly static. Keep this in sync with that team.
 *
 * @param {{ core: Core }} args
 * @returns {Set<string>}
 */
function getCoreReviewers({ core }) {
  const logins = [
    'hemarina',
    'JeffreyCA',
    'RickWinter',
    // TODO: bring me back from the dead!
    // 'richardpark-msft',
    'tg-msft',
    'vhvb1989',
  ];

  core.info(`Loaded ${logins.length} registry maintainer(s): ${logins.join(', ')}`);
  return new Set(logins);
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

  const headSha = context.payload.pull_request['head']?.sha;
  if (!headSha) {
    throw new Error('Unable to determine PR head sha for approval freshness check');
  }

  // Users can have multiple reviews (ie, they requested changes, then they approved), so we'll
  // make sure we get their absolutely latest *decisive* review state. There's a bit of trickiness
  // that you can have multiple states (order preserved) associated with the same commit SHA 
  // (for instance, you approve a PR, then request changes, etc..)
  const END_STATES = new Set(['APPROVED', 'CHANGES_REQUESTED', 'DISMISSED']);

  // NOTE: reviews come back in chronological order (see "List reviews for a pull request":
  // https://docs.github.com/en/rest/pulls/reviews#list-reviews-for-a-pull-request), which is
  // critical for us since we have to actually know the last state of the review.
  /** @type {Map<string, { state: Review['state'], commitId: Review['commit_id'] }>} */
  const latestByUser = new Map();

  for (const review of reviews) {
    if (review.user != null && coreTeam.has(review.user.login) && END_STATES.has(review.state)) {
      latestByUser.set(review.user.login, {
        state: review.state,
        commitId: review.commit_id,
      });
    }
  }

  const coreApprovals = [...latestByUser]
    .filter(([, review]) => review.state === 'APPROVED' && review.commitId === headSha)
    .map(([login]) => login);

  if (coreApprovals != null && coreApprovals.length > 0) {
    core.info(`PR head commit approved by registry maintainer(s) (${coreApprovals.join(",")})`)
    return true;
  }

  return false;
}

/**
 * @param {{ context: Context, core: Core, coreTeam: Set<string> }} args
 * @returns {boolean} true if the PR author is a registry maintainer, false otherwise.
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
 * @returns {Promise<string[]>} the reasons core review is needed; empty means the change is approved
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
    `PR changes files outside ${REGISTRY_JSON_PATH}; core review required for non-registry-only PRs: ${unexpectedFiles.join(', ')}`,
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
 * whether the change can proceed without core review, or whether a core reviewer
 * needs to review it.
 *
 * New releases can proceed without core review when they keep the previous release's
 * capabilities and providers, and only add a new release to an existing extension.
 * 
 * @param {RegistryJson} baseRegistry  registry.json as it exists on main
 * @param {RegistryJson} prRegistry    registry.json as proposed by the PR
 * @returns {string[]} the reasons core review is needed; empty means the change is approved
 */
function diffRegistry(baseRegistry, prRegistry) {
  /** @type {string[]} */
  const reasons = [];

  /**
   * Builds a Map keyed by `k(item)`, throwing on duplicate keys.
   * @template T, K
   * @param {Iterable<T>} items
   * @param {(item: T) => K} k
   */
  function toMap(items, k) {
    /** @type {Map<K, T>} */
    const m = new Map();
    for (const item of items ?? []) {
      const key = k(item);
      if (m.has(key)) throw new Error(`duplicate key: ${key}`);
      m.set(key, item);
    }
    return m; // inferred Map<K, T>
  }

  reasons.push(...diffRegistryMetadata(baseRegistry, prRegistry));

  const baseExtensions = toMap(baseRegistry.extensions, (e) => e.id);
  const prExtensions = toMap(prRegistry.extensions, (e) => e.id);

  // brand new extensions require core review.
  for (const id of prExtensions.keys()) {
    if (!baseExtensions.has(id)) {
      reasons.push(`extension '${id}' is new; new extensions require core review`);
    }
  }

  // removing an existing extension requires core review.
  for (const id of baseExtensions.keys()) {
    if (!prExtensions.has(id)) {
      reasons.push(`extension '${id}' was removed; removing extensions requires core review`);
    }
  }

  for (const [id, prExtension] of prExtensions) {
    const baseExtension = baseExtensions.get(id);

    if (baseExtension == null) {
      continue; // already reported as a new extension above
    }

    reasons.push(...diffExtensionMetadata(id, baseExtension, prExtension));

    const baseVersions = toMap(baseExtension.versions, (v) => v.version);
    const prVersions = toMap(prExtension.versions, (v) => v.version);

    reasons.push(...diffPublishedReleases(id, baseVersions, prVersions));
    reasons.push(...diffNewReleases(id, baseExtension.versions ?? [], baseVersions, prVersions));
  }

  return reasons;
}

/**
 * Compares the registry-level metadata (everything except `extensions`, which has its own
 * diffing rules) between the base and PR registries. Any difference requires core review,
 * since these root fields (for example `schemaVersion`) govern how azd loads the entire
 * registry and can make it unusable. We don't track or know about specific fields: anything
 * outside `extensions` is expected to be identical.
 *
 * @param {RegistryJson} baseRegistry
 * @param {RegistryJson} prRegistry
 * @returns {string[]}
 */
function diffRegistryMetadata(baseRegistry, prRegistry) {
  const baseMetadata = registryMetadata(baseRegistry);
  const prMetadata = registryMetadata(prRegistry);

  const changedFields = changedMetadataFields(baseMetadata, prMetadata);

  if (changedFields.length === 0) {
    return [];
  }

  return [
    `registry changes top-level metadata that requires core review (${changedFields.join(', ')}); only extension changes may proceed without core review`,
  ];
}

/**
 * Returns a copy of the registry without `extensions` (which has its own diffing rules).
 *
 * @param {RegistryJson} registry
 * @returns {Record<string, unknown>}
 */
function registryMetadata(registry) {
  return Object.fromEntries(
    Object.entries(registry).filter(([name]) => name !== 'extensions'),
  );
}

/**
 * Compares the extension-level metadata (everything except `versions`) between the base
 * and PR registries. Any difference requires core review, except for the cosmetic fields
 * in ALLOWED_EXTENSION_METADATA_CHANGES. We don't track or know about specific fields:
 * anything not in the allowlist is expected to be identical.
 *
 * @param {string} id
 * @param {Extension} baseExtension
 * @param {Extension} prExtension
 * @returns {string[]}
 */
function diffExtensionMetadata(id, baseExtension, prExtension) {
  const baseMetadata = extensionMetadata(baseExtension);
  const prMetadata = extensionMetadata(prExtension);

  const changedFields = changedMetadataFields(baseMetadata, prMetadata);

  if (changedFields.length === 0) {
    return [];
  }

  return [
    `extension '${id}' changes metadata that requires core review (${changedFields.join(', ')}); only ${[...ALLOWED_EXTENSION_METADATA_CHANGES].join(', ')} may change without core review`,
  ];
}

/**
 * Returns a copy of an extension without `versions` (which has its own release rules) or
 * any allowlisted cosmetic field.
 *
 * @param {Extension} extension
 * @returns {Record<string, unknown>}
 */
function extensionMetadata(extension) {
  return Object.fromEntries(
    Object.entries(extension).filter(
      // we compare versions elsewhere.
      ([name]) => name !== 'versions' && !ALLOWED_EXTENSION_METADATA_CHANGES.has(name),
    )
  );
}

/**
 * @param {Record<string, unknown>} baseMetadata
 * @param {Record<string, unknown>} prMetadata
 * @returns {string[]}
 */
function changedMetadataFields(baseMetadata, prMetadata) {
  return [
    ...new Set([...Object.keys(baseMetadata), ...Object.keys(prMetadata)]),
  ]
    .filter((field) => !isDeepStrictEqual(baseMetadata[field], prMetadata[field]))
    .sort();
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

    const capabilityChanges = diffArrays(baseVersion.capabilities ?? [], prVersion.capabilities ?? []);
    if (capabilityChanges.length > 0) {
      reasons.push(`extension '${id}' release '${version}' changes capabilities (${capabilityChanges.join('; ')}); published capability declarations require core review`);
    }

    const providerChanges = diffArrays(providerIdentityLabels(baseVersion), providerIdentityLabels(prVersion));
    if (providerChanges.length > 0) {
      reasons.push(`extension '${id}' release '${version}' changes providers (${providerChanges.join('; ')}); published provider declarations require core review`);
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

  const newReleases = [...prVersions].filter(([version]) => !baseVersions.has(version));

  if (newReleases.length === 0) {
    return reasons;
  }

  // A simple version bump adds exactly one release. Anything more (or a first-ever release with
  // no baseline to compare against) needs a human to look at it.
  if (newReleases.length > 1) {
    const added = newReleases.map(([version]) => version).sort();
    reasons.push(
      `extension '${id}' adds ${newReleases.length} new releases (${added.join(', ')}); only a single new release may be added without core review`,
    );
    return reasons;
  }

  const newRelease = newReleases[0];
  if (newRelease == null) {
    return reasons;
  }
  const [version, prVersion] = newRelease;

  if (previousRelease == null) {
    reasons.push(`extension '${id}' release '${version}' has no previous release to compare against`);
    return reasons;
  }

  // The new release must move the extension forward; re-adding an older version (or one that ties
  // the current latest) isn't a simple bump and could downgrade what azd resolves.
  if (compareSemver(version, previousRelease.version) <= 0) {
    reasons.push(
      `extension '${id}' release '${version}' is not newer than the current latest release '${previousRelease.version}'; only forward version bumps may proceed without core review`,
    );
  }

  const capabilityChanges = diffArrays(previousRelease.capabilities ?? [], prVersion.capabilities ?? []);
  if (capabilityChanges.length > 0) {
    reasons.push(
      `extension '${id}' release '${version}' changes capabilities from the previous release '${previousRelease.version}' (${capabilityChanges.join('; ')})`,
    );
  }

  const providerChanges = diffArrays(providerIdentityLabels(previousRelease), providerIdentityLabels(prVersion));
  if (providerChanges.length > 0) {
    reasons.push(
      `extension '${id}' release '${version}' changes providers from the previous release '${previousRelease.version}' (${providerChanges.join('; ')})`,
    );
  }

  reasons.push(...validateArtifactURLs(id, prVersion));

  return reasons;
}

/**
 * Flags any artifact whose download URL is not hosted under the official azure-dev
 * releases location, so a new release can't point auto-approval at an arbitrary blob.
 *
 * A missing or non-string URL is malformed registry data, so we throw outright rather
 * than routing it to review.
 *
 * @param {string} id
 * @param {ExtensionVersion} version
 * @returns {string[]}
 * @throws {Error} if an artifact has no string URL
 */
function validateArtifactURLs(id, version) {
  /** @type {string[]} */
  const reasons = [];

  for (const [platform, artifact] of Object.entries(version.artifacts ?? {})) {
    const url = artifact?.url;
    if (typeof url !== 'string') {
      throw new Error(
        `extension '${id}' release '${version.version}' artifact '${platform}' has no string URL (got ${JSON.stringify(url)})`,
      );
    }
    if (!isAllowedArtifactURL(url)) {
      reasons.push(
        `extension '${id}' release '${version.version}' artifact '${platform}' has a URL outside ${ALLOWED_ARTIFACT_URL_PREFIX} (${url}); release artifacts must be hosted there`,
      );
    }
  }

  return reasons;
}

/**
 * @param {string} value
 * @returns {boolean}
 */
function isAllowedArtifactURL(value) {
  let url;
  try {
    url = new URL(value);
  } catch {
    return false;
  }

  return url.origin === ALLOWED_ARTIFACT_URL_ORIGIN &&
    url.pathname.startsWith(ALLOWED_ARTIFACT_URL_PATH_PREFIX);
}

/**
 * @param {string[]} baseItems
 * @param {string[]} prItems
 * @returns {string[]}
 */
function diffArrays(baseItems, prItems) {
  const baseSet = new Set(baseItems);
  const prSet = new Set(prItems);
  const added = [...prSet].filter((item) => !baseSet.has(item)).sort();
  const removed = [...baseSet].filter((item) => !prSet.has(item)).sort();
  /** @type {string[]} */
  const changes = [];

  if (added.length > 0) {
    changes.push(`added: ${added.join(', ')}`);
  }

  if (removed.length > 0) {
    changes.push(`removed: ${removed.join(', ')}`);
  }

  return changes;
}

/**
 * @param {ExtensionVersion} version
 * @returns {string[]}
 */
function providerIdentityLabels(version) {
  return providerIdentities(version.providers ?? []).map((provider) => `${provider.name} (${provider.type})`);
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


