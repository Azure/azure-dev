param environmentName string
param location string = resourceGroup().location

var tags = { 'azd-env-name': environmentName }
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))

resource appServicePlan 'Microsoft.Web/serverfarms@2022-03-01' = {
  name: 'plan-${resourceToken}'
  location: location
  tags: tags
  sku: {
    // S1 tier required for deployment slots
    name: 'S1'
    tier: 'Standard'
  }
  properties: {
    reserved: true
    zoneRedundant: false
  }
}

resource web 'Microsoft.Web/sites@2022-03-01' = {
  name: 'app-${resourceToken}'
  location: location
  tags: union(tags, { 'azd-service-name': 'api' })
  properties: {
    serverFarmId: appServicePlan.id
    httpsOnly: true
    siteConfig: {
      linuxFxVersion: 'PYTHON|3.11'
      appCommandLine: 'gunicorn --bind=0.0.0.0 --timeout 600 server:app'
    }
  }
  identity: {
    type: 'SystemAssigned'
  }

  resource appSettings 'config' = {
    name: 'appsettings'
    properties: {
      SCM_DO_BUILD_DURING_DEPLOYMENT: 'true'
    }
  }
}

// Deployment slot for staging
resource stagingSlot 'Microsoft.Web/sites/slots@2022-03-01' = {
  parent: web
  name: 'staging'
  location: location
  tags: tags
  properties: {
    serverFarmId: appServicePlan.id
    httpsOnly: true
    siteConfig: {
      linuxFxVersion: 'PYTHON|3.11'
      appCommandLine: 'gunicorn --bind=0.0.0.0 --timeout 600 server:app'
    }
  }
  identity: {
    type: 'SystemAssigned'
  }

  resource slotSettings 'config' = {
    name: 'appsettings'
    properties: {
      SCM_DO_BUILD_DURING_DEPLOYMENT: 'true'
    }
  }
}

output WEBSITE_URL string = 'https://${web.properties.defaultHostName}/'
output SLOT_URL string = 'https://${stagingSlot.properties.defaultHostName}/'
