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

@description('API Management SKU to use if APIM is enabled')
param apimSku string = 'Consumption'

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
var defaultDatabaseName = 'Todo'
var actualDatabaseName = !empty(sqlDatabaseName) ? sqlDatabaseName : defaultDatabaseName
var webUri = 'https://${web.outputs.defaultHostname}'
var apiUri = 'https://${api.outputs.defaultHostname}'
var apimApiUri = 'https://${apim.outputs.name}.azure-api.net/todo'

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
    appInsightResourceId: applicationInsights.outputs.resourceId
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
    appInsightResourceId: applicationInsights.outputs.resourceId
    managedIdentities: {
      systemAssigned: true
    }
    siteConfig: {
      cors: {
        allowedOrigins: [ 'https://portal.azure.com', 'https://ms.portal.azure.com' , webUri ]
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
          value: 'Server=${sqlService.outputs.name}${environment().suffixes.sqlServerHostname}; Database=${actualDatabaseName}; User=${appUser}; Password=${appUserPassword}'
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
        name: actualDatabaseName
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

//Add appuser to database owner
module sqlDeploymentScript '../../../../../common/infra/bicep/app/sql-deployment-script.bicep' = {
  name: 'sqldeploymentscript'
  scope: rg
  params: {
    location: location
    appUserPassword: appUserPassword
    sqlAdminPassword: sqlAdminPassword
    sqlDatabaseName: actualDatabaseName
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
      name: 'B3'
      tier: 'Basic'
    }
    location: location
    tags: tags
    reserved: true
    kind: 'Linux'
  }
}

// Create a keyvault to store secrets
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
module logAnalytics 'br/public:avm/res/operational-insights/workspace:0.3.4' = {
  name: 'loganalytics'
  scope: rg
  params: {
    name: !empty(logAnalyticsName) ? logAnalyticsName : '${abbrs.operationalInsightsWorkspaces}${resourceToken}'
    location: location
  }
}

// Monitor application with Azure applicationInsights
module applicationInsights 'br/public:avm/res/insights/component:0.3.0' = {
  name: 'applicationinsights'
  scope: rg
  params: {
    name: !empty(applicationInsightsName) ? applicationInsightsName : '${abbrs.insightsComponents}${resourceToken}'
    workspaceResourceId: logAnalytics.outputs.resourceId
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
    applicationInsightsId: applicationInsights.outputs.resourceId
  }
}

// Creates Azure API Management (APIM) service to mediate the requests between the frontend and the backend API
module apim 'br/public:avm/res/api-management/service:0.1.7' = if (useAPIM) {
  name: 'apim-deployment'
  scope: rg
  params: {
    name: !empty(apimServiceName) ? apimServiceName : '${abbrs.apiManagementService}${resourceToken}'
    publisherEmail: 'noreply@microsoft.com'
    publisherName: 'n/a'
    location: location
    tags: tags
    sku: apimSku
    skuCount: 0
    apis: [
      {
        name: 'todo-api'
        path: 'todo'
        displayName: 'Simple Todo API'
        apiDescription: 'This is a simple Todo API'
        serviceUrl: apiUri
        subscriptionRequired: false
        protocols: [ 'https' ]
        type: 'http'
        value: loadTextContent('../../../../../api/common/openapi.yaml')
        policies: [
          {
            value: replace(loadTextContent('../../../../../../common/infra/shared/gateway/apim/apim-api-policy.xml'), '{origin}', webUri)
            format: 'rawxml'
          }
        ]
      }
    ]
  }
}

// Configures the API in the Azure API Management (APIM) service
module apimSettings '../../../../../common/infra/bicep/app/apim-api-settings.bicep' = if (useAPIM) {
  name: 'apim-api-settings'
  scope: rg
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
output API_BASE_URL string = useAPIM ? apimApiUri : apiUri
output REACT_APP_WEB_BASE_URL string = webUri
output USE_APIM bool = useAPIM
output SERVICE_API_ENDPOINTS array = useAPIM ? [ apimApiUri, apiUri ]: []
