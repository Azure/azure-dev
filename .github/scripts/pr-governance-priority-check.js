// PR Governance: Check sprint/milestone status of linked issues
// and post informational comments to help contributors understand prioritization.
module.exports = async ({ github, context, core }) => {
  let issueNumbers;
  try {
    issueNumbers = JSON.parse(process.env.ISSUE_NUMBERS || '[]');
  } catch {
    console.log('No valid issue numbers provided, skipping priority check');
    return;
  }
  if (!Array.isArray(issueNumbers) || issueNumbers.length === 0) return;
  const pr = context.payload.pull_request;
  const projectToken = process.env.PROJECT_TOKEN;

  // Determine current month milestone name (e.g., "April 2026")
  const now = new Date();
  const monthNames = [
    'January', 'February', 'March', 'April', 'May', 'June',
    'July', 'August', 'September', 'October', 'November', 'December'
  ];
  const currentMilestoneName = `${monthNames[now.getMonth()]} ${now.getFullYear()}`;
  let issueDetails = [];

  // Check sprint assignment via Project #182 (if token available)
  let sprintInfo = {};
  if (projectToken) {
    try {
      async function graphqlWithToken(query, token) {
        const response = await fetch('https://api.github.com/graphql', {
          method: 'POST',
          headers: {
            'Authorization': `bearer ${token}`,
            'Content-Type': 'application/json',
          },
          body: JSON.stringify({ query }),
        });
        const json = response.ok ? await response.json() : null;
        if (!response.ok) throw new Error(`GitHub API returned ${response.status}: ${response.statusText}`);
        if (json.errors) throw new Error(json.errors[0].message);
        return json.data;
      }

      // Get current sprint iteration
      const sprintData = await graphqlWithToken(`{
        organization(login: "Azure") {
          projectV2(number: 182) {
            field(name: "Sprint") {
              ... on ProjectV2IterationField {
                id
                configuration {
                  iterations { id title startDate duration }
                }
              }
            }
          }
        }
      }`, projectToken);

      const iterations = sprintData.organization.projectV2.field.configuration.iterations;

      // Find the current sprint (today falls within start + duration)
      const today = new Date();
      const currentSprint = iterations.find(iter => {
        const start = new Date(iter.startDate);
        const end = new Date(start);
        end.setDate(end.getDate() + iter.duration);
        return today >= start && today < end;
      });

      if (currentSprint) {
        console.log(`Current sprint: ${currentSprint.title}`);

        // Query sprint assignment per issue
        for (const issueNum of issueNumbers) {
          const num = parseInt(issueNum, 10);
          if (isNaN(num)) continue;
          try {
            const issueData = await graphqlWithToken(`{
              repository(owner: "Azure", name: "azure-dev") {
                issue(number: ${num}) {
                  projectItems(first: 10) {
                    nodes {
                      project { number }
                      fieldValueByName(name: "Sprint") {
                        ... on ProjectV2ItemFieldIterationValue {
                          title
                        }
                      }
                    }
                  }
                }
              }
            }`, projectToken);

            const projectItems = issueData.repository.issue.projectItems.nodes;
            const match = projectItems.find(item =>
              item.project.number === 182 && item.fieldValueByName?.title === currentSprint.title
            );
            if (match) {
              sprintInfo[issueNum] = match.fieldValueByName.title;
              console.log(`Issue #${issueNum} sprint: ${match.fieldValueByName.title}`);
            }
          } catch (err) {
            console.log(`Could not check sprint for issue #${issueNum}: ${err.message}`);
          }
        }
      }
    } catch (err) {
      console.log(`Sprint check skipped: ${err.message}`);
    }
  } else {
    console.log('Sprint check skipped: no PROJECT_READ_TOKEN');
  }

  // If sprint found, skip milestone check entirely
  const hasCurrentSprint = issueNumbers.some(n => sprintInfo[n]);

  if (!hasCurrentSprint) {
    // Fetch milestones for each issue
    for (const issueNum of issueNumbers) {
      try {
        const issue = await github.rest.issues.get({
          owner: context.repo.owner,
          repo: context.repo.repo,
          issue_number: issueNum,
        });

        const milestone = issue.data.milestone;
        const milestoneTitle = milestone ? milestone.title : 'None';

        issueDetails.push({
          number: issueNum,
          milestone: milestoneTitle,
          sprint: null,
          isCurrentMonth: milestoneTitle === currentMilestoneName,
        });
      } catch (err) {
        console.log(`Could not fetch issue #${issueNum}: ${err.message}`);
      }
    }
  }

  const hasCurrentMilestone = issueDetails.some(i => i.isCurrentMonth);
  const allLookupsFailed = !hasCurrentSprint && issueDetails.length === 0;

  if (allLookupsFailed) {
    console.log('⚠️ Could not determine sprint or milestone status — skipping comment');
    return;
  }

  // Find existing bot comment to update
  const BOT_MARKER = '<!-- pr-governance-priority -->';
  let existingComment;
  try {
    const comments = await github.paginate(github.rest.issues.listComments, {
      owner: context.repo.owner,
      repo: context.repo.repo,
      issue_number: pr.number,
      per_page: 100,
    });
    existingComment = comments.find(c => c.body?.includes(BOT_MARKER));
  } catch (e) {
    console.log(`Could not list comments (expected for fork PRs): ${e.message}`);
  }

  let commentBody = '';

  if (hasCurrentSprint) {
    const sprintName = Object.values(sprintInfo)[0] || 'current sprint';
    console.log(`✅ Issue is in current sprint: ${sprintName}. All good!`);

    // Delete existing comment if one was posted earlier
    if (existingComment) {
      try {
        await github.rest.issues.deleteComment({
          owner: context.repo.owner,
          repo: context.repo.repo,
          comment_id: existingComment.id,
        });
        console.log('Removed prior governance comment — issue is now in sprint');
      } catch (e) {
        console.log(`Could not remove comment (expected for fork PRs): ${e.message}`);
      }
    }
    return;
  } else if (hasCurrentMilestone) {
    console.log('✅ Issue is in the current milestone');
    commentBody = [
      BOT_MARKER,
      `### 📋 Milestone: ${currentMilestoneName}`,
      '',
      `This work is tracked for **${currentMilestoneName}**. The team will review it soon!`,
    ].join('\n');
  } else {
    console.log('ℹ️ Issue is not in current sprint or milestone');
    commentBody = [
      BOT_MARKER,
      `### 📋 Prioritization Note`,
      '',
      `Thanks for the contribution! The linked issue isn't in the current milestone yet.`,
      'Review may take a bit longer — reach out to **@rajeshkamal5050** or **@kristenwomack** if you\'d like to discuss prioritization.',
    ].join('\n');
  }

  // Post or update comment
  try {
    if (existingComment) {
      await github.rest.issues.updateComment({
        owner: context.repo.owner,
        repo: context.repo.repo,
        comment_id: existingComment.id,
        body: commentBody,
      });
      console.log('Updated existing governance comment');
    } else {
      await github.rest.issues.createComment({
        owner: context.repo.owner,
        repo: context.repo.repo,
        issue_number: pr.number,
        body: commentBody,
      });
      console.log('Posted governance comment');
    }
  } catch (e) {
    console.log(`Could not post comment (expected for fork PRs): ${e.message}`);
  }
};
