param resourceName string
param location string = resourceGroup().location

param networkPluginIsKubenet bool = false
param aksPrincipleId string = ''

param vnetAddressPrefix string
param vnetAksSubnetAddressPrefix string

//Nsg
param workspaceName string = ''
param workspaceResourceGroupName string = ''
param networkSecurityGroups bool = true

//Firewall
param azureFirewalls bool = false
param azureFirewallsSku string = 'Basic'
param azureFirewallsManagementSeperation bool = azureFirewalls && azureFirewallsSku=='Basic'
param vnetFirewallSubnetAddressPrefix string = ''
param vnetFirewallManagementSubnetAddressPrefix string = ''

//Ingress
param ingressApplicationGateway bool = false
param ingressApplicationGatewayPublic bool = false
param vnetAppGatewaySubnetAddressPrefix string =''

//Private Link
param privateLinks bool = false
param privateLinkSubnetAddressPrefix string = ''
param privateLinkAcrId string = ''
param privateLinkAkvId string = ''

//ACR
param acrPrivatePool bool = false
param acrAgentPoolSubnetAddressPrefix string = ''

//NatGatewayEgress
param natGateway bool = false
param natGatewayPublicIps int = 2
param natGatewayIdleTimeoutMins int = 30

//Bastion
param bastion bool =false
param bastionSubnetAddressPrefix string = ''

@description('Used by the Bastion Public IP')
param availabilityZones array = []


var bastion_subnet_name = 'AzureBastionSubnet'
var bastion_baseSubnet = {
  name: bastion_subnet_name
  properties: {
    addressPrefix: bastionSubnetAddressPrefix
  }
}
var bastion_subnet = bastion && networkSecurityGroups ? union(bastion_baseSubnet, nsgBastion.outputs.nsgSubnetObj) : bastion_baseSubnet

var acrpool_subnet_name = 'acrpool-sn'
var acrpool_baseSubnet = {
  name: acrpool_subnet_name
  properties: {
    addressPrefix: acrAgentPoolSubnetAddressPrefix
  }
}
var acrpool_subnet = privateLinks && networkSecurityGroups ? union(acrpool_baseSubnet, nsgAcrPool.outputs.nsgSubnetObj) : acrpool_baseSubnet

var private_link_subnet_name = 'privatelinks-sn'
var private_link_baseSubnet = {
  name: private_link_subnet_name
  properties: {
    addressPrefix: privateLinkSubnetAddressPrefix
    privateEndpointNetworkPolicies: 'Disabled'
    privateLinkServiceNetworkPolicies: 'Enabled'
  }
}
var private_link_subnet = privateLinks && networkSecurityGroups ? union(private_link_baseSubnet, nsgPrivateLinks.outputs.nsgSubnetObj) : private_link_baseSubnet


var appgw_subnet_name = 'appgw-sn'
var appgw_baseSubnet = {
  name: appgw_subnet_name
  properties: {
    addressPrefix: vnetAppGatewaySubnetAddressPrefix
  }
}
var appgw_subnet = ingressApplicationGateway && networkSecurityGroups ? union(appgw_baseSubnet, nsgAppGw.outputs.nsgSubnetObj) : appgw_baseSubnet

var fw_subnet_name = 'AzureFirewallSubnet' // Required by FW
var fw_subnet = {
  name: fw_subnet_name
  properties: {
    addressPrefix: vnetFirewallSubnetAddressPrefix
  }
}

/// ---- Firewall VNET config
module calcAzFwIp './calcAzFwIp.bicep' = if (azureFirewalls) {
  name: 'calcAzFwIp'
  params: {
    vnetFirewallSubnetAddressPrefix: vnetFirewallSubnetAddressPrefix
  }
}

var fwmgmt_subnet_name = 'AzureFirewallManagementSubnet' // Required by FW
var fwmgmt_subnet = {
  name: fwmgmt_subnet_name
  properties: {
    addressPrefix: vnetFirewallManagementSubnetAddressPrefix
  }
}

var routeFwTableName = 'rt-afw-${resourceName}'
resource vnet_udr 'Microsoft.Network/routeTables@2022-07-01' = if (azureFirewalls) {
  name: routeFwTableName
  location: location
  properties: {
    routes: [
      {
        name: 'AKSNodesEgress'
        properties: {
          addressPrefix: '0.0.0.0/1'
          nextHopType: 'VirtualAppliance'
          nextHopIpAddress: azureFirewalls ? calcAzFwIp.outputs.FirewallPrivateIp : null
        }
      }
    ]
  }
}

