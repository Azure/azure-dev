param environmentName string
param location string = resourceGroup().location
param sku object = {
  name: 'Standard'
}

var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var tags = { 'azd-env-name': environmentName }
var abbrs = loadJsonContent('../../abbreviations.json')

// 2022-02-01-preview needed for anonymousPullEnabled
resource containerRegistry 'Microsoft.ContainerRegistry/registries@2022-02-01-preview' = {
  name: '${abbrs.containerRegistryRegistries}${resourceToken}'
  location: location
  tags: tags
  sku: sku
  properties: {
    adminUserEnabled: true
    anonymousPullEnabled: false
    dataEndpointEnabled: false
    encryption: {
      status: 'disabled'
    }
    networkRuleBypassOptions: 'AzureServices'
    publicNetworkAccess: 'Enabled'
    zoneRedundancy: 'Disabled'
  }
}

output AZURE_CONTAINER_REGISTRY_ENDPOINT string = containerRegistry.properties.loginServer
output AZURE_CONTAINER_REGISTRY_NAME string = containerRegistry.name
