# Setting Up Automatic GitHub Copilot Code Review

This document explains how to configure automatic GitHub Copilot code review for pull requests in the Azure Developer CLI repository.

## Overview

GitHub Copilot can automatically review pull requests when they are opened or updated, providing AI-powered feedback on code quality, potential bugs, security issues, and best practices. This feature helps maintain code quality and catch issues early in the development process.

## Prerequisites

- Repository must have GitHub Copilot enabled (requires GitHub Copilot Business or Enterprise)
- User must have admin permissions to configure repository rulesets
- Pull requests must be enabled for the repository

## Configuration Steps

### 1. Enable Automatic Copilot Reviews via Repository Rulesets

1. Navigate to the repository on GitHub
2. Click **Settings** → **Rules** → **Rulesets**
3. Click **New ruleset** → **New branch ruleset**
4. Configure the ruleset:
   - **Name**: "Copilot PR Review"
   - **Enforcement status**: Active
   - **Target branches**: Select branches (e.g., `main`, `develop`, or use patterns like `feature/*`)
5. Under **Branch rules**, enable **"Automatically request Copilot code review"**
6. Configure when reviews should trigger:
   - ✅ **Review new pull requests** (recommended)
   - ⚠️ **Review new pushes** (optional, may increase review volume)
   - ⚠️ **Review draft pull requests** (optional)
7. Click **Create** to save the ruleset

### 2. Configure Repository Settings

Ensure the following files are in place to guide Copilot's reviews:

- **`.github/copilot-instructions.md`**: Contains coding standards and architectural guidelines specific to this repository
- **`.github/PULL_REQUEST_TEMPLATE.md`**: Provides a checklist for PR authors and helps Copilot understand expectations
- **`.github/workflows/copilot-setup-steps.yml`**: Ensures the development environment is properly set up for Copilot coding sessions

### 3. Customize Copilot Review Behavior

Copilot reviews are influenced by:

1. **Repository guidelines** in `.github/copilot-instructions.md`:
   - Add specific patterns or anti-patterns to check for
   - Include security requirements
   - Define code style preferences

2. **Pull request context**:
   - PR title and description
   - Linked issues
   - File changes and diff content

3. **Repository history**:
   - Previous PR reviews
   - Commit messages
   - Code patterns in the repository

### 4. Testing the Configuration

1. Create a test branch and make a small change
2. Open a pull request targeting a branch with the ruleset enabled
3. Copilot should automatically post a review comment within a few moments
4. Review the feedback and adjust `.github/copilot-instructions.md` as needed

## Using Copilot Reviews

### For PR Authors

- Copilot reviews appear as comments on your PR, similar to human reviews
- Address Copilot's feedback by making code changes or responding to comments
- You can request additional reviews by pushing new commits or using the "Re-request review" button
- Not all Copilot suggestions need to be implemented - use your judgment

### For PR Reviewers

- Copilot reviews supplement, not replace, human code reviews
- Use Copilot's feedback as a starting point for deeper review
- Copilot may catch common issues, allowing reviewers to focus on architecture and design
- Flag any incorrect or misleading Copilot comments to help improve future reviews

## Best Practices

1. **Start with targeted branches**: Enable Copilot reviews for feature branches first before applying to main/production branches
2. **Iterate on instructions**: Regularly update `.github/copilot-instructions.md` based on recurring review patterns
3. **Combine with CI/CD**: Use Copilot reviews alongside automated testing and linting for comprehensive quality checks
4. **Monitor feedback quality**: Track false positives and adjust guidelines to reduce noise
5. **Educate team members**: Ensure all contributors understand how to work with Copilot reviews

## Troubleshooting

### Copilot reviews are not appearing

- Verify the repository has Copilot enabled (check repository settings)
- Confirm the ruleset is active and targets the correct branches
- Check that the PR is not in draft mode (unless draft reviews are enabled)
- Ensure the PR has actual code changes (empty or documentation-only PRs may not trigger reviews)

### Review feedback is not relevant

- Update `.github/copilot-instructions.md` with more specific guidelines
- Add examples of desired patterns in the instructions
- Use clear, descriptive PR titles and descriptions
- Link related issues for additional context

### Too many reviews are being triggered

- Disable "Review new pushes" if getting reviews on every commit
- Use more specific branch targeting in the ruleset
- Consider separate rulesets for different types of branches (e.g., feature vs. hotfix)

## Additional Resources

- [GitHub Copilot Code Review Documentation](https://docs.github.com/en/copilot/how-tos/use-copilot-agents/request-a-code-review)
- [Configuring Automatic Code Review](https://docs.github.com/en/copilot/how-tos/use-copilot-agents/request-a-code-review/configure-automatic-review)
- [GitHub Rulesets Documentation](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets)
- [Azure Developer CLI Contribution Guidelines](https://github.com/Azure/azure-dev/blob/main/CONTRIBUTING.md)

## Support

For issues with Copilot reviews:
- Check [GitHub Copilot status](https://www.githubstatus.com/)
- Review [GitHub Community discussions](https://github.com/orgs/community/discussions)
- Contact repository maintainers for repository-specific configuration help
