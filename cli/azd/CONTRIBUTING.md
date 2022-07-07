# Contributing to `azd`

## Prerequisites

`azd` is written in Golang and requires Go 1.18 or above:

- [Go](https://go.dev/dl/)

If you don't have a preferred editor for Go code, we recommend [Visual Studio Code](https://code.visualstudio.com/Download). We have a `settings.json` file checked in, so if you launch `code` from within `cli/azd` you should get our customizations.

It also requires the `az` CLI:

- [AZ CLI](https://docs.microsoft.com/cli/azure/)

We have some additional linting tools that we run in CI, and you will want to be able to run locally:

- [golangci-lint](https://golangci-lint.run/usage/install/#local-installation)

In order to run some of the tests, you'll need the toolchain for all the languages we support:

- [NodeJS](https://nodejs.org/en/download/)
- [Python](https://www.python.org/downloads)
- [DotNet CLI](https://get.dot.net)

Also, you'll want to install Docker (as we use that for some of our tests)

- [Docker](https://docs.docker.com/desktop/#download-and-install)

## Submitting A Change

As part of our pre-check-in validation we run tests that require creating Azure resources. Right now this requires that PRs don't come from Forks.

1. Create a branch named `<your-github-user>/<some-description>` (e.g. `ellismg/fix-123` for a bug fix or `ellismg/add-deploy-command`).
2. Push the branch to `azure/azure-dev` on GitHub.
3. Open a pull request in GitHub.

## Debugging

In VS Code you can create a launch.json that runs the tool with a specified set of arguments and in a specific folder, for example:

```json
{
    "name": "dev-azd (launch)",
    "type": "go",
    "request": "launch",
    "mode": "debug",
    "program": "${workspaceFolder}",
    "args": [
        "restore"
    ],
    "cwd": "/Users/karolz/code/scratch/azd-devel/src/api"
},
```

## Testing

We use `go test`.  The `functional` package contains end to end tests that run `azd` and deploy live resources. You need to ensure you have run `go build` to
build a copy of `azd` at the root of the repository. The tests look for that binary.  We hope to improve this. Use the `-run` flag of `go test` to filter tests,
as usual. You'll want to pass a larger `-timeout` since these tests deploy live resources.

### Run all the tests

`go test -timeout 15m -v ./...`

### Run a specific test

`go test -timeout 15m -v ./... -run Test_CLI_RestoreCommand`

## Linting

Run `golangci-lint run ./...`

> On Windows you may need to add `C:\Program Files\Git\usr\bin` to `%PATH%`

## Spell checking

Install [cspell](https://cspell.org/) and then run `cspell lint "**/*.go" --relative --config ./.vscode/cspell.yaml`
