param name string
param location string

resource function 'Microsoft.Web/sites@2021-03-01' = {
  name: '${name}func'
  location: location
  kind: 'functionapp,linux'

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
      FUNCTIONS_EXTENSION_VERSION: '~3'
      FUNCTIONS_WORKER_RUNTIME: 'python'
      AzureWebJobsStorage: 'DefaultEndpointsProtocol=https;AccountName=${storage.name};EndpointSuffix=${environment().suffixes.storage};AccountKey=${storage.listKeys().keys[0].value}'
      SCM_DO_BUILD_DURING_DEPLOYMENT: 'true'
    }
  }
}

resource storage 'Microsoft.Storage/storageAccounts@2021-09-01' = {
  name: '${replace(name, '-', '')}store'
  location: location
  sku: {
    name: 'Standard_LRS'
  }
  kind: 'Storage'
}

resource appServicePlan 'Microsoft.Web/serverfarms@2021-03-01' = {
  // https://docs.microsoft.com/azure/templates/microsoft.web/2020-06-01/serverfarms?tabs=bicep
  name: '${name}plan'
  location: location
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
  kind: 'functionapp'
}
