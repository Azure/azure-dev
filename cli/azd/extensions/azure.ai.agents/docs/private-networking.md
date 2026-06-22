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
> `isolationMode` line for an `agentSubnet` block (see comment `(b)` and the BYO
> cheatsheet below); `isolationMode` is then invalid and must be removed.

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

### Requirements and limits

- **`peSubnet` is mandatory.** A network-bound account always gets a private
  endpoint; there is no public data-plane fallback. Run `azd deploy` /
  `azd ai agent invoke` from inside the VNet, a peered VNet, or VPN.
- **Single VNet (v1).** When `agentSubnet` is present it must live in the same
  VNet as `peSubnet`.
- **BYO container image required.** Secured agents must reference a pre-built
  image via `agents[].image` (`registry/image:tag`); the developer owns the
  registry's SKU, private endpoint, DNS, and firewall. Local build into a
  private ACR is not supported in v1.
- **Brownfield (`endpoint:`) ignores `network:`.** When `endpoint:` is set the
  account's network posture is fixed by whoever created it; azd warns and does
  not reconcile `network:`.

### Known limitations

- **BYO egress is single-VNet (v1).** When `agentSubnet` is set it must
  reference the same VNet as `peSubnet`; azd errors otherwise. Cross-VNet
  topologies (agent injected in one VNet, account private endpoint in another)
  are deferred: they require customer-managed VNet **peering** between the two
  VNets — so the agent can route to the account private endpoint — plus private
  DNS zone links to *both* VNets. azd does not provision or validate that
  peering, so the data path would silently fail. Managed egress is unaffected:
  the agent reaches the account over Microsoft-managed connectivity and never
  the customer ingress VNet, so it needs only the single `peSubnet` VNet.

- **One default-DNS account per VNet.** By default (no `dns:` block) azd
  creates the three `privatelink.*` AI zones and **links them to your VNet**.
  Azure allows a VNet to be linked to only one zone per namespace, so a second
  Foundry account that also owns its DNS cannot reuse the same VNet — the link
  fails with `A virtual network cannot be linked to multiple zones with
  overlapping namespaces`. If the VNet is already linked to those zones (a
  second account, or a brownfield hub that pre-links the AI privatelink zones),
  set the `dns:` block to **reference** the existing zones; reference mode binds
  the account private endpoint to them and skips creating a new VNet link.

### Cheatsheet: managed-egress account (private data plane)

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

Configure and provision:

```bash
azd env new my-env --subscription "<sub>" --location westus
azd env set AZURE_RESOURCE_GROUP "<rg>"
azd env set AZURE_VNET_ID "<vnet-resource-id>"
azd env set AZD_AGENT_SKIP_ACR true   # BYO image: skip the ACR build
azd provision --no-prompt
```

If using a BYO image, grant the Foundry project MI ACR pull permission, then run
deploy/invoke from a host that can reach the account private endpoint:

```bash
azd deploy --no-prompt
azd ai agent invoke --new-session "hello"
```

Validate the real resource state (not azd's echoed output):

```bash
# account data plane is actually private
az cognitiveservices account show -g "<rg>" -n "<account>" \
  --query "properties.publicNetworkAccess" -o tsv          # -> Disabled

# the V2 managed network actually carries the isolation mode
az resource show --ids "<account-id>/managedNetworks/default" \
  --api-version 2025-10-01-preview \
  --query "properties.managedNetwork.isolationMode" -o tsv  # -> AllowInternetOutbound
```

A successful `azd ai agent invoke` echo response over the private endpoint is the
end-to-end proof. azd also writes `AZURE_FOUNDRY_NETWORK_MODE` and
`AZURE_FOUNDRY_MANAGED_ISOLATION_MODE` to the environment as outputs, but those
are azd's own classification — confirm posture from the resource state above.

> **`isolationMode` note.** When set, azd provisions the account's V2
> managed network (`managednetworks/default`) with the chosen isolation mode.
> `AllowOnlyApprovedOutbound` additionally requires approved outbound rules for
> the agent to reach dependent resources; for the platform-managed stores used
> here those are managed by the Foundry platform.

### Cheatsheet: BYO image + VNet hosted agent (BYO egress)

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

Configure and provision:

```bash
azd env new my-env --subscription "$SUBSCRIPTION_ID" --location "$LOCATION"
azd env set AZURE_RESOURCE_GROUP "$RESOURCE_GROUP"
azd env set AZURE_VNET_ID "$VNET_ID"
azd env set AZD_AGENT_SKIP_ACR true   # BYO image: skip the ACR build
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
