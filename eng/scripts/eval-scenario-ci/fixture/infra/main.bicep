targetScope = 'resourceGroup'

resource reconciliationMarker 'Microsoft.Resources/deployments@2022-09-01' = {
  name: 'azd-eval-hero-reconciliation'
  properties: {
    mode: 'Incremental'
    template: {
      '$schema': 'https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#'
      contentVersion: '1.0.0.0'
      resources: []
    }
  }
}
