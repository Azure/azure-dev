param location string
param resourceToken string
param tags object

var abbrs = loadJsonContent('../../../../../../../common/infra/bicep/abbreviations.json')

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
  kind: 'app,linux'
  properties: {
    serverFarmId: appServicePlan.id
    siteConfig: {
      linuxFxVersion: 'NODE|16-lts'
      alwaysOn: true
      ftpsState: 'FtpsOnly'
      appCommandLine: 'pm2 serve /home/site/wwwroot --no-daemon --spa'
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
      httpLogs: {
        fileSystem: {
          enabled: true
          retentionInDays: 1
          retentionInMb: 35
        }
      }
    }
  }
}

output WEB_URI string = 'https://${web.properties.defaultHostName}'
