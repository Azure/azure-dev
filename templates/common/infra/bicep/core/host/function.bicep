param environmentName string
param location string = resourceGroup().location

param allowedOrigins array = []
param alwaysOn bool = false
param applicationInsightsName string
param appServicePlanId string
param appSettings object = {}
param clientAffinityEnabled bool = false
param functionAppScaleLimit int = 200
param functionsExtensionVersion string = '~4'
param functionsWorkerRuntime string
param kind string = 'functionapp,linux'
param linuxFxVersion string = ''
param keyVaultName string = ''
param managedIdentity bool = !(empty(keyVaultName))
param minimumElasticInstanceCount int = 0
param numberOfWorkers int = 1
param scmDoBuildDuringDeployment bool = true
param serviceName string
param storageAccountName string
param use32BitWorkerProcess bool = false

resource storage 'Microsoft.Storage/storageAccounts@2021-09-01' existing = {
  name: storageAccountName
}

module function 'appservice.bicep' = {
  name: 'function-resources-${serviceName}'
  params: {
    environmentName: environmentName
    location: location
    allowedOrigins: allowedOrigins
    alwaysOn: alwaysOn
    applicationInsightsName: applicationInsightsName
    appServicePlanId: appServicePlanId
    appSettings: union(appSettings, {
        AzureWebJobsStorage: 'DefaultEndpointsProtocol=https;AccountName=${storage.name};AccountKey=${storage.listKeys().keys[0].value};EndpointSuffix=${environment().suffixes.storage}'
        FUNCTIONS_EXTENSION_VERSION: functionsExtensionVersion
        FUNCTIONS_WORKER_RUNTIME: functionsWorkerRuntime
      })
    clientAffinityEnabled: clientAffinityEnabled
    functionAppScaleLimit: functionAppScaleLimit
    keyVaultName: keyVaultName
    kind: kind
    linuxFxVersion: linuxFxVersion
    managedIdentity: managedIdentity
    minimumElasticInstanceCount: minimumElasticInstanceCount
    numberOfWorkers: numberOfWorkers
    scmDoBuildDuringDeployment: scmDoBuildDuringDeployment
    serviceName: serviceName
    use32BitWorkerProcess: use32BitWorkerProcess
  }
}

output identityPrincipalId string = managedIdentity ? function.outputs.identityPrincipalId : ''
output name string = function.outputs.name
output uri string = function.outputs.uri
