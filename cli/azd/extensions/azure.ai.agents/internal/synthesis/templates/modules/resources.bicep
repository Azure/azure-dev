// Resource-group-scoped resources for a microsoft.foundry service: the
// Foundry (AIServices) account, its project, model deployments, the optional
// container registry, and the developer role assignment.
//
// Deployed by main.bicep into a resource group it creates at subscription
// scope. Kept as a separate module so main.bicep can target the subscription
// (and thus create the resource group) while these resources stay RG-scoped.

targetScope = 'resourceGroup'

// User-defined types

@description('Shape of one model deployment entry in azure.yaml.')
type deploymentsType = deploymentType[]

@description('Shape of a single model deployment.')
type deploymentType = {
  name: string
  model: {
    name: string
    format: string
    version: string
  }
  sku: {
    name: string
    capacity: int
  }
}

// Parameters

@description('Azure region for all resources.')
param location string = resourceGroup().location

@description('Tags applied to all resources.')
param tags object = {}

@description('Optional salt to vary resource names across re-provisions.')
param resourceTokenSalt string = ''

@description('Foundry project name. 3-32 alphanumeric/hyphen chars.')
@minLength(3)
@maxLength(32)
param foundryProjectName string

@description('Model deployments to provision on the Foundry account.')
param deployments deploymentsType = []

@description('Include an Azure Container Registry. Set true when any agent uses docker:.')
param includeAcr bool = false

@description('Object id of the developer running azd. When set, grants Cognitive Services User on the project. Empty disables the role assignment so headless / CI runs do not fail.')
param principalId string = ''

@description('Principal type used in the developer role assignment.')
param principalType string = 'User'

// Network isolation parameters. All default off so an absent network: block in
// azure.yaml yields a public account identical to the pre-network template.

@description('Master switch: when true the account is VNet-bound (private).')
param enableNetworkIsolation bool = false

@description('When true (and isolation on), the agent runtime uses the Microsoft-managed network instead of injecting into a customer subnet.')
param useManagedEgress bool = false

@description('ARM id of the existing customer VNet (byo mode).')
param vnetId string = ''

@description('Agent (delegated) subnet name.')
param agentSubnetName string = 'agent-subnet'

@description('Agent subnet CIDR. Empty derives a /24 from the VNet space.')
param agentSubnetPrefix string = ''

@description('When true, create the agent subnet; when false, reference it.')
param createAgentSubnet bool = false

@description('Private-endpoint subnet name.')
param peSubnetName string = 'pe-subnet'

@description('Private-endpoint subnet CIDR. Empty derives a /24 from the VNet space.')
param peSubnetPrefix string = ''

@description('When true, create the PE subnet; when false, reference it.')
param createPESubnet bool = false

@description('Managed-network isolation mode (managed mode). AllowInternetOutbound | AllowOnlyApprovedOutbound.')
param managedIsolationMode string = ''

@description('Resource group holding existing private DNS zones. Empty creates and links new zones.')
param dnsZonesResourceGroup string = ''

@description('Subscription holding existing private DNS zones. Empty defaults to this subscription.')
param dnsZonesSubscription string = ''

// Variables

var resourceToken = empty(resourceTokenSalt)
  ? uniqueString(subscription().id, resourceGroup().id, location)
  : uniqueString(subscription().id, resourceGroup().id, location, resourceTokenSalt)

var abbrs = loadJsonContent('../abbreviations.json')

var foundryAccountName = '${abbrs.cognitiveServicesAccounts}${resourceToken}'

// Egress: byo injects the agent into a customer subnet; managed uses the
// Microsoft-managed network. Ingress: an account private endpoint is always
// provisioned when isolation is on, so the data plane is never left public.
var useByoNetwork = enableNetworkIsolation && !useManagedEgress
var useManagedNetwork = enableNetworkIsolation && useManagedEgress
var disablePublicDataPlaneAccess = enableNetworkIsolation

// Built-in role definition ids. See: https://learn.microsoft.com/azure/role-based-access-control/built-in-roles
var cognitiveServicesUserRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'a97b65f3-24c7-4388-baec-2e87135dc908'
)

// Resources

// Customer VNet wiring: reference the VNet and create or reference the agent
// (byo egress only) + private-endpoint subnets. Runs whenever isolation is on
// because the account private endpoint is always provisioned.
module network 'network.bicep' = if (enableNetworkIsolation) {
  name: 'foundry-network'
  params: {
    vnetId: vnetId
    agentSubnetName: agentSubnetName
    agentSubnetPrefix: agentSubnetPrefix
    createAgentSubnet: createAgentSubnet
    peSubnetName: peSubnetName
    peSubnetPrefix: peSubnetPrefix
    createPESubnet: createPESubnet
  }
}

// networkInjections wires the account into the agent subnet (byo) or the
// Microsoft-managed network (managed). Null when isolation is off.
//
// subnetArmId is built as a concrete string from the (concrete) vnetId param
// rather than network!.outputs.agentSubnetId. The account and the network
// module deploy in the same template, so an inter-module reference() here is
// unresolved at the CognitiveServices RP preflight, which then fails to convert
// networkInjections to its typed contract (ARM what-if does not catch this).
// The deterministic id avoids the unresolved reference; an explicit dependsOn
// on the network module preserves ordering (the subnet must exist first).
var agentSubnetArmId = '${vnetId}/subnets/${agentSubnetName}'
var agentNetworkInjections = useByoNetwork
  ? [
      {
        scenario: 'agent'
        subnetArmId: agentSubnetArmId
        useMicrosoftManagedNetwork: false
      }
    ]
  : (useManagedNetwork
      ? [
          {
            scenario: 'agent'
            useMicrosoftManagedNetwork: true
          }
        ]
      : null)

