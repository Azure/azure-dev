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
param keyVaultName string = ''
param logAnalyticsName string = ''
param resourceGroupName string = ''
param sqlServerName string = ''
param sqlDatabaseName string = ''
param webServiceName string = ''
param apimServiceName string = ''
param connectionStringKey string = 'AZURE-SQL-CONNECTION-STRING'

@description('Flag to use Azure API Management to mediate the calls between the Web frontend and the backend API')
param useAPIM bool = false

@description('Id of the user or app to assign application roles')
param principalId string = ''

@secure()
@description('SQL Server administrator password')
param sqlAdminPassword string

@secure()
@description('Application user password')
param appUserPassword string
param appUser string = 'appUser'
param sqlAdmin string = 'sqlAdmin'
var abbrs = loadJsonContent('../../../../../../common/infra/bicep/abbreviations.json')
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var tags = { 'azd-env-name': environmentName }
var appInsightResourceId = resourceId(subscription().subscriptionId, rg.name,
'Microsoft.Insights/components', applicationInsights.outputs.name)

// Organize resources in a resource group
resource rg 'Microsoft.Resources/resourceGroups@2021-04-01' = {
  name: !empty(resourceGroupName) ? resourceGroupName : '${abbrs.resourcesResourceGroups}${environmentName}'
  location: location
  tags: tags
}

// The application frontend
module web 'br/public:avm/res/web/site:0.3.2' = {
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
      appCommandLine: 'pm2 serve /home/site/wwwroot --no-daemon --spa'
      linuxFxVersion: 'node|20-lts'
      alwaysOn: true
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
    tags: union(tags, { 'azd-service-name': 'api' })
    location: location
    appInsightResourceId: appInsightResourceId
    managedIdentities: {
      systemAssigned: true
    }
    siteConfig: {
      cors: {
        allowedOrigins: [ 'https://portal.azure.com', 'https://ms.portal.azure.com' ,'https://${web.outputs.defaultHostname}' ]
      }
      linuxFxVersion: 'dotnetcore|8.0'
      alwaysOn: true
      appCommandLine: ''
    }
    appSettingsKeyValuePairs: {
      AZURE_SQL_CONNECTION_STRING_KEY: connectionStringKey
      AZURE_KEY_VAULT_ENDPOINT: keyVault.outputs.uri
      SCM_DO_BUILD_DURING_DEPLOYMENT: 'False'
      ENABLE_ORYX_BUILD: 'True'
    }
  }
}

// Give the API access to KeyVault
module accessKeyVault 'br/public:avm/res/key-vault/vault:0.3.5' = {
  name: 'accesskeyvault'
  scope: rg
  params: {
    name: keyVault.outputs.name
    enableRbacAuthorization: false
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
    secrets:{
      secureList: [
        {
          name: 'sqlAdmin'
          value: sqlAdminPassword
        }
        {
          name: 'appUser'
          value: appUserPassword 
        }
        {
          name: connectionStringKey
          value: 'Server=${sqlService.outputs.name}${environment().suffixes.sqlServerHostname}; Database=${!empty(sqlDatabaseName) ? sqlDatabaseName : 'Todo'}; User=${appUser}; Password=${appUserPassword}'
        }
      ]
    }
  }
}

// The application database
module sqlService 'br/public:avm/res/sql/server:0.2.0' = {
  name: 'sqlservice'
  scope: rg
  params: {
    name: !empty(sqlServerName) ? sqlServerName : '${abbrs.sqlServers}${resourceToken}'
    administratorLogin: sqlAdmin
    administratorLoginPassword: sqlAdminPassword
    location: location
    tags: tags
    publicNetworkAccess: 'Enabled'
    databases: [
      {
        name: !empty(sqlDatabaseName) ? sqlDatabaseName : 'Todo'
      }
    ]
    firewallRules:[
      {
        name: 'Azure Services'
        startIpAddress: '0.0.0.1'
        endIpAddress: '255.255.255.254'
      }
    ]
  }
}

module sqldeploymentscript '../../../../../common/infra/bicep/app/sql-deployment-script.bicep' = {
  scope: rg
  name: 'sqldeploymentscript'
  params: {
    location: location
    appUserPassword: appUserPassword
    sqlAdminPassword: sqlAdminPassword
    sqlDatabaseName: !empty(sqlDatabaseName) ? sqlDatabaseName : 'Todo'
    sqlServiceName: sqlService.outputs.name
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
    tags: tags
    reserved: true
    kind: 'Linux'
  }
}

