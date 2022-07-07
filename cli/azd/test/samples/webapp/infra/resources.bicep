param name string
param location string

resource web 'Microsoft.Web/sites@2021-03-01' = {
  name: '${name}web'
  location: location
  properties: {
    serverFarmId: appServicePlan.id
    httpsOnly: true
  }
  identity: {
    type: 'SystemAssigned'
  }

  resource appSettings 'config' = {
    name: 'appsettings'
    properties: {
      'WEBSITE_RUN_FROM_PACKAGE': '1'
    }
  }
}

resource appServicePlan 'Microsoft.Web/serverfarms@2021-03-01' = {
  name: '${name}plan'
  location: location
  sku: {
    name: 'D1'
  }
}

output WEBSITE_URL string = 'https://${web.properties.defaultHostName}/'
