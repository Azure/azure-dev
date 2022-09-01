param environmentName string
param location string = resourceGroup().location
param kind string = 'app,linux'
param appCommandLine string = 'pm2 serve /home/site/wwwroot --no-daemon --spa'
param linuxFxVersion string = 'NODE|16-lts'

var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var tags = { 'azd-env-name': environmentName }
var abbrs = loadJsonContent('../../../../../../common/infra/bicep/abbreviations.json')

resource applicationInsights 'Microsoft.Insights/components@2020-02-02' existing = {
  name: '${abbrs.insightsComponents}${resourceToken}'
}

resource appServicePlan 'Microsoft.Web/serverfarms@2022-03-01' existing = {
  name: '${abbrs.webServerFarms}${resourceToken}'
}

resource web 'Microsoft.Web/sites@2022-03-01' = {
  name: '${abbrs.webSitesAppService}web-${resourceToken}'
  location: location
  tags: union(tags, { 'azd-service-name': 'web' })
  kind: kind
  properties: {
    serverFarmId: appServicePlan.id
    siteConfig: {
      linuxFxVersion: linuxFxVersion
      alwaysOn: true
      ftpsState: 'FtpsOnly'
      appCommandLine: appCommandLine
    }
    httpsOnly: true
  }

  resource appSettings 'config' = {
    name: 'appsettings'
    properties: {
      SCM_DO_BUILD_DURING_DEPLOYMENT: 'false'
      APPLICATIONINSIGHTS_CONNECTION_STRING: applicationInsights.properties.ConnectionString
    }
  }

  resource logs 'config' = {
    name: 'logs'
    properties: {
      applicationLogs: { fileSystem: { level: 'Verbose' } }
      detailedErrorMessages: { enabled: true }
      failedRequestsTracing: { enabled: true }
      httpLogs: { fileSystem: { enabled: true, retentionInDays: 1, retentionInMb: 35 } }
    }
  }
}

output WEB_URI string = 'https://${web.properties.defaultHostName}'
