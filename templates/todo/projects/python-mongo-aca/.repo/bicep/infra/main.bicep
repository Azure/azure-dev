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
param apiContainerAppName string = ''
param applicationInsightsDashboardName string = ''
param applicationInsightsName string = ''
param containerAppsEnvironmentName string = ''
param containerRegistryName string = ''
param cosmosAccountName string = ''
param keyVaultName string = ''
param logAnalyticsName string = ''
param resourceGroupName string = ''
param webContainerAppName string = ''
param apimServiceName string = ''

@description('Flag to use Azure API Management to mediate the calls between the Web frontend and the backend API')
param useAPIM bool = false

@description('API Management SKU to use if APIM is enabled')
param apimSku string = 'Consumption'

@description('Hostname suffix for container registry. Set when deploying to sovereign clouds')
param containerRegistryHostSuffix string = 'azurecr.io'

@description('Id of the user or app to assign application roles')
param principalId string = ''

var abbrs = loadJsonContent('../../../../../../common/infra/bicep/abbreviations.json')
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var tags = { 'azd-env-name': environmentName }
var apiContainerAppNameOrDefault = '${abbrs.appContainerApps}web-${resourceToken}'
var corsAcaUrl = 'https://${apiContainerAppNameOrDefault}.${containerAppsEnvironment.outputs.defaultDomain}'
var acrPullRole = subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '7f951dda-4ed3-4680-a7ca-43fe172d538d')
var webUri = 'https://${web.outputs.fqdn}'
var apiUri = 'https://${api.outputs.fqdn}'

// Organize resources in a resource group
resource rg 'Microsoft.Resources/resourceGroups@2021-04-01' = {
  name: !empty(resourceGroupName) ? resourceGroupName : '${abbrs.resourcesResourceGroups}${environmentName}'
  location: location
  tags: tags
}

// Container registry
module containerRegistry 'br/public:avm/res/container-registry/registry:0.1.1' = {
  name: 'registry'
  scope: rg
  params: {
    name: !empty(containerRegistryName) ? containerRegistryName : '${abbrs.containerRegistryRegistries}${resourceToken}'
    location: location
    acrAdminUserEnabled: true
    tags: tags
    publicNetworkAccess: 'Enabled'
    roleAssignments:[
      {
        principalId: webIdentity.outputs.principalId
        principalType: 'ServicePrincipal'
        roleDefinitionIdOrName: acrPullRole
      }
      {
        principalId: apiIdentity.outputs.principalId
        principalType: 'ServicePrincipal'
        roleDefinitionIdOrName: acrPullRole
      }
    ]
  }
}

//Container apps environment
module containerAppsEnvironment 'br/public:avm/res/app/managed-environment:0.4.5' = {
  name: 'container-apps-environment'
  scope: rg
  params: {
    logAnalyticsWorkspaceResourceId: monitoring.outputs.logAnalyticsWorkspaceResourceId
    name: !empty(containerAppsEnvironmentName) ? containerAppsEnvironmentName : '${abbrs.appManagedEnvironments}${resourceToken}'
    location: location
    zoneRedundant: false
    tags: tags
  }
}

//the managed identity for web frontend
module webIdentity 'br/public:avm/res/managed-identity/user-assigned-identity:0.2.1' = {
  name: 'webidentity'
  scope: rg
  params: {
    name: '${abbrs.managedIdentityUserAssignedIdentities}web-${resourceToken}'
    location: location
  }
}

// Web frontend
module web 'br/public:avm/res/app/container-app:0.2.0' = {
  name: 'web'
  scope: rg
  params: {
    name: !empty(webContainerAppName) ? webContainerAppName : '${abbrs.appContainerApps}web-${resourceToken}'
    containers: [
      {
        image: 'mcr.microsoft.com/azuredocs/containerapps-helloworld:latest'
        name: 'simple-hello-world-container'
        resources: {
          cpu: json('0.5')
          memory: '1.0Gi'
        }
      }
    ]
    managedIdentities:{
      systemAssigned: false
      userAssignedResourceIds: [webIdentity.outputs.resourceId]
    }
    registries:[
      {
        server: '${containerRegistry.outputs.name}.${containerRegistryHostSuffix}'
        identity: webIdentity.outputs.resourceId
      }
    ]
    dapr: {
        enabled: true
        appId: 'main'
        appProtocol: 'http'
        appPort: 80
    }
    environmentId: containerAppsEnvironment.outputs.resourceId
    location: location
    tags: union(tags, { 'azd-service-name': 'web' })
  }
}

//the managed identity for api backend
module apiIdentity 'br/public:avm/res/managed-identity/user-assigned-identity:0.2.1' = {
  name: 'apiidentity'
  scope: rg
  params: {
    name: '${abbrs.managedIdentityUserAssignedIdentities}api-${resourceToken}'
    location: location
  }
}

