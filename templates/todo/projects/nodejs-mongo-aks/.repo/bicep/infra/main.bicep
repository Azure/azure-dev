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
@description('The resource name of the AKS cluster')
param clusterName string = ''

@description('The resource name of the Container Registry (ACR)')
param containerRegistryName string = ''

param applicationInsightsDashboardName string = ''
param applicationInsightsName string = ''
param cosmosAccountName string = ''
param cosmosDatabaseName string = ''
param keyVaultName string = ''
param logAnalyticsName string = ''
param resourceGroupName string = ''
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

@description('Id of the user or app to assign application roles')
param principalId string = ''

@allowed([
  'CostOptimised'
  'Standard'
  'HighSpec'
  'Custom'
])
@description('The System Pool Preset sizing')
param systemPoolType string = 'CostOptimised'

var abbrs = loadJsonContent('../../../../../../common/infra/bicep/abbreviations.json')
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var tags = { 'azd-env-name': environmentName }
var defaultDatabaseName = 'Todo'
var actualDatabaseName = !empty(cosmosDatabaseName) ? cosmosDatabaseName : defaultDatabaseName
var acrPullRole = subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '7f951dda-4ed3-4680-a7ca-43fe172d538d')
var aksClusterAdminRole = subscriptionResourceId('Microsoft.Authorization/roleDefinitions', 'b1ff04bb-8a4e-4dc4-8eb5-8693973ce19b')
var systemPoolSpec = nodePoolPresets[systemPoolType]
var nodePoolPresets = {
  CostOptimised: {
    vmSize: 'Standard_B4ms'
    count: 1
    minCount: 1
    maxCount: 3
    enableAutoScaling: true
    availabilityZones: []
  }
  Standard: {
    vmSize: 'Standard_DS2_v2'
    count: 3
    minCount: 3
    maxCount: 5
    enableAutoScaling: true
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  HighSpec: {
    vmSize: 'Standard_D4s_v3'
    count: 3
    minCount: 3
    maxCount: 5
    enableAutoScaling: true
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
}
var nodePoolBase = {
  osType: 'Linux'
  maxPods: 30
  type: 'VirtualMachineScaleSets'
  upgradeSettings: {
    maxSurge: '33%'
  }
}

// Organize resources in a resource group
resource rg 'Microsoft.Resources/resourceGroups@2021-04-01' = {
  name: !empty(resourceGroupName) ? resourceGroupName : '${abbrs.resourcesResourceGroups}${environmentName}'
  location: location
  tags: tags
}

// The AKS cluster to host applications
module managedCluster 'br/public:avm/res/container-service/managed-cluster:0.1.7' = {
  name: 'managed-cluster'
  scope: rg
  params: {
    name: !empty(clusterName) ? clusterName : '${abbrs.containerServiceManagedClusters}${resourceToken}'
    primaryAgentPoolProfile: [
      union(
        { name: 'npsystem', mode: 'System' },
        nodePoolBase,
        systemPoolSpec
      )
    ]
    skuTier: 'Free'
    kubernetesVersion: '1.27.9'
    location: location
    networkPlugin: 'azure'
    networkPolicy: 'azure'
    publicNetworkAccess: 'Enabled'
    webApplicationRoutingEnabled: true
    enableKeyvaultSecretsProvider: true
    roleAssignments: [
      {
        principalId: principalId
        roleDefinitionIdOrName: aksClusterAdminRole
      }
    ]
    monitoringWorkspaceId: logAnalytics.outputs.resourceId
    diagnosticSettings: [
      {
        workspaceResourceId: logAnalytics.outputs.resourceId
        logCategoriesAndGroups: [
          {
            category: 'cluster-autoscaler'
            enabled: true
          }
          {
            category: 'kube-controller-manager'
            enabled: true
          }
          {
            category: 'kube-audit-admin'
            enabled: true
          }
          {
            category: 'guard'
            enabled: true
          }
        ]
        metricCategories: [
          {
            category: 'AllMetrics'
          }
        ]
      }
    ]
    agentPools: [
      {
        name: 'npuserpool'
        mode: 'User'
        osType: 'Linux'
        maxPods: 30
        type: 'VirtualMachineScaleSets'
        maxSurge: '33%'
        vmSize: 'standard_a2'
      }
    ]
    managedIdentities: {
      systemAssigned: true
    }
  }
}

//Azure Container Registries (ACR)
module containerRegistry 'br/public:avm/res/container-registry/registry:0.1.1' = {
  name: 'container-registry'
  scope: rg
  params: {
    name: !empty(containerRegistryName) ? containerRegistryName : '${abbrs.containerRegistryRegistries}${resourceToken}'
    acrSku: 'Basic'
    publicNetworkAccess: 'Enabled'
    location: location
    diagnosticSettings: [
      {
        workspaceResourceId: logAnalytics.outputs.resourceId
        logCategoriesAndGroups: [
          {
            category: 'ContainerRegistryRepositoryEvents'
            enabled: true
          }
          {
            category: 'ContainerRegistryLoginEvents'
            enabled: true
          }
        ]
        metricCategories: [
          {
            category: 'AllMetrics'
            enabled: true
          }
        ]
      }
    ]
    roleAssignments: [
      {
        principalId: managedCluster.outputs.kubeletIdentityObjectId
        roleDefinitionIdOrName: acrPullRole
        principalType: 'ServicePrincipal'
      }
    ]
  }
}

// The application database
module cosmos 'br/public:avm/res/document-db/database-account:0.5.1' = {
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

// Create a keyvault to store secrets
module keyVault 'br/public:avm/res/key-vault/vault:0.3.5' = {
  name: 'keyvault'
  scope: rg
  params: {
    name: !empty(keyVaultName) ? keyVaultName : '${abbrs.keyVaultVaults}${resourceToken}'
    location: location
    tags: tags
    enableRbacAuthorization: false
    accessPolicies: [
      {
        objectId: managedCluster.outputs.kubeletIdentityObjectId
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
module applicationInsights 'br/public:avm/res/insights/component:0.3.1' = {
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

// Data outputs
output AZURE_COSMOS_CONNECTION_STRING_KEY string = connectionStringKey
output AZURE_COSMOS_DATABASE_NAME string = actualDatabaseName

// App outputs
output APPLICATIONINSIGHTS_CONNECTION_STRING string = applicationInsights.outputs.connectionString
output AZURE_KEY_VAULT_ENDPOINT string = keyVault.outputs.uri
output AZURE_KEY_VAULT_NAME string = keyVault.outputs.name
output AZURE_LOCATION string = location
output AZURE_TENANT_ID string = tenant().tenantId
output AZURE_AKS_CLUSTER_NAME string = managedCluster.outputs.name
output AZURE_AKS_IDENTITY_CLIENT_ID string = managedCluster.outputs.kubeletIdentityClientId
output AZURE_CONTAINER_REGISTRY_ENDPOINT string = containerRegistry.outputs.loginServer
output AZURE_CONTAINER_REGISTRY_NAME string = containerRegistry.outputs.name
