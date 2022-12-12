param name string = ''
param mysqlServerName string = ''

resource database 'Microsoft.DBforMySQL/flexibleServers/databases@2021-05-01' = {
  parent: mysqlServer
  name: name
  properties: {
    charset: 'utf8'
    collation: 'utf8_general_ci'
  }
}

resource mysqlServer 'Microsoft.DBforMySQL/flexibleServers@2021-05-01' existing = {
  name: mysqlServerName
}

output jdbcUrl string = 'jdbc:mysql://${mysqlServer.properties.fullyQualifiedDomainName}:3306/${name}?useSSL=true&requireSSL=false'
output databaseName string = database.name
