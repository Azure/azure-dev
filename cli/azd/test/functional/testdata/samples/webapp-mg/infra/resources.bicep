param environmentName string
param location string = resourceGroup().location
param serviceName string
var tags = { 'azd-env-name': environmentName }
var resourceToken = toLower(uniqueString(resourceGroup().id))

resource web 'Microsoft.Web/sites@2022-03-01' = {
  name: 'app-${resourceToken}'
  location: location
  tags: union(tags, { 'azd-service-name': serviceName })
  properties: {
    serverFarmId: appServicePlan.id
    httpsOnly: true
    siteConfig: {
      linuxFxVersion: 'DOTNETCORE|6.0'
    }
  }
  identity: {
    type: 'SystemAssigned'
  }

  resource appSettings 'config' = {
    name: 'appsettings'
    properties: {
      WEBSITE_RUN_FROM_PACKAGE: '1'
    }
  }
}

resource appServicePlan 'Microsoft.Web/serverfarms@2022-03-01' = {
  name: 'plan-${resourceToken}'
  location: location
  tags: tags
  sku: {
    name: 'B1'
  }
  properties: {
    reserved: true
    zoneRedundant: false
  }
}

output WEBSITE_URL string = 'https://${web.properties.defaultHostName}/'
