param keyVaultName string
param principalId string
param apiPrincipalId string
param cosmosDbId string
param connectionStringKey string

module apiKeyVaultAccess 'br/public:avm/res/key-vault/vault:0.3.5' = {
  name: 'api-keyvault-access'
  params: {
    name: keyVaultName
    enableRbacAuthorization: false
    accessPolicies: [
      {
        objectId: principalId
        permissions: {
          secrets: [ 'get', 'list' ]
        }
      }
      {
        objectId: apiPrincipalId
        permissions: {
          secrets: [ 'get', 'list' ]
        }
      }
    ]
    secrets: {
      secureList: [
        {
          name: connectionStringKey
          value: listConnectionStrings(cosmosDbId, '2022-08-15').connectionStrings[0].connectionString
        }
      ]
    }
  }
}
