param AksName string

param PoolName string

@description('The zones to use for a node pool')
param availabilityZones array = []

@description('OS disk type')
param osDiskType string

@description('VM SKU')
param agentVMSize string

@description('Disk size in GB')
param osDiskSizeGB int = 0

@description('The number of agents for the user node pool')
param agentCount int = 1

@description('The maximum number of nodes for the user node pool')
param agentCountMax int = 3
var autoScale = agentCountMax > agentCount

@description('The maximum number of pods per node.')
param maxPods int = 30

@description('Any taints that should be applied to the node pool')
param nodeTaints array = []

@description('Any labels that should be applied to the node pool')
param nodeLabels object = {}

@description('The subnet the node pool will use')
param subnetId string

@description('OS Type for the node pool')
@allowed(['Linux','Windows'])
param osType string

@allowed(['Ubuntu','Windows2019','Windows2022'])
param osSKU string

@description('Assign a public IP per node')
param enableNodePublicIP bool = false

@description('Apply a default sku taint to Windows node pools')
param autoTaintWindows bool = false

var taints = autoTaintWindows ? union(nodeTaints, ['sku=Windows:NoSchedule']) : nodeTaints

resource aks 'Microsoft.ContainerService/managedClusters@2021-10-01' existing = {
  name: AksName
}

resource userNodepool 'Microsoft.ContainerService/managedClusters/agentPools@2021-10-01' = {
  parent: aks
  name: PoolName
  properties: {
    mode: 'User'
    vmSize: agentVMSize
    count: agentCount
    minCount: autoScale ? agentCount : json('null')
    maxCount: autoScale ? agentCountMax : json('null')
    enableAutoScaling: autoScale
    availabilityZones: !empty(availabilityZones) ? availabilityZones : null
    osDiskType: osDiskType
    osSKU: osSKU
    osDiskSizeGB: osDiskSizeGB
    osType: osType
    maxPods: maxPods
    type: 'VirtualMachineScaleSets'
    vnetSubnetID: !empty(subnetId) ? subnetId : json('null')
    upgradeSettings: {
      maxSurge: '33%'
    }
    nodeTaints: taints
    nodeLabels: nodeLabels
    enableNodePublicIP: enableNodePublicIP
  }
}