var contributorRoleId = subscriptionResourceId('Microsoft.Authorization/roleDefinitions', 'b24988ac-6180-42a0-ab88-20f7382dd24c')

@description('Required for kubenet networking.')
resource vnet_udr_rbac 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (azureFirewalls && !empty(aksPrincipleId) && networkPluginIsKubenet) {
  scope: vnet_udr
  name: guid(vnet_udr.id, aksPrincipleId, contributorRoleId)
  properties: {
    principalId: aksPrincipleId
    roleDefinitionId: contributorRoleId
    principalType: 'ServicePrincipal'
  }
}

var aks_subnet_name = 'aks-sn'
var aks_baseSubnet =  {
  name: aks_subnet_name
  properties: union({
      addressPrefix: vnetAksSubnetAddressPrefix
    }, privateLinks ? {
      privateEndpointNetworkPolicies: 'Disabled'
      privateLinkServiceNetworkPolicies: 'Enabled'
    } : {}, natGateway ? {
      natGateway: {
        id: natGw.id
      }
    } : {}, azureFirewalls ? {
      routeTable: {
        id: vnet_udr.id //resourceId('Microsoft.Network/routeTables', routeFwTableName)
      }
    }: {})
}

var aks_subnet = networkSecurityGroups ? union(aks_baseSubnet, nsgAks.outputs.nsgSubnetObj) : aks_baseSubnet

var subnets = union(
  array(aks_subnet),
  azureFirewalls ? array(fw_subnet) : [],
  privateLinks ? array(private_link_subnet) : [],
  acrPrivatePool ? array(acrpool_subnet) : [],
  bastion ? array(bastion_subnet) : [],
  ingressApplicationGateway ? array(appgw_subnet) : [],
  azureFirewallsManagementSeperation ? array(fwmgmt_subnet) : []
)
output debugSubnets array = subnets

var vnetName = 'vnet-${resourceName}'
resource vnet 'Microsoft.Network/virtualNetworks@2021-02-01' = {
  name: vnetName
  location: location
  properties: {
    addressSpace: {
      addressPrefixes: [
        vnetAddressPrefix
      ]
    }
    subnets: subnets
  }
}
output vnetId string = vnet.id
output vnetName string = vnet.name
output aksSubnetId string = resourceId('Microsoft.Network/virtualNetworks/subnets', vnet.name, aks_subnet_name)
output fwSubnetId string = azureFirewalls ? '${vnet.id}/subnets/${fw_subnet_name}' : ''
output fwMgmtSubnetId string = azureFirewalls ? '${vnet.id}/subnets/${fwmgmt_subnet_name}' : ''
output acrPoolSubnetId string = acrPrivatePool ? '${vnet.id}/subnets/${acrpool_subnet_name}' : ''
output appGwSubnetId string = resourceId('Microsoft.Network/virtualNetworks/subnets', vnet.name, appgw_subnet_name)
output privateLinkSubnetId string = resourceId('Microsoft.Network/virtualNetworks/subnets', vnet.name, private_link_subnet_name)

module aks_vnet_con 'networksubnetrbac.bicep' = if (!empty(aksPrincipleId)) {
  name: '${resourceName}-subnetRbac'
  params: {
    servicePrincipalId: aksPrincipleId
    subnetName: aks_subnet_name
    vnetName: vnet.name
  }
}

/*   --------------------------------------------------------------------------  Private Link for ACR      */
var privateLinkAcrName = 'pl-acr-${resourceName}'
resource privateLinkAcr 'Microsoft.Network/privateEndpoints@2021-08-01' = if (!empty(privateLinkAcrId)) {
  name: privateLinkAcrName
  location: location
  properties: {
    customNetworkInterfaceName: 'nic-${privateLinkAcrName}'
    privateLinkServiceConnections: [
      {
        name: 'Acr-Connection'
        properties: {
          privateLinkServiceId: privateLinkAcrId
          groupIds: [
            'registry'
          ]
        }
      }
    ]
    subnet: {
      id: '${vnet.id}/subnets/${private_link_subnet_name}'
    }
  }
}

resource privateDnsAcr 'Microsoft.Network/privateDnsZones@2020-06-01' = if (!empty(privateLinkAcrId))  {
  name: 'privatelink.azurecr.io'
  location: 'global'
}

var privateDnsAcrLinkName = 'vnet-dnscr-${resourceName}'
resource privateDnsAcrLink 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2020-06-01' = if (!empty(privateLinkAcrId))  {
  parent: privateDnsAcr
  name: privateDnsAcrLinkName
  location: 'global'
  properties: {
    registrationEnabled: false
    virtualNetwork: {
      id: vnet.id
    }
  }
}

