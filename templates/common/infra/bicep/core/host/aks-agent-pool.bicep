param clusterName string

@description('The agent pool name')
param name string

@description('The agent pool configuration')
param config object

@description('Custom tags to apply to the AKS resources')
param tags object = {}

resource aksCluster 'Microsoft.ContainerService/managedClusters@2022-11-02-preview' existing = {
  name: clusterName
}

resource nodePool 'Microsoft.ContainerService/managedClusters/agentPools@2022-11-02-preview' = {
  parent: aksCluster
  name: name
  properties: config
  tags: tags
}
