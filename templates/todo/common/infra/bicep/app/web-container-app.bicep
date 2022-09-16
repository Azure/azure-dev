param environmentName string
param location string = resourceGroup().location

param apiName string = ''
param applicationInsightsName string = ''
param containerAppsEnvironmentName string = ''
param containerRegistryName string = ''
param imageName string = ''
param keyVaultName string = ''
param serviceName string = 'web'

var abbrs = loadJsonContent('../../../../../common/infra/bicep/abbreviations.json')
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))

module web '../../../../../common/infra/bicep/core/host/container-app.bicep' = {
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
    keyVaultName: keyVault.name
    serviceName: serviceName
    targetPort: 80
  }
}

resource applicationInsights 'Microsoft.Insights/components@2020-02-02' existing = {
  name: !empty(applicationInsightsName) ? applicationInsightsName : '${abbrs.insightsComponents}${resourceToken}'
}

resource keyVault 'Microsoft.KeyVault/vaults@2022-07-01' existing = {
  name: !empty(keyVaultName) ? keyVaultName : '${abbrs.keyVaultVaults}${resourceToken}'
}

resource api 'Microsoft.App/containerApps@2022-03-01' existing = {
  name: !empty(apiName) ? apiName : '${abbrs.appContainerApps}api-${resourceToken}'
}

output WEB_NAME string = web.outputs.name
output WEB_URI string = web.outputs.uri
