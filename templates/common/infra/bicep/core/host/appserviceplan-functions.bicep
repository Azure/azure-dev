param environmentName string
param location string = resourceGroup().location
param sku object = {
  name: 'Y1'
  tier: 'Dynamic'
  size: 'Y1'
  family: 'Y'
}

module appServicePlanFunctions 'appserviceplan.bicep' = {
  name: 'appserviceplanfunctions-resources'
  params: {
    environmentName: environmentName
    location: location
    sku: sku
    kind: 'functionapp'
  }
}

output AZURE_APP_SERVICE_PLAN_ID string = appServicePlanFunctions.outputs.AZURE_APP_SERVICE_PLAN_ID
