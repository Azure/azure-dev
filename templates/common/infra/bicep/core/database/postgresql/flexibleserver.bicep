param name string
param location string = resourceGroup().location
param tags object = {}

param sku object
param storage object

param databaseName string
param administratorLogin string
@secure()
param administratorLoginPassword string

// PostgreSQL version
@allowed(['11', '12', '13', '14', '15'])
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

  resource database 'databases' = {
    name: databaseName
  }

  resource firewall 'firewallRules' = {
    name: 'AllowAllWindowsAzureIps'
    properties: {
        startIpAddress: '0.0.0.0'
        endIpAddress: '0.0.0.0'
    }
  }
}

