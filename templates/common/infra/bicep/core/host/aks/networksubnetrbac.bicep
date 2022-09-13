param vnetName string
param subnetName string
param servicePrincipalId string

var networkContributorRole = subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '4d97b98b-1d4f-4787-a291-c67834d212e7')

resource subnet 'Microsoft.Network/virtualNetworks/subnets@2022-01-01' existing = {
  name: '${vnetName}/${subnetName}'
}

resource aks_vnet_cont 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subnet.id, servicePrincipalId, networkContributorRole)
  scope: subnet
  properties: {
    roleDefinitionId: networkContributorRole
    principalId: servicePrincipalId
    principalType: 'ServicePrincipal'
  }
}
