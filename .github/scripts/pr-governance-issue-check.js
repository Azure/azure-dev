// PR Governance: Check that a PR has at least one linked GitHub issue.
// Posts a comment if no issue is found and fails the check.
module.exports = async ({ github, context, core }) => {
  const pr = context.payload.pull_request;

  // Skip for dependabot and automated PRs
  const skipAuthors = ['dependabot[bot]', 'dependabot', 'app/dependabot'];
  if (skipAuthors.includes(pr.user.login)) {
    console.log(`Skipping: automated PR by ${pr.user.login}`);
    core.setOutput('skipped', 'true');
    return;
  }

  // Skip for PRs with specific labels
  const labels = pr.labels.map(l => l.name);
  const skipLabels = ['skip-governance'];
  if (labels.some(l => skipLabels.includes(l))) {
    console.log(`Skipping: PR has exempt label`);
    core.setOutput('skipped', 'true');
    return;
  }

  // Check linked issues via GitHub's closingIssuesReferences API
  // Covers closing keywords (Fixes/Closes/Resolves #123) and sidebar links
  const query = `query($owner: String!, $repo: String!, $number: Int!) {
    repository(owner: $owner, name: $repo) {
      pullRequest(number: $number) {
        closingIssuesReferences(first: 10) {
          nodes { number }
        }
      }
    }
  }`;

  const result = await github.graphql(query, {
    owner: context.repo.owner,
    repo: context.repo.repo,
    number: pr.number,
  });

  const linkedIssues = result.repository.pullRequest.closingIssuesReferences.nodes;
  const linkedIssueNumbers = linkedIssues.map(i => i.number);

  if (linkedIssueNumbers.length === 0) {
    const BOT_MARKER = '<!-- pr-governance-priority -->';
    const comments = await github.paginate(github.rest.issues.listComments, {
      owner: context.repo.owner,
      repo: context.repo.repo,
      issue_number: pr.number,
      per_page: 100,
    });
    const existingComment = comments.find(c => c.body && c.body.includes(BOT_MARKER));

    const commentBody = [
      BOT_MARKER,
      `### 🔗 Linked Issue Required`,
      '',
      'Thanks for the contribution! Please link a GitHub issue to this PR by adding `Fixes #123` to the description or using the sidebar.',
      'No issue yet? Feel free to [create one](https://github.com/Azure/azure-dev/issues/new)!',
    ].join('\n');

    try {
      if (existingComment) {
        await github.rest.issues.updateComment({
          owner: context.repo.owner,
          repo: context.repo.repo,
          comment_id: existingComment.id,
          body: commentBody,
        });
      } else {
        await github.rest.issues.createComment({
          owner: context.repo.owner,
          repo: context.repo.repo,
          issue_number: pr.number,
          body: commentBody,
        });
      }
    } catch (e) {
      console.log(`Could not post comment (expected for fork PRs): ${e.message}`);
    }

    core.setFailed('PR must be linked to a GitHub issue.');
    return;
  }

  console.log(`✅ PR has linked issue(s): ${linkedIssueNumbers.join(', ')}`);
  core.setOutput('issue_numbers', JSON.stringify(linkedIssueNumbers));
};
