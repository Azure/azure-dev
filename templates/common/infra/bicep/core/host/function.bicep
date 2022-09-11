param environmentName string
param location string = resourceGroup().location
param serviceName string
param kind string = 'functionapp,linux'
param linuxFxVersion string = ''
param scmDoBuildDuringDeployment bool = true
param appSettings object = {}
param keyVaultName string = ''
param useKeyVault bool = !(empty(keyVaultName))
param managedIdentity bool = useKeyVault
param applicationInsightsName string
param appServicePlanId string
param storageAccountName string
param functionsWorkerRuntime string
param functionsExtensionVersion string = '~4'
param allowedOrigins array = []
param numberOfWorkers int = 1
param alwaysOn bool = false
param minimumElasticInstanceCount int = 0
param use32BitWorkerProcess bool = false
param clientAffinityEnabled bool = false
param functionAppScaleLimit int = 200

resource storage 'Microsoft.Storage/storageAccounts@2021-09-01' existing = {
  name: storageAccountName
}

module function 'appservice.bicep' = {
  name: 'function-resources-${serviceName}'
  params: {
    environmentName: environmentName
    location: location
    serviceName: serviceName
    kind: kind
    linuxFxVersion: linuxFxVersion
    scmDoBuildDuringDeployment: scmDoBuildDuringDeployment
    keyVaultName: keyVaultName
    managedIdentity: managedIdentity
    applicationInsightsName: applicationInsightsName
    appServicePlanId: appServicePlanId
    numberOfWorkers: numberOfWorkers
    alwaysOn: alwaysOn
    allowedOrigins: allowedOrigins
    functionAppScaleLimit: functionAppScaleLimit
    minimumElasticInstanceCount: minimumElasticInstanceCount
    use32BitWorkerProcess: use32BitWorkerProcess
    clientAffinityEnabled: clientAffinityEnabled
    appSettings: union(appSettings, {
        AzureWebJobsStorage: 'DefaultEndpointsProtocol=https;AccountName=${storage.name};AccountKey=${storage.listKeys().keys[0].value};EndpointSuffix=${environment().suffixes.storage}'
        FUNCTIONS_EXTENSION_VERSION: functionsExtensionVersion
        FUNCTIONS_WORKER_RUNTIME: functionsWorkerRuntime
      })
  }
}

output NAME string = function.outputs.NAME
output URI string = function.outputs.URI
output IDENTITY_PRINCIPAL_ID string = managedIdentity ? function.outputs.IDENTITY_PRINCIPAL_ID : ''
