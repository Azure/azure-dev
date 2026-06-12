// Provisioning template for a microsoft.foundry service.
//
// Inputs are derived from the host: microsoft.foundry service body in
// azure.yaml by internal/synthesis. Greenfield only (no endpoint:); a
// brownfield path is a future addition.

targetScope = 'resourceGroup'

@description('Azure region for all resources.')
param location string = resourceGroup().location

@description('Tags applied to all resources.')
param tags object = {}

@description('Optional salt to vary resource names across re-provisions.')
param resourceTokenSalt string = ''

@description('Foundry project name.')
param foundryProjectName string

@description('Model deployments to provision on the Foundry account.')
param deployments deploymentsType = []

@description('Include an Azure Container Registry. Set true when any agent uses docker:.')
param includeAcr bool = false

@description('Principal id of the developer running azd; receives data-plane role assignments.')
param principalId string

@description('Principal type of the developer (User, ServicePrincipal, Group).')
param principalType string = 'User'

var resourceToken = empty(resourceTokenSalt)
  ? uniqueString(subscription().id, resourceGroup().id, location)
  : uniqueString(subscription().id, resourceGroup().id, location, resourceTokenSalt)

var abbrs = loadJsonContent('abbreviations.json')

resource foundryAccount 'Microsoft.CognitiveServices/accounts@2025-06-01' = {
  name: '${abbrs.cognitiveServicesAccounts}${resourceToken}'
  location: location
  tags: tags
  sku: { name: 'S0' }
  kind: 'AIServices'
  identity: { type: 'SystemAssigned' }
  properties: {
    allowProjectManagement: true
    customSubDomainName: '${abbrs.cognitiveServicesAccounts}${resourceToken}'
    publicNetworkAccess: 'Enabled'
    disableLocalAuth: true
    networkAcls: {
      defaultAction: 'Allow'
      virtualNetworkRules: []
      ipRules: []
    }
  }

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
    identity: { type: 'SystemAssigned' }
    properties: {
      description: '${foundryProjectName} Project'
      displayName: foundryProjectName
    }
    dependsOn: [ modelDeployments ]
  }
}

module acr 'modules/acr.bicep' = if (includeAcr) {
  name: 'acr'
  params: {
    location: location
    tags: tags
    name: '${abbrs.containerRegistryRegistries}${resourceToken}'
    foundryAccountId: foundryAccount.id
    foundryProjectName: foundryAccount::project.name
  }
}

// Grant the developer Cognitive Services User on the project so they can call
// the Foundry data-plane (chat/completions, agents API) from their machine.
var cogServicesUserRoleId = '/subscriptions/${subscription().subscriptionId}/providers/Microsoft.Authorization/roleDefinitions/a97b65f3-24c7-4388-baec-2e87135dc908'

resource developerCogServicesUser 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(foundryAccount::project.id, principalId, cogServicesUserRoleId)
  scope: foundryAccount::project
  properties: {
    principalId: principalId
    principalType: principalType
    roleDefinitionId: cogServicesUserRoleId
  }
}

output AZURE_AI_PROJECT_ID string = foundryAccount::project.id
output AZURE_AI_ACCOUNT_NAME string = foundryAccount.name
output AZURE_AI_PROJECT_NAME string = foundryAccount::project.name
output AZURE_RESOURCE_GROUP string = resourceGroup().name
output AZURE_OPENAI_ENDPOINT string = 'https://${foundryAccount.name}.openai.azure.com/'
output FOUNDRY_PROJECT_ENDPOINT string = 'https://${foundryAccount.name}.services.ai.azure.com/api/projects/${foundryAccount::project.name}'
output AZURE_CONTAINER_REGISTRY_ENDPOINT string = includeAcr ? acr!.outputs.loginServer : ''
output AZURE_CONTAINER_REGISTRY_RESOURCE_ID string = includeAcr ? acr!.outputs.resourceId : ''

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
