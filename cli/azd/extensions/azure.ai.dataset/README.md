# Foundry datasets (Beta)

Register and version Foundry datasets from your terminal.

```console
$ azd extension install azure.ai.dataset
$ azd ai dataset --help
```

A dataset is a general Foundry asset: evaluation needs one, and so do
fine-tuning and other scenarios. That is why these commands live here rather
than inside `azure.ai.evaluations`.

## Commands

| Command | What it does |
|---|---|
| `azd ai dataset create <name> --from-file <path>` | Register a dataset, publishing its first version |
| `azd ai dataset update <name> --from-file <path>` | Publish a further version |
| `azd ai dataset list` | List the project's datasets |
| `azd ai dataset show <name>` | Show one dataset |
| `azd ai dataset delete <name>` | Delete a dataset version |
| `azd ai dataset versions list <name>` | List a dataset's versions |

## Generating a dataset

Generation is `azd ai eval dataset generate`, in `azure.ai.evaluations`, and
stays there: it writes the `datasets:` entry in `evals/eval.yaml`, which is that
extension's file. Splitting the two would leave a generated dataset registered
with the service but absent from the configuration, so `azd up` would not
reconcile it and no eval could name it.

Once a file exists, `create` registers it here.

## Project endpoint

Every command resolves the Foundry project endpoint in this order:

1. `--project-endpoint`
2. `FOUNDRY_PROJECT_ENDPOINT` in the active azd environment
3. the host environment variable of the same name

## Building

```console
$ go build ./...
$ go test ./...
```
