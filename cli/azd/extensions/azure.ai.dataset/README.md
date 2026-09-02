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

`--version` names the version to publish, on `create` and `update` alike. Omit
it and the next version after the latest registered one is published; a version
the service already holds is refused rather than stepped past, because a version
you named is one you meant.

## Generating a dataset

Generation is `azd ai eval generate`, in `azure.ai.evaluations`, and stays
there: it writes the `datasets:` entry in `evals/azure.eval.yaml`, which is that
extension's file. Splitting the two would leave a generated dataset registered
with the service but absent from the configuration, so `azd up` would not
reconcile it and no eval could name it.

Once a file exists, `create` registers it here.

## Project endpoint

Every command resolves the Foundry project endpoint in this order:

1. `--project-endpoint`
2. `FOUNDRY_PROJECT_ENDPOINT` in the active azd environment, then
   `AZURE_AI_PROJECT_ENDPOINT` there
3. `extensions.ai-agents.project.context.endpoint` in azd's global config,
   which `azure.ai.agents` writes and this extension only reads
4. `FOUNDRY_PROJECT_ENDPOINT` in the host environment, then
   `AZURE_AI_PROJECT_ENDPOINT`

Level 3 is worth knowing about: it is machine-wide rather than per-project, so
a project context left behind by `azd ai agent` somewhere else takes precedence
over the variable exported in this shell. `--debug` prints which level answered.

## Building

```console
$ go build ./...
$ go test ./...
```

## TODO before release

Both are files the azd extensions team owns, so they are not changed here:

- [ ] **`cli/azd/extensions/registry.json`** — add the `azure.ai.dataset` entry.
  Until it exists `azd extension install azure.ai.dataset` cannot resolve, so
  the extension is only reachable through `azd x pack` + `azd x publish` into
  the local source registry.
- [ ] **`microsoft.foundry/extension.yaml`** — add the dependency, but only
  after the registry entry lands. Declaring a dependency that cannot resolve
  breaks installing the bundle.
