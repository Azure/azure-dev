param environmentName string
param location string = resourceGroup().location
var tags = { 'azd-env-name': environmentName }
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))

resource functions 'Microsoft.Web/sites@2022-03-01' = {
  name: 'func-${resourceToken}'
  location: location
  kind: 'functionapp,linux'
  tags: union(tags, { 'azd-service-name': 'func' })
  properties: {
    enabled: true
    serverFarmId: appServicePlan.id
    httpsOnly: false
    reserved: true

    siteConfig: {
      functionAppScaleLimit: 200
      use32BitWorkerProcess: false
      ftpsState: 'FtpsOnly'
      cors: {
        allowedOrigins: [
          // allow testing through the Azure portal
          'https://ms.portal.azure.com'
        ]
        supportCredentials: false
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
      FUNCTIONS_EXTENSION_VERSION: '~4'
      FUNCTIONS_WORKER_RUNTIME: 'python'
      AzureWebJobsStorage__accountName: storage.name
      SCM_DO_BUILD_DURING_DEPLOYMENT: 'true'
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

resource storage_StorageBlobDataContributor 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(storage.id, functions.id, subscriptionResourceId('Microsoft.Authorization/roleDefinitions', 'b7e6dc6d-f1e8-4753-8033-0f276bb0955b'))
  properties: {
    principalId: functions.identity.principalId
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', 'b7e6dc6d-f1e8-4753-8033-0f276bb0955b')
    principalType: 'ServicePrincipal'
  }
  scope: storage
}

resource appServicePlan 'Microsoft.Web/serverfarms@2022-03-01' = {
  name: 'plan-${resourceToken}'
  location: location
  tags: tags
  kind: 'functionapp'
  sku: {
    name: 'Y1'
    tier: 'Dynamic'
    size: 'Y1'
    family: 'Y'
    capacity: 0
  }
  properties: {
    reserved: true
  }
}

output AZURE_FUNCTION_URI string = 'https://${functions.properties.defaultHostName}'
