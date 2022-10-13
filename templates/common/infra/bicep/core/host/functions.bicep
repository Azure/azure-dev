param environmentName string
param location string = resourceGroup().location

// AppService Settings
param allowedOrigins array = []
param alwaysOn bool = false
param applicationInsightsName string = ''
param appServicePlanId string
param appSettings object = {}
param kind string = 'functionapp,linux'
param keyVaultName string = ''
param managedIdentity bool = !(empty(keyVaultName))
param remoteBuild bool = true
param serviceName string

// Function Settings
param clientAffinityEnabled bool = false
@allowed([
  '~4', '~3', '~2', '~1'
])
param extensionVersion string = '~4'
param functionAppScaleLimit int = 200
param minimumElasticInstanceCount int = 0
param numberOfWorkers int = 1
@allowed([
  'dotnet', 'dotnet-isolated', 'node', 'python', 'java', 'powershell', 'custom'
])
param runtimeName string
param runtimeNameAndVersion string = '${runtimeName}|${runtimeVersion}'
param runtimeVersion string
param storageAccountName string
param use32BitWorkerProcess bool = false

module functions 'appservice.bicep' = {
  name: '${serviceName}-functions'
  params: {
    environmentName: environmentName
    location: location
    allowedOrigins: allowedOrigins
    alwaysOn: alwaysOn
    applicationInsightsName: applicationInsightsName
    appServicePlanId: appServicePlanId
    appSettings: union(appSettings, {
        AzureWebJobsStorage: 'DefaultEndpointsProtocol=https;AccountName=${storage.name};AccountKey=${storage.listKeys().keys[0].value};EndpointSuffix=${environment().suffixes.storage}'
        FUNCTIONS_EXTENSION_VERSION: extensionVersion
        FUNCTIONS_WORKER_RUNTIME: runtimeName
      })
    clientAffinityEnabled: clientAffinityEnabled
    functionAppScaleLimit: functionAppScaleLimit
    keyVaultName: keyVaultName
    kind: kind
    linuxFxVersion: runtimeNameAndVersion
    managedIdentity: managedIdentity
    minimumElasticInstanceCount: minimumElasticInstanceCount
    numberOfWorkers: numberOfWorkers
    remoteBuild: remoteBuild
    serviceName: serviceName
    use32BitWorkerProcess: use32BitWorkerProcess
  }
}

resource storage 'Microsoft.Storage/storageAccounts@2021-09-01' existing = {
  name: storageAccountName
}

output identityPrincipalId string = managedIdentity ? functions.outputs.identityPrincipalId : ''
output name string = functions.outputs.name
output uri string = functions.outputs.uri
