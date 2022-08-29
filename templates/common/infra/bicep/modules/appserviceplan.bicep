param location string
param resourceToken string
param tags object

var abbrs = loadJsonContent('../abbreviations.json')

resource appServicePlan 'Microsoft.Web/serverfarms@2022-03-01' = {
  name: '${abbrs.webServerFarms}${resourceToken}'
  location: location
  tags: tags
  sku: {
    name: 'B1'
  }
  properties: {
    reserved: true
  }
}

output AZURE_APP_SERVICE_PLAN_ID string = appServicePlan.id
