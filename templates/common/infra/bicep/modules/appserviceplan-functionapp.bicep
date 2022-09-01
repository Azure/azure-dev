param environmentName string
param location string = resourceGroup().location

module appServicePlanFunctionAppResources 'appserviceplan.bicep' = {
  name: 'appserviceplanfunctionapp-resources'
  params: {
    environmentName: environmentName
    location: location
    sku: {
      name: 'Y1'
      tier: 'Dynamic'
      size: 'Y1'
      family: 'Y'
    }
    kind: 'functionapp'
  }
}
