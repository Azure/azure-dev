param databaseName string = ''
param serverName string = ''

resource database 'Microsoft.DBforMySQL/flexibleServers/databases@2021-05-01' = {
  parent: server
  name: databaseName
  properties: {
    charset: 'utf8'
    collation: 'utf8_general_ci'
  }
}

resource server 'Microsoft.DBforMySQL/flexibleServers@2021-05-01' existing = {
  name: serverName
}

output endpoint string = 'jdbc:mysql://${server.properties.fullyQualifiedDomainName}:3306/${databaseName}?useSSL=true&requireSSL=false'
output databaseName string = database.name
