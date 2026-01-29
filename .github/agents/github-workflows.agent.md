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

Example workflow:
```yaml
steps:
  - uses: actions/checkout@v4
  - name: Register ShellCheck problem matcher
    run: echo "::add-matcher::.github/shellcheck-matcher.json"
  - name: Run ShellCheck
    run: |
      find . -name "*.sh" -type f \
        -not -path "*/.*" \
        -not -path "*/node_modules/*" \
        -not -path "*/vendor/*" \
        -exec shellcheck -f gcc {} +
  - name: Unregister ShellCheck problem matcher
    if: always()
    run: echo "::remove-matcher owner=shellcheck-gcc::"
```

Note: 
- Using `{} +` instead of `{} \;` batches all files into a single shellcheck invocation, improving performance and allowing shellcheck to follow sourced dependencies.
- Excludes hidden directories (e.g., `.git/`), `node_modules/`, and `vendor/` to avoid checking unintended scripts.

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
