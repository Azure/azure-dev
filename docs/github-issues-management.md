# GitHub Issues Management Guidelines

This document provides guidelines for managing GitHub issues in the Azure Developer CLI repository, based on analysis and best practices.

## Issue Analysis Tool

Use the `Analyze-GitHubIssues.ps1` script in `eng/scripts/` to get insights about issue distribution:

```powershell
# Basic analysis of open issues
.\eng\scripts\Analyze-GitHubIssues.ps1 -AuthToken "your-token"

# Export detailed analysis to JSON
.\eng\scripts\Analyze-GitHubIssues.ps1 -AuthToken "your-token" -OutputFormat "json" -OutputFile "issues-analysis.json"

# Analyze all issues (open and closed) and export to CSV
.\eng\scripts\Analyze-GitHubIssues.ps1 -AuthToken "your-token" -State "all" -OutputFormat "csv" -OutputFile "all-issues.csv"
```

## Labeling Strategy

### Priority Labels
- `critical` - Issues that block releases or cause data loss
- `high` - Important issues that should be addressed soon
- `medium` - Standard priority issues
- `low` - Nice-to-have improvements

### Type Labels
- `bug` - Something isn't working as expected
- `enhancement` - Improvements to existing functionality
- `feature` - New functionality requests
- `documentation` - Documentation improvements
- `question` - User questions or support requests
- `task` - Internal tasks or maintenance work

### Component Labels
- `cli` - Issues related to the CLI itself
- `templates` - Issues with azd templates
- `infrastructure` - Bicep/Terraform infrastructure issues
- `hooks` - Issues with project hooks
- `auth` - Authentication and authorization issues
- `deployment` - Deployment-related issues
- `telemetry` - Telemetry and monitoring issues

### Status Labels
- `needs-triage` - New issues that need initial assessment
- `needs-investigation` - Issues requiring research or reproduction
- `blocked` - Issues blocked by external dependencies
- `duplicate` - Duplicate of another issue
- `wontfix` - Issues that won't be addressed
- `help-wanted` - Issues suitable for community contributions

## Issue Lifecycle Management

### 1. New Issues (Within 24 hours)
- **Triage**: Add appropriate labels (type, priority, component)
- **Assignment**: Assign to appropriate team member if clear ownership
- **Milestone**: Add to milestone if part of planned work
- **Response**: Acknowledge receipt and ask for clarification if needed

### 2. Active Issues
- **Updates**: Provide regular updates on progress
- **Blocking**: Mark as blocked if waiting on external dependencies
- **Scope**: Break down large issues into smaller, actionable tasks

### 3. Stale Issues (30+ days without activity)
- **Review**: Assess if still relevant and actionable
- **Ping**: Contact assignee or stakeholders for status update
- **Reprioritize**: Adjust priority based on current needs

### 4. Old Issues (90+ days without activity)
- **Evaluate**: Determine if still valid with current codebase
- **Close**: Close if no longer relevant or superseded
- **Archive**: Convert to discussions if still relevant but not actionable

## Cleanup Process

### Monthly Review
1. **Run Analysis**: Use the analysis script to identify problem areas
2. **Review Unlabeled**: Triage issues without labels
3. **Check Stale**: Review issues without recent activity
4. **Update Milestones**: Adjust milestone assignments based on priorities

### Quarterly Cleanup
1. **Close Outdated**: Close issues that are no longer relevant
2. **Merge Duplicates**: Consolidate duplicate issues
3. **Update Documentation**: Refresh issue templates and guidelines
4. **Review Labels**: Evaluate label effectiveness and consistency

## Automation Opportunities

### GitHub Actions
- **Auto-labeling**: Automatically label issues based on keywords or file paths
- **Stale bot**: Automatically mark and close inactive issues
- **Triage bot**: Auto-assign issues based on component labels
- **Milestone management**: Automatically move issues between milestones

### Issue Templates
Create specific templates for:
- Bug reports with reproduction steps
- Feature requests with use cases
- Documentation improvements
- Template issues

## Metrics and KPIs

Track these metrics monthly:
- **Total open issues**: Target < 800
- **Issues without labels**: Target < 5%
- **Stale issues (90+ days)**: Target < 10%
- **Average time to first response**: Target < 2 days
- **Average time to resolution**: Track by issue type

## Best Practices

### For Maintainers
1. **Respond quickly**: Acknowledge new issues within 24 hours
2. **Be clear**: Use clear, actionable language in comments
3. **Link related issues**: Cross-reference related issues and PRs
4. **Update regularly**: Provide status updates on active issues
5. **Close promptly**: Close issues when completed or no longer valid

### For Contributors
1. **Search first**: Look for existing issues before creating new ones
2. **Use templates**: Fill out issue templates completely
3. **Be specific**: Provide clear reproduction steps and expected behavior
4. **Stay engaged**: Respond to questions and provide additional information
5. **Test solutions**: Verify that fixes actually resolve the issue

### For Users
1. **Provide context**: Include environment details and use cases
2. **Be patient**: Understand that maintainers are volunteers with limited time
3. **Help others**: Answer questions when you have knowledge to share
4. **Report bugs clearly**: Include steps to reproduce and expected vs actual behavior

## Tools and Resources

### GitHub CLI Commands
```bash
# List issues by label
gh issue list --label "bug" --state open

# Create issue with labels
gh issue create --title "Bug title" --body "Description" --label "bug,high"

# Close issue with comment
gh issue close 123 --comment "Fixed in PR #456"
```

### Useful Queries
- Open bugs: `is:issue is:open label:bug`
- High priority items: `is:issue is:open label:high`
- Stale issues: `is:issue is:open updated:<2024-01-01`
- Unlabeled issues: `is:issue is:open no:label`

## Review and Updates

This document should be reviewed and updated:
- **Monthly**: Based on analysis results and team feedback
- **Quarterly**: For major process improvements
- **After incidents**: When issues reveal process gaps
- **On tool changes**: When GitHub features or automation changes

## Contact

For questions about issue management:
- **Team Lead**: @kristenwomack
- **Maintainers**: See [CODEOWNERS](.github/CODEOWNERS)
- **Discussions**: Use GitHub Discussions for process questions