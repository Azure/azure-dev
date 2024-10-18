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
    contentType: 'string'
    attributes: {
      enabled: true
      exp: 0
      nbf: 0
    }
    value: redisConn.listKeys().primaryKey
  }
}

resource urlSecret 'Microsoft.KeyVault/vaults/secrets@2022-07-01' = {
  name: urlSecretName
  parent: keyVault
  properties: {
    contentType: 'string'
    attributes: {
      enabled: true
      exp: 0
      nbf: 0
    }
    value: 'rediss://:${redisConn.listKeys().primaryKey}@${redisConn.properties.hostName}:${redisConn.properties.sslPort}'
  }
}

