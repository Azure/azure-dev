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

## Private networking for `host: microsoft.foundry`

A Foundry service can be provisioned as a **network-secured (VNet-bound)**
account by adding a `network:` block to the service body in `azure.yaml`. When
`network:` is omitted the account uses public networking (unchanged behavior).
When present, azd provisions or references a private account, its private
endpoint, and the AI private DNS zones; dependent stores (Cosmos DB, AI Search,
Storage) stay platform-managed.

```yaml
services:
  my-project:
    host: microsoft.foundry
    network:
      mode: byo                  # byo | managed
      byo:
        vnet:
          id: ${AZURE_VNET_ID}   # required for byo (v1); must already exist
        agentSubnet:
          name: agent-subnet
          prefix: 192.168.0.0/24
        peSubnet:
          name: pe-subnet
          prefix: 192.168.1.0/24
      dns:
        resourceGroup: rg-private-dns
        subscription: ${AZURE_DNS_SUBSCRIPTION_ID}
    agents:
      - name: my-agent
        kind: hosted
        project: src/my-agent
        image: myprivacr.azurecr.io/agents/my-agent:v1   # BYO image required
```

### Field reference

| Field | Rule |
| --- | --- |
| `mode` | Required. `byo` (customer VNet) or `managed` (Foundry-managed VNet). The matching sub-block is required; the other must be absent. |
| `byo.vnet.id` | Required in v1. ARM id of an existing VNet. Supports `${VAR}` resolved from the azd environment. |
| `byo.agentSubnet` / `byo.peSubnet` | Tri-state. Omitted: azd creates a default subnet. Name only: azd references an existing subnet. Name **and** prefix: azd creates the subnet with that name/CIDR. |
| `managed.isolationMode` | `AllowInternetOutbound` or `AllowOnlyApprovedOutbound`. |
| `dns.resourceGroup` | Omitted: azd creates and links the AI private DNS zones. Set: azd references existing zones in that resource group. |
| `dns.subscription` | Optional. Defaults to the deployment subscription. Accepts a bare GUID or `${VAR}`. |

### Environment variables

Network fields support `${VAR}` references resolved client-side from the azd
environment (run `azd env set <KEY> <value>`). The variable names are
user-chosen; the example above uses:

| Variable | Format | Used by |
| --- | --- | --- |
| `AZURE_VNET_ID` | ARM resource id of an existing `Microsoft.Network/virtualNetworks` | `network.byo.vnet.id` |
| `AZURE_DNS_SUBSCRIPTION_ID` | bare GUID or `/subscriptions/<guid>` | `network.dns.subscription` |

### Requirements and limits

- **BYO container image required.** Secured agents must reference a pre-built
  image via `agents[].image` (`registry/image:tag`); the developer owns the
  registry's SKU, private endpoint, DNS, and firewall. Local build into a
  private ACR is not supported in v1.
- **Brownfield (`endpoint:`) ignores `network:`.** When `endpoint:` is set the
  account's network posture is fixed by whoever created it; azd warns and does
  not reconcile `network:`.

### Cheatsheet: BYO image + VNet hosted agent

```bash
export SUBSCRIPTION_ID="<sub>"
export LOCATION="westus"
export RESOURCE_GROUP="<rg>"
export VNET_ID="<vnet-resource-id>"
export IMAGE="<acr>.azurecr.io/<repo>:<tag>"
```

ACR requirements:

- The BYO image must be pullable by the Foundry **project managed identity**.
- For ABAC-enabled ACR, grant the project MI `Container Registry Repository Reader`.
- For private-only ACR, use Premium SKU, an ACR private endpoint, and a
  `privatelink.azurecr.io` DNS zone linked to the VNet. Disable public access
  only after the image is pushed.

Create `azure.yaml`:

```yaml
name: my-agent
infra:
  provider: microsoft.foundry

services:
  my-agent:
    host: azure.ai.agent
    deployments: []
    network:
      mode: byo
      byo:
        vnet:
          id: ${AZURE_VNET_ID}
        agentSubnet:
          name: agent-subnet
          prefix: 192.168.10.0/24
        peSubnet:
          name: pe-subnet
          prefix: 192.168.11.0/24
```

Create `agent.yaml`:

```yaml
kind: hosted
name: my-agent
image: ${IMAGE}
protocols:
  - protocol: responses
    version: 1.0.0
resources:
  cpu: "0.5"
  memory: 1Gi
```

Configure and provision:

```bash
azd env new my-env --subscription "$SUBSCRIPTION_ID" --location "$LOCATION"
azd env set AZURE_RESOURCE_GROUP "$RESOURCE_GROUP"
azd env set AZURE_VNET_ID "$VNET_ID"
azd env set IMAGE "$IMAGE"
azd env set AZD_AGENT_SKIP_ACR true
azd provision --no-prompt
```

Deploy and invoke from a host that can reach the Foundry private endpoint:

```bash
azd deploy --no-prompt
azd ai agent invoke --new-session "hello"
```

Common failures:

- `403 Public access is disabled`: run deploy/invoke from inside the VNet, a peered VNet, or VPN.
- `ImageError: registry authentication failed`: grant ACR pull permission to the Foundry project MI.

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
