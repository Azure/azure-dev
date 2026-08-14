// This script runs as part of the .github/workflows/ext-registry-check.yml. It lets the core azd team ensure that complicated
// registry updates (changes to capabilities, or providers) and other assorted changes always fall under core team review, while
// simple changes (simple version bump, no changes to important fields) can go by with just a simple approval from any developer.
const { isDeepStrictEqual } = require('node:util');

// The registries this check governs. The ext-registry-check workflow keeps an inline
// copy of this list (it runs its detection step before the checkout, so it can't
// require this file); a test asserts the two stay in sync.
const PROD_REGISTRY_JSON_PATH = 'cli/azd/extensions/registry.json';
const REGISTRY_JSON_PATHS = new Set([
  PROD_REGISTRY_JSON_PATH,
  'cli/azd/extensions/registry.dev.json',
]);

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
  REGISTRY_JSON_PATHS,
}

// TestFigSpec snapshots may accompany production registry updates.
const ALLOWED_COMPANION_PATHS = new Set([
  'cli/azd/cmd/testdata/TestFigSpec.ts',
]);

// We only allow URLs that point to our GitHub releases page.
const ALLOWED_ARTIFACT_URL_ORIGIN = 'https://github.com';
const ALLOWED_ARTIFACT_URL_PATH_PREFIX = '/Azure/azure-dev/releases/download/';
const ALLOWED_ARTIFACT_URL_PREFIX = `${ALLOWED_ARTIFACT_URL_ORIGIN}${ALLOWED_ARTIFACT_URL_PATH_PREFIX}`;

// Extension-level fields that may change without core review, since they're cosmetic. 
// Everything else on an extension object (aside from `versions`, which has its own release 
// rules) must be identical between the base and PR registries. Note, we're not trying to 
// understand those other fields, just preserve the status quo. 
const ALLOWED_EXTENSION_METADATA_CHANGES = new Set(['displayName', 'description', 'tags']);

// GitHub recomputes `refs/pull/<number>/merge` asynchronously, so it can briefly be missing or
// point at an older PR head. We retry a few times rather than failing the check on that lag.
const MERGE_PREVIEW_ATTEMPTS = 3;
const MERGE_PREVIEW_RETRY_DELAY_MS = 2_000;

/**
 * Raised when GitHub can't give us a merge preview that matches the head being evaluated.
 * Usually the pull request has merge conflicts, so the failure is reported to the contributor
 * directly instead of being labelled an internal script error.
 */
class MergePreviewUnavailableError extends Error {
  /** @param {string} message */
  constructor(message) {
    super(message);
    this.name = 'MergePreviewUnavailableError';
  }
}

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
 * The two commits the registry policy compares: the base the PR would merge into, and
 * the state the registry would have after that merge.
 *
 * @typedef {object} RegistryComparisonRefs
 * @property {string} baseRef
 * @property {string} proposedRef
 */

/**
 * @param {{
 *   github: Octokit,
 *   context: Context,
 *   core: Core,
 *   coreTeam?: Set<string>,
 *   registryComparisonRefs?: RegistryComparisonRefs,
 * }} args
 */
