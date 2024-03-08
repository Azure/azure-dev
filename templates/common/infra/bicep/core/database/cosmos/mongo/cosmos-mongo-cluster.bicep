@description('Azure Cosmos DB MongoDB vCore cluster name')
@maxLength(40)
param name string

@description('Location for the cluster.')
param location string = resourceGroup().location

param tags object = {}

@description('Username for admin user')
param administratorLogin string

@secure()
@description('Password for admin user')
@minLength(8)
@maxLength(128)
param administratorLoginPassword string


param sku string
param storage int
param nodeCount int
param highAvailabilityMode bool = false

param allowAzureIPsFirewall bool = false
param allowAllIPsFirewall bool = false
param allowedSingleIPs array = []

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
