// Provisioning template for a Foundry project service.
//
// Inputs are derived from the host: azure.ai.project service body in
// azure.yaml by internal/synthesis. This entry point creates a new Foundry
// account and project; existing projects use the separate editable entry point.
//
// Subscription-scoped so a new resource group is part of the deployment. This
// keeps `azd provision --preview` side-effect free: the resource group shows
// up as a previewed Create instead of being created up front to satisfy a
// resource-group-scoped what-if.

targetScope = 'subscription'

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

type agentHostingType = {
  enabled: bool
  hostingType: 'ManagedCluster' | ''
  name: string
  clusterResourceId: string
}

// Parameters

@description('Azure region for all resources.')
param location string

@description('Name of the resource group to deploy resources into.')
@minLength(1)
@maxLength(90)
param resourceGroupName string

@description('Create or update the resource group. False when BYO AKS reuses an existing group.')
param createResourceGroup bool = true

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

@description('Include the native Azure Container Registry. Required for docker: agents or BYO AKS.')
param includeAcr bool = false

@description('Existing AKS cluster hosting. Supporting resources are created automatically.')
param agentHosting agentHostingType = {
  enabled: false
  hostingType: ''
  name: ''
  clusterResourceId: ''
}

@description('AKS kubelet principal ID, resolved by microsoft.foundry for registry access.')
param kubeletObjectId string = ''

@description('Built-in federation-management role, resolved by microsoft.foundry.')
param federatedIdentityRoleDefinitionId string = ''

// Network isolation parameters (see modules/resources.bicep for semantics).
// All default off so an absent network: block yields a public account.

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

@description('Managed-network isolation mode (managed mode).')
param managedIsolationMode string = ''

@description('Resource group holding existing private DNS zones. Empty creates new zones.')
param dnsZonesResourceGroup string = ''

@description('Subscription holding existing private DNS zones. Empty defaults to this subscription.')
param dnsZonesSubscription string = ''

// Resources

resource resourceGroup 'Microsoft.Resources/resourceGroups@2021-04-01' = if (createResourceGroup) {
  name: resourceGroupName
  location: location
  tags: tags
}

module resources 'modules/resources.bicep' = {
  name: 'foundry-resources'
  scope: az.resourceGroup(resourceGroupName)
  params: {
    location: location
    tags: tags
    resourceTokenSalt: resourceTokenSalt
    foundryProjectName: foundryProjectName
    deployments: deployments
    includeAcr: includeAcr
    agentHosting: agentHosting
    kubeletObjectId: kubeletObjectId
    federatedIdentityRoleDefinitionId: federatedIdentityRoleDefinitionId
    enableNetworkIsolation: enableNetworkIsolation
    useManagedEgress: useManagedEgress
    vnetId: vnetId
    agentSubnetName: agentSubnetName
    agentSubnetPrefix: agentSubnetPrefix
    createAgentSubnet: createAgentSubnet
    peSubnetName: peSubnetName
    peSubnetPrefix: peSubnetPrefix
    createPESubnet: createPESubnet
    managedIsolationMode: managedIsolationMode
    dnsZonesResourceGroup: dnsZonesResourceGroup
    dnsZonesSubscription: dnsZonesSubscription
  }
  dependsOn: [resourceGroup]
}

// Outputs

output AZURE_RESOURCE_GROUP string = resourceGroupName
output AZURE_FOUNDRY_RESOURCE_GROUP string = resourceGroupName
output AZURE_AI_PROJECT_ID string = resources.outputs.AZURE_AI_PROJECT_ID
output AZURE_AI_ACCOUNT_NAME string = resources.outputs.AZURE_AI_ACCOUNT_NAME
output AZURE_AI_PROJECT_NAME string = resources.outputs.AZURE_AI_PROJECT_NAME
output AZURE_OPENAI_ENDPOINT string = resources.outputs.AZURE_OPENAI_ENDPOINT
output FOUNDRY_PROJECT_ENDPOINT string = resources.outputs.FOUNDRY_PROJECT_ENDPOINT
output AZURE_CONTAINER_REGISTRY_ENDPOINT string = resources.outputs.AZURE_CONTAINER_REGISTRY_ENDPOINT
output AZURE_CONTAINER_REGISTRY_RESOURCE_ID string = resources.outputs.AZURE_CONTAINER_REGISTRY_RESOURCE_ID
output AZURE_AI_PROJECT_ACR_CONNECTION_NAME string = resources.outputs.AZURE_AI_PROJECT_ACR_CONNECTION_NAME
output AZURE_FOUNDRY_NETWORK_MODE string = resources.outputs.AZURE_FOUNDRY_NETWORK_MODE
output AZURE_FOUNDRY_MANAGED_ISOLATION_MODE string = resources.outputs.AZURE_FOUNDRY_MANAGED_ISOLATION_MODE
