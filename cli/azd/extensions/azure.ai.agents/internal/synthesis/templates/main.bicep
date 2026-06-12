// Provisioning template for a microsoft.foundry service.
//
// Inputs are derived from the host: microsoft.foundry service body in
// azure.yaml by internal/synthesis. Greenfield only (no endpoint:); a
// brownfield path is a future addition.

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

// Variables

var resourceToken = empty(resourceTokenSalt)
  ? uniqueString(subscription().id, resourceGroup().id, location)
  : uniqueString(subscription().id, resourceGroup().id, location, resourceTokenSalt)

var abbrs = loadJsonContent('abbreviations.json')

var foundryAccountName = '${abbrs.cognitiveServicesAccounts}${resourceToken}'

// Built-in role definition ids. See: https://learn.microsoft.com/azure/role-based-access-control/built-in-roles
var cognitiveServicesUserRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'a97b65f3-24c7-4388-baec-2e87135dc908'
)

// Resources

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
    publicNetworkAccess: 'Enabled'
    disableLocalAuth: true
    networkAcls: {
      defaultAction: 'Allow'
      virtualNetworkRules: []
      ipRules: []
    }
  }

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

module acr 'modules/acr.bicep' = if (includeAcr) {
  name: 'acr'
  params: {
    location: location
    tags: tags
    name: '${abbrs.containerRegistryRegistries}${resourceToken}'
    foundryAccountName: foundryAccount.name
    foundryProjectName: foundryAccount::project.name
    foundryAccountPrincipalId: foundryAccount.identity.principalId
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
