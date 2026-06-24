# Private networking for `host: azure.ai.agent`

A Foundry service can be provisioned as a **network-secured (VNet-bound)**
account by adding a `network:` block to the service body in `azure.yaml`. When
`network:` is omitted the account uses public networking (unchanged behavior).

When `network:` is present, azd always provisions an **account private
endpoint** and disables public network access — the data plane is never left
public. Dependent stores (Cosmos DB, AI Search, Storage) stay platform-managed.

The block models two orthogonal axes:

- **Egress** (agent runtime network) — set `agentSubnet` to inject the agent
  into your subnet (BYO VNet), or omit it to use the Microsoft-managed network.
  `isolationMode` tunes the managed network's outbound posture and is valid only
  when `agentSubnet` is omitted.
- **Ingress** (account data plane) — `peSubnet` is **required** and always
  yields an account private endpoint, so callers (`azd deploy`,
  `azd ai agent invoke`) must reach the account from inside the VNet, a peered
  VNet, or VPN.

```yaml
services:
  my-project:
    host: azure.ai.agent
    network:
      # ----- Egress: agent runtime network (pick ONE) -----
      #
      # (a) Managed egress (shown live below): omit agentSubnet so the agent
      #     runs in the Microsoft-managed network. isolationMode is valid only
      #     in this mode.
      isolationMode: AllowOnlyApprovedOutbound  # or AllowInternetOutbound (default)
      #
      # (b) BYO egress: inject the agent into your subnet instead. Replace the
      #     isolationMode line above with an agentSubnet block (same VNet as
      #     peSubnet in v1):
      #   agentSubnet:
      #     vnet: ${AZURE_VNET_ID}
      #     name: agent-subnet
      #     prefix: 192.168.10.0/24   # omit prefix to reference an existing subnet

      # ----- Ingress: account private endpoint (REQUIRED) -----
      peSubnet:
        vnet: ${AZURE_VNET_ID}        # ARM id of the VNet (must already exist)
        name: pe-subnet
        prefix: 192.168.11.0/24       # omit prefix to reference an existing subnet

      # ----- Private DNS (optional) -----
      dns:
        resourceGroup: rg-private-dns               # omit to let azd create + link the zones
        subscription: ${AZURE_DNS_SUBSCRIPTION_ID}  # optional; defaults to the deployment subscription
    agents:
      - name: my-agent
        kind: hosted
        project: src/my-agent
        image: myprivacr.azurecr.io/agents/my-agent:v1   # BYO image required
```

> You do not hand-author the `agents:` entry above. Run
> `azd ai agent init --no-prompt --agent-name my-agent --image <registry/image:tag>`
> to scaffold it (it writes `agent.yaml`); then add the `network:` block to the
> generated service.

> The example above uses **managed egress** so every field — including
> `isolationMode` — is shown as valid YAML. For **BYO egress**, swap the
> `isolationMode` line for an `agentSubnet` block (see comment `(b)` and
> Scenario 2 below); `isolationMode` is then invalid and must be removed.

### Field reference

| Field | Rule |
| --- | --- |
| `agentSubnet` | Optional. Present: the agent is injected into this customer subnet (BYO egress). Absent: the agent uses the Microsoft-managed network (managed egress). |
| `peSubnet` | **Required.** Subnet for the account private endpoint. Establishes the private data plane (public access disabled). |
| `isolationMode` | Optional. `AllowInternetOutbound` or `AllowOnlyApprovedOutbound`. Valid **only** when `agentSubnet` is omitted (managed egress). |
| subnet `vnet` | Required. ARM id of the VNet that holds (or will hold) the subnet. Supports `${VAR}`. When `agentSubnet` is present, it must reference the same VNet as `peSubnet`. |
| subnet `name` | Required. Subnet name. |
| subnet `prefix` | Optional. Omit to reference an existing subnet; set to create the subnet with that CIDR. |
| `dns.resourceGroup` | Omitted: azd creates and links the AI private DNS zones. Set: azd references existing zones in that resource group. Requires `peSubnet`. |
| `dns.subscription` | Optional. Defaults to the deployment subscription. Accepts a bare GUID or `${VAR}`. |

### Environment variables

Network fields support `${VAR}` references resolved client-side from the azd
environment (run `azd env set <KEY> <value>`). The variable names are
user-chosen; the example above uses:

| Variable | Format | Used by |
| --- | --- | --- |
| `AZURE_VNET_ID` | ARM resource id of an existing `Microsoft.Network/virtualNetworks` | subnet `vnet` |
| `AZURE_DNS_SUBSCRIPTION_ID` | bare GUID or `/subscriptions/<guid>` | `network.dns.subscription` |

