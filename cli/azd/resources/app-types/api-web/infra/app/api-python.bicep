param name string
param location string = resourceGroup().location
param tags object = {}

param allowedOrigins array = []
param appCommandLine string = 'gunicorn --workers 4 --threads 2 --timeout 60 --access-logfile "-" --error-logfile "-" --bind=0.0.0.0:8000 -k uvicorn.workers.UvicornWorker todo.app:app'
param applicationInsightsName string = ''
param appServicePlanId string
param appSettings object = {}
param keyVaultName string
param serviceName string = 'api'

module api '../core/host/appservice.bicep' = {
  name: '${name}-app-module'
  params: {
    name: name
    location: location
    tags: union(tags, { 'azd-service-name': serviceName })
    allowedOrigins: allowedOrigins
    appCommandLine: appCommandLine
    applicationInsightsName: applicationInsightsName
    appServicePlanId: appServicePlanId
    appSettings: appSettings
    keyVaultName: keyVaultName
    runtimeName: 'python'
    runtimeVersion: '3.8'
    scmDoBuildDuringDeployment: true
  }
}

output SERVICE_API_IDENTITY_PRINCIPAL_ID string = api.outputs.identityPrincipalId
output SERVICE_API_NAME string = api.outputs.name
output SERVICE_API_URI string = api.outputs.uri
