param resourceName string
param location string = resourceGroup().location
param workspaceId string = ''
param workspaceResourceId string = ''
param workspaceRegion string = resourceGroup().location

var nsgName = 'nsg-${resourceName}'

resource nsg 'Microsoft.Network/networkSecurityGroups@2021-05-01' = {
  name: nsgName
  location: location
}
output nsgId string = nsg.id

param ruleInAllowGwManagement bool = false
param ruleInGwManagementPort string = '443,65200-65535'
resource ruleAppGwManagement 'Microsoft.Network/networkSecurityGroups/securityRules@2020-11-01' = if(ruleInAllowGwManagement) {
  parent: nsg
  name: 'Allow_AppGatewayManagement'
  properties: {
    protocol: '*'
    sourcePortRange: '*'
    destinationPortRange: ruleInGwManagementPort
    sourceAddressPrefix: 'GatewayManager'
    destinationAddressPrefix: '*'
    access: 'Allow'
    priority: 110
    direction: 'Inbound'
  }
}

param ruleInAllowAzureLoadBalancer bool = false
resource ruleAzureLoadBalancer 'Microsoft.Network/networkSecurityGroups/securityRules@2020-11-01' = if (ruleInAllowAzureLoadBalancer) {
  parent: nsg
  name: 'Allow_AzureLoadBalancer'
  properties: {
    protocol: '*'
    sourcePortRange: '*'
    destinationPortRange: '*'
    sourceAddressPrefix: 'AzureLoadBalancer'
    destinationAddressPrefix: '*'
    access: 'Allow'
    priority: 120
    direction: 'Inbound'
    sourcePortRanges: []
    destinationPortRanges: []
    sourceAddressPrefixes: []
    destinationAddressPrefixes: []
  }
}

param ruleInDenyInternet bool = false
resource ruleDenyInternet 'Microsoft.Network/networkSecurityGroups/securityRules@2020-11-01' = if(ruleInDenyInternet) {
  parent: nsg
  name: 'Deny_AllInboundInternet'
  properties: {
    description: 'Azure infrastructure communication'
    protocol: '*'
    sourcePortRange: '*'
    destinationPortRange: '*'
    sourceAddressPrefix: 'Internet'
    destinationAddressPrefix: '*'
    access: 'Deny'
    priority: 4096
    direction: 'Inbound'
    sourcePortRanges: []
    destinationPortRanges: []
    sourceAddressPrefixes: []
    destinationAddressPrefixes: []
  }
}

param ruleInAllowInternetHttp bool = false
resource ruleInternetHttp 'Microsoft.Network/networkSecurityGroups/securityRules@2020-11-01' = if(ruleInAllowInternetHttp) {
  parent: nsg
  name: 'Allow_Internet_Http'
  properties: {
    protocol: 'Tcp'
    sourcePortRange: '*'
    sourceAddressPrefix: 'Internet'
    destinationAddressPrefix: '*'
    access: 'Allow'
    priority: 200
    direction: 'Inbound'
    sourcePortRanges: []
    destinationPortRanges: [
      '80'
    ]
    sourceAddressPrefixes: []
    destinationAddressPrefixes: []
  }
}

param ruleInAllowInternetHttps bool = false
resource ruleInternetHttps 'Microsoft.Network/networkSecurityGroups/securityRules@2020-11-01' = if(ruleInAllowInternetHttps) {
  parent: nsg
  name: 'Allow_Internet_Https'
  properties: {
    protocol: 'Tcp'
    sourcePortRange: '*'
    sourceAddressPrefix: 'Internet'
    destinationAddressPrefix: '*'
    access: 'Allow'
    priority: 210
    direction: 'Inbound'
    sourcePortRanges: []
    destinationPortRanges: [
      '443'
    ]
    sourceAddressPrefixes: []
    destinationAddressPrefixes: []
  }
}

param ruleInAllowBastionHostComms bool = false
resource ruleBastionHost 'Microsoft.Network/networkSecurityGroups/securityRules@2020-11-01' = if(ruleInAllowBastionHostComms) {
  parent: nsg
  name: 'Allow_Bastion_Host_Communication'
  properties: {
    protocol: '*'
    sourcePortRange: '*'
    sourceAddressPrefix: 'VirtualNetwork'
    destinationAddressPrefix: 'VirtualNetwork'
    access: 'Allow'
    priority: 700
    direction: 'Inbound'
    sourcePortRanges: []
    destinationPortRanges: [
      '8080'
      '5701'
    ]
    sourceAddressPrefixes: []
    destinationAddressPrefixes: []
  }
}

