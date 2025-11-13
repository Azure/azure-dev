## What does this PR do?
`[Provide a clear, concise description of the changes]`

`[Any additional context, screenshots, or information that helps reviewers]`

## GitHub issue number?
`[Link to the GitHub issue this PR addresses]`

## Pre-merge Checklist
- [ ] Required for All PRs
    - [ ] **Read [contribution guidelines](https://github.com/Azure/azure-dev/blob/main/CONTRIBUTING.md)**
    - [ ] PR title clearly describes the change
    - [ ] Commit history is clean with descriptive messages
    - [ ] Added comprehensive tests for new/modified functionality
    - [ ] Code follows repository standards (see `.github/copilot-instructions.md`)
    - [ ] All checks pass (formatting, linting, tests)

- [ ] For azd CLI changes (`cli/azd/`):
    - [ ] Ran `gofmt -s -w .` from `cli/azd/` directory
    - [ ] Ran `golangci-lint run ./...` from `cli/azd/` directory
    - [ ] Ran `cspell lint "**/*.go" --relative --config ./.vscode/cspell.yaml --no-progress` from `cli/azd/` directory
    - [ ] All Go files include standard copyright header
    - [ ] Updated `cli/azd/CHANGELOG.md` for user-facing changes (features, bug fixes, breaking changes)
    - [ ] Ran `go test ./... -short` (allow up to 10 minutes)
    - [ ] Updated snapshot tests if CLI help output changed (set `UPDATE_SNAPSHOTS=true`)

- [ ] For template changes (`templates/`):
    - [ ] Tested template deployment end-to-end
    - [ ] Updated template documentation

- [ ] For extension changes (`ext/`):
    - [ ] Tested extension functionality
    - [ ] Updated extension documentation

## Additional Notes
`[Optional: Any deployment considerations, breaking changes, or migration steps]`
