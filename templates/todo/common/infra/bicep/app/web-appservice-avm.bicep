param name string
param location string = resourceGroup().location
param tags object = {}
param serviceName string = 'web'
param appCommandLine string = 'pm2 serve /home/site/wwwroot --no-daemon --spa'
param appInsightResourceId string
param appServicePlanId string
param linuxFxVersion string
param kind string = 'app,linux'

module web 'br/public:avm/res/web/site:0.6.0' = {
  name: '${name}-deployment'
  params: {
    kind: kind
    name: name
    serverFarmResourceId: appServicePlanId
    tags: union(tags, { 'azd-service-name': serviceName })
    location: location
    appInsightResourceId: appInsightResourceId
    siteConfig: {
      appCommandLine: appCommandLine
      linuxFxVersion: linuxFxVersion
      alwaysOn: true
    }
    logsConfiguration: {
      applicationLogs: { fileSystem: { level: 'Verbose' } }
      detailedErrorMessages: { enabled: true }
      failedRequestsTracing: { enabled: true }
      httpLogs: { fileSystem: { enabled: true, retentionInDays: 1, retentionInMb: 35 } }
    }
    appSettingsKeyValuePairs: { ApplicationInsightsAgent_EXTENSION_VERSION: contains(kind, 'linux') ? '~3' : '~2' }
  }
}

output SERVICE_WEB_IDENTITY_PRINCIPAL_ID string = web.outputs.systemAssignedMIPrincipalId
output SERVICE_WEB_NAME string = web.outputs.name
output SERVICE_WEB_URI string = 'https://${web.outputs.defaultHostname}'
