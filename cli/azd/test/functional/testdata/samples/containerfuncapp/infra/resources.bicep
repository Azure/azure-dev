@description('Name of the azd environment.')
param environmentName string

@description('Location for all resources.')
param location string = resourceGroup().location

@description('Object ID of the user or service principal running azd.')
param principalId string

@description('Tags applied to all resources.')
param tags object = {}

var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var acrPullRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '7f951dda-4ed3-4680-a7ca-43fe172d538d'
)
var acrPushRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '8311e382-0749-4cb8-b61a-304f252e45ec'
)

resource storage 'Microsoft.Storage/storageAccounts@2023-05-01' = {
  name: 'st${resourceToken}'
  location: location
  tags: tags
  sku: {
    name: 'Standard_LRS'
  }
  kind: 'StorageV2'
  properties: {
    allowBlobPublicAccess: false
    minimumTlsVersion: 'TLS1_2'
  }
}

resource registry 'Microsoft.ContainerRegistry/registries@2023-07-01' = {
  name: 'acr${resourceToken}'
  location: location
  tags: tags
  sku: {
    name: 'Basic'
  }
  properties: {
    adminUserEnabled: false
  }
}

resource plan 'Microsoft.Web/serverfarms@2023-12-01' = {
  name: 'plan-${resourceToken}'
  location: location
  tags: tags
  kind: 'linux'
  sku: {
    name: 'EP1'
    tier: 'ElasticPremium'
  }
  properties: {
    reserved: true
  }
}

resource functionApp 'Microsoft.Web/sites@2023-12-01' = {
  name: 'func-${resourceToken}'
  location: location
  tags: union(tags, {
    'azd-service-name': 'func'
  })
  kind: 'functionapp,linux,container'
  identity: {
    type: 'SystemAssigned'
  }
  properties: {
    serverFarmId: plan.id
    httpsOnly: true
    siteConfig: {
      linuxFxVersion: 'DOCKER|${registry.properties.loginServer}/placeholder:latest'
      acrUseManagedIdentityCreds: true
      ftpsState: 'Disabled'
      minTlsVersion: '1.2'
      appSettings: [
        {
          name: 'FUNCTIONS_EXTENSION_VERSION'
          value: '~4'
        }
        {
          name: 'FUNCTIONS_WORKER_RUNTIME'
          value: 'node'
        }
        {
          name: 'AzureWebJobsStorage'
          value: 'DefaultEndpointsProtocol=https;AccountName=${storage.name};AccountKey=${storage.listKeys().keys[0].value};EndpointSuffix=${environment().suffixes.storage}'
        }
        {
          name: 'WEBSITES_ENABLE_APP_SERVICE_STORAGE'
          value: 'false'
        }
      ]
    }
  }
}

resource functionAcrPull 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(registry.id, functionApp.id, acrPullRoleId)
  scope: registry
  properties: {
    principalId: functionApp.identity.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: acrPullRoleId
  }
}

resource deployerAcrPush 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(registry.id, principalId, acrPushRoleId)
  scope: registry
  properties: {
    principalId: principalId
    roleDefinitionId: acrPushRoleId
  }
}

output AZURE_CONTAINER_REGISTRY_ENDPOINT string = registry.properties.loginServer
output AZURE_CONTAINER_REGISTRY_NAME string = registry.name
output AZURE_FUNCTION_URI string = 'https://${functionApp.properties.defaultHostName}'
