param environmentName string
param location string = resourceGroup().location
param serviceName string
param linuxFxVersion string = 'PYTHON|3.8'
param appSettings object = {}
param keyVaultName string = ''
param useKeyVault bool = !(empty(keyVaultName))
param managedIdentity bool = useKeyVault
param applicationInsightsName string
param appServicePlanId string
param storageAccountName string
param functionsWorkerRuntime string = 'python'
param allowedOrigins array = []

module function 'function.bicep' = {
  name: 'function-python-resources-${serviceName}'
  params: {
    environmentName: environmentName
    location: location
    serviceName: serviceName
    allowedOrigins: allowedOrigins
    linuxFxVersion: linuxFxVersion
    functionsWorkerRuntime: functionsWorkerRuntime
    storageAccountName: storageAccountName
    applicationInsightsName: applicationInsightsName
    appServicePlanId: appServicePlanId
    managedIdentity: managedIdentity
    appSettings: appSettings
    keyVaultName: keyVaultName
  }
}

output NAME string = function.outputs.NAME
output URI string = function.outputs.URI
output IDENTITY_PRINCIPAL_ID string = function.outputs.IDENTITY_PRINCIPAL_ID