resource foundryAccount 'Microsoft.CognitiveServices/accounts@2025-06-01' = {
  name: foundryAccountName
  location: location
  tags: tags
  sku: {
    name: 'S0'
  }
  kind: 'AIServices'
  identity: {
    type: 'SystemAssigned'
  }
  properties: {
    allowProjectManagement: true
    customSubDomainName: foundryAccountName
    publicNetworkAccess: disablePublicDataPlaneAccess ? 'Disabled' : 'Enabled'
    disableLocalAuth: true
    networkAcls: {
      defaultAction: disablePublicDataPlaneAccess ? 'Deny' : 'Allow'
      bypass: disablePublicDataPlaneAccess ? 'AzureServices' : null
      virtualNetworkRules: []
      ipRules: []
    }
    networkInjections: agentNetworkInjections
  }

  // The account injects into the agent subnet via a deterministic id (above),
  // so Bicep cannot infer the dependency on the network module that creates
  // that subnet. Declare it explicitly so the subnet exists before injection.
  dependsOn: useByoNetwork ? [network] : []

  // Sequential model deployment creation; ARM throttles concurrent
  // deployments on the same account.
  @batchSize(1)
  resource modelDeployments 'deployments' = [
    for d in deployments: {
      name: d.name
      properties: {
        model: d.model
      }
      sku: d.sku
    }
  ]

  resource project 'projects' = {
    name: foundryProjectName
    location: location
    identity: {
      type: 'SystemAssigned'
    }
    properties: {
      description: '${foundryProjectName} Project'
      displayName: foundryProjectName
    }
    // Explicit dependsOn ensures all model deployments complete before
    // the project is created; the project does not reference them so
    // there is no implicit dependency Bicep can infer.
    dependsOn: [
      modelDeployments
    ]
  }
}

// Managed-network isolation (managed egress only). Applies the chosen outbound
// isolation mode to the Microsoft-managed VNet that hosts the agent runtime.
// Only deployed when an explicit isolationMode is requested; otherwise the
// platform default applies. Note: AllowOnlyApprovedOutbound additionally
// requires approved outbound rules for the agent to reach dependent resources;
// for the platform-managed stores used here those are managed by the platform.
resource foundryManagedNetwork 'Microsoft.CognitiveServices/accounts/managednetworks@2025-10-01-preview' =
  if (useManagedNetwork && !empty(managedIsolationMode)) {
    parent: foundryAccount
    name: 'default'
    properties: {
      managedNetwork: {
        isolationMode: managedIsolationMode
      }
    }
  }

module acr 'acr.bicep' = if (includeAcr) {
  name: 'acr'
  params: {
    location: location
    tags: tags
    name: '${abbrs.containerRegistryRegistries}${resourceToken}'
    foundryAccountName: foundryAccount.name
    foundryProjectName: foundryAccount::project.name
    foundryProjectPrincipalId: foundryAccount::project.identity.principalId
    enableNetworkIsolation: enableNetworkIsolation
  }
}

// Account private endpoint + AI private DNS zones. The account is always given a
// private endpoint when isolation is on (byo or managed egress); dependent
// stores stay platform-managed, so only the account gets an endpoint.
module privateEndpointDns 'private-endpoint-dns.bicep' = if (enableNetworkIsolation) {
  name: 'foundry-private-endpoint-dns'
  params: {
    aiAccountName: foundryAccount.name
    vnetId: network!.outputs.vnetId
    peSubnetId: network!.outputs.peSubnetId
    suffix: resourceToken
    dnsZonesResourceGroup: dnsZonesResourceGroup
    dnsZonesSubscription: dnsZonesSubscription
  }
}

// Grant the developer Cognitive Services User on the project so they can call
// the Foundry data-plane (chat/completions, agents API) from their machine.
resource developerCognitiveServicesUser 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (!empty(principalId)) {
  name: guid(foundryAccount::project.id, principalId, cognitiveServicesUserRoleId)
  scope: foundryAccount::project
  properties: {
    principalId: principalId
    principalType: principalType
    roleDefinitionId: cognitiveServicesUserRoleId
  }
}

// Outputs

output AZURE_AI_PROJECT_ID string = foundryAccount::project.id
output AZURE_AI_ACCOUNT_NAME string = foundryAccount.name
output AZURE_AI_PROJECT_NAME string = foundryAccount::project.name
output AZURE_OPENAI_ENDPOINT string = 'https://${foundryAccount.name}.openai.azure.com/'
output FOUNDRY_PROJECT_ENDPOINT string = 'https://${foundryAccount.name}.services.ai.azure.com/api/projects/${foundryAccount::project.name}'
output AZURE_CONTAINER_REGISTRY_ENDPOINT string = includeAcr ? acr!.outputs.loginServer : ''
output AZURE_CONTAINER_REGISTRY_RESOURCE_ID string = includeAcr ? acr!.outputs.resourceId : ''
output AZURE_AI_PROJECT_ACR_CONNECTION_NAME string = includeAcr ? acr!.outputs.connectionName : ''
output AZURE_FOUNDRY_NETWORK_MODE string = !enableNetworkIsolation ? 'none' : (useManagedEgress ? 'managed' : 'byo')
output AZURE_FOUNDRY_MANAGED_ISOLATION_MODE string = useManagedNetwork ? managedIsolationMode : ''
