const CODEOWNERS_PATH = '.github/CODEOWNERS';
const FALLBACK_PATHS = ['CODEOWNERS', 'docs/CODEOWNERS'];
const CODEOWNERS_PATHS = [CODEOWNERS_PATH, ...FALLBACK_PATHS];

/**
 * @typedef {ReturnType<typeof import('@actions/github').getOctokit>} Octokit
 * @typedef {Pick<typeof import('@actions/github').context, 'repo' | 'payload'>} Context
 * @typedef {Pick<typeof import('@actions/core'), 'info' | 'error' | 'setFailed'>} Core
 * @typedef {{ owner: string, repo: string, ref: string }} Revision
 */

/**
 * Validates CODEOWNERS changes against the canonical location, GitHub's native errors, and file-size limit.
 * @param {{ github: Octokit, context: Context, core: Core }} args
 */
async function run({ github, context, core }) {
  const pr = context.payload.pull_request;
  if (!pr) {
    throw new Error('CODEOWNERS validation requires a pull request.');
  }
  const files = await github.paginate(github.rest.pulls.listFiles, {
    ...context.repo,
    pull_number: pr.number,
    per_page: 100,
  });
  const changed = files.some((file) => CODEOWNERS_PATHS.includes(file.filename));
  if (!changed) {
    if (files.length < pr['changed_files']) {
      throw new Error('GitHub returned an incomplete changed-file list; cannot determine whether CODEOWNERS changed.');
    }
    core.info('No CODEOWNERS changes.');
    return;
  }

  // Use the upstream repository's permissions, not the fork's, at the event's immutable head.
  const revision = { ...context.repo, ref: pr['head'].sha };
  if (!await checkCodeownersLocations({ github, revision, core })) {
    return;
  }
  const { data } = await github.rest.repos.codeownersErrors(revision);
  if (!Array.isArray(data.errors)) {
    throw new Error('GitHub returned an invalid CODEOWNERS validation response.');
  }
  for (const error of data.errors) {
    const message = error.kind === 'Unknown owner'
      ? `${error.message}\n\nOwners who need write access should request membership in @Azure/azure-dev-write.`
      : error.message;
    core.error(message, {
      file: error.path,
      startLine: error.line,
      startColumn: error.column,
      title: error.kind,
    });
  }
  if (data.errors.length > 0) {
    core.setFailed(`GitHub reported ${data.errors.length} CODEOWNERS error(s). Fix the annotated entries.`);
    return;
  }

  if (await checkCodeownersSize({ github, revision, core })) {
    core.info('No CODEOWNERS errors reported by GitHub.');
  }
}

/**
 * Rejects fallback files even when GitHub ignores them in favor of the canonical location.
 * @param {{ github: Octokit, revision: Revision, core: Core }} args
 * @returns {Promise<boolean>}
 */
async function checkCodeownersLocations({ github, revision, core }) {
  let valid = true;
  for (const path of FALLBACK_PATHS) {
    let file;
    try {
      ({ data: file } = await github.rest.repos.getContent({ ...revision, path }));
    } catch (error) {
      if (error && typeof error === 'object' && 'status' in error && error.status === 404) {
        continue;
      }
      throw error;
    }
    if (Array.isArray(file) || file.type !== 'file') {
      continue;
    }
    core.error(`Keep ownership rules in ${CODEOWNERS_PATH}; remove the fallback file ${path}.`, {
      file: path,
      startLine: 1,
      title: 'Unsupported CODEOWNERS location',
    });
    valid = false;
  }
  if (!valid) {
    core.setFailed(`Only ${CODEOWNERS_PATH} is allowed. Remove the annotated fallback files.`);
  }
  return valid;
}

/**
 * Checks that the canonical CODEOWNERS file is within GitHub's size limit.
 * https://docs.github.com/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/about-code-owners#codeowners-file-size
 * The errors API returns errors: [] above 3 MiB even for invalid syntax; exactly 3 MiB is still parsed.
 * @param {{ github: Octokit, revision: Revision, core: Core }} args
 * @returns {Promise<boolean>}
 */
async function checkCodeownersSize({ github, revision, core }) {
  const maxBytes = 3 * 1024 * 1024;
  const { data: file } = await github.rest.repos.getContent({ ...revision, path: CODEOWNERS_PATH });
  if (Array.isArray(file) || file.type !== 'file') {
    throw new Error('Could not read CODEOWNERS to verify the file size.');
  }
  if (file.size > maxBytes) {
    core.setFailed(`${CODEOWNERS_PATH} exceeds GitHub's 3 MiB limit and cannot be validated.`);
    return false;
  }
  return true;
}

module.exports = run;
