# GitHub Copilot instructions

This is the Azure Developer CLI - a Go-based CLI tool for managing Azure application development workflows. It handles infrastructure provisioning, application deployment, environment management, and project lifecycle automation. Please follow these guidelines when contributing.

## Code standards

### Required before each commit
**IMPORTANT**: Before committing any changes, ensure all the following checks are performed:
- From `cli/azd/` directory, run `gofmt -s -w .` before committing any changes to ensure proper code formatting
- From `cli/azd/` directory, run `golangci-lint run ./...` to check for linting issues
- From `cli/azd/` directory, run `cspell lint "**/*.go" --relative --config ./.vscode/cspell.yaml --no-progress` to check spelling
- All Go files must include the standard copyright header:
  ```go
  // Copyright (c) Microsoft Corporation. All rights reserved.
  // Licensed under the MIT License.
  ```

### Development flow

**Build `azd` binary:**
```bash
cd cli/azd
go build
```

**Test:**
```bash
go test ./... -short
```

## Repository structure
- `cli/azd/`: Main CLI application and command definitions
- `cli/azd/cmd/`: CLI command implementations (Cobra framework)
- `cli/azd/test/`: Test helpers and mocks
- `templates/`: Sample azd templates
- `schemas/`: JSON schemas for azure.yaml
- `ext/`: Extensions for VS Code, Azure DevOps, and Dev Containers
- `eng/`: Build scripts and CI/CD pipelines

## Key guidelines
1. Follow Go best practices and idiomatic patterns.
1. Maintain existing code structure and organization, unless refactoring is necessary or directly requested.
1. Use dependency injection patterns where appropriate.
1. Write unit tests for new functionality. Use table-driven unit tests when possible.

## Testing approach
- Unit tests in `*_test.go` files alongside source code
- Update test snapshots in `cli/azd/cmd/testdata/*.snap` when changing CLI help output by setting `UPDATE_SNAPSHOTS=true` before running `go test`

## Changelog updates for releases

When preparing a new release changelog, update `cli/azd/CHANGELOG.md` and `cli/version.txt`:

### Step 1: Prepare version header
Rename any existing `## 1.x.x-beta.1 (Unreleased)` section to the version being released, without the `-beta.1` and `Unreleased` parts. Do the same for `cli/version.txt`.

### Step 2: Gather commits
**Find cutoff commit**: 
```bash
git --no-pager log --grep="Increment CLI version" --invert-grep -n 3 --follow -p -- cli/azd/CHANGELOG.md
```
Look at the commit messages and diff output to identify the commit that added the previous version's changelog.

**Get commits to process**:
```bash
git --no-pager log --oneline --pretty=format:"%h (%ad) %s" --date=short -20 origin/main
```
Increase `-20` if needed to find the cutoff commit. `git log` shows commits in reverse chronological order (newest first). You must identify the cutoff commit and only take commits newer than (above) it.

### Step 3: Gather context and write changelog entry
**IMPORTANT: For EACH commit collected, do the following:**

1. **Extract PR number**: Look for `(#XXXX)` pattern in commit message
2. **Fetch PR details** using GitHub tools: owner: `Azure`, repo: `azure-dev`, pullNumber: `PR#`
    - Get the GitHub handle of the PR owner, and determine whether the owner is outside the core team (handle not in `.github/CODEOWNERS`)
3. **Identify linked issues**: Scan PR details for GitHub issue references
4. **Fetch linked issue details** using GitHub tools: owner: `Azure`, repo: `azure-dev`, issue_number: `XXXX`
5. **Categorize change**: Features Added, Bugs Fixed, Other Changes
6. **Write changelog entry**:
    - **Format**: `- [[PR#]](https://github.com/Azure/azure-dev/pull/PR#) User-friendly description.`
    - **Guidelines**: Be brief. Start with action verbs (Add, Fix, Update, etc.) and describe user impact. Follow existing changelog entries for style.
    - **Attribution**: For PRs from contributors outside the core team, append: " Thanks @handle for the contribution!"

### Step 4: Organize and finalize
1. **Remove empty categories** and **validate formatting**
2. **Spell check**: Run `cspell lint "cli/azd/CHANGELOG.md" --relative --config cli/azd/.vscode/cspell.yaml --no-progress` and update `.vscode/cspell-github-user-aliases.txt` if needed
