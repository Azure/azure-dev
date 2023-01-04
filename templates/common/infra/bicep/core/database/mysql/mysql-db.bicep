param serverName string
param databaseName string
param location string = resourceGroup().location
param tags object = {}

param keyVaultName string
@description('Database administrator login name')
@minLength(1)
param serverAdminName string

// this is not the password, but the key used to load password from Key Vault
#disable-next-line secure-secrets-in-params
param serverAdminPasswordKey string = 'MYSQL-PASS'

@description('Database administrator password')
@minLength(8)
@secure()
param serverAdminPassword string

// The database server
module server 'mysql-server.bicep' = {
  name: 'mysql-server'
  params: {
    name: serverName
    location: location
    tags: tags
    adminName: serverAdminName
    adminPassword: serverAdminPassword
    adminPasswordKey: serverAdminPasswordKey
    keyVaultName: keyVaultName
  }
}

resource database 'Microsoft.DBforMySQL/flexibleServers/databases@2021-05-01' = {
  name: '${serverName}/${databaseName}'
  properties: {
    charset: 'utf8'
    collation: 'utf8_general_ci'
  }

  dependsOn: [
    server
  ]
}

// this is not the password, but the key used to load password from Key Vault
#disable-next-line outputs-should-not-contain-secrets
output serverAdminPasswordKey string = serverAdminPasswordKey
output databaseName string = databaseName
output endpoint string = 'jdbc:mysql://${server.outputs.fullyQualifiedDomainName}:3306/${databaseName}?useSSL=true&requireSSL=false'
