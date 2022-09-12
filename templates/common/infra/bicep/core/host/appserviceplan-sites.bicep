param environmentName string
param location string = resourceGroup().location
param sku object = { name: 'B1' }

module appServicePlanSite 'appserviceplan.bicep' = {
  name: 'appserviceplansite-resources'
  params: {
    environmentName: environmentName
    location: location
    sku: sku
  }
}

output AZURE_APP_SERVICE_PLAN_ID string = appServicePlanSite.outputs.AZURE_APP_SERVICE_PLAN_ID
