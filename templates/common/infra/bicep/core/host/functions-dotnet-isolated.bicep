param environmentName string
param location string = resourceGroup().location

param allowedOrigins array = []
param applicationInsightsName string = ''
param appServicePlanId string
param appSettings object = {}
param enableOryxBuild bool = false
param keyVaultName string = ''
param linuxFxVersion string = 'DOTNET-ISOLATED|6.0'
param managedIdentity bool = !(empty(keyVaultName))
param scmDoBuildDuringDeployment bool = false
param serviceName string
param storageAccountName string

module functions 'functions.bicep' = {
  name: '${serviceName}-functions-csharp'
  params: {
    environmentName: environmentName
    location: location
    allowedOrigins: allowedOrigins
    applicationInsightsName: applicationInsightsName
    appServicePlanId: appServicePlanId
    appSettings: appSettings
    enableOryxBuild: enableOryxBuild
    functionsWorkerRuntime: 'dotnet-isolated'
    keyVaultName: keyVaultName
    linuxFxVersion: linuxFxVersion
    managedIdentity: managedIdentity
    scmDoBuildDuringDeployment: scmDoBuildDuringDeployment
    serviceName: serviceName
    storageAccountName: storageAccountName
  }
}

output identityPrincipalId string = functions.outputs.identityPrincipalId
output name string = functions.outputs.name
output uri string = functions.outputs.uri
