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
param storageAccountName string = ''
param webServiceName string = ''
param apimServiceName string = ''
param connectionStringKey string = 'AZURE-COSMOS-CONNECTION-STRING'
param collections array = [
  {
    name: 'TodoList'
    id: 'TodoList'
    shardKey: {keys: [
      'Hash'
    ]}
    indexes: [
      {
        key: {
          keys: [
            '_id'
          ]
        }
      }
    ]
  }
  {
    name: 'TodoItem'
    id: 'TodoItem'
    shardKey: {keys: [
      'Hash'
    ]}
    indexes: [
      {
        key: {
          keys: [
            '_id'
          ]
        }
      }
    ]
  }
]

@description('Flag to use Azure API Management to mediate the calls between the Web frontend and the backend API')
param useAPIM bool = false

@description('API Management SKU to use if APIM is enabled')
param apimSku string = 'Consumption'

@description('Id of the user or app to assign application roles')
param principalId string = ''

var abbrs = loadJsonContent('../../../../../../common/infra/bicep/abbreviations.json')
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var tags = { 'azd-env-name': environmentName }
var defaultDatabaseName = 'Todo'
var actualDatabaseName = !empty(cosmosDatabaseName) ? cosmosDatabaseName : defaultDatabaseName
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
module web 'br/public:avm/res/web/static-site:0.3.0' = {
  name: 'staticweb'
  scope: rg
  params: {
    name: !empty(webServiceName) ? webServiceName : '${abbrs.webStaticSites}web-${resourceToken}'
    location: location
    provider: 'Custom'
    tags: union(tags, { 'azd-service-name': 'web' })
  }
}

// The application backend
module api 'br/public:avm/res/web/site:0.3.5' = {
  name: 'api'
  scope: rg
  params: {
    kind: 'functionapp'
    name: !empty(apiServiceName) ? apiServiceName : '${abbrs.webSitesFunctions}api-${resourceToken}'
    serverFarmResourceId: appServicePlan.outputs.resourceId
    tags: union(tags, { 'azd-service-name': 'api' })
    location: location
    appInsightResourceId: applicationInsights.outputs.resourceId
    managedIdentities: {
      systemAssigned: true
    }
    clientAffinityEnabled: false
    siteConfig: {
      cors: {
        allowedOrigins: [ 'https://portal.azure.com', 'https://ms.portal.azure.com' , webUri ]
      }
      linuxFxVersion: 'python|3.10'
      use32BitWorkerProcess: false
    }
    appSettingsKeyValuePairs: {
      API_ALLOW_ORIGINS: webUri
      AZURE_COSMOS_CONNECTION_STRING_KEY: connectionStringKey
      AZURE_COSMOS_DATABASE_NAME: actualDatabaseName
      AZURE_KEY_VAULT_ENDPOINT:keyVault.outputs.uri
      AZURE_COSMOS_ENDPOINT: 'https://${cosmos.outputs.name}.documents.azure.com:443/'
      FUNCTIONS_EXTENSION_VERSION: '~4'
      FUNCTIONS_WORKER_RUNTIME: 'python'
      SCM_DO_BUILD_DURING_DEPLOYMENT: 'True'
      ENABLE_ORYX_BUILD: 'True'
    }
    storageAccountResourceId: storage.outputs.resourceId
  }
}

// Give the API access to KeyVault
module accessKeyVault 'br/public:avm/res/key-vault/vault:0.5.1' = {
  name: 'accesskeyvault'
  scope: rg
  params: {
    name: keyVault.outputs.name
    enableRbacAuthorization: false
    accessPolicies: [
      {
        objectId: api.outputs.systemAssignedMIPrincipalId
        permissions: {
          secrets: [ 'get', 'list' ]
        }
      }
      {
        objectId: principalId
        permissions: {
          secrets: [ 'get', 'list' ]
        }
      }
    ]
  }
}

// The application database
module cosmos 'br/public:avm/res/document-db/database-account:0.4.0' = {
  name: 'cosmos'
  scope: rg
  params: {
    locations: [
      {
        failoverPriority: 0
        isZoneRedundant: false
        locationName: location
      }
    ]
    name: !empty(cosmosAccountName) ? cosmosAccountName : '${abbrs.documentDBDatabaseAccounts}${resourceToken}'
    location: location
    mongodbDatabases: [
      {
        name: actualDatabaseName
        tags: tags
        collections: collections
      }
    ]
    secretsKeyVault: {
      keyVaultName: keyVault.outputs.name
      primaryWriteConnectionStringSecretName: connectionStringKey
    }
  }
}

// Create an App Service Plan to group applications under the same payment plan and SKU
module appServicePlan 'br/public:avm/res/web/serverfarm:0.1.1' = {
  name: 'appserviceplan'
  scope: rg
  params: {
    name: !empty(appServicePlanName) ? appServicePlanName : '${abbrs.webServerFarms}${resourceToken}'
    sku: {
      name: 'Y1'
      tier: 'Dynamic'
    }
    location: location
    tags: tags
    reserved: true
    kind: 'Linux'
  }
}

// Backing storage for Azure functions backend API
module storage 'br/public:avm/res/storage/storage-account:0.8.3' = {
  name: 'storage'
  scope: rg
  params: {
    name: !empty(storageAccountName) ? storageAccountName : '${abbrs.storageStorageAccounts}${resourceToken}'
    allowBlobPublicAccess: true
    dnsEndpointType: 'Standard'
    publicNetworkAccess:'Enabled'
    networkAcls:{
      bypass: 'AzureServices'
      defaultAction: 'Allow'
    }
    location: location
    tags: tags
  }
}

// Create a keyvault to store secrets
module keyVault 'br/public:avm/res/key-vault/vault:0.5.1' = {
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
    apiName: 'todo-api'
    name: useAPIM ? apim.outputs.name : ''
    apiPath: 'todo'
    applicationInsightsName: applicationInsights.outputs.name
  }
}

// Data outputs
output AZURE_COSMOS_CONNECTION_STRING_KEY string = connectionStringKey
output AZURE_COSMOS_DATABASE_NAME string = actualDatabaseName

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
