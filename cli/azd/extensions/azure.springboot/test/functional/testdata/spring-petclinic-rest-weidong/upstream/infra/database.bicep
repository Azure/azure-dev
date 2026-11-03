param location string
@allowed(['small', 'larger'])
param size string
param administratorObjectId string
param administratorName string
@minLength(1)
param outboundIps array

var skuName = size == 'small' ? 'Standard_B1ms' : 'Standard_B2s'

resource server 'Microsoft.DBforPostgreSQL/flexibleServers@2024-08-01' = {
  name: 'pg-petclinic-${uniqueString(resourceGroup().id)}'
  location: location
  sku: { name: skuName, tier: 'Burstable' }
  properties: {
    version: '16'
    authConfig: {
      passwordAuth: 'Disabled'
      activeDirectoryAuth: 'Enabled'
      tenantId: tenant().tenantId
    }
    storage: { storageSizeGB: 32, autoGrow: 'Enabled' }
    backup: { backupRetentionDays: 7, geoRedundantBackup: 'Disabled' }
    highAvailability: { mode: 'Disabled' }
    network: { publicNetworkAccess: 'Enabled' }
  }
}

resource administrator 'Microsoft.DBforPostgreSQL/flexibleServers/administrators@2024-08-01' = {
  parent: server
  name: administratorObjectId
  // Entra administrator creation requires an accessible, configured server.
  dependsOn: [database, requireTls]
  properties: {
    principalName: administratorName
    principalType: 'User'
    tenantId: tenant().tenantId
  }
}

resource database 'Microsoft.DBforPostgreSQL/flexibleServers/databases@2024-08-01' = {
  parent: server
  name: 'petclinic'
  properties: { charset: 'UTF8', collation: 'en_US.utf8' }
}

resource requireTls 'Microsoft.DBforPostgreSQL/flexibleServers/configurations@2024-08-01' = {
  parent: server
  name: 'require_secure_transport'
  properties: { value: 'on', source: 'user-override' }
}

resource firewall 'Microsoft.DBforPostgreSQL/flexibleServers/firewallRules@2024-08-01' = [
  for ip in union(outboundIps, []): {
    parent: server
    name: 'aca-${replace(ip, '.', '-')}'
    properties: { startIpAddress: ip, endIpAddress: ip }
  }
]

// HTTPS and PostgreSQL traffic can use different client egress IPs.
// The postprovision hook removes this temporary rule on success or failure.
resource bootstrapFirewall 'Microsoft.DBforPostgreSQL/flexibleServers/firewallRules@2024-08-01' = {
  parent: server
  name: 'bootstrap-local-ip'
  properties: { startIpAddress: '0.0.0.0', endIpAddress: '255.255.255.255' }
}

output host string = server.properties.fullyQualifiedDomainName
output serverId string = server.id
