param environmentName string
param location string = resourceGroup().location

module appServicePlanSiteResources 'appserviceplan.bicep' = {
  name: 'appserviceplansite-resources'
  params: {
    environmentName: environmentName
    location: location
    sku: { name: 'B1' }
  }
}

output AZURE_APP_SERVICE_PLAN_ID string = appServicePlanSiteResources.outputs.AZURE_APP_SERVICE_PLAN_ID
