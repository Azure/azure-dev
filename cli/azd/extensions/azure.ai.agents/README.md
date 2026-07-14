# Azure Developer CLI (azd) Agents Extension

## Running Local Agents

`azd ai agent run` starts the selected agent locally and, by default, opens the
Agent Inspector after the local agent port is accepting connections. The
inspector launch is best-effort: if `azure.ai.inspector` is not installed or
fails to start, the agent process keeps running and azd prints install guidance.

Use `--no-inspector` to run only the local agent process:

```bash
azd ai agent run --no-inspector
```

## Migrating Legacy Agent Configuration

New Foundry agent projects keep the agent definition directly on the
`azure.ai.agent` service entry in `azure.yaml`. Older projects may still have the
definition in an `agent.yaml` file or under the service's `config:` block. Those
legacy shapes continue to work during the migration window, but azd prints a
deprecation warning when it loads them.

To migrate, re-run `azd ai agent init` from the project root and keep the
generated `azure.yaml` service entry. After confirming `azd deploy` still works,
remove the old `agent.yaml` or nested `config:` definition.

Before:

```yaml
services:
  my-agent:
    host: azure.ai.agent
    project: .
    config:
      kind: hosted
      description: My hosted agent
```

After:

```yaml
services:
  my-agent:
    host: azure.ai.agent
    project: .
    kind: hosted
    description: My hosted agent
```

## Private networking for `host: azure.ai.project`

Foundry project services can be provisioned as network-secured, VNet-bound
accounts by adding a `network:` block to the `host: azure.ai.project` service in
`azure.yaml`. The `azure.ai.projects` extension owns that service and the
`microsoft.foundry` provider; this extension still authors the block during
agent init. See
[Private networking for `host: azure.ai.project`](docs/private-networking.md)
for the schema reference, BYO-image requirements, and VNet deployment
cheatsheet.

## Local Development

### Prerequisites

1. **Install developer kit extension** (if not already installed):
   ```bash
   azd ext install microsoft.azd.extensions
   ```

   > **Note**: If you encounter an error about the extension not being in the registry, verify you have the default source configured:
   > ```bash
   > azd ext source list
   > ```
   > If missing, add it:
   > ```bash
   > azd ext source add -n azd -t url -l "https://aka.ms/azd/extensions/registry"
   > ```

### Building and Installing

1. **Navigate to the extension directory**:
   ```bash
   cd cli/azd/extensions/azure.ai.agents
   ```

2. **Initial setup** (first time only):
   ```bash
   azd x build
   azd x pack
   azd x publish
   ```

3. **Install the extension**:
   ```bash
   azd ext install azure.ai.agents
   ```

4. **For subsequent development** (after initial setup):
   ```bash
   azd x watch
   ```
   This automatically watches for file changes, rebuilds, and installs updates locally.

   Or for manual builds:
   ```bash
   azd x build
   ```
   This builds and automatically installs the updated extension.

> [!NOTE]
> The `pack` and `publish` steps are only required for the first time setup. For ongoing development, `azd x watch` or `azd x build` handles all updates automatically.
