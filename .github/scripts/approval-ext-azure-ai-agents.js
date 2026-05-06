// Required Approval Gate for azure.ai.agents Extension
//
// Validates that at least one required approver has approved the current HEAD,
// or that a valid break-glass override comment exists.
//
// See .github/workflows/approval-ext-azure-ai-agents.yml for full documentation.
module.exports = async ({ github, context, core }) => {
  const requiredApprovers = [
    'trangevi',
    'trrwilson',
    'therealjohn',
    'glharper',
  ];

  const EXTENSION_PATH = 'cli/azd/extensions/azure.ai.agents/';
  const WORKFLOW_PATH = '.github/workflows/approval-ext-azure-ai-agents.yml';
  const OVERRIDE_COMMAND = '/agents-extension-approval override';

  const prNumber =
    context.payload.pull_request?.number ??
    context.payload.review?.pull_request?.number ??
    context.payload.issue?.number;

  if (!prNumber) {
    core.setFailed('Could not determine PR number from event payload.');
    return;
  }

  // Helper: fetch PR data and HEAD commit date.
  async function getPrAndPushDate() {
    const { data: pr } = await github.rest.pulls.get({
      owner: context.repo.owner,
      repo: context.repo.repo,
      pull_number: prNumber,
    });
    const { data: headCommit } = await github.rest.repos.getCommit({
      owner: context.repo.owner,
      repo: context.repo.repo,
      ref: pr.head.sha,
    });
    const lastPushDate = new Date(headCommit.commit.committer.date);
    return { pr, headSha: pr.head.sha, lastPushDate };
  }

  // Helper: wrap API calls with 403 handling for fork PRs.
  async function safeCall(fn, description) {
    try {
      return await fn();
    } catch (err) {
      if (err.status === 403) {
        core.setFailed(
          `Insufficient permissions to ${description}. ` +
          'Fork PRs may not have the required token permissions for this check.'
        );
        return null;
      }
      throw err;
    }
  }

  // --- issue_comment handler (break-glass override, writes commit status) ---
  if (context.eventName === 'issue_comment') {
    const comment = (context.payload.comment.body || '').trim();
    if (comment === OVERRIDE_COMMAND) {
      const commenter = context.payload.comment.user.login.toLowerCase();
      if (!requiredApprovers.includes(commenter)) {
        core.setFailed(
          `Override denied: ${context.payload.comment.user.login} is not in the required approvers list.`
        );
        return;
      }

      // Validate the override comment was posted after the latest push.
      const { pr, headSha, lastPushDate } = await getPrAndPushDate();
      const commentDate = new Date(context.payload.comment.created_at);
      if (commentDate <= lastPushDate) {
        core.setFailed(
          'Override denied: comment was posted before the latest push. ' +
          'Please post a new override comment after the most recent commit.'
        );
        return;
      }

      // issue_comment runs don't update PR status checks automatically.
      // Write a commit status directly to the PR head SHA.
      await github.rest.repos.createCommitStatus({
        owner: context.repo.owner,
        repo: context.repo.repo,
        sha: headSha,
        state: 'success',
        context: 'Required Approval',
        description: `Override (break-glass) by ${context.payload.comment.user.login}`,
      });
      core.info(
        `Override granted via comment by ${context.payload.comment.user.login}. ` +
        `Status set on ${headSha}.`
      );
      return;
    }
  }

  // --- pull_request_review: runtime path-scope check ---
  // GitHub Actions does not support `paths:` filters on pull_request_review or
  // issue_comment events. This runtime check compensates for that limitation.
  if (context.eventName === 'pull_request_review') {
    const files = await safeCall(
      () => github.paginate(
        github.rest.pulls.listFiles,
        { owner: context.repo.owner, repo: context.repo.repo, pull_number: prNumber }
      ),
      'list PR files'
    );
    if (files === null) return;

    const touchesExtension = files.some(f =>
      f.filename.startsWith(EXTENSION_PATH) || f.filename === WORKFLOW_PATH
    );
    if (!touchesExtension) {
      core.info('PR does not touch agents extension — skipping approval check.');
      return;
    }
  }

  // --- Check for an existing override comment (break-glass, fallback scan) ---
  const comments = await safeCall(
    () => github.paginate(
      github.rest.issues.listComments,
      { owner: context.repo.owner, repo: context.repo.repo, issue_number: prNumber }
    ),
    'list PR comments'
  );
  if (comments === null) return;

  const { pr, headSha, lastPushDate } = await getPrAndPushDate();

  const validOverride = comments.find(c =>
    (c.body || '').trim() === OVERRIDE_COMMAND &&
    requiredApprovers.includes(c.user.login.toLowerCase()) &&
    new Date(c.created_at) > lastPushDate
  );
  if (validOverride) {
    core.info(
      `Override (break-glass) granted via comment by ${validOverride.user.login}.`
    );
    return;
  }

  // --- Check reviews for a valid approval ---
  const reviews = await safeCall(
    () => github.paginate(
      github.rest.pulls.listReviews,
      { owner: context.repo.owner, repo: context.repo.repo, pull_number: prNumber }
    ),
    'list PR reviews'
  );
  if (reviews === null) return;

  // Collapse reviews per user to the latest state on the current HEAD.
  // This ensures a CHANGES_REQUESTED after an APPROVED invalidates the
  // earlier approval (matching GitHub's effective review state).
  const latestByUser = new Map();
  for (const r of reviews.filter(r => r.commit_id === headSha)) {
    const key = r.user.login.toLowerCase();
    const prev = latestByUser.get(key);
    if (!prev || new Date(r.submitted_at) > new Date(prev.submitted_at)) {
      latestByUser.set(key, r);
    }
  }
  const validApprovals = [...latestByUser.values()].filter(
    r => r.state === 'APPROVED' && requiredApprovers.includes(r.user.login.toLowerCase())
  );

  if (validApprovals.length > 0) {
    const names = validApprovals.map(r => r.user.login).join(', ');
    core.info(`Approved by: ${names}`);
  } else {
    core.setFailed(
      `Requires approval from at least one of: ${requiredApprovers.join(', ')}. ` +
      'No qualifying approval found on the current commit. ' +
      'Break-glass: a required approver can post exactly "/agents-extension-approval override" to bypass.'
    );
  }
};
