# Private networking for `host: azure.ai.project`

A Foundry project service can be provisioned as a **network-secured (VNet-bound)** account by adding a `network:` block to the `host: azure.ai.project` service in `azure.yaml`. When `network:` is omitted, the account uses public networking.

Do **not** place `network:` on `host: azure.ai.agent`. Agent services describe deployable agents and depend on the project through `uses:`; the project service owns account-level provisioning inputs such as `endpoint:`, `deployments:`, and `network:`.

When `network:` is present, azd always provisions an **account private endpoint** and disables public data-plane access. Dependent stores (Cosmos DB, AI Search, Storage) stay platform-managed.

```yaml
infra:
  provider: microsoft.foundry

services:
  my-agent:
    host: azure.ai.agent
    project: src/my-agent
    uses:
      - ai-project
    image: myprivacr.azurecr.io/agents/my-agent:v1

  ai-project:
    host: azure.ai.project
    deployments:
      - name: gpt-4o-mini
        model: { format: OpenAI, name: gpt-4o-mini, version: "2024-07-18" }
        sku: { name: GlobalStandard, capacity: 50 }
    network:
      # ----- Egress: agent runtime network (pick ONE) -----
      #
      # (a) Managed egress (shown live below): omit agentSubnet so the agent
      #     runs in the Microsoft-managed network. isolationMode is valid only
      #     in this mode.
      isolationMode: AllowOnlyApprovedOutbound  # or AllowInternetOutbound
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
        vnet: ${AZURE_VNET_ID}
        name: pe-subnet
        prefix: 192.168.11.0/24       # omit prefix to reference an existing subnet

      # ----- Private DNS (optional) -----
      dns:
        resourceGroup: rg-private-dns               # omit to let azd create + link the zones
        subscription: ${AZURE_DNS_SUBSCRIPTION_ID}  # optional; defaults to deployment subscription
```

Run `azd ai agent init --no-prompt --agent-name my-agent --image <registry/image:tag>` to scaffold the agent. The generated `azure.yaml` contains an `azure.ai.agent` service and an `azure.ai.project` service named `ai-project`; add the `network:` block to the `ai-project` service.

## Field reference

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

## Environment variables

Network fields support `${VAR}` references resolved client-side from the azd environment. The variable names are user-chosen; the examples use:

| Variable | Format | Used by |
| --- | --- | --- |
| `AZURE_VNET_ID` | ARM resource id of an existing `Microsoft.Network/virtualNetworks` | subnet `vnet` |
| `AZURE_DNS_SUBSCRIPTION_ID` | bare GUID or `/subscriptions/<guid>` | `network.dns.subscription` |

Configure them with `azd env set`, for example:

```bash
azd env set AZURE_VNET_ID "<vnet-resource-id>"
azd env set AZURE_DNS_SUBSCRIPTION_ID "<subscription-id>"
```

## Limitations

- **Project-owned network settings.** `network:` is supported only on the `host: azure.ai.project` service. Multiple agents may depend on the same project; they do not carry separate VNet settings.
- **Single VNet (v1).** When `agentSubnet` is present it must live in the same VNet as `peSubnet`. Managed egress is unaffected because it needs only the `peSubnet` VNet.
- **BYO container image required.** Secured agents should use a pre-built image. The image belongs to the `azure.ai.agent` service; the VNet configuration belongs to the `azure.ai.project` service. The developer owns the registry's SKU, private endpoint, DNS, and firewall.
- **Brownfield (`endpoint:`) ignores `network:`.** When `endpoint:` is set on the project service, the account's network posture is fixed by whoever created it; azd warns and does not reconcile `network:`.
- **One default-DNS account per VNet.** Without a `dns:` block azd links the three `privatelink.*` AI zones to your VNet, and a VNet may hold only one link per namespace. A second account (or a brownfield hub that pre-links the zones) must use `dns:` reference mode to bind the private endpoint without re-linking.
- **Terraform IaC is not supported for private networking (v1).** Bicep-only today; `azd ai agent init --infra=terraform` is refused when `network:` is declared. Eject Bicep instead.

## Scenario 1 — Managed egress: private account, agent on Microsoft's network

Omit `agentSubnet` so the hosted-agent runtime uses a Microsoft-managed network. `peSubnet` is still required: the account data plane stays private behind an account private endpoint in your VNet, reachable from inside the VNet, a peered VNet, or VPN.

```yaml
infra:
  provider: microsoft.foundry

services:
  my-agent:
    host: azure.ai.agent
    project: src/my-agent
    uses:
      - ai-project
    image: myprivacr.azurecr.io/agents/my-agent:v1

  ai-project:
    host: azure.ai.project
    deployments: []
    network:
      isolationMode: AllowInternetOutbound
      peSubnet:
        vnet: ${AZURE_VNET_ID}
        name: pe-subnet
        prefix: 192.168.11.0/24
```

Provision and deploy:

```bash
azd env set AZURE_SUBSCRIPTION_ID "<sub>"
azd env set AZURE_LOCATION westus
azd env set AZURE_RESOURCE_GROUP "<rg>"
azd env set AZURE_VNET_ID "<vnet-resource-id>"
azd provision --no-prompt
azd deploy --no-prompt
```

Run deploy and invoke from a host that can reach the account private endpoint:

```bash
azd ai agent invoke --new-session "hello"
```

## Scenario 2 — BYO egress: agent injected into your VNet subnet

Set `agentSubnet` to inject the hosted-agent runtime into your customer subnet. `agentSubnet` and `peSubnet` must reference the same VNet in v1.

```yaml
services:
  my-agent:
    host: azure.ai.agent
    project: src/my-agent
    uses:
      - ai-project
    image: myprivacr.azurecr.io/agents/my-agent:v1

  ai-project:
    host: azure.ai.project
    network:
      agentSubnet:
        vnet: ${AZURE_VNET_ID}
        name: agent-subnet
        prefix: 192.168.10.0/24
      peSubnet:
        vnet: ${AZURE_VNET_ID}
        name: pe-subnet
        prefix: 192.168.11.0/24
```

Common failures:

- `403 Public access is disabled`: the data plane is private in every network-bound mode — run deploy/invoke from inside the VNet, a peered VNet, or VPN.
- `ImageError: registry authentication failed`: grant ACR pull permission to the Foundry project managed identity.

## Scenario 3 — Eject and customize the Bicep (advanced)

The synthesized template covers the common private-networking shapes. When you need something it does not express — an extra subnet, a private endpoint for a BYO dependent store, custom DNS wiring, a non-default account property, or additional `networkInjections` rules — eject the Bicep, edit it directly, and let azd provision your edited tree.

```bash
# Scaffold first, declare network: on the azure.ai.project service, then eject:
azd ai agent init --infra
```

Eject reads `network:` from the `host: azure.ai.project` service and writes the full Bicep tree: `infra/main.bicep`, `infra/modules/{resources,network,subnet,private-endpoint-dns,acr}.bicep`, and `infra/main.parameters.json`. `${VAR}` placeholders are preserved in the generated parameters file and resolved from the azd environment at provision time. `azure.yaml` is left unchanged: `infra.provider` stays `microsoft.foundry`.

```bash
azd provision --no-prompt
```
