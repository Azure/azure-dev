param environmentName string
param location string = resourceGroup().location

param allowedOrigins array = []
param appCommandLine string = 'gunicorn --workers 4 --threads 2 --timeout 60 --access-logfile "-" --error-logfile "-" --bind=0.0.0.0:8000 -k uvicorn.workers.UvicornWorker todo.app:app'
param applicationInsightsName string = ''
param appServicePlanId string
param appSettings object = {}
param keyVaultName string
param serviceName string = 'api'

module api '../../../../../common/infra/bicep/core/host/appservice.bicep' = {
  name: '${serviceName}-appservice-python-module'
  params: {
    environmentName: environmentName
    location: location
    allowedOrigins: allowedOrigins
    appCommandLine: appCommandLine
    applicationInsightsName: applicationInsightsName
    appServicePlanId: appServicePlanId
    appSettings: appSettings
    keyVaultName: keyVaultName
    runtimeName: 'python'
    runtimeVersion: '3.8'
    scmDoBuildDuringDeployment: true
    serviceName: serviceName
  }
}

output API_IDENTITY_PRINCIPAL_ID string = api.outputs.identityPrincipalId
output API_NAME string = api.outputs.name
output API_URI string = api.outputs.uri
