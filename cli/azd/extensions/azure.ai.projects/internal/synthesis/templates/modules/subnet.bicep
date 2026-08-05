// Single subnet on an existing VNet. Kept as its own module so the parent can
// place subnets in the VNet's resource group (which may differ from the
// deployment RG) and serialize subnet writes via module dependsOn.

targetScope = 'resourceGroup'

@description('Name of the virtual network the subnet belongs to.')
param vnetName string

@description('Name of the subnet to create.')
param subnetName string

@description('CIDR for the subnet.')
param addressPrefix string

@description('Subnet delegations (e.g. Microsoft.App/environments for the agent subnet).')
param delegations array = []

resource subnet 'Microsoft.Network/virtualNetworks/subnets@2024-05-01' = {
  name: '${vnetName}/${subnetName}'
  properties: {
    addressPrefix: addressPrefix
    delegations: delegations
  }
}

output subnetId string = subnet.id
output subnetName string = subnetName