// // Store secrets in a keyvault
module keyVault 'br/public:avm/res/key-vault/vault:0.3.5' = {
  name: 'keyvault'
  scope: rg
  params: {
    name: !empty(keyVaultName) ? keyVaultName : '${abbrs.keyVaultVaults}${resourceToken}'
    location: location
    tags: tags
    enableRbacAuthorization: false
  }
}

// Monitor application with Azure loganalytics
module loganalytics 'br/public:avm/res/operational-insights/workspace:0.3.4' = {
  name: 'loganalytics'
  scope: rg
  params: {
    name: !empty(logAnalyticsName) ? logAnalyticsName : '${abbrs.operationalInsightsWorkspaces}${resourceToken}'
    location: location
  }
}

// Monitor application with Azure applicationInsights
module applicationInsights 'br/public:avm/res/insights/component:0.3.0' = {
  name: 'applicationInsights'
  scope: rg
  params: {
    name: !empty(applicationInsightsName) ? applicationInsightsName : '${abbrs.insightsComponents}${resourceToken}'
    workspaceResourceId: loganalytics.outputs.resourceId
    location: location
  }
}

// Monitor application with Azure applicationInsightsDashboard
module applicationInsightsDashboard '../../../../../common/infra/bicep/app/applicationinsights-dashboard.bicep' = {
  name: 'application-insights-dashboard'
  scope: rg
  params: {
    name: !empty(applicationInsightsDashboardName) ? applicationInsightsDashboardName : '${abbrs.portalDashboards}${resourceToken}'
    location: location
    applicationInsightsName: applicationInsights.outputs.name
  }
}

// Creates Azure API Management (APIM) service to mediate the requests between the frontend and the backend API
module apim 'br/public:avm/res/api-management/service:0.1.3' = if (useAPIM) {
  name: 'apim-deployment'
  scope: rg
  params: {
    name: !empty(apimServiceName) ? apimServiceName : '${abbrs.apiManagementService}${resourceToken}'
    publisherEmail: 'noreply@microsoft.com'
    publisherName: 'n/a'
    location: location
    tags: tags
    apis: [
      {
        name: 'todo-api'
        path: 'todo'
        displayName: 'Simple Todo API'
        apiDescription: 'This is a simple Todo API'
        serviceUrl: 'https://${api.outputs.defaultHostname}'
        subscriptionRequired: false
        value: loadTextContent('../../../../../api/common/openapi.yaml')
        policies: [
          {
            value: replace(loadTextContent('../../../../../../common/infra/shared/gateway/apim/apim-api-policy.xml'), '{origin}', 'https://${web.outputs.defaultHostname}')
            format: 'rawxml'
          }
        ]
      }
    ]
  }
}

// Configures the API in the Azure API Management (APIM) service
module apimsettings '../../../../../common/infra/bicep/app/apim-api-settings.bicep' = if (useAPIM) {
  scope: rg
  name: 'apim-api-settings'
  params: {
    apiAppName: api.outputs.name
    apiName: 'todo-api'
    name: useAPIM ? apim.outputs.name : ''
    apiPath: 'todo'
    applicationInsightsName: applicationInsights.outputs.name
  }
}

// Data outputs
output AZURE_SQL_CONNECTION_STRING_KEY string = connectionStringKey

// App outputs
output APPLICATIONINSIGHTS_CONNECTION_STRING string = applicationInsights.outputs.connectionString
output AZURE_KEY_VAULT_ENDPOINT string = keyVault.outputs.uri
output AZURE_KEY_VAULT_NAME string = keyVault.outputs.name
output AZURE_LOCATION string = location
output AZURE_TENANT_ID string = tenant().tenantId
output API_BASE_URL string = useAPIM ? 'https://${apim.outputs.name}.azure-api.net/todo' : 'https://${api.outputs.defaultHostname}'
output REACT_APP_WEB_BASE_URL string = 'https://${web.outputs.defaultHostname}'
output USE_APIM bool = useAPIM
output SERVICE_API_ENDPOINTS array = useAPIM ? [ 'https://${apim.outputs.name}.azure-api.net/todo', 'https://${api.outputs.defaultHostname}' ]: []
