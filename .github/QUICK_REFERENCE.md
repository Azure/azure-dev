# Quick Reference: GitHub Copilot PR Review

## For Repository Admins

### Enable Automatic Copilot Reviews (One-Time Setup)

1. Go to **Settings** → **Rules** → **Rulesets**
2. Click **New ruleset** → **New branch ruleset**
3. Configure:
   - Name: `Copilot PR Review`
   - Enforcement: `Active`
   - Target branches: `main` (or your default branch)
4. Enable **"Automatically request Copilot code review"**
5. Check **"Review new pull requests"**
6. Click **Create**

📖 **Full Guide**: See [COPILOT_REVIEW_SETUP.md](COPILOT_REVIEW_SETUP.md)

## For Contributors

### When Creating a Pull Request

1. **Use the PR template** (auto-populated when creating PR)
2. **Fill out all sections**:
   - What does this PR do?
   - GitHub issue number
   - Pre-merge checklist
3. **Run required checks** before submitting:
   ```bash
   # For CLI changes
   cd cli/azd
   gofmt -s -w .
   golangci-lint run ./...
   cspell lint "**/*.go" --relative --config ./.vscode/cspell.yaml --no-progress
   go test ./... -short
   ```
4. **Wait for Copilot review** (appears automatically if enabled)
5. **Address feedback** or respond to comments

### Working with Copilot Reviews

- ✅ Copilot reviews supplement human reviews
- ✅ Not all suggestions need to be implemented - use judgment
- ✅ You can request additional reviews by pushing new commits
- ✅ Mark conversations as resolved when addressed

## For Reviewers

### Understanding Copilot Reviews

- 🤖 Copilot reviews appear as comments on the PR
- 🤖 They check for:
  - Code quality issues
  - Potential bugs
  - Security vulnerabilities
  - Best practices violations
  - Consistency with repository guidelines
- 🤖 Copilot reviews are **AI-generated** - verify before enforcing

### Best Practices

1. Review Copilot's feedback first
2. Use it as a starting point for deeper review
3. Focus your human review on:
   - Architecture and design
   - Business logic correctness
   - User experience
   - Integration with existing code
4. Flag any incorrect Copilot comments

## Documentation Index

| Document | Purpose | Audience |
|----------|---------|----------|
| [PULL_REQUEST_TEMPLATE.md](PULL_REQUEST_TEMPLATE.md) | PR submission checklist | Contributors |
| [copilot-instructions.md](copilot-instructions.md) | Coding standards and architecture | Contributors & Copilot |
| [COPILOT_REVIEW_SETUP.md](COPILOT_REVIEW_SETUP.md) | Setup guide for automatic reviews | Repository Admins |
| [COPILOT_COMPARISON.md](COPILOT_COMPARISON.md) | Comparison with microsoft/mcp | Maintainers |
| [IMPLEMENTATION_SUMMARY.md](IMPLEMENTATION_SUMMARY.md) | Implementation overview | Maintainers |
| [QUICK_REFERENCE.md](QUICK_REFERENCE.md) | This document | Everyone |

## Common Issues

### Copilot review not appearing?

1. Check if repository has Copilot enabled
2. Verify ruleset is active for your branch
3. Ensure PR has actual code changes
4. Check if draft PRs are excluded from review

### Copilot feedback not relevant?

- Update [copilot-instructions.md](copilot-instructions.md) with more specific guidelines
- Use clear PR titles and descriptions
- Link related issues for context

### Too many reviews?

- Adjust ruleset to not review every push
- Use more specific branch targeting
- Consider separate rulesets for different branch types

## Resources

- [GitHub Copilot Documentation](https://docs.github.com/en/copilot)
- [Configuring Automatic Reviews](https://docs.github.com/en/copilot/how-tos/use-copilot-agents/request-a-code-review/configure-automatic-review)
- [Repository Contribution Guidelines](../CONTRIBUTING.md)

---

**Questions?** See [COPILOT_REVIEW_SETUP.md](COPILOT_REVIEW_SETUP.md) for detailed information.
