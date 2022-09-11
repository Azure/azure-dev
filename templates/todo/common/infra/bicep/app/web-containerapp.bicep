param environmentName string
param location string = resourceGroup().location
param serviceName string = 'web'
param imageName string

var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var abbrs = loadJsonContent('../../../../../common/infra/bicep/abbreviations.json')

resource applicationInsights 'Microsoft.Insights/components@2020-02-02' existing = {
  name: '${abbrs.insightsComponents}${resourceToken}'
}

resource api 'Microsoft.App/containerApps@2022-03-01' existing = {
  name: '${abbrs.appContainerApps}api-${resourceToken}'
}

module app '../../../../../common/infra/bicep/core/host/container-app.bicep' = {
  name: 'web-containerapp-${serviceName}'
  params: {
    environmentName: environmentName
    location: location
    serviceName: serviceName
    targetPort: 80
    imageName: imageName
    env: [ {
        name: 'REACT_APP_APPLICATIONINSIGHTS_CONNECTION_STRING'
        value: applicationInsights.properties.ConnectionString
      }
      {
        name: 'REACT_APP_API_BASE_URL'
        value: 'https://${api.properties.configuration.ingress.fqdn}'
      } ]
  }
}

output NAME string = app.outputs.NAME
output URI string = app.outputs.URI
output IDENTITY_PRINCIPAL_ID string = app.outputs.IDENTITY_PRINCIPAL_ID