### Limitations

- **Single VNet (v1).** When `agentSubnet` is present it must live in the same
  VNet as `peSubnet`; azd errors otherwise. Cross-VNet topologies (agent and
  account private endpoint in different VNets) are deferred — they need
  customer-managed peering plus DNS-zone links to both VNets, which azd does not
  provision. Managed egress is unaffected (it needs only the `peSubnet` VNet).
- **BYO container image required.** Secured agents must reference a pre-built
  image via `agents[].image`; local build into a private ACR is not supported in
  v1. The developer owns the registry's SKU, private endpoint, DNS, and firewall.
- **Brownfield (`endpoint:`) ignores `network:`.** When `endpoint:` is set the
  account's network posture is fixed by whoever created it; azd warns and does
  not reconcile `network:`.
- **One default-DNS account per VNet.** Without a `dns:` block azd links the
  three `privatelink.*` AI zones to your VNet, and a VNet may hold only one link
  per namespace. A second account (or a brownfield hub that pre-links the zones)
  must use `dns:` **reference** mode to bind the private endpoint without
  re-linking.
- **Terraform IaC is not supported for private networking (v1).** Bicep-only
  today; `azd ai agent init --infra=terraform` is refused when `network:` is
  declared. Eject Bicep instead (see *Scenario 3 — Eject and customize the
  Bicep*).

### Scenario 1 — Managed egress: private account, agent on Microsoft's network

Omit `agentSubnet` so the hosted-agent runtime uses a Microsoft-managed network
instead of your VNet. `peSubnet` is still required: the account data plane stays
private behind an account private endpoint in your VNet, reachable from inside
the VNet / VPN.

Scaffold the agent with a pre-built (BYO) image (writes `azure.yaml` and
`agent.yaml`):

```bash
azd ai agent init --no-prompt --agent-name my-agent \
  --image myprivacr.azurecr.io/agents/my-agent:v1
```

Then add a `network:` block to the generated service in `azure.yaml` (omit
`agentSubnet` for managed egress; `isolationMode` is valid only in this mode):

```yaml
name: my-agent
infra:
  provider: microsoft.foundry

services:
  my-agent:
    host: azure.ai.agent
    deployments: []
    network:
      isolationMode: AllowInternetOutbound   # managed-egress outbound posture
      peSubnet:
        vnet: ${AZURE_VNET_ID}
        name: pe-subnet
        prefix: 192.168.11.0/24
```

`azd ai agent init --image` already created and selected an azd environment and
set `AZD_AGENT_SKIP_ACR=true` (BYO image → no ACR build). Set the deployment
inputs on that environment and provision:

```bash
azd env set AZURE_SUBSCRIPTION_ID "<sub>"
azd env set AZURE_LOCATION westus
azd env set AZURE_RESOURCE_GROUP "<rg>"
azd env set AZURE_VNET_ID "<vnet-resource-id>"
azd provision --no-prompt
```

Grant the Foundry project MI ACR pull permission, then run deploy/invoke from a
host that can reach the account private endpoint:

```bash
azd deploy --no-prompt
azd ai agent invoke --new-session "hello"
```

> **`isolationMode` note.** When set, azd provisions the account's V2
> managed network (`managednetworks/default`) with the chosen isolation mode.
> `AllowOnlyApprovedOutbound` additionally requires approved outbound rules for
> the agent to reach dependent resources; for the platform-managed stores used
> here those are managed by the Foundry platform.

### Scenario 2 — BYO egress: agent injected into your VNet subnet

ACR requirements:

- The BYO image must be pullable by the Foundry **project managed identity**.
- For ABAC-enabled ACR, grant the project MI `Container Registry Repository Reader`.
- For private-only ACR, use Premium SKU, an ACR private endpoint, and a
  `privatelink.azurecr.io` DNS zone linked to the VNet. Disable public access
  only after the image is pushed.

Scaffold the agent with a pre-built (BYO) image — this writes `azure.yaml` and
`agent.yaml` for you, so there is no hand-edited manifest to keep in sync:

```bash
azd ai agent init --no-prompt --agent-name my-agent \
  --image myprivacr.azurecr.io/agents/my-agent:v1
```

Then add a `network:` block to the generated service in `azure.yaml`:

```yaml
services:
  my-agent:
    host: azure.ai.agent
    network:
      agentSubnet:                  # omit the whole block for managed egress
        vnet: ${AZURE_VNET_ID}
        name: agent-subnet
        prefix: 192.168.10.0/24     # omit prefix to reference an existing subnet
      peSubnet:                      # required: makes the data plane private
        vnet: ${AZURE_VNET_ID}
        name: pe-subnet
        prefix: 192.168.11.0/24
```

