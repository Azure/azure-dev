// Required Approval Gate for Foundry Extensions (Shared)
//
// Validates that at least one required approver has approved the current HEAD,
// or that a valid break-glass override comment exists.
//
// This script is parameterized and called by per-extension approval workflows.
// Parameters are passed via environment variables:
//   EXTENSION_PATH    - e.g. 'cli/azd/extensions/azure.ai.connections/'
//   WORKFLOW_PATH     - e.g. '.github/workflows/approval-ext-azure-ai-connections.yml'
//   OVERRIDE_COMMAND  - e.g. '/connections-extension-approval override'
//   REQUIRED_APPROVERS - JSON array of GitHub logins (optional, uses default list if not set)
module.exports = async ({ github, context, core }) => {
  const DEFAULT_APPROVERS = [
    'trangevi',
    'trrwilson',
    'therealjohn',
    'glharper',
  ];

  const EXTENSION_PATH = process.env.EXTENSION_PATH;
  const WORKFLOW_PATH = process.env.WORKFLOW_PATH;
  const OVERRIDE_COMMAND = process.env.OVERRIDE_COMMAND;

  if (!EXTENSION_PATH || !WORKFLOW_PATH || !OVERRIDE_COMMAND) {
    core.setFailed(
      'Missing required environment variables: EXTENSION_PATH, WORKFLOW_PATH, OVERRIDE_COMMAND'
    );
    return;
  }

  const requiredApprovers = process.env.REQUIRED_APPROVERS
    ? JSON.parse(process.env.REQUIRED_APPROVERS)
    : DEFAULT_APPROVERS;

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
      `Break-glass: a required approver can post exactly "${OVERRIDE_COMMAND}" to bypass.`
    );
  }
};
