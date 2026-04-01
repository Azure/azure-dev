// PR Governance: Check that a PR has at least one linked GitHub issue.
// Posts a comment if no issue is found and fails the check.
module.exports = async ({ github, context, core }) => {
  const pr = context.payload.pull_request;
  const body = pr.body || '';

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

  // Check for issue references in body
  const issuePatterns = [
    /\b(?:fixes|closes|resolves|fix|close|resolve)\s+#(\d+)/gi,
    /\b(?:fixes|closes|resolves|fix|close|resolve)\s+https:\/\/github\.com\/[^/]+\/[^/]+\/issues\/(\d+)/gi,
    /https:\/\/github\.com\/[^/]+\/[^/]+\/issues\/(\d+)/gi,
  ];

  let linkedIssueNumbers = [];
  for (const pattern of issuePatterns) {
    let match;
    while ((match = pattern.exec(body)) !== null) {
      const num = parseInt(match[1]);
      if (!linkedIssueNumbers.includes(num)) {
        linkedIssueNumbers.push(num);
      }
    }
  }

  // Also check GitHub's closing issue references (sidebar links)
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

  const sidebarLinked = result.repository.pullRequest.closingIssuesReferences.nodes;
  for (const issue of sidebarLinked) {
    if (!linkedIssueNumbers.includes(issue.number)) {
      linkedIssueNumbers.push(issue.number);
    }
  }

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
