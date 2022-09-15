param environmentName string
param location string = resourceGroup().location

param apiName string
param applicationInsightsName string
param containerAppsEnvironmentName string = ''
param containerRegistryName string = ''
param imageName string = 'nginx:latest'
param serviceName string = 'web'

var abbrs = loadJsonContent('../../../../../common/infra/bicep/abbreviations.json')
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))

resource applicationInsights 'Microsoft.Insights/components@2020-02-02' existing = {
  name: !empty(applicationInsightsName) ? applicationInsightsName : '${abbrs.insightsComponents}${resourceToken}'
}

resource api 'Microsoft.App/containerApps@2022-03-01' existing = {
  name: !empty(apiName) ? apiName : '${abbrs.appContainerApps}api-${resourceToken}'
}

module app '../../../../../common/infra/bicep/core/host/container-app.bicep' = {
  name: '${serviceName}-container-app-module'
  params: {
    environmentName: environmentName
    location: location
    containerAppsEnvironmentName: containerAppsEnvironmentName
    containerRegistryName: containerRegistryName
    env: [
      {
        name: 'REACT_APP_APPLICATIONINSIGHTS_CONNECTION_STRING'
        value: applicationInsights.properties.ConnectionString
      }
      {
        name: 'REACT_APP_API_BASE_URL'
        value: 'https://${api.properties.configuration.ingress.fqdn}'
      }
      {
        name: 'APPLICATIONINSIGHTS_CONNECTION_STRING'
        value: applicationInsights.properties.ConnectionString
      }
    ]
    imageName: !empty(imageName) ? imageName : 'nginx:latest'
    serviceName: serviceName
    targetPort: 80
  }
}

output webName string = app.outputs.name
output webUri string = app.outputs.uri