// Api backend
module api 'br/public:avm/res/app/container-app:0.2.0' = {
  name: 'api'
  scope: rg
  params: {
    name: !empty(apiContainerAppName) ? apiContainerAppName : '${abbrs.appContainerApps}api-${resourceToken}'
    containers: [
      {
        image: 'mcr.microsoft.com/azuredocs/containerapps-helloworld:latest'
        name: 'simple-hello-world-container'
        resources: {
          cpu: json('1.0')
          memory: '2.0Gi'
        }
        env: [
          {
            name: 'AZURE_CLIENT_ID'
            value: apiIdentity.outputs.clientId
          }
          {
            name: 'AZURE_KEY_VAULT_ENDPOINT'
            value: keyVault.outputs.uri
          }
          {
            name: 'APPLICATIONINSIGHTS_CONNECTION_STRING'
            value: monitoring.outputs.applicationInsightsConnectionString
          }
          {
            name: 'API_ALLOW_ORIGINS'
            value: corsAcaUrl
          }
        ]
      }
    ]
    managedIdentities:{
      systemAssigned: false
      userAssignedResourceIds: [apiIdentity.outputs.resourceId]
    }
    registries:[
      {
        server: '${containerRegistry.outputs.name}.${containerRegistryHostSuffix}'
        identity: apiIdentity.outputs.resourceId
      }
    ]
    environmentId: containerAppsEnvironment.outputs.resourceId
    ingressTargetPort: 3100
    location: location
    tags: union(tags, { 'azd-service-name': 'api' })
  }
}

// The application database
module cosmos '../../../../../common/infra/bicep/app/cosmos-mongo-db-avm.bicep' = {
  name: 'cosmos'
  scope: rg
  params: {
    accountName: !empty(cosmosAccountName) ? cosmosAccountName : '${abbrs.documentDBDatabaseAccounts}${resourceToken}'
    location: location
    tags: tags
    keyVaultResourceId: keyVault.outputs.resourceId
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
    enableVaultForDeployment: false
    enableVaultForTemplateDeployment: false
    enablePurgeProtection: false
    sku: 'standard'
    accessPolicies: [
      {
        objectId: principalId
        permissions: {
          secrets: [ 'get', 'list' ]
        }
      }
      {
        objectId: apiIdentity.outputs.principalId
        permissions: {
          secrets: [ 'get', 'list' ]
        }
      }
    ]
  }
}

// Monitor application with Azure Monitor
module monitoring 'br/public:avm/ptn/azd/monitoring:0.1.0' = {
  name: 'monitoringDeployment'
  scope: rg
  params: {
    applicationInsightsName: !empty(applicationInsightsName) ? applicationInsightsName : '${abbrs.insightsComponents}${resourceToken}'
    logAnalyticsName: !empty(logAnalyticsName) ? logAnalyticsName : '${abbrs.operationalInsightsWorkspaces}${resourceToken}'
    applicationInsightsDashboardName: !empty(applicationInsightsDashboardName) ? applicationInsightsDashboardName : '${abbrs.portalDashboards}${resourceToken}'
    location: location
    tags: tags
  }
}

// Creates Azure API Management (APIM) service to mediate the requests between the frontend and the backend API
module apim 'br/public:avm/res/api-management/service:0.2.0' = if (useAPIM) {
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
    zones: []
    customProperties: {}
    loggers: [
      {
        name: 'app-insights-logger'
        credentials: {
          instrumentationKey: monitoring.outputs.applicationInsightsInstrumentationKey
        }
        loggerDescription: 'Logger to Azure Application Insights'
        isBuffered: false
        loggerType: 'applicationInsights'
        targetResourceId: monitoring.outputs.applicationInsightsResourceId
      }
    ]
  }
}

//Configures the API settings for an api app within the Azure API Management (APIM) service.
module apimApi 'br/public:avm/ptn/azd/apim-api:0.1.0' = if (useAPIM) {
  name: 'apim-api-deployment'
  scope: rg
  params: {
    apiBackendUrl: apiUri
    apiDescription: 'This is a simple Todo API'
    apiDisplayName: 'Simple Todo API'
    apiName: 'todo-api'
    apiPath: 'todo'
    name: useAPIM ? apim.outputs.name : ''
    webFrontendUrl: webUri
    location: location
  }
}

// Data outputs
output AZURE_COSMOS_CONNECTION_STRING_KEY string = cosmos.outputs.connectionStringKey
output AZURE_COSMOS_DATABASE_NAME string = cosmos.outputs.databaseName

// App outputs
output API_CORS_ACA_URL string = corsAcaUrl
output APPLICATIONINSIGHTS_CONNECTION_STRING string = monitoring.outputs.applicationInsightsConnectionString
output APPLICATIONINSIGHTS_NAME string = monitoring.outputs.applicationInsightsName
output AZURE_CONTAINER_ENVIRONMENT_NAME string = containerAppsEnvironment.outputs.name
output AZURE_CONTAINER_REGISTRY_ENDPOINT string = containerRegistry.outputs.loginServer
output AZURE_CONTAINER_REGISTRY_NAME string = containerRegistry.outputs.name
output AZURE_KEY_VAULT_ENDPOINT string = keyVault.outputs.uri
output AZURE_KEY_VAULT_NAME string = keyVault.outputs.name
output AZURE_LOCATION string = location
output AZURE_TENANT_ID string = tenant().tenantId
output API_BASE_URL string = useAPIM ? apimApi.outputs.serviceApiUri : apiUri
output REACT_APP_WEB_BASE_URL string = webUri
output SERVICE_API_NAME string = api.outputs.name
output SERVICE_WEB_NAME string = web.outputs.name
output USE_APIM bool = useAPIM
output SERVICE_API_ENDPOINTS array = useAPIM ? [ apimApi.outputs.serviceApiUri, apiUri ] : []
