targetScope = 'subscription'

@minLength(1)
@maxLength(64)
@description('Name of the the environment which is used to generate a short unique hash used in all resources.')
param environmentName string

@minLength(1)
@description('Primary location for all resources')
param location string

// Optional parameters to override the default azd resource naming conventions. Update the main.parameters.json file to provide values. e.g.,:
// "resourceGroupName": {
//      "value": "myGroupName"
// }
param apiServiceName string = ''
param applicationInsightsDashboardName string = ''
param applicationInsightsName string = ''
param appServicePlanName string = ''
param cosmosAccountName string = ''
param cosmosDatabaseName string = ''
param keyVaultName string = ''
param logAnalyticsName string = ''
param resourceGroupName string = ''
param webServiceName string = ''
param apimServiceName string = ''

@description('Flag to use Azure API Management to mediate the calls between the Web frontend and the backend API')
param useAPIM bool = false

@description('Id of the user or app to assign application roles')
param principalId string = ''

var abbrs = loadJsonContent('../../../../../../common/infra/bicep/abbreviations.json')
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var tags = { 'azd-env-name': environmentName }
var appInsightResourceId = resourceId(subscription().subscriptionId, rg.name,
'MMicrosoft.Insights/components', monitoring.outputs.applicationInsightsName)

// Organize resources in a resource group
resource rg 'Microsoft.Resources/resourceGroups@2021-04-01' = {
  name: !empty(resourceGroupName) ? resourceGroupName : '${abbrs.resourcesResourceGroups}${environmentName}'
  location: location
  tags: tags
}

// The application frontend
module web 'br/public:avm/res/web/site:0.2.0' = {
  name: 'web'
  scope: rg
  params: {
    kind: 'app'
    name: !empty(webServiceName) ? webServiceName : '${abbrs.webSitesAppService}web-${resourceToken}'
    serverFarmResourceId: appServicePlan.outputs.resourceId
    tags: union(tags, { 'azd-service-name': 'web' })
    location: location
    appInsightResourceId: appInsightResourceId
    siteConfig: {
      appCommandLine: './entrypoint.sh -o ./env-config.js && pm2 serve /home/site/wwwroot --no-daemon --spa'
      linuxFxVersion: 'node|18-lts'
      alwaysOn: true
    }
  }
}

module webAppSettings 'br/public:avm/res/web/site:0.2.0' = {
  scope: rg
  name: 'web-appsettings'
  params: {
    kind: 'app'
    name: web.outputs.name
    serverFarmResourceId: appServicePlan.outputs.resourceId
    tags: union(tags, { 'azd-service-name': 'web' })
    appSettingsKeyValuePairs: {
      REACT_APP_API_BASE_URL: 'https://${api.outputs.defaultHostname}'
      REACT_APP_APPLICATIONINSIGHTS_CONNECTION_STRING: monitoring.outputs.applicationInsightsConnectionString
    }
  }
}

// The application backend
module api 'br/public:avm/res/web/site:0.2.0' = {
  name: 'api'
  scope: rg
  params: {
    kind: 'app'
    name: !empty(apiServiceName) ? apiServiceName : '${abbrs.webSitesAppService}api-${resourceToken}'
    serverFarmResourceId: appServicePlan.outputs.resourceId
    location: location
    tags: union(tags, { 'azd-service-name': 'api' })
    appInsightResourceId: appInsightResourceId
    managedIdentities: {
      systemAssigned: true
    }
    appSettingsKeyValuePairs: {
      AZURE_KEY_VAULT_ENDPOINT: keyVault.outputs.uri
      AZURE_COSMOS_CONNECTION_STRING_KEY: cosmos.outputs.connectionStringKey
      AZURE_COSMOS_DATABASE_NAME: !empty(cosmosDatabaseName) ? cosmosDatabaseName: 'Todo'
      AZURE_COSMOS_ENDPOINT: cosmos.outputs.endpoint
      API_ALLOW_ORIGINS: 'https://${web.outputs.defaultHostname}'
      SCM_DO_BUILD_DURING_DEPLOYMENT: 'True'
      ENABLE_ORYX_BUILD: 'True'
      JAVA_OPTS: join(
        concat(
          [],
          ['-Djdk.attach.allowAttachSelf=true']),
          ' ')
      APPLICATIONINSIGHTS_CONNECTION_STRING: monitoring.outputs.applicationInsightsConnectionString
      applicationLogs: { fileSystem: { level: 'Verbose' } }
      detailedErrorMessages: { enabled: true }
      failedRequestsTracing: { enabled: true }
      httpLogs: { fileSystem: { enabled: true, retentionInDays: 1, retentionInMb: 35 } }
    }
    siteConfig: {
      cors: {
        allowedOrigins: [ 'https://portal.azure.com', 'https://ms.portal.azure.com' ,'https://${web.outputs.defaultHostname}' ]
      }
      linuxFxVersion: 'java|17-java17'
      alwaysOn: true
      appCommandLine: ''
      ftpsState: 'FtpsOnly'
      minTlsVersion: '1.2'
      use32BitWorkerProcess: false
      clientAffinityEnabled: false
    }
    basicPublishingCredentialsPolicies: [
      {
        name: 'ftp'
        allow: false
      }
      {
        name: 'scm'
        allow: false
      }
    ]
  }
}

