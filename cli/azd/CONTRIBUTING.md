# Contributing to `azd`

Hello there 👋! Thank you for showing interest in contributing to `azd`.

## Guidelines

- [Code Style Guide](./docs/style-guidelines/azd-style-guide.md)
- [Extensions Style Guide](./docs/extensions/extensions-style-guide.md)
- [Adding New Commands](./docs/style-guidelines/new-azd-command.md)
- [Guiding Principles](./docs/style-guidelines/guiding-principles.md)

In general, to make contributions a smooth and easy experience, we encourage the following:

- Check existing issues for [bugs][bug issues] or [enhancements][enhancement issues].
- Open an issue if things aren't working as expected, or if an enhancement is being proposed.
- Start a conversation on the issue if you are thinking of submitting a pull request.
- Submit a pull request **linked to the issue** (e.g., add `Fixes #123` to the PR description). PRs without a linked issue will be flagged by our automated checks. Issues in the current milestone get priority review — if yours isn't prioritized yet, tag **@rajeshkamal5050** or **@kristenwomack** and we'll help get it sorted.
- The `azd` team will work with you to review the changes and provide feedback. Once the pull request is accepted, a member will merge the changes. Thank you for taking time out of your day to help improve our community!

## Building `azd`

Prerequisites:

- [Go](https://go.dev/dl/) 1.26.1

Build:

```bash
cd cli/azd
go build
```

Run the newly produced `azd` or `azd.exe` binary:

- Unix-like systems: `./azd`
- Windows: `.\azd.exe`

### Dev Install (build + add to PATH)

Install [mage](https://magefile.org/) (`go install github.com/magefile/mage@latest`), then:

```bash
cd cli/azd
mage dev:install
```

This builds `azd-dev` (to avoid conflicting with a production `azd` install) with version
info from `cli/version.txt` and the current git commit, installs it to `~/.azd/bin/`, and
automatically adds that directory to your PATH if it isn't already there.

### Preflight (all quality checks)

Run all pre-commit checks (formatting, copyright headers, linting, spell check for Go and docs/misc files, build, unit tests) in one command:

```bash
cd cli/azd
mage preflight
```

> **Tip**: If you're using GitHub Copilot, the `/azd-preflight` skill runs `mage preflight` and auto-fixes any issues it discovers.

Run tests:

```bash
go test ./... -short
```

Run tests (including end-to-end [functional][functional tests] tests)

```bash
go test ./...
```

Run cspell (install [cspell](https://cspell.org/)):

```bash
# Go source (from cli/azd/)
cspell lint "**/*.go" --relative --config ./.vscode/cspell.yaml

# Docs and misc files (from repo root)
cd ../..
cspell lint "**/*" --relative --config ./.vscode/cspell.misc.yaml
cd cli/azd
```

Run linter (install [golangci-lint](https://golangci-lint.run/welcome/install/#local-installation)):

```bash
golangci-lint run ./...
```

Run code modernization check ([go fix](https://go.dev/blog/gofix)):

```bash
go fix -diff ./...
```

If `go fix -diff` reports any changes, apply them with `go fix ./...` and commit the result.
CI enforces this check — PRs with pending `go fix` suggestions will fail the lint workflow.

### Code Coverage

azd collects coverage from both unit tests and integration/functional tests. Several modes are available depending on your needs.

> **Working directory**: Run all coverage commands from the **repository root** (not `cli/azd/`). The scripts handle `cd cli/azd` internally.

| Mode | Command | Mage Target | Prerequisites | Speed |
|------|---------|-------------|--------------|-------|
| **Unit only** (recommended) | `./eng/scripts/Get-LocalCoverageReport.ps1 -ShowReport -UnitOnly` | `mage coverage:unit` | None | ~5-10 min |
| **Hybrid** (local unit + CI integration) | `./eng/scripts/Get-LocalCoverageReport.ps1 -ShowReport -MergeWithCI` | `mage coverage:hybrid` | `az login` | ~6-11 min |
| **Full local** (unit + integration) | `./eng/scripts/Get-LocalCoverageReport.ps1 -ShowReport` | `mage coverage:full` | Azure subscription + service principal | ~30-60 min |
| **CI baseline** (latest main) | `./eng/scripts/Get-CICoverageReport.ps1 -ShowReport` | `mage coverage:ci` | `az login` | ~1 min |

Additional mage targets: `mage coverage:html` (HTML report), `mage coverage:check` (enforce 50% unit-only threshold; CI gate is 55% combined).
Override the threshold with: `COVERAGE_MIN=55 mage coverage:check`.

**Typical workflow**: Use *Unit only* during development for fast feedback. After pushing a PR, use *Hybrid* or check your PR's CI coverage with `Get-CICoverageReport.ps1 -PullRequestId <N> -ShowReport`.

For HTML reports: add `-Html` to any local command. For threshold checks: add `-MinCoverage <N>`.

See [Code Coverage Guide](./docs/code-coverage-guide.md) for architecture details, prerequisites, and troubleshooting.

> Note: On Windows you may need to add `C:\Program Files\Git\usr\bin` to `%PATH%`

### Debugging (with VSCode)

If you don't have a preferred editor for Go code, we recommend [Visual Studio Code](https://code.visualstudio.com/Download).

Launch and debug:

1. Open VSCode in either `cli/azd` (preferred) or in the root directory.
1. In VSCode, put a breakpoint on the line of code you would like to debug.
1. Press F5. Alternatively: Select the "Run and Debug" side pane. With the launch task set to "Debug azd cli", click on the launch button.
1. An interactive VSCode prompt should appear. Provide the args for running `azd` in this prompt window. Press enter when you're done.
1. `azd` should now be running inside the VSCode terminal with the debugger attached.

Launch `azd` separately, then attach:

1. Set `AZD_DEBUG=true` in your shell. If this environment variable is set, `azd` will pause early in its startup process and allow you to attach to it.
1. In VSCode, run the launch task "Attach to process".
1. Select `azd` and press enter.
1. VSCode debugger should now be attached to the running `azd` process.
1. In the shell with `azd` running, press enter to resume execution.

> Tip: Use the VSCode terminal to perform all `azd` build and run commands.

## Submitting a change

1. Create a new branch: `git checkout -b my-branch-name`
1. Make your change, add tests, and ensure tests pass
1. Submit a pull request: `gh pr create --web` (install [gh cli][gh cli] if needed). Select "Create a fork" to set up a fork for the first time if prompted for.

## Troubleshooting

### Access is denied

Windows Security may block execution of unsigned .exe files. This may happen when validating unsigned .exe files produced in
a PR build.

```bash
> azd version
Access is denied.
```

To fix:

1. Run `where azd` (cmd) or `(Get-Command azd).Source` (PowerShell) to get the command path
1. Click the Start button and type `Windows Security`, select and launch the "Windows Security" application
1. Select `Virus & threat protection` tab on the left side of the window
1. Click the `Manage settings` link under `Virus & threat protection settings`
1. Scroll down in the window to the `Exclusions` heading and click the `Add or remove exclusions link`
1. Select `Add an exclusion` and add the path to the exe from step 1

[bug issues]: https://github.com/Azure/azure-dev/issues?q=is%3Aopen+is%3Aissue+label%3Abug
[enhancement issues]: https://github.com/Azure/azure-dev/issues?q=is%3Aopen+is%3Aissue+label%3Aenhancement
[functional tests]: https://github.com/Azure/azure-dev/tree/main/cli/azd/test/functional
[gh cli]: https://github.com/cli/cli?tab=readme-ov-file#installation
