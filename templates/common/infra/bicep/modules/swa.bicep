param environmentName string
param location string = resourceGroup().location

var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var tags = { 'azd-env-name': environmentName }
var abbrs = loadJsonContent('../../../../common/infra/bicep/abbreviations.json')

resource web 'Microsoft.Web/staticSites@2022-03-01' = {
  name: '${abbrs.webStaticSites}web-${resourceToken}'
  location: location
  tags: union(tags, { 'azd-service-name': 'web' })
  sku: {
    name: 'Free'
    tier: 'Free'
  }
  properties: {
    provider: 'Custom'
  }
}

output WEB_URI string = 'https://${web.properties.defaultHostname}'