async function run({ github: octokit, context, core, coreTeam, registryComparisonRefs }) {
  try {
    assertHasPullRequest(context);
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

    const changedFiles = await getChangedFiles({ octokit, context });

    // Non-registry file changes require core review.
    const changedFileReviewReasons = diffChangedFiles(changedFiles);

    // Simple release-only registry changes can proceed without core review. Deleted registries
    // are reported by diffChangedFiles above and skipped here, since there's nothing to fetch
    // from the proposed merge result.
    const changedRegistryPaths = [...REGISTRY_JSON_PATHS]
      .filter((registryPath) => changedFiles.some(
        (file) => file.filename === registryPath && file.status !== 'removed'));
    const registryReviewReasons = [];
    if (changedRegistryPaths.length > 0) {
      const comparisonRefs = registryComparisonRefs ??
        await getRegistryComparisonRefs({ octokit, context });
      for (const registryPath of changedRegistryPaths) {
        const reasons = await isAllowedRegistryJsonUpdate({
          octokit,
          context,
          registryComparisonRefs: comparisonRefs,
          registryPath,
        });
        registryReviewReasons.push(...reasons.map((reason) => `${registryPath}: ${reason}`));
      }
    }

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
    // A missing or stale merge preview is normally the contributor's PR to fix (most often a
    // merge conflict), so it's reported as-is rather than as a script bug.
    if (err instanceof MergePreviewUnavailableError) {
      core.setFailed(err.message);
      return;
    }

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
    // TODO: bring me back after we get this all working in production
    //'richardpark-msft',
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

  // If the review state changes without a push, this workflow won't be retriggered;
  // normal GitHub branch protection still blocks the PR when changes are requested.
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
 * Both sides are read from the base repository at a matched pair of commits, so the
 * comparison describes exactly what this PR changes, and isn't skewed by registry updates
 * that landed on the base branch after the PR branch was created.
 *
 * @param {{
 *   octokit: Octokit,
 *   context: Context,
 *   registryPath: string,
 *   registryComparisonRefs: RegistryComparisonRefs,
 * }} args
 * @returns {Promise<string[]>} the reasons core review is needed; empty means the change is approved
 */
async function isAllowedRegistryJsonUpdate({
  octokit,
  context,
  registryPath,
  registryComparisonRefs,
}) {
  const [baseRegistry, proposedRegistry] = await Promise.all([
    getRegistryJson({ octokit, ...context.repo, ref: registryComparisonRefs.baseRef, registryPath }),
    getRegistryJson({ octokit, ...context.repo, ref: registryComparisonRefs.proposedRef, registryPath }),
  ]);

  return diffRegistry(baseRegistry, proposedRegistry);
}

/**
 * Resolves the commits the registry policy should compare, using GitHub's synthetic merge
 * commit (`refs/pull/<number>/merge`) as the proposed state.
 *
 * The refs are deliberately taken as a pair: the merge commit's first parent is the exact
 * base the preview was built from, so the diff always describes what this PR changes,
 * never what other PRs merged in the meantime.
 *
 * The second parent must match the head this workflow run is evaluating, otherwise the
 * preview is stale and could approve content we never looked at.
 *
 * @param {{ octokit: Octokit, context: Context }} args
 * @returns {Promise<RegistryComparisonRefs>}
 */
async function getRegistryComparisonRefs({ octokit, context }) {
  assertHasPullRequest(context);

  const headSha = context.payload.pull_request['head']?.sha;
  if (!headSha) {
    throw new Error('Unable to determine PR head commit for registry.json update check');
  }

  const mergeRef = `refs/pull/${context.payload.pull_request.number}/merge`;
  /** @type {Error | undefined} */
  let lastError;

  for (let attempt = 1; attempt <= MERGE_PREVIEW_ATTEMPTS; attempt++) {
    if (attempt > 1) {
      await new Promise((resolve) => setTimeout(resolve, MERGE_PREVIEW_RETRY_DELAY_MS));
    }

    try {
      const { data: mergeCommit } = await octokit.rest.repos.getCommit({
        ...context.repo,
        ref: mergeRef,
      });
      const [baseParent, headParent] = mergeCommit.parents;

      if (!baseParent?.sha || !headParent?.sha) {
        lastError = new Error(`GitHub's merge preview for this PR is not a two-parent merge commit`);
        continue;
      }

      if (headParent.sha !== headSha) {
        lastError = new Error(`GitHub's merge preview is stale for the current PR head (${headSha})`);
        continue;
      }

      return {
        baseRef: baseParent.sha,
        proposedRef: mergeCommit.sha,
      };
    } catch (err) {
      lastError = err instanceof Error ? err : new Error(String(err));
    }
  }

  throw new MergePreviewUnavailableError(
    `Unable to load a current GitHub merge preview for this PR after ${MERGE_PREVIEW_ATTEMPTS} attempts. ` +
    `Resolve any merge conflicts or re-run the check after GitHub computes the preview: ${lastError?.message}`,
  );
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
 * Whether a changed file refers to one of the known registries, on either side of a rename.
 * Checking `previous_filename` catches a registry that was renamed away, where the new
 * filename is no longer one of the registry paths.
 *
 * @param {PullRequestFile} file
 * @returns {boolean}
 */
function isRegistryFile(file) {
  return REGISTRY_JSON_PATHS.has(file.filename) ||
    (file.previous_filename != null && REGISTRY_JSON_PATHS.has(file.previous_filename));
}

/**
 * @param {PullRequestFile} file
 * @returns {boolean}
 */
function isInPlaceModification(file) {
  return file.previous_filename == null &&
    (file.status == null || file.status === 'modified');
}

/**
 * @param {PullRequestFile} file
 * @returns {boolean}
 */
function isAllowedCompanionFileChange(file) {
  return ALLOWED_COMPANION_PATHS.has(file.filename) && isInPlaceModification(file);
}

/**
 * Flags file changes that require core review. Companion snapshots are allowed only
 * with an in-place production registry update.
 *
 * @param {PullRequestFile[]} changedFiles
 * @returns {string[]} the reasons core review is needed; empty means every change is registry-only
 */
function diffChangedFiles(changedFiles) {
  const registryPaths = [...REGISTRY_JSON_PATHS].join(', ');
  const updatesProductionRegistry = changedFiles.some(
    (file) => file.filename === PROD_REGISTRY_JSON_PATH &&
      isInPlaceModification(file));

  const nonRegistryFiles = changedFiles
    .filter((file) =>
      !isRegistryFile(file) &&
      !(updatesProductionRegistry && isAllowedCompanionFileChange(file)))
    .map((file) => file.filename);

  const renamedRegistryFiles = changedFiles
    .filter((file) => isRegistryFile(file) && file.previous_filename != null)
    .map((file) => `${file.previous_filename} -> ${file.filename}`);

  const deletedRegistryFiles = changedFiles
    .filter((file) => isRegistryFile(file) && file.status === 'removed')
    .map((file) => file.filename);

  const reasons = [];

  if (nonRegistryFiles.length > 0) {
    reasons.push(
      `PR changes files outside the extension registries (${registryPaths}), ` +
      `which requires core review: ${nonRegistryFiles.join(', ')}`,
    );
  }

  if (renamedRegistryFiles.length > 0) {
    reasons.push(
      `PR renames extension registry files, which requires core review: ` +
      `${renamedRegistryFiles.join(', ')}`,
    );
  }

  if (deletedRegistryFiles.length > 0) {
    reasons.push(
      `PR deletes extension registry files, which requires core review: ` +
      `${deletedRegistryFiles.join(', ')}`,
    );
  }

  return reasons;
}

/**
 * Fetches and parses an extension registry at a given ref.
 *
 * @param {{ octokit: Octokit, owner: string, repo: string, ref: string, registryPath: string }} args
 * @returns {Promise<RegistryJson>}
 */
async function getRegistryJson({ octokit, owner, repo, ref, registryPath }) {
  const { data } = await octokit.rest.repos.getContent({
    owner,
    repo,
    path: registryPath,
    ref,
    mediaType: {
      format: 'raw',
    },
  });

  if (typeof data !== 'string') {
    throw new Error(`Unable to load ${registryPath} from ${owner}/${repo}@${ref}`);
  }

  return JSON.parse(data);
}

/**
 * Diffs the base-branch registry against the registry the PR would produce, and decides
 * whether the change can proceed without core review, or whether a core reviewer
 * needs to review it.
 *
 * New releases can proceed without core review when they keep the previous release's
 * capabilities and providers, and only add a new release to an existing extension.
 * 
 * @param {RegistryJson} baseRegistry  registry.json as it exists on the base branch
 * @param {RegistryJson} prRegistry    registry.json as it would exist once the PR merges
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
 */
function isAllowedArtifactURL(value) {
  try {
    const decodedValue = decodeURIComponent(value);
    if (decodedValue !== value) {
      return false;
    }

    const url = new URL(value);
    return url.origin === ALLOWED_ARTIFACT_URL_ORIGIN &&
      url.pathname.startsWith(ALLOWED_ARTIFACT_URL_PATH_PREFIX);
  } catch {
    return false;
  }
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
