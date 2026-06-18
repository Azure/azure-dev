# Private networking for `host: microsoft.foundry`

A Foundry service can be provisioned as a **network-secured (VNet-bound)**
account by adding a `network:` block to the service body in `azure.yaml`. When
`network:` is omitted the account uses public networking (unchanged behavior).
When present, azd configures the Foundry account for either customer BYO VNet
mode or Microsoft-managed VNet mode. BYO mode provisions or references the
customer VNet, private endpoint, and AI private DNS zones; dependent stores
(Cosmos DB, AI Search, Storage) stay platform-managed.

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

### Cheatsheet: managed VNet account

Use `managed` mode when Foundry should use a Microsoft-managed network for the
hosted-agent runtime instead of injecting into your VNet. Managed mode does not
create a customer private endpoint, so the Foundry data plane remains public for
`azd deploy` and `azd ai agent invoke`.

```yaml
name: my-agent
infra:
  provider: microsoft.foundry

services:
  my-agent:
    host: azure.ai.agent
    deployments: []
    network:
      mode: managed
      managed:
        isolationMode: AllowInternetOutbound
```

```bash
azd env new my-env --subscription "<sub>" --location westus
azd env set AZURE_RESOURCE_GROUP "<rg>"
azd provision --no-prompt
```

If using a BYO image, grant the Foundry project MI ACR pull permission, then:

```bash
azd deploy --no-prompt
azd ai agent invoke --new-session "hello"
```

Expected outputs:

```text
AZURE_FOUNDRY_NETWORK_MODE=managed
AZURE_FOUNDRY_MANAGED_ISOLATION_MODE=AllowInternetOutbound
```

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

- `403 Public access is disabled`: for BYO VNet mode, run deploy/invoke from inside the VNet, a peered VNet, or VPN.
- `ImageError: registry authentication failed`: grant ACR pull permission to the Foundry project MI.
