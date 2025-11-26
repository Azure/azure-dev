param environmentName string
param location string = resourceGroup().location
var tags = { 'azd-env-name': environmentName }
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))

resource functions 'Microsoft.Web/sites@2023-12-01' = {
  name: 'func-${resourceToken}'
  location: location
  kind: 'functionapp,linux'
  tags: union(tags, { 'azd-service-name': 'func' })
  properties: {
    serverFarmId: flexFunctionPlan.id
    httpsOnly: false
    functionAppConfig: {
      deployment: {
        storage: {
          type: 'blobContainer'
          value: '${storage.properties.primaryEndpoints.blob}${blobContainer.name}'
          authentication: {
            type: 'SystemAssignedIdentity'
          }
        }
      }
      scaleAndConcurrency: {
        maximumInstanceCount: 100
        instanceMemoryMB: 2048
      }
      runtime: {
        name: 'python'
        version: '3.11'
      }
    }
  }

  identity: {
    type: 'SystemAssigned'
  }

  resource appSettings 'config' = {
    name: 'appsettings'
    // https://docs.microsoft.com/azure/azure-functions/functions-app-settings
    properties: {
      AzureWebJobsStorage__accountName: storage.name
    }
  }
}

resource storage 'Microsoft.Storage/storageAccounts@2022-05-01' = {
  name: 'st${resourceToken}'
  location: location
  tags: tags
  sku: {
    name: 'Standard_LRS'
  }
  kind: 'Storage'
  properties: {
    allowSharedKeyAccess: false
  }
}

resource blobServices 'Microsoft.Storage/storageAccounts/blobServices@2025-01-01' = {
  name: 'default'
  parent: storage
}

resource blobContainer 'Microsoft.Storage/storageAccounts/blobServices/containers@2025-01-01' = {
  name: 'func-deploy'
  parent: blobServices
  properties: {
    publicAccess: 'None'
  }
}

resource storage_StorageBlobDataContributor 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(storage.id, functions.id, subscriptionResourceId('Microsoft.Authorization/roleDefinitions', 'b7e6dc6d-f1e8-4753-8033-0f276bb0955b'))
  properties: {
    principalId: functions.identity.principalId
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', 'b7e6dc6d-f1e8-4753-8033-0f276bb0955b')
    principalType: 'ServicePrincipal'
  }
  scope: storage
}

resource flexFunctionPlan 'Microsoft.Web/serverfarms@2023-12-01' = {
  name: 'plan-${resourceToken}'
  location: location
  tags: tags
  kind: 'functionapp'
  sku: {
    tier: 'FlexConsumption'
    name: 'FC1'
  }
  properties: {
    reserved: true
  }
}

output AZURE_FUNCTION_URI string = 'https://${functions.properties.defaultHostName}'
