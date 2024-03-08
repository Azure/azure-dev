metadata description = 'Azure Cosmos DB MongoDB vCore cluster'
@maxLength(40)
param name string
param location string = resourceGroup().location
param tags object = {}

param administratorLogin string
@secure()
param administratorLoginPassword string
param allowAllIPsFirewall bool = false
param allowAzureIPsFirewall bool = false
param allowedSingleIPs array = []
param highAvailabilityMode bool = false
param nodeCount int
param sku string
param storage int


resource mognoCluster 'Microsoft.DocumentDB/mongoClusters@2023-03-01-preview' = {
  name: name
  tags: tags
  location: location
  properties: {
    administratorLogin: administratorLogin
    administratorLoginPassword: administratorLoginPassword
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
