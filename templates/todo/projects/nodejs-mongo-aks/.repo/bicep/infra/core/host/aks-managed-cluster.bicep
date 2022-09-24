param environmentName string
param principalId string = ''
param location string = resourceGroup().location

var abbrs = loadJsonContent('../../abbreviations.json')
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))

module cluster 'aks/main.bicep' = {
  name: 'aks'
  params: {
    resourceName: '${abbrs.containerServiceManagedClusters}-${resourceToken}'
    acrPushRolePrincipalId: principalId
    location: location
    upgradeChannel: 'stable'
    AksPaidSkuForSLA: true
    agentCountMax: 20
    omsagent: true
    retentionInDays: 30
    ingressApplicationGateway: true
    keyVaultAksCSI: true
    registries_sku: 'Basic'
    JustUseSystemPool: true
  }
}
