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
