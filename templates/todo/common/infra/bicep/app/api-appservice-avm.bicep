param name string
param location string = resourceGroup().location
param tags object = {}

param allowedOrigins array = []
param appCommandLine string?
param appInsightResourceId string
param appServicePlanId string
@secure()
param appSettings object = {}
param siteConfig object = {}
param serviceName string = 'api'

@description('Required. Type of site to deploy.')
param kind string

@description('Optional. If client affinity is enabled.')
param clientAffinityEnabled bool = true

@description('Optional. Required if app of kind functionapp. Resource ID of the storage account to manage triggers and logging function executions.')
param storageAccountResourceId string?

module api 'br/public:avm/res/web/site:0.6.0' = {
  name: '${name}-app-module'
  params: {
    kind: kind
    name: name
    serverFarmResourceId: appServicePlanId
    tags: union(tags, { 'azd-service-name': serviceName })
    location: location
    appInsightResourceId: appInsightResourceId
    clientAffinityEnabled: clientAffinityEnabled
    storageAccountResourceId: storageAccountResourceId
    managedIdentities: {
      systemAssigned: true
    }
    siteConfig: union(siteConfig, {
      cors: {
        allowedOrigins: union(['https://portal.azure.com', 'https://ms.portal.azure.com'], allowedOrigins)
      }
      appCommandLine: appCommandLine
    })
    appSettingsKeyValuePairs: union(
      appSettings,
      { ENABLE_ORYX_BUILD: true, ApplicationInsightsAgent_EXTENSION_VERSION: contains(kind, 'linux') ? '~3' : '~2' }
    )
    logsConfiguration: {
      applicationLogs: { fileSystem: { level: 'Verbose' } }
      detailedErrorMessages: { enabled: true }
      failedRequestsTracing: { enabled: true }
      httpLogs: { fileSystem: { enabled: true, retentionInDays: 1, retentionInMb: 35 } }
    }
  }
}

output SERVICE_API_IDENTITY_PRINCIPAL_ID string = api.outputs.systemAssignedMIPrincipalId
output SERVICE_API_NAME string = api.outputs.name
output SERVICE_API_URI string = 'https://${api.outputs.defaultHostname}'
