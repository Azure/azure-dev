param name string
param keyVaultName string
param secretName string

resource account 'Microsoft.CognitiveServices/accounts@2023-05-01' existing = {
  name: name
}

resource keyVault 'Microsoft.KeyVault/vaults@2022-07-01' existing = {
  name: keyVaultName
}

resource secret 'Microsoft.KeyVault/vaults/secrets@2022-07-01' = {
  name: secretName
  parent: keyVault
  properties: {
    contentType: 'string'
    attributes: {
      enabled: true
      exp: 0
      nbf: 0
    }
    value: account.listKeys().key1
  }
}
