@description('The location used for all deployed resources')
param location string = resourceGroup().location

@description('Tags that will be applied to all resources')
param tags object = {}

@description('The configuration for the LlamaIndex application')
param llamaIndexConfig object = {}


var principalType = isContinuousIntegration ? 'ServicePrincipal' : 'User'

param isContinuousIntegration bool
param llamaIndexJavascriptExists bool

@description('Id of the user or app to assign application roles')
param principalId string

var abbrs = loadJsonContent('./abbreviations.json')
var resourceToken = uniqueString(subscription().id, resourceGroup().id, location)

// Monitor application with Azure Monitor
module monitoring 'br/public:avm/ptn/azd/monitoring:0.1.0' = {
  name: 'monitoring'
  params: {
    logAnalyticsName: '${abbrs.operationalInsightsWorkspaces}${resourceToken}'
    applicationInsightsName: '${abbrs.insightsComponents}${resourceToken}'
    applicationInsightsDashboardName: '${abbrs.portalDashboards}${resourceToken}'
    location: location
    tags: tags
  }
}
// Container registry
module containerRegistry 'br/public:avm/res/container-registry/registry:0.1.1' = {
  name: 'registry'
  params: {
    name: '${abbrs.containerRegistryRegistries}${resourceToken}'
    location: location
    tags: tags
    publicNetworkAccess: 'Enabled'
    roleAssignments:[
      {
        principalId: llamaIndexJavascriptIdentity.outputs.principalId
        principalType: 'ServicePrincipal'
        roleDefinitionIdOrName: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '7f951dda-4ed3-4680-a7ca-43fe172d538d')
      }
    ]
  }
}

// Container apps environment
module containerAppsEnvironment 'br/public:avm/res/app/managed-environment:0.4.5' = {
  name: 'container-apps-environment'
  params: {
    logAnalyticsWorkspaceResourceId: monitoring.outputs.logAnalyticsWorkspaceResourceId
    name: '${abbrs.appManagedEnvironments}${resourceToken}'
    location: location
    zoneRedundant: false
  }
}

module llamaIndexJavascriptIdentity 'br/public:avm/res/managed-identity/user-assigned-identity:0.2.1' = {
  name: 'llamaIndexJavascriptidentity'
  params: {
    name: '${abbrs.managedIdentityUserAssignedIdentities}llamaIndexJavascript-${resourceToken}'
    location: location
  }
}
module llamaIndexJavascriptFetchLatestImage './modules/fetch-container-image.bicep' = {
  name: 'llamaIndexJavascript-fetch-image'
  params: {
    exists: llamaIndexJavascriptExists
    name: 'llama-index-javascript'
  }
}

module llamaIndexJavascript 'br/public:avm/res/app/container-app:0.8.0' = {
  name: 'llamaIndexJavascript'
  params: {
    name: 'llama-index-javascript'
    ingressTargetPort: 3000
    scaleMinReplicas: 1
    scaleMaxReplicas: 10
    secrets: {
      secureList:  [
      ]
    }
    containers: [
      {
        image: llamaIndexJavascriptFetchLatestImage.outputs.?containers[?0].?image ?? 'mcr.microsoft.com/azuredocs/containerapps-helloworld:latest'
        name: 'main'
        resources: {
          cpu: json('0.5')
          memory: '1.0Gi'
        }
        env: [
          {
            name: 'APPLICATIONINSIGHTS_CONNECTION_STRING'
            value: monitoring.outputs.applicationInsightsConnectionString
          }
          {
            name: 'AZURE_CLIENT_ID'
            value: llamaIndexJavascriptIdentity.outputs.clientId
          }
          {
            name: 'PORT'
            value: '3000'
          }
          {
            name: 'AZURE_OPENAI_ENDPOINT' 
            value: openAi.outputs.endpoint
          }
          {
            name: 'AZURE_DEPLOYMENT_NAME' 
            value: llamaIndexConfig.chat.deployment
          }
          {
            name: 'AZURE_OPENAI_API_VERSION' 
            value: llamaIndexConfig.openai_api_version
          }
          {
            name: 'MODEL_PROVIDER' 
            value: llamaIndexConfig.model_provider
          }
          {
            name: 'MODEL' 
            value: llamaIndexConfig.chat.model
          }
          {
            name: 'EMBEDDING_MODEL' 
            value: llamaIndexConfig.embedding.model
          }
          {
            name: 'EMBEDDING_DIM' 
            value: llamaIndexConfig.embedding.dim
          }
          {
            name: 'LLM_TEMPERATURE' 
            value: llamaIndexConfig.llm_temperature
          }
          {
            name: 'LLM_MAX_TOKENS' 
            value: llamaIndexConfig.llm_max_tokens
          }
          {
            name: 'TOP_K' 
            value: llamaIndexConfig.top_k
          }
          {
            name: 'FILESERVER_URL_PREFIX' 
            value: llamaIndexConfig.fileserver_url_prefix
          }
          {
            name: 'SYSTEM_PROMPT' 
            value: llamaIndexConfig.system_prompt
          }
          {
            name: 'OPENAI_API_TYPE'
            value: 'AzureOpenAI'
          }
          {
            name: 'STORAGE_CACHE_DIR'
            value: './cache'
          }
        ]
      }
    ]
    managedIdentities:{
      systemAssigned: false
      userAssignedResourceIds: [llamaIndexJavascriptIdentity.outputs.resourceId]
    }
    registries:[
      {
        server: containerRegistry.outputs.loginServer
        identity: llamaIndexJavascriptIdentity.outputs.resourceId
      }
    ]
    environmentResourceId: containerAppsEnvironment.outputs.resourceId
    location: location
    tags: union(tags, { 'azd-service-name': 'llama-index-javascript' })
  }
}

module openAi 'br/public:avm/res/cognitive-services/account:0.10.2' =  {
  name: 'openai'
  params: {
    name: '${abbrs.cognitiveServicesAccounts}${resourceToken}'
    tags: tags
    location: location
    kind: 'OpenAI'
    disableLocalAuth: true
    customSubDomainName: '${abbrs.cognitiveServicesAccounts}${resourceToken}'
    publicNetworkAccess: 'Enabled'
    deployments: [
      {
        name: llamaIndexConfig.chat.deployment
        model: {
          format: 'OpenAI'
          name: llamaIndexConfig.chat.model
          version: llamaIndexConfig.chat.version
        }
        sku: {
          capacity: llamaIndexConfig.chat.capacity
          name: 'GlobalStandard'
        }
      }
      {
        name: llamaIndexConfig.embedding.deployment
        model: {
          format: 'OpenAI'
          name: llamaIndexConfig.embedding.model
          version: llamaIndexConfig.embedding.version
        }
        sku: {
          capacity: llamaIndexConfig.embedding.capacity
          name: 'Standard'
        }
      }
    ]
    roleAssignments: [
      {
        principalId: principalId
        principalType: principalType
        roleDefinitionIdOrName: 'Cognitive Services OpenAI User'
      }
      {
        principalId: llamaIndexJavascriptIdentity.outputs.principalId
        principalType: 'ServicePrincipal'
        roleDefinitionIdOrName: 'Cognitive Services OpenAI User'
      }
    ]
  }
}

output AZURE_CONTAINER_REGISTRY_ENDPOINT string = containerRegistry.outputs.loginServer
output AZURE_RESOURCE_LLAMA_INDEX_JAVASCRIPT_ID string = llamaIndexJavascript.outputs.resourceId
output AZURE_OPENAI_ENDPOINT string = openAi.outputs.endpoint
