param name string
param location string = resourceGroup().location
param tags object = {}

param allowedOrigins array = []
param appCommandLine string = ''
param applicationInsightsName string = ''
param appServicePlanId string
param appSettings object = {}
param keyVaultName string
param serviceName string = 'api'

@description('JVM runtime options. Use this instead of defining JAVA_OPTS manually on appSettings.')
param javaRuntimeOptions array = []

// applicationinsights-runtime-attach (and other plugins) that uses runtime attach
// require allowAttachSelf to be enabled on App Service. Otherwise, plugins will fail to attach
// on App Service.
var defaultJavaRuntimeOptions = ['-Djdk.attach.allowAttachSelf=true']

module api '../../../../../common/infra/bicep/core/host/appservice.bicep' = {
  name: '${name}-app-module'
  params: {
    name: name
    location: location
    tags: union(tags, { 'azd-service-name': serviceName })
    allowedOrigins: allowedOrigins
    appCommandLine: appCommandLine
    applicationInsightsName: applicationInsightsName
    appServicePlanId: appServicePlanId
    appSettings: union(appSettings, {
      JAVA_OPTS: join(
        concat(
            javaRuntimeOptions,
            defaultJavaRuntimeOptions),
          ' ')
     })
    keyVaultName: keyVaultName
    runtimeName: 'java'
    runtimeVersion: '17-java17'
    scmDoBuildDuringDeployment: true
  }
}

output SERVICE_API_IDENTITY_PRINCIPAL_ID string = api.outputs.identityPrincipalId
output SERVICE_API_NAME string = api.outputs.name
output SERVICE_API_URI string = api.outputs.uri
