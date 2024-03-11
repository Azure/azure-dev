metadata description = 'Azure Cosmos DB MongoDB vCore cluster'
@maxLength(40)
param name string
param location string = resourceGroup().location
param tags object = {}

@description('Username for admin user')
param administratorLogin string
@secure()
@description('Password for admin user')
@minLength(8)
@maxLength(128)
param administratorLoginPassword string
@description('Whether to allow all IPs or not. Warning: No IP addresses will be blocked and any host on the Internet can access the coordinator in this server group. It is strongly recommended to use this rule only temporarily and only on test clusters that do not contain sensitive data.')
param allowAllIPsFirewall bool = false
@description('Whether to allow Azure internal IPs or not')
param allowAzureIPsFirewall bool = false
@description('IP addresses to allow access to the cluster from')
param allowedSingleIPs array = []
@description('Mode to create the mongo cluster')
param createMode string = 'Default'
@description('Whether high availability is enabled on the node group')
param highAvailabilityMode bool = false
@description('Number of nodes in the node group')
param nodeCount int
@description('Node type deployed in the node group')
param nodeType string = 'Shard'
@description('SKU defines the CPU and memory that is provisioned for each node')
param sku string
@description('Disk storage size for the node group in GB')
param storage int

resource mognoCluster 'Microsoft.DocumentDB/mongoClusters@2023-03-01-preview' = {
  name: name
  tags: tags
  location: location
  properties: {
    administratorLogin: administratorLogin
    administratorLoginPassword: administratorLoginPassword
    createMode: createMode
    nodeGroupSpecs: [
        {
            diskSizeGB: storage
            enableHa: highAvailabilityMode
            kind: 'Shard'
            nodeCount: nodeCount
            sku: sku
        }
    ]
  }

  resource firewall_all 'firewallRules' = if (allowAllIPsFirewall) {
    name: 'allow-all-IPs'
    properties: {
      startIpAddress: '0.0.0.0'
      endIpAddress: '255.255.255.255'
    }
  }

  resource firewall_azure 'firewallRules' = if (allowAzureIPsFirewall) {
    name: 'allow-all-azure-internal-IPs'
    properties: {
      startIpAddress: '0.0.0.0'
      endIpAddress: '0.0.0.0'
    }
  }

  resource firewall_single 'firewallRules' = [for ip in allowedSingleIPs: {
    name: 'allow-single-${replace(ip, '.', '')}'
    properties: {
      startIpAddress: ip
      endIpAddress: ip
    }
  }]

}

output connectionStringKey string = mognoCluster.properties.connectionString
