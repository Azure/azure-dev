# Issue Templates Guide

This directory contains GitHub issue forms that help users report bugs and request features for the Azure Developer CLI (azd).

## Available Templates

### Bug Report (`bug_report.yml`)
Use this template to report bugs, errors, or unexpected behavior in azd.

**Required Information:**
- **azd version**: Output from `azd version` command
- **Operating System**: Dropdown selection (Windows, macOS, Linux variants, WSL, Other)
- **OS Version**: Specific version (e.g., Windows 11 22H2, macOS 14.1, Ubuntu 22.04)
- **Description**: Clear description of the bug
- **Command executed**: Full azd command with all arguments and flags
- **Expected behavior**: What should have happened
- **Actual behavior**: What actually happened (include error messages)
- **Steps to reproduce**: Detailed steps to reproduce the issue

**Optional Information:**
- **azd template used**: Template name if applicable (e.g., Azure-Samples/todo-nodejs-mongo)
- **Azure region**: Target Azure region if relevant
- **Environment details**: Programming language, IDE, shell, Docker version, etc.
- **Full error logs**: Complete logs from `azd <command> --debug` or from `~/.azd/logs/`
- **Additional context**: Screenshots, workarounds, etc.

### Feature Request (`feature_request.yml`)
Use this template to suggest new features or enhancements.

**Required Information:**
- **Proposed solution**: What you'd like to see implemented

**Optional Information:**
- **Problem description**: Related problem or frustration
- **Alternatives**: Other solutions you've considered
- **Use case**: Real-world scenarios where this would help
- **Additional context**: Examples, references, etc.

## Configuration (`config.yml`)

The configuration file:
- Disables blank issue creation (users must use one of the templates)
- Provides helpful links to:
  - GitHub Discussions for questions
  - Official documentation
  - azd template samples

## Why These Fields Matter

The information collected in the bug report template is specifically designed to enable issue reproduction:

1. **azd version** - Different versions may have different bugs; essential for reproduction
2. **Operating System + Version** - Many issues are platform-specific
3. **Command executed** - Exact command is crucial for reproduction
4. **Steps to reproduce** - Allows maintainers to follow the same path
5. **Expected vs Actual behavior** - Clarifies what's wrong
6. **Template name** - Templates can have specific issues
7. **Azure region** - Some issues are region-specific
8. **Environment details** - Language/IDE versions can affect behavior
9. **Full logs** - Detailed error information helps diagnosis

## Best Practices for Issue Reporters

1. **Search first**: Check if the issue already exists
2. **Update azd**: Verify you're on the latest version
3. **Be specific**: The more detail, the easier to reproduce
4. **Include commands**: Copy-paste exact commands used
5. **Add logs**: Use `--debug` flag for verbose output
6. **Minimal reproduction**: Simplify to the smallest reproducible case

## Best Practices for Maintainers

1. **Request missing info**: Use the template fields as a checklist
2. **Label appropriately**: Use the auto-applied labels (bug, needs-triage, enhancement)
3. **Confirm reproduction**: Try to reproduce with the provided information
4. **Ask for clarification**: If steps are unclear, ask for more detail
5. **Update labels**: Change from needs-triage once triaged

## Testing Issue Forms Locally

GitHub issue forms are rendered on GitHub.com, but you can validate YAML syntax locally:

```bash
# Validate YAML syntax
python3 -c "import yaml; yaml.safe_load(open('.github/ISSUE_TEMPLATE/bug_report.yml'))"
python3 -c "import yaml; yaml.safe_load(open('.github/ISSUE_TEMPLATE/feature_request.yml'))"
python3 -c "import yaml; yaml.safe_load(open('.github/ISSUE_TEMPLATE/config.yml'))"
```

## References

- [GitHub Issue Forms Documentation](https://docs.github.com/en/communities/using-templates-to-encourage-useful-issues-and-pull-requests/syntax-for-issue-forms)
- [GitHub Issue Form Schema](https://docs.github.com/en/communities/using-templates-to-encourage-useful-issues-and-pull-requests/syntax-for-githubs-form-schema)
