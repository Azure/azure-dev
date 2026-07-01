// Virtual network wiring for a network-secured (VNet-injected) Foundry account.
//
// Bring-your-own VNet only (network.mode: byo). The VNet must already exist;
// v1 references it by the ARM id supplied in azure.yaml. Each subnet follows
// the tri-state rule from the synthesizer:
//
//   create=true,  prefix set    -> create the subnet with that prefix
//   create=true,  prefix empty  -> create the subnet with a derived /24 prefix
//   create=false                -> reference an existing subnet as-is
//
// All subnet ids are deterministic ('<vnetId>/subnets/<name>'), so outputs are
// valid whether the subnet was created here or already existed.

targetScope = 'resourceGroup'

@description('ARM resource id of the existing customer VNet.')
param vnetId string

@description('Name of the agent (delegated) subnet.')
param agentSubnetName string

@description('CIDR for the agent subnet. Empty derives a /24 from the VNet space.')
param agentSubnetPrefix string = ''

@description('When true, create the agent subnet; when false, reference it.')
param createAgentSubnet bool

@description('Name of the private-endpoint subnet.')
param peSubnetName string

@description('CIDR for the private-endpoint subnet. Empty derives a /24 from the VNet space.')
param peSubnetPrefix string = ''

@description('When true, create the PE subnet; when false, reference it.')
param createPESubnet bool

// The VNet may live in a different resource group than the deployment RG.
var vnetParts = split(vnetId, '/')
var vnetSubscriptionId = vnetParts[2]
var vnetResourceGroupName = vnetParts[4]
var vnetName = last(vnetParts)

resource vnet 'Microsoft.Network/virtualNetworks@2024-05-01' existing = {
  name: vnetName
  scope: resourceGroup(vnetSubscriptionId, vnetResourceGroupName)
}

var vnetAddressSpace = vnet.properties.addressSpace.addressPrefixes[0]
var agentPrefix = empty(agentSubnetPrefix) ? cidrSubnet(vnetAddressSpace, 24, 0) : agentSubnetPrefix
var pePrefix = empty(peSubnetPrefix) ? cidrSubnet(vnetAddressSpace, 24, 1) : peSubnetPrefix

// Create the agent subnet, delegated to Microsoft.App/environments so the
// hosted agent's container app environment can be injected into it.
module agentSubnet 'subnet.bicep' = if (createAgentSubnet) {
  name: 'agent-subnet-${uniqueString(deployment().name, agentSubnetName)}'
  scope: resourceGroup(vnetSubscriptionId, vnetResourceGroupName)
  params: {
    vnetName: vnetName
    subnetName: agentSubnetName
    addressPrefix: agentPrefix
    delegations: [
      {
        name: 'Microsoft.App/environments'
        properties: {
          serviceName: 'Microsoft.App/environments'
        }
      }
    ]
  }
}

// Create the private-endpoint subnet. Depends on the agent subnet so the two
// subnet PUTs against the same VNet do not race (ARM serializes subnet writes).
module peSubnet 'subnet.bicep' = if (createPESubnet) {
  name: 'pe-subnet-${uniqueString(deployment().name, peSubnetName)}'
  scope: resourceGroup(vnetSubscriptionId, vnetResourceGroupName)
  params: {
    vnetName: vnetName
    subnetName: peSubnetName
    addressPrefix: pePrefix
    delegations: []
  }
  dependsOn: [
    agentSubnet
  ]
}

output vnetId string = vnet.id
output vnetName string = vnetName
output vnetSubscriptionId string = vnetSubscriptionId
output vnetResourceGroupName string = vnetResourceGroupName
output agentSubnetId string = '${vnet.id}/subnets/${agentSubnetName}'
output peSubnetId string = '${vnet.id}/subnets/${peSubnetName}'
output peSubnetName string = peSubnetName
