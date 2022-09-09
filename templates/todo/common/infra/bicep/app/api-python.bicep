param environmentName string
param location string = resourceGroup().location
param serviceName string = 'api'
param appCommandLine string = 'gunicorn --workers 4 --threads 2 --timeout 60 --access-logfile "-" --error-logfile "-" --bind=0.0.0.0:8000 -k uvicorn.workers.UvicornWorker todo.app:app'
param applicationInsightsName string
param appServicePlanId string
param keyVaultName string

module api '../../../../../common/infra/bicep/core/host/appservice-python.bicep' = {
  name: 'application-appservice-python-${serviceName}'
  params: {
    environmentName: environmentName
    location: location
    serviceName: serviceName
    applicationInsightsName: applicationInsightsName
    appServicePlanId: appServicePlanId
    keyVaultName: keyVaultName
    appCommandLine: appCommandLine
    scmDoBuildDuringDeployment: true
  }
}

output NAME string = api.outputs.NAME
output URI string = api.outputs.URI
output IDENTITY_PRINCIPAL_ID string = api.outputs.IDENTITY_PRINCIPAL_ID
