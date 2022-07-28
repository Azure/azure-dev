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

We use a fork based workflow for `azd`. Here are simple steps:

1. Fork `azure/azure-dev` on GitHub.
2. Create a branch named `<some-description>` (e.g. `fix-123` for a bug fix or `add-deploy-command`) in your forked Git repository.
3. Push the branch to your fork on GitHub.
4. Open a pull request in GitHub.

Here is a more [in-depth guide to forks in GitHub](https://guides.github.com/activities/forking/).

As part of CI validation, we run a series of live tests which provision and deprovision Azure resources. For external contributors, these tests will not run automatically, but someone on the team will be able to run them for your PR on your behalf.

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
    "cwd": "${workspaceFolder}"
},
```

## Testing

We use `go test`.  The `functional` package contains end to end tests that run `azd` and deploy live resources. You need to ensure you have run `go build` to
build a copy of `azd` at the root of the repository. The tests look for that binary.  We hope to improve this. Use the `-run` flag of `go test` to filter tests,
as usual. You'll want to pass a larger `-timeout` since these tests deploy live resources.

### Run all the tests

`go test -timeout 20m -v ./...`

### Run a specific test

`go test -timeout 20m -v ./... -run Test_CLI_RestoreCommand`

## Linting

Run `golangci-lint run ./...`

> On Windows you may need to add `C:\Program Files\Git\usr\bin` to `%PATH%`

## Spell checking

Install [cspell](https://cspell.org/) and then run `cspell lint "**/*.go" --relative --config ./.vscode/cspell.yaml`

## Troubleshooting

### Access is denied

Windows Security may block execution of unsigned .exe files. This may happen when validating unsigned .exe files produced in a PR build.

```
> azd version
Access is denied.
>
```

To fix: 

1. Run `where azd` (cmd) or `(Get-Command azd).Source` (PowerShell) to get the command path
1. Click the Start button and type `Windows Security`, select and launch the "Windows Security" application
1. Select `Virus & threat protection` tab on the left side of the window
1. Click the `Manage settings` link under `Virus & threat protection settings`
1. Scroll down in the window to the `Exclusions` heading and click the `Add or remove exclusions link` 
1. Select `Add an exclusion` and add the path to the exe from step 1
