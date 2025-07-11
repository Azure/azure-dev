param name string
param location string = resourceGroup().location
param tags object = {}

param allowedOrigins array = []
param appCommandLine string = ''
param applicationInsightsName string = ''
param appServicePlanId string
param appSettings object = {}
param keyVaultName string
param serviceName string = 'petclinic'
param managedIdentityID string
param managedIdentityName string

@description('JVM runtime options. Use this instead of defining JAVA_OPTS manually on appSettings.')
param javaRuntimeOptions array = []

// applicationinsights-runtime-attach (and other plugins) that uses runtime attach
// require allowAttachSelf to be enabled on App Service. Otherwise, plugins will fail to attach
// on App Service.
var defaultJavaRuntimeOptions = [ '-Djdk.attach.allowAttachSelf=true' ]

module app '../core/host/appservice.bicep' = {
  name: '${name}-app-module'
  params: {
    userAssignedIdentityID: managedIdentityID
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
    runtimeVersion: '11-java11'
    scmDoBuildDuringDeployment: true
  }
}

//output APP_IDENTITY_PRINCIPAL_ID string = app.outputs.identityPrincipalId
output APP_NAME string = app.outputs.name
output APP_URI string = app.outputs.uri
