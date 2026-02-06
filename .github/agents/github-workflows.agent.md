---
name: GitHub Workflows Instructions
description: Guidelines for creating and maintaining GitHub Actions workflows
infer: true
---

# GitHub Workflows Instructions

Guidelines for creating and maintaining GitHub Actions workflows in `.github/workflows/*.yml`.

## General Principles

### Third-Party Actions Policy

**Do not use third-party actions** except for the following approved actions:
- `actions/*` (official GitHub actions like `actions/checkout`, `actions/setup-go`, `actions/setup-node`)
- Well-established, organization-specific actions that are widely used (e.g., `golangci/golangci-lint-action`)

For linting and checking tools that don't have approved third-party actions:
1. Install and run the tool directly using shell commands
2. Use GitHub Actions problem matchers to create annotations

### Problem Matchers for Annotations

To surface errors and warnings as GitHub Actions annotations without third-party actions:

1. **Create a problem matcher JSON file** in `.github/` directory (e.g., `.github/shellcheck-matcher.json`):
   ```json
   {
     "problemMatcher": [
       {
         "owner": "tool-name",
         "pattern": [
           {
             "regexp": "^([^:]+):(\\d+):(\\d+):\\s+(warning|error|note|style):\\s+(.*)$",
             "file": 1,
             "line": 2,
             "column": 3,
             "severity": 4,
             "message": 5
           }
         ]
       }
     ]
   }
   ```
   
   Note: Adjust the severity levels in the regex to match your tool's output format.

2. **Register the matcher** before running the tool:
   ```yaml
   - name: Register problem matcher
     run: echo "::add-matcher::.github/tool-matcher.json"
   ```

3. **Run the tool** with output format matching the regex:
   ```yaml
   - name: Run tool
     run: tool --format gcc files
   ```

4. **Unregister the matcher** (optional, but recommended):
   ```yaml
   - name: Unregister problem matcher
     if: always()
     run: echo "::remove-matcher owner=tool-name::"
   ```

### ShellCheck Example

For shellcheck specifically:
- ShellCheck is pre-installed on `ubuntu-latest` runners
- Use `shellcheck -f gcc` format for gcc-style output
- Problem matcher regex should match: `file:line:column: severity: message [CODE]`
- **Check only changed files** in PRs to avoid blocking unrelated changes

Example workflow (checking only changed files in PR):
```yaml
steps:
  - uses: actions/checkout@v4
    with:
      fetch-depth: 0  # Required for git diff
  - name: Get changed shell scripts
    id: changed-files
    run: |
      git fetch origin ${{ github.base_ref }}
      CHANGED_SH_FILES=$(git diff --name-only --diff-filter=ACMRT origin/${{ github.base_ref }}...HEAD | grep '\.sh$' || true)
      if [ -z "$CHANGED_SH_FILES" ]; then
        echo "No shell scripts changed"
        echo "files=" >> $GITHUB_OUTPUT
      else
        echo "Changed shell scripts:"
        echo "$CHANGED_SH_FILES"
        echo "files<<EOF" >> $GITHUB_OUTPUT
        echo "$CHANGED_SH_FILES" >> $GITHUB_OUTPUT
        echo "EOF" >> $GITHUB_OUTPUT
      fi
  - name: Register ShellCheck problem matcher
    if: steps.changed-files.outputs.files != ''
    run: echo "::add-matcher::.github/shellcheck-matcher.json"
  - name: Run ShellCheck
    if: steps.changed-files.outputs.files != ''
    run: |
      echo "${{ steps.changed-files.outputs.files }}" | xargs shellcheck -f gcc
  - name: Unregister ShellCheck problem matcher
    if: always() && steps.changed-files.outputs.files != ''
    run: echo "::remove-matcher owner=shellcheck-gcc::"
```

Note: 
- Using `fetch-depth: 0` ensures full git history is available for diff comparison
- `--diff-filter=ACMRT` includes only added, copied, modified, renamed, or type-changed files
- Checking only changed files prevents blocking PRs due to pre-existing issues in unrelated scripts

## Workflow Structure

### Standard Patterns

All workflows should follow these patterns from existing workflows:

1. **Trigger only on relevant changes**:
   ```yaml
   on:
     pull_request:
       paths:
         - "path/to/relevant/files/**"
         - ".github/workflows/this-workflow.yml"
       branches: [main]
   ```

2. **Include concurrency control** to cancel old runs:
   ```yaml
   concurrency:
     group: ${{ github.workflow }}-${{ github.event.pull_request.number }}
     cancel-in-progress: true
   ```

3. **Minimal permissions** (principle of least privilege):
   ```yaml
   permissions:
     contents: read
   ```

4. **Use specific versions** for setup actions:
   ```yaml
   - uses: actions/setup-go@v6
     with:
       go-version: "^1.25"
   ```

## Pre-installed Tools

The following tools are pre-installed on `ubuntu-latest` runners and don't need installation:
- shellcheck
- git
- curl
- jq
- Many others - check GitHub's runner images documentation

## Testing Workflows

Before committing a new workflow:
1. Validate YAML syntax: `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/new-workflow.yml'))"`
2. Test any shell commands locally
3. Verify problem matcher regex against actual tool output