resource privateDnsAcrZoneGroup 'Microsoft.Network/privateEndpoints/privateDnsZoneGroups@2021-08-01' = if (!empty(privateLinkAcrId))  {
  parent: privateLinkAcr
  name: 'default'
  properties: {
    privateDnsZoneConfigs: [
      {
        name: 'vnet-pl-acr'
        properties: {
          privateDnsZoneId: privateDnsAcr.id
        }
      }
    ]
  }
}


/*   --------------------------------------------------------------------------  Private Link for KeyVault      */
var privateLinkAkvName = 'pl-akv-${resourceName}'
resource privateLinkAkv 'Microsoft.Network/privateEndpoints@2021-08-01' = if (!empty(privateLinkAkvId)) {
  name: privateLinkAkvName
  location: location
  properties: {
    customNetworkInterfaceName: 'nic-${privateLinkAkvName}'
    privateLinkServiceConnections: [
      {
        name: 'Akv-Connection'
        properties: {
          privateLinkServiceId: privateLinkAkvId
          groupIds: [
            'vault'
          ]
        }
      }
    ]
    subnet: {
      id: '${vnet.id}/subnets/${private_link_subnet_name}'
    }
  }
}

resource privateDnsAkv 'Microsoft.Network/privateDnsZones@2020-06-01' = if (!empty(privateLinkAkvId))  {
  name: 'privatelink.vaultcore.azure.net'
  location: 'global'
}

var privateDnsAkvLinkName = 'vnet-dnscr-${resourceName}'
resource privateDnsAkvLink 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2020-06-01' = if (!empty(privateLinkAkvId))  {
  parent: privateDnsAkv
  name: privateDnsAkvLinkName
  location: 'global'
  properties: {
    registrationEnabled: false
    virtualNetwork: {
      id: vnet.id
    }
  }
}

resource privateDnsAkvZoneGroup 'Microsoft.Network/privateEndpoints/privateDnsZoneGroups@2021-08-01' = if (!empty(privateLinkAkvId))  {
  parent: privateLinkAkv
  name: 'default'
  properties: {
    privateDnsZoneConfigs: [
      {
        name: 'vnet-pl-akv'
        properties: {
          privateDnsZoneId: privateDnsAkv.id
        }
      }
    ]
  }
}

param bastionHostName string = 'bas-${resourceName}'
var publicIpAddressName = 'pip-${bastionHostName}'

@allowed([
  'Standard'
  'Basic'
])
param bastionSku string = 'Standard'

resource bastionPip 'Microsoft.Network/publicIPAddresses@2021-03-01' = if(bastion) {
  name: publicIpAddressName
  location: location
  sku: {
    name: 'Standard'
  }
  zones: !empty(availabilityZones) ? availabilityZones : []
  properties: {
    publicIPAllocationMethod: 'Static'
  }
}

resource bastionHost 'Microsoft.Network/bastionHosts@2021-05-01' = if(bastion) {
  name: bastionHostName
  location: location
  sku: {
    name: bastionSku
  }
  properties: {
    enableTunneling: true
    ipConfigurations: [
      {
        name: 'IpConf'
        properties: {
          subnet: {
            id: '${vnet.id}/subnets/${bastion_subnet_name}'
          }
          publicIPAddress: {
            id: bastionPip.id
          }
        }
      }
    ]
  }
}

resource log 'Microsoft.OperationalInsights/workspaces@2021-06-01' existing = if(networkSecurityGroups && !empty(workspaceName)) {
  name: workspaceName
  scope: resourceGroup(workspaceResourceGroupName)
}

param CreateNsgFlowLogs bool = false

var flowLogStorageRawName = replace(toLower('stflow${resourceName}${uniqueString(resourceGroup().id, resourceName)}'),'-','')
var flowLogStorageName = length(flowLogStorageRawName) > 24 ? substring(flowLogStorageRawName, 0, 24) : flowLogStorageRawName
resource flowLogStor 'Microsoft.Storage/storageAccounts@2021-08-01' = if(CreateNsgFlowLogs && networkSecurityGroups) {
  name: flowLogStorageName
  kind: 'StorageV2'
  sku: {
    name: 'Standard_LRS'
  }
  location: location
  properties: {
    minimumTlsVersion: 'TLS1_2'
  }
}

