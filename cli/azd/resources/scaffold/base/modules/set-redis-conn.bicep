param name string
param keyVaultName string
param passwordSecretName string
param urlSecretName string

resource redisConn 'Microsoft.Cache/redis@2024-03-01' existing = {
  name: name
}

resource keyVault 'Microsoft.KeyVault/vaults@2022-07-01' existing = {
  name: keyVaultName
}

resource passwordSecret 'Microsoft.KeyVault/vaults/secrets@2022-07-01' = {
  name: passwordSecretName
  parent: keyVault
  properties: {
    value: redisConn.listKeys().primaryKey
  }
}

resource urlSecret 'Microsoft.KeyVault/vaults/secrets@2022-07-01' = {
  name: urlSecretName
  parent: keyVault
  properties: {
    value: 'rediss://:${redisConn.listKeys().primaryKey}@${redisConn.properties.hostName}:${redisConn.properties.sslPort}'
  }
}

output keyVaultUrlForPass string = 'https://${keyVaultName}${environment().suffixes.keyvaultDns}/secrets/${passwordSecretName}'
output keyVaultUrlForUrl string = 'https://${keyVaultName}${environment().suffixes.keyvaultDns}/secrets/${urlSecretName}'

