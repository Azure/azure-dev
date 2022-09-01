param environmentName string
param location string = resourceGroup().location
param sku object
param kind string = ''
param reserved bool = true

var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var tags = { 'azd-env-name': environmentName }
var abbrs = loadJsonContent('../abbreviations.json')

resource appServicePlan 'Microsoft.Web/serverfarms@2022-03-01' = {
  name: '${abbrs.webServerFarms}${resourceToken}'
  location: location
  tags: tags
  sku: sku
  kind: kind
  properties: {
    reserved: reserved
  }
}

output AZURE_APP_SERVICE_PLAN_ID string = appServicePlan.id
