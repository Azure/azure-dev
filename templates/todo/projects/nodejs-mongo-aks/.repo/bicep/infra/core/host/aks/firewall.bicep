param resourceName string
param location string = resourceGroup().location
param workspaceDiagsId string = ''
param fwSubnetId string
param vnetAksSubnetAddressPrefix string
param certManagerFW bool = false
param acrPrivatePool bool = false
param acrAgentPoolSubnetAddressPrefix string = ''
param availabilityZones array = []

var firewallPublicIpName = 'pip-afw-${resourceName}'

resource fw_pip 'Microsoft.Network/publicIPAddresses@2021-03-01' = {
  name: firewallPublicIpName
  location: location
  sku: {
    name: 'Standard'
  }
  zones: !empty(availabilityZones) ? availabilityZones : []
  properties: {
    publicIPAllocationMethod: 'Static'
    publicIPAddressVersion: 'IPv4'
  }
}

resource fwDiags 'Microsoft.Insights/diagnosticSettings@2021-05-01-preview' = if (!empty(workspaceDiagsId)) {
  scope: fw
  name: 'fwDiags'
  properties: {
    workspaceId: workspaceDiagsId
    logs: [
      {
        category: 'AzureFirewallApplicationRule'
        enabled: true
        retentionPolicy: {
          days: 10
          enabled: false
        }
      }
      {
        category: 'AzureFirewallNetworkRule'
        enabled: true
        retentionPolicy: {
          days: 10
          enabled: false
        }
      }
    ]
    metrics: [
      {
        category: 'AllMetrics'
        enabled: true
        retentionPolicy: {
          enabled: false
          days: 0
        }
      }
    ]
  }
}

@description('Whitelist dnsZone name (required by cert-manager validation process)')
param appDnsZoneName string = ''

var fw_name = 'afw-${resourceName}'
resource fw 'Microsoft.Network/azureFirewalls@2021-03-01' = {
  name: fw_name
  location: location
  zones: !empty(availabilityZones) ? availabilityZones : []
  properties: {
    ipConfigurations: [
      {
        name: 'IpConf1'
        properties: {
          subnet: {
            id: fwSubnetId
          }
          publicIPAddress: {
            id: fw_pip.id
          }
        }
      }
    ]
    threatIntelMode: 'Alert'
    firewallPolicy: {
      id: fwPolicy.id

    }
    applicationRuleCollections: []
    networkRuleCollections: []
  }
}

resource fwPolicy 'Microsoft.Network/firewallPolicies@2020-11-01' = {
  name: 'afwp-${resourceName}'
  location: location
  properties: {
    sku: {
      tier: 'Standard'
    }
    threatIntelMode: 'Alert'
    threatIntelWhitelist: {
      fqdns: []
      ipAddresses: []
    }
  }
}

resource fwpRules 'Microsoft.Network/firewallPolicies/ruleCollectionGroups@2020-11-01' = {
  parent: fwPolicy
  name: 'AKSConstructionRuleGroup'
  properties: {
    priority: 200
    ruleCollections:  [
      {
        ruleCollectionType: 'FirewallPolicyFilterRuleCollection'
        name: 'CoreAksNetEgress'
        priority: 100
        action: {
          type: 'Allow'
        }
        rules: concat([
          {
            name: 'ControlPlaneTCP'
            ruleType: 'NetworkRule'
            ipProtocols: [
              'TCP'
            ]
            sourceAddresses: [
              vnetAksSubnetAddressPrefix
            ]
            destinationAddresses: [
              'AzureCloud.${location}'
            ]
            destinationPorts: [
              '9000' /* For tunneled secure communication between the nodes and the control plane. */
              '22'
            ]
          }
          {
            name: 'ControlPlaneUDP'
            ruleType: 'NetworkRule'
            ipProtocols: [
              'UDP'
            ]
            sourceAddresses: [
              vnetAksSubnetAddressPrefix
            ]
            destinationAddresses: [
              'AzureCloud.${location}'
            ]
            destinationPorts: [
              '1194' /* For tunneled secure communication between the nodes and the control plane. */
            ]
          }
          {
            name: 'AzureMonitorForContainers'
            ruleType: 'NetworkRule'
            ipProtocols: [
              'TCP'
            ]
            sourceAddresses: [
              vnetAksSubnetAddressPrefix
            ]
            destinationAddresses: [
              'AzureMonitor'
            ]
            destinationPorts: [
              '443'
            ]
          }
        ], acrPrivatePool ? [
          {
            name: 'acr-agentpool'
            ruleType: 'NetworkRule'
            ipProtocols: [
              'TCP'
            ]
            sourceAddresses: [
              acrAgentPoolSubnetAddressPrefix
            ]
            destinationAddresses: [
              'AzureKeyVault'
              'Storage'
              'EventHub'
              'AzureActiveDirectory'
              'AzureMonitor'
            ]
            destinationPorts: [
              '443'
            ]
          }
        ]:[])
      }
      {
        ruleCollectionType: 'FirewallPolicyFilterRuleCollection'
        name: 'CoreAksHttpEgress'
        priority: 400
        action: {
          type: 'Allow'
        }
        rules: concat([
            {
              name: 'aks'
              ruleType: 'ApplicationRule'
              protocols: [
                {
                  port: 443
                  protocolType: 'Https'
                }
                {
                  port: 80
                  protocolType: 'Http'
                }
              ]
              targetFqdns: []
              fqdnTags: [
                'AzureKubernetesService'
              ]
              sourceAddresses: [
                vnetAksSubnetAddressPrefix
              ]
            }
          ], certManagerFW ? [
            {
              name: 'certman-quay'
              ruleType: 'ApplicationRule'
              protocols: [
                {
                  port: 443
                  protocolType: 'Https'
                }
                {
                  port: 80
                  protocolType: 'Http'
                }
              ]
              targetFqdns: [
                'quay.io'
                '*.quay.io'
              ]
              sourceAddresses: [
                vnetAksSubnetAddressPrefix
              ]
            }
            {
              name: 'certman-letsencrypt'
              ruleType: 'ApplicationRule'
              protocols: [
                {
                  port: 443
                  protocolType: 'Https'
                }
                {
                  port: 80
                  protocolType: 'Http'
                }
              ]
              targetFqdns: [
                'letsencrypt.org'
                '*.letsencrypt.org'
              ]
              sourceAddresses: [
                vnetAksSubnetAddressPrefix
              ]
            }
          ] : [], certManagerFW && !empty(appDnsZoneName) ? [
            {
              name: 'certman-appDnsZoneName'
              ruleType: 'ApplicationRule'
              protocols: [
                {
                  port: 443
                  protocolType: 'Https'
                }
                {
                  port: 80
                  protocolType: 'Http'
                }
              ]
              targetFqdns: [
                appDnsZoneName
                '*.${appDnsZoneName}'
              ]
              sourceAddresses: [
                vnetAksSubnetAddressPrefix
              ]
            }
          ] : [])
      }
    ]
  }
}
