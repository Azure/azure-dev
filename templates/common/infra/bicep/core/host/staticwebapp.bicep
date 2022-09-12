param environmentName string
param location string = resourceGroup().location
param serviceName string
param sku object = {
  name: 'Free'
  tier: 'Free'
}

var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var tags = { 'azd-env-name': environmentName }
var abbrs = loadJsonContent('../../abbreviations.json')

resource web 'Microsoft.Web/staticSites@2022-03-01' = {
  name: '${abbrs.webStaticSites}${serviceName}-${resourceToken}'
  location: location
  tags: union(tags, { 'azd-service-name': serviceName })
  sku: sku
  properties: {
    provider: 'Custom'
  }
}

output NAME string = web.name
output URI string = 'https://${web.properties.defaultHostname}'