//NSG's
module nsgAks 'nsg.bicep' = if(networkSecurityGroups) {
  name: 'nsgAks'
  params: {
    location: location
    resourceName: '${aks_subnet_name}-${resourceName}'
    workspaceId: !empty(workspaceName) ? log.properties.customerId : ''
    workspaceRegion:  !empty(workspaceName) ? log.location : ''
    workspaceResourceId:  !empty(workspaceName) ? log.id : ''
    ruleInAllowInternetHttp: true
    ruleInAllowInternetHttps: true
    ruleInDenySsh: true
    FlowLogStorageAccountId: CreateNsgFlowLogs ? flowLogStor.id : ''
  }
}

module nsgAcrPool 'nsg.bicep' = if(acrPrivatePool && networkSecurityGroups) {
  name: 'nsgAcrPool'
  params: {
    location: location
    resourceName: '${acrpool_subnet_name}-${resourceName}'
    workspaceId: !empty(workspaceName) ? log.properties.customerId : ''
    workspaceRegion:  !empty(workspaceName) ? log.location : ''
    workspaceResourceId:  !empty(workspaceName) ? log.id : ''
    FlowLogStorageAccountId: CreateNsgFlowLogs ? flowLogStor.id : ''
  }
  dependsOn: [
    nsgAks
  ]
}

module nsgAppGw 'nsg.bicep' = if(ingressApplicationGateway && networkSecurityGroups) {
  name: 'nsgAppGw'
  params: {
    location: location
    resourceName: '${appgw_subnet_name}-${resourceName}'
    workspaceId: !empty(workspaceName) ? log.properties.customerId : ''
    workspaceRegion:  !empty(workspaceName) ? log.location : ''
    workspaceResourceId:  !empty(workspaceName) ? log.id : ''
    ruleInAllowInternetHttp: ingressApplicationGatewayPublic
    ruleInAllowInternetHttps: ingressApplicationGatewayPublic
    ruleInAllowGwManagement: true
    ruleInAllowAzureLoadBalancer: true
    ruleInDenyInternet: true
    ruleInGwManagementPort: '65200-65535'
    FlowLogStorageAccountId: CreateNsgFlowLogs ? flowLogStor.id : ''
  }
  dependsOn: [
    nsgAcrPool
  ]
}

module nsgBastion 'nsg.bicep' = if(bastion && networkSecurityGroups) {
  name: 'nsgBastion'
  params: {
    location: location
    resourceName: '${bastion_subnet_name}-${resourceName}'
    workspaceId: !empty(workspaceName) ? log.properties.customerId : ''
    workspaceRegion:  !empty(workspaceName) ? log.location : ''
    workspaceResourceId:  !empty(workspaceName) ? log.id : ''
    ruleInAllowBastionHostComms: true
    ruleInAllowInternetHttps: true
    ruleInAllowGwManagement: true
    ruleInAllowAzureLoadBalancer: true
    ruleOutAllowBastionComms: true
    ruleInGwManagementPort: '443'
    FlowLogStorageAccountId: CreateNsgFlowLogs ? flowLogStor.id : ''
  }
  dependsOn: [
    nsgAppGw
  ]
}

module nsgPrivateLinks 'nsg.bicep' = if(privateLinks && networkSecurityGroups) {
  name: 'nsgPrivateLinks'
  params: {
    location: location
    resourceName: '${private_link_subnet_name}-${resourceName}'
    workspaceId: !empty(workspaceName) ? log.properties.customerId : ''
    workspaceRegion:  !empty(workspaceName) ? log.location : ''
    workspaceResourceId:  !empty(workspaceName) ? log.id : ''
    FlowLogStorageAccountId: CreateNsgFlowLogs ? flowLogStor.id : ''
  }
  dependsOn: [
    nsgBastion
  ]
}

resource natGwIp 'Microsoft.Network/publicIPAddresses@2021-08-01' =  [for i in range(0, natGatewayPublicIps): if(natGateway) {
  name: 'pip-${natGwName}-${i+1}'
  location: location
  sku: {
    name: 'Standard'
  }
  zones: !empty(availabilityZones) ? availabilityZones : []
  properties: {
    publicIPAllocationMethod: 'Static'
  }
}]

var natGwName = 'ng-${resourceName}'
resource natGw 'Microsoft.Network/natGateways@2021-08-01' = if(natGateway) {
  name: natGwName
  location: location
  sku: {
    name: 'Standard'
  }
  zones: !empty(availabilityZones) ? availabilityZones : []
  properties: {
    publicIpAddresses: [for i in range(0, natGatewayPublicIps): {
      id: natGwIp[i].id
    }]
    idleTimeoutInMinutes: natGatewayIdleTimeoutMins
  }
  dependsOn: [
    natGwIp
  ]
}

