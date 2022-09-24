//using a seperate module file as Byo subnet scenario caters from where the subnet is in the another resource group
//name/rg required to new up an existing reference and form a dependency
//principalid required as it needs to be used to establish a unique roleassignment name
param byoAKSSubnetId string
param user_identity_name string
param user_identity_rg string
param user_identity_principalId string

@allowed([
  'Subnet'
  'Vnet'
])
param rbacAssignmentScope string = 'Subnet'

var networkContributorRole = resourceId('Microsoft.Authorization/roleDefinitions', '4d97b98b-1d4f-4787-a291-c67834d212e7')

var existingAksSubnetName = !empty(byoAKSSubnetId) ? split(byoAKSSubnetId, '/')[10] : ''
var existingAksVnetName = !empty(byoAKSSubnetId) ? split(byoAKSSubnetId, '/')[8] : ''

resource existingvnet 'Microsoft.Network/virtualNetworks@2021-02-01' existing =  {
  name: existingAksVnetName
}
resource existingAksSubnet 'Microsoft.Network/virtualNetworks/subnets@2020-08-01' existing = {
  parent: existingvnet
  name: existingAksSubnetName
}

resource uai 'Microsoft.ManagedIdentity/userAssignedIdentities@2018-11-30' existing = {
  name: user_identity_name
  scope: resourceGroup(user_identity_rg)
}

resource subnetRbac 'Microsoft.Authorization/roleAssignments@2020-04-01-preview' = if (rbacAssignmentScope == 'subnet') {
  name:  guid(user_identity_principalId, networkContributorRole, existingAksSubnetName)
  scope: existingAksSubnet
  properties: {
    roleDefinitionId: networkContributorRole
    principalId: uai.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

resource existingVnetRbac 'Microsoft.Authorization/roleAssignments@2020-04-01-preview' = if (rbacAssignmentScope != 'subnet') {
  name:  guid(user_identity_principalId, networkContributorRole, existingAksVnetName)
  scope: existingvnet
  properties: {
    roleDefinitionId: networkContributorRole
    principalId: uai.properties.principalId
    principalType: 'ServicePrincipal'
  }
}
