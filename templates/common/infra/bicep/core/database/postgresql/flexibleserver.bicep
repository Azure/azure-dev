param name string
param location string = resourceGroup().location
param tags object = {}

param sku object
param storage object
param administratorLogin string
@secure()
param administratorLoginPassword string
param databaseNames array = []
param enableFirewall bool = false

// PostgreSQL version
param version string

resource postgresServer 'Microsoft.DBforPostgreSQL/flexibleServers@2022-01-20-preview' = {
  location: location
  tags: tags
  name: name
  sku: sku
  properties: {
    version: version
    administratorLogin: administratorLogin
    administratorLoginPassword: administratorLoginPassword
    storage: storage
    highAvailability: {
      mode: 'Disabled'
    }
  }

  resource database 'databases' = [for name in databaseNames: {
    name: name
  }]

  resource firewall 'firewallRules' = if (enableFirewall) {
    name: 'postgresql-firwall'
    properties: {
        startIpAddress: '0.0.0.0'
        endIpAddress: '255.255.255.255'
    }
  }
}