Configure and provision (`init --image` already created/selected the env and set
`AZD_AGENT_SKIP_ACR=true`):

```bash
azd env set AZURE_SUBSCRIPTION_ID "<sub>"
azd env set AZURE_LOCATION westus
azd env set AZURE_RESOURCE_GROUP "<rg>"
azd env set AZURE_VNET_ID "<vnet-resource-id>"
azd provision --no-prompt
```

Deploy and invoke from a host that can reach the Foundry private endpoint:

```bash
azd deploy --no-prompt
azd ai agent invoke --new-session "hello"
```

Common failures:

- `403 Public access is disabled`: the data plane is private in every
  network-bound mode — run deploy/invoke from inside the VNet, a peered VNet, or
  VPN.
- `ImageError: registry authentication failed`: grant ACR pull permission to the Foundry project MI.

### Scenario 3 — Eject and customize the Bicep (advanced)

The synthesized template covers the common private-networking shapes. When you
need something it doesn't express — an extra subnet, a private endpoint for a BYO
dependent store, custom DNS wiring, a non-default account property, additional
`networkInjections` rules — eject the Bicep, edit it directly, and let azd
provision your edited tree.

```bash
# 1. Scaffold + declare a network: block in azure.yaml (see Scenarios 1–2
#    above), then eject the infrastructure:
azd ai agent init --infra          # writes ./infra/ from azure.yaml
```

Eject writes the **full** Bicep tree from your `network:` block —
`infra/main.bicep`, `infra/modules/{resources,network,subnet,private-endpoint-dns,acr}.bicep`,
and `infra/main.parameters.json` — and **preserves `${VAR}` placeholders**
(resolved from the azd environment at provision time). `azure.yaml` is left
unchanged: `infra.provider` stays `microsoft.foundry`.

```bash
# 2. Edit the ejected Bicep to taste. Two worked examples:

# (a) infra/modules/network.bicep — add a subnet the network: schema can't
#     express (e.g. for a future dependent-store private endpoint). Pick a CIDR
#     free in your VNet space. Use the '<vnet>/<subnet>' name form (vnet is an
#     existing resource in a possibly different RG):
#
#   resource extraStoreSubnet 'Microsoft.Network/virtualNetworks/subnets@2024-05-01' = {
#     name: '${vnetName}/byo-store-pe-subnet'
#     properties: {
#       addressPrefix: '192.168.30.0/24'
#       privateEndpointNetworkPolicies: 'Disabled'
#     }
#     dependsOn: [ peSubnet ]
#   }

# (b) infra/modules/resources.bicep — set an extra account property directly on
#     the foundryAccount resource, e.g. merge a tag:
#
#   tags: union(tags, { editedByPowerUser: 'true' })
```

```bash
# 3. Provision the edited tree. azd detects ./infra/main.bicep and compiles it
#    instead of synthesizing from azure.yaml:
azd env set AZURE_SUBSCRIPTION_ID "<sub>"
azd env set AZURE_LOCATION westus
azd env set AZURE_RESOURCE_GROUP "<rg>"
azd env set AZURE_VNET_ID "<vnet-resource-id>"
azd provision --no-prompt

# 4. Deploy and invoke from a host with line-of-sight to the account private
#    endpoint (inside the VNet, a peered VNet, or VPN):
azd deploy --no-prompt
azd ai agent invoke --new-session "hello"
```

**How it works.** Once `./infra/main.bicep` exists, azd provisions it directly
and **stops synthesizing from `azure.yaml`** — your edited Bicep is now the
source of truth. Your `main.parameters.json` values are layered over azd's
host-derived parameters (subscription, location, resource group, project name,
`principalId`); you win on keys you set, and azd fills in the rest.

**Notes.**

- Re-running `azd ai agent init --infra` is **refused** while `./infra/` exists,
  so your edits are never overwritten — delete `./infra/` to regenerate from
  `azure.yaml`.
- After ejecting, further `network:` edits in `azure.yaml` have **no effect**;
  change the Bicep directly.
- `infra/main.arm.json` is intentionally not ejected (it would go stale the
  moment you edit `main.bicep`); azd compiles `main.bicep` on each provision.
- **Terraform is not supported for private networking.**
  `azd ai agent init --infra=terraform` is refused for a service that declares
  `network:` (the Terraform module has no VNet / private-endpoint / DNS
  resources, so ejecting it would silently provision a public account). Use
  `--infra` (Bicep) and customize as above.
