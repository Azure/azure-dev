param registryName string
param principalId string
param postgresServerName string
param mysqlServerName string
param outboundIps array

resource registry 'Microsoft.ContainerRegistry/registries@2023-07-01' existing = {
  name: registryName
}

resource pull 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: registry
  name: guid(registry.id, principalId, 'AcrPull')
  properties: {
    principalId: principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '7f951dda-4ed3-4680-a7ca-43fe172d538d')
  }
}

resource postgres 'Microsoft.DBforPostgreSQL/flexibleServers@2024-08-01' existing = {
  name: postgresServerName
}

resource firewall 'Microsoft.DBforPostgreSQL/flexibleServers/firewallRules@2024-08-01' = [
  for ip in union(outboundIps, []): if (postgresServerName != '') {
    parent: postgres
    name: 'aca-${replace(ip, '.', '-')}'
    properties: { startIpAddress: ip, endIpAddress: ip }
  }
]

resource mysql 'Microsoft.DBforMySQL/flexibleServers@2023-12-30' existing = {
  name: mysqlServerName
}

resource mysqlFirewall 'Microsoft.DBforMySQL/flexibleServers/firewallRules@2023-12-30' = [
  for ip in union(outboundIps, []): if (mysqlServerName != '') {
    parent: mysql
    name: 'aca-${replace(ip, '.', '-')}'
    properties: { startIpAddress: ip, endIpAddress: ip }
  }
]