// Give the API access to KeyVault
module apiKeyVaultAccess 'br/public:avm/res/key-vault/vault:0.3.5' = {
  name: 'api-keyvault-access'
  scope: rg
  params: {
    name: keyVault.outputs.name
    enableRbacAuthorization: false
    tags: tags
    accessPolicies: [
      {
        objectId: principalId
        permissions: {
          secrets: [ 'get', 'list' ]
        }
      }
      {
        objectId: api.outputs.systemAssignedMIPrincipalId
        permissions: {
          secrets: [ 'get', 'list' ]
        }
      }
    ]
  }
}

// The application database
module cosmos '../../../../../common/infra/bicep/app/cosmos-mongo-db.bicep' = {
  name: 'cosmos1'
  scope: rg
  params: {
    accountName: !empty(cosmosAccountName) ? cosmosAccountName : '${abbrs.documentDBDatabaseAccounts}${resourceToken}'
    databaseName: cosmosDatabaseName
    location: location
    tags: tags
    keyVaultName: keyVault.outputs.name
  }
}

// Create an App Service Plan to group applications under the same payment plan and SKU
module appServicePlan 'br/public:avm/res/web/serverfarm:0.1.0' = {
  name: 'appserviceplan'
  scope: rg
  params: {
    name: !empty(appServicePlanName) ? appServicePlanName : '${abbrs.webServerFarms}${resourceToken}'
    sku: {
      capacity: 1
      family: 'B'
      name: 'B1'
      size: 'B1'
      tier: 'Basic'
    }
    location: location
    reserved: true
    kind: 'Linux'
  }
}

// Store secrets in a keyvault
module keyVault 'br/public:avm/res/key-vault/vault:0.3.5' = {
  name: 'keyvault'
  scope: rg
  params: {
    name: !empty(keyVaultName) ? keyVaultName : '${abbrs.keyVaultVaults}${resourceToken}'
    location: location
    tags: tags
  }
}

// Monitor application with Azure Monitor
module monitoring '../../../../../../common/infra/bicep/core/monitor/monitoring.bicep' = {
  name: 'monitoring'
  scope: rg
  params: {
    location: location
    tags: tags
    logAnalyticsName: !empty(logAnalyticsName) ? logAnalyticsName : '${abbrs.operationalInsightsWorkspaces}${resourceToken}'
    applicationInsightsName: !empty(applicationInsightsName) ? applicationInsightsName : '${abbrs.insightsComponents}${resourceToken}'
    applicationInsightsDashboardName: !empty(applicationInsightsDashboardName) ? applicationInsightsDashboardName : '${abbrs.portalDashboards}${resourceToken}'
  }
}

// Creates Azure API Management (APIM) service to mediate the requests between the frontend and the backend API
module apim '../../../../../../common/infra/bicep/core/gateway/apim.bicep' = if (useAPIM) {
  name: 'apim-deployment'
  scope: rg
  params: {
    name: !empty(apimServiceName) ? apimServiceName : '${abbrs.apiManagementService}${resourceToken}'
    location: location
    tags: tags
    applicationInsightsName: monitoring.outputs.applicationInsightsName
  }
}

// Configures the API in the Azure API Management (APIM) service
module apimApi '../../../../../common/infra/bicep/app/apim-api.bicep' = if (useAPIM) {
  name: 'apim-api-deployment'
  scope: rg
  params: {
    name: useAPIM ? apim.outputs.apimServiceName : ''
    apiName: 'todo-api'
    apiDisplayName: 'Simple Todo API'
    apiDescription: 'This is a simple Todo API'
    apiPath: 'todo'
    webFrontendUrl: 'https://${web.outputs.defaultHostname}'
    apiBackendUrl: 'https://${api.outputs.defaultHostname}'
    apiAppName: api.outputs.name
  }
}

// Data outputs
output AZURE_COSMOS_CONNECTION_STRING_KEY string = cosmos.outputs.connectionStringKey
output AZURE_COSMOS_DATABASE_NAME string = cosmos.outputs.databaseName

// App outputs
output APPLICATIONINSIGHTS_CONNECTION_STRING string = monitoring.outputs.applicationInsightsConnectionString
output AZURE_KEY_VAULT_ENDPOINT string = keyVault.outputs.uri
output AZURE_KEY_VAULT_NAME string = keyVault.outputs.name
output AZURE_LOCATION string = location
output AZURE_TENANT_ID string = tenant().tenantId
output REACT_APP_API_BASE_URL string = useAPIM ? apimApi.outputs.SERVICE_API_URI : 'https://${api.outputs.defaultHostname}'
output REACT_APP_APPLICATIONINSIGHTS_CONNECTION_STRING string = monitoring.outputs.applicationInsightsConnectionString
output REACT_APP_WEB_BASE_URL string = 'https://${web.outputs.defaultHostname}'
output USE_APIM bool = useAPIM
output SERVICE_API_ENDPOINTS array = useAPIM ? [ apimApi.outputs.SERVICE_API_URI, 'https://${api.outputs.defaultHostname}' ]: []
