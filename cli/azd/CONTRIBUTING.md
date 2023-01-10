# Contributing to `azd`

## Prerequisites

`azd` is written in Golang and requires Go 1.19 or above:

- [Go](https://go.dev/dl/)

If you don't have a preferred editor for Go code, we recommend [Visual Studio Code](https://code.visualstudio.com/Download).
We have a `settings.json` file checked in, so if you launch `code` from within `cli/azd` you should get our customizations.

While `azd` itself does not depend on the `az` CLI, some of our tests use it, so you should make sure you've installed it
and logged in.

- [AZ CLI](https://docs.microsoft.com/cli/azure/)

We have some additional linting tools that we run in CI, and you will want to be able to run locally:

- [golangci-lint](https://golangci-lint.run/usage/install/#local-installation)

In order to run end-to-end tests and develop templates, you'll need the following dependencies:

Language tooling:

- [NodeJS](https://nodejs.org/en/download/)
- [Python](https://www.python.org/downloads)
- [DotNet CLI](https://get.dot.net)

Infrastructure-as-code providers:

- [Bicep CLI](https://aka.ms/bicep-install)
- [Terraform](https://learn.hashicorp.com/tutorials/terraform/install-cli) (if working on Terraform templates)

Docker:

- [Docker](https://docs.docker.com/desktop/#download-and-install)

## Submitting A Change

We use a fork based workflow for `azd`. Here are simple steps:

1. Fork `azure/azure-dev` on GitHub.
2. Create a branch named `<some-description>` (e.g. `fix-123` for a bug fix or `add-deploy-command`) in your forked Git
   repository.
3. Push the branch to your fork on GitHub.
4. Open a pull request in GitHub.

Here is a more [in-depth guide to forks in GitHub](https://guides.github.com/activities/forking/).

As part of CI validation, we run a series of live tests which provision and deprovision Azure resources. For external
contributors, these tests will not run automatically, but someone on the team will be able to run them for your PR on your
behalf.

## Debugging

In VS Code you can create a launch.json that runs the tool with a specified set of arguments and in a specific folder, for
example:

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

We use `gotestsum`, which is a simple tool that wraps `go test` except with better formatting output. Install the tool by
running `go install gotest.tools/gotestsum@latest`.

### Run all unit tests

`gotestsum -- -short ./...`

### Run all end-to-end tests

```bash
go build
gotestsum -- -timeout 20m -run Test_CLI ./...
```

This runs all end-to-end tests that run `azd` locally and deploy live resources. Run `go build` first to ensure the
integration tests target the latest `azd` binary built at the root of `cli/azd`. Note that `go install` "helpfully" removes
the binary produced by `go build` when run, and so if you've run `go install` since the last time you ran `go build` you'll
have to run `go build` again or the end-to-end tests will fail.

### Run all tests (including end-to-end tests)

```bash
go build
gotestsum -- -timeout 20m ./...
```

### Run a specific test

`gotestsum -- -timeout 20m -run Test_CLI_RestoreCommand ./...`

This can be useful for running specific end-to-end tests that cover the relevant scenarios.

## Linting

Run `golangci-lint run ./...`

> On Windows you may need to add `C:\Program Files\Git\usr\bin` to `%PATH%`

## Spell checking

1. Install [cspell](https://cspell.org/)
2. CD to /cli/azd
3. Run `cspell lint "**/*.go" --relative --config ./.vscode/cspell.yaml`

## Tracing

`azd` supports logging trace information to either a file or an OpenTelemetry compatible HTTP endpoint. The
`--trace-log-file` can be used to write a JSON file containing all the spans for an command execution. Also,
`--trace-log-url` can be used to provide an endpoint to send spans using the OTLP HTTP protocol.

You can use the Jaeger all in one docker image to run Jaeger locally to collect and inspect traces:

```
$ docker run -d --name jaeger \
 -e COLLECTOR_OTLP_ENABLED=true \
 -e JAEGER_DISABLED=true \
 -p 16686:16686 \
 -p 4318:4318 \
 jaegertracing/all-in-one
```

And then pass `--trace-log-url localhost` to a command and view the results in the Jaeger UI served at
[http://localhost:16686/search](http://localhost:16686/search)

## Troubleshooting

### Access is denied

Windows Security may block execution of unsigned .exe files. This may happen when validating unsigned .exe files produced in
a PR build.

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
