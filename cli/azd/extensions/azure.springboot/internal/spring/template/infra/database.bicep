param location string
param engine string
param databaseName string
param authentication string
@secure()
param password string
param administratorObjectId string
param administratorName string

var token = uniqueString(resourceGroup().id)
var managedIdentity = authentication == 'managedIdentity'

resource postgres 'Microsoft.DBforPostgreSQL/flexibleServers@2024-08-01' = if (engine == 'postgresql') {
  name: 'pg-spring-${token}'
  location: location
  sku: { name: 'Standard_B1ms', tier: 'Burstable' }
  properties: union({
    version: '15'
    authConfig: {
      activeDirectoryAuth: managedIdentity ? 'Enabled' : 'Disabled'
      passwordAuth: managedIdentity ? 'Disabled' : 'Enabled'
      tenantId: tenant().tenantId
    }
    storage: { storageSizeGB: 32, autoGrow: 'Enabled' }
    backup: { backupRetentionDays: 7, geoRedundantBackup: 'Disabled' }
    highAvailability: { mode: 'Disabled' }
    network: { publicNetworkAccess: 'Enabled' }
  }, managedIdentity ? {} : {
    administratorLogin: 'azdadmin'
    administratorLoginPassword: password
  })
}

resource pgAdmin 'Microsoft.DBforPostgreSQL/flexibleServers/administrators@2024-08-01' = if (engine == 'postgresql' && managedIdentity) {
  parent: postgres
  name: administratorObjectId
  properties: {
    principalName: administratorName
    principalType: 'User'
    tenantId: tenant().tenantId
  }
}

resource pgDatabase 'Microsoft.DBforPostgreSQL/flexibleServers/databases@2024-08-01' = if (engine == 'postgresql') {
  parent: postgres
  name: databaseName
  properties: { charset: 'UTF8', collation: 'en_US.utf8' }
}

// Temporary public access for PostgreSQL managed-identity bootstrap. The local HTTPS egress IP
// may differ from the TCP 5432 egress IP, so the bootstrap cannot use an exact client allowlist.
// The postprovision bootstrap-postgresql-managed-identity.py hook removes this rule on success or failure.
resource pgFirewall 'Microsoft.DBforPostgreSQL/flexibleServers/firewallRules@2024-08-01' = if (engine == 'postgresql' && managedIdentity) {
  parent: postgres
  name: 'bootstrap-local-ip'
  properties: {
    startIpAddress: '0.0.0.0'
    endIpAddress: '255.255.255.255'
  }
}

resource mysql 'Microsoft.DBforMySQL/flexibleServers@2023-12-30' = if (engine == 'mysql') {
  name: 'mysql-spring-${token}'
  location: location
  sku: { name: 'Standard_B1ms', tier: 'Burstable' }
  properties: {
    version: '8.0.21'
    administratorLogin: 'azdadmin'
    administratorLoginPassword: password
    storage: { storageSizeGB: 32, autoGrow: 'Enabled' }
    backup: { backupRetentionDays: 7, geoRedundantBackup: 'Disabled' }
    highAvailability: { mode: 'Disabled' }
    network: { publicNetworkAccess: 'Enabled' }
  }
}

resource mysqlDatabase 'Microsoft.DBforMySQL/flexibleServers/databases@2023-12-30' = if (engine == 'mysql') {
  parent: mysql
  name: databaseName
  properties: { charset: 'utf8mb4', collation: 'utf8mb4_unicode_ci' }
}

output host string = engine == 'mysql' ? mysql!.properties.fullyQualifiedDomainName : postgres!.properties.fullyQualifiedDomainName
output serverName string = engine == 'mysql' ? mysql!.name : postgres!.name