param ruleOutAllowBastionComms bool = false
resource ruleBastionEgressSshRdp 'Microsoft.Network/networkSecurityGroups/securityRules@2020-11-01' = if(ruleOutAllowBastionComms) {
  parent: nsg
  name: 'Allow_SshRdp_Outbound'
  properties: {
    protocol: '*'
    sourcePortRange: '*'
    sourceAddressPrefix: '*'
    destinationAddressPrefix: 'VirtualNetwork'
    access: 'Allow'
    priority: 200
    direction: 'Outbound'
    sourcePortRanges: []
    destinationPortRanges: [
      '22'
      '3389'
    ]
    sourceAddressPrefixes: []
    destinationAddressPrefixes: []
  }
}

resource ruleBastionEgressAzure 'Microsoft.Network/networkSecurityGroups/securityRules@2020-11-01' = if(ruleOutAllowBastionComms) {
  parent: nsg
  name: 'Allow_Azure_Cloud_Outbound'
  properties: {
    protocol: 'Tcp'
    sourcePortRange: '*'
    sourceAddressPrefix: '*'
    destinationAddressPrefix: 'AzureCloud'
    access: 'Allow'
    priority: 210
    direction: 'Outbound'
    sourcePortRanges: []
    destinationPortRanges: [
      '443'
    ]
    sourceAddressPrefixes: []
    destinationAddressPrefixes: []
  }
}

resource ruleBastionEgressBastionComms 'Microsoft.Network/networkSecurityGroups/securityRules@2020-11-01' = if(ruleOutAllowBastionComms) {
  parent: nsg
  name: 'Allow_Bastion_Communication'
  properties: {
    protocol: '*'
    sourcePortRange: '*'
    sourceAddressPrefix: 'VirtualNetwork'
    destinationAddressPrefix: 'VirtualNetwork'
    access: 'Allow'
    priority: 220
    direction: 'Outbound'
    sourcePortRanges: []
    destinationPortRanges: [
      '8080'
      '5701'
    ]
    sourceAddressPrefixes: []
    destinationAddressPrefixes: []
  }
}

resource ruleBastionEgressSessionInfo 'Microsoft.Network/networkSecurityGroups/securityRules@2020-11-01' = if(ruleOutAllowBastionComms) {
  parent: nsg
  name: 'Allow_Get_Session_Info'
  properties: {
    protocol: '*'
    sourcePortRange: '*'
    sourceAddressPrefix: '*'
    destinationAddressPrefix: 'Internet'
    access: 'Allow'
    priority: 230
    direction: 'Outbound'
    sourcePortRanges: []
    destinationPortRanges: [
      '80'
    ]
    sourceAddressPrefixes: []
    destinationAddressPrefixes: []
  }
}

param ruleInDenySsh bool = false
resource ruleSshIngressDeny 'Microsoft.Network/networkSecurityGroups/securityRules@2020-11-01' = if(ruleInDenySsh) {
  parent: nsg
  name: 'DenySshInbound'
  properties: {
    protocol: '*'
    sourcePortRange: '*'
    sourceAddressPrefix: '*'
    destinationAddressPrefix: '*'
    access: 'Deny'
    priority: 100
    direction: 'Inbound'
    sourcePortRanges: []
    destinationPortRanges: [
      '22'
    ]
    sourceAddressPrefixes: []
    destinationAddressPrefixes: []
  }
}

param NsgDiagnosticCategories array = [
  'NetworkSecurityGroupEvent'
  'NetworkSecurityGroupRuleCounter'
]

resource nsgDiags 'Microsoft.Insights/diagnosticSettings@2021-05-01-preview' = if (!empty(workspaceResourceId)) {
  name: 'diags-${nsgName}'
  scope: nsg
  properties: {
    workspaceId: workspaceResourceId
    logs: [for diagCategory in NsgDiagnosticCategories: {
      category: diagCategory
      enabled: true
    }]
  }
}

//If multiple NSG's are trying to add flow logs at the same, this will result in CONFLICT: AnotherOperationInProgress
//Therefore advised to create FlowLogs outside of NSG creation, to better coordinate the creation - or sequence the NSG creation with DependsOn
param FlowLogStorageAccountId string = ''
param FlowLogTrafficAnalytics bool = !empty(FlowLogStorageAccountId)
module nsgFlow 'networkwatcherflowlog.bicep' = if(!empty(FlowLogStorageAccountId)) {
  name: 'flow-${nsgName}'
  scope: resourceGroup('NetworkWatcherRG')
  params: {
    location:location
    name: 'flowNsg-${nsgName}'
    nsgId: nsg.id
    storageId: FlowLogStorageAccountId
    trafficAnalytics: FlowLogTrafficAnalytics
    workspaceId: workspaceId
    workspaceResourceId: workspaceResourceId
    workspaceRegion: workspaceRegion
  }
}

output nsgSubnetObj object = {
  properties: {
    networkSecurityGroup: {
      id: nsg.id
    }
  }
}
