param keyVaultName string

@description('An array of Service Principal IDs')
#disable-next-line secure-secrets-in-params //Disabling validation of this linter rule as param does not contain a secret.
param rbacSecretUserSps array = []

@description('An array of Service Principal IDs')
#disable-next-line secure-secrets-in-params //Disabling validation of this linter rule as param does not contain a secret.
param rbacSecretOfficerSps array = []

@description('An array of Service Principal IDs')
param rbacCertOfficerSps array = []

@description('An array of User IDs')
#disable-next-line secure-secrets-in-params //Disabling validation of this linter rule as param does not contain a secret.
param rbacSecretOfficerUsers array = []

@description('An array of User IDs')
param rbacCertOfficerUsers array = []

var keyVaultSecretsUserRole = resourceId('Microsoft.Authorization/roleDefinitions', '4633458b-17de-408a-b874-0445c86b69e6')
var keyVaultSecretsOfficerRole = resourceId('Microsoft.Authorization/roleDefinitions', 'b86a8fe4-44ce-4948-aee5-eccb2c155cd7')
var keyVaultCertsOfficerRole = resourceId('Microsoft.Authorization/roleDefinitions', 'a4417e6f-fecd-4de8-b567-7b0420556985')

resource kv 'Microsoft.KeyVault/vaults@2021-11-01-preview' existing = {
  name: keyVaultName
}

resource rbacSecretUserSp 'Microsoft.Authorization/roleAssignments@2022-04-01' = [for rbacSp in rbacSecretUserSps : if(!empty(rbacSp)) {
  scope: kv
  name: guid(kv.id, rbacSp, keyVaultSecretsUserRole)
  properties: {
    roleDefinitionId: keyVaultSecretsUserRole
    principalType: 'ServicePrincipal'
    principalId: rbacSp
  }
}]

resource rbacSecretOfficerSp 'Microsoft.Authorization/roleAssignments@2022-04-01' = [for rbacSp in rbacSecretOfficerSps : if(!empty(rbacSp)) {
  scope: kv
  name: guid(kv.id, rbacSp, keyVaultSecretsOfficerRole)
  properties: {
    roleDefinitionId: keyVaultSecretsOfficerRole
    principalType: 'ServicePrincipal'
    principalId: rbacSp
  }
}]

resource rbacCertsOfficerSp 'Microsoft.Authorization/roleAssignments@2022-04-01' = [for rbacSp in rbacCertOfficerSps : if(!empty(rbacSp)) {
  scope: kv
  name: guid(kv.id, rbacSp, keyVaultCertsOfficerRole)
  properties: {
    roleDefinitionId: keyVaultCertsOfficerRole
    principalType: 'ServicePrincipal'
    principalId: rbacSp
  }
}]

resource rbacSecretOfficerUser 'Microsoft.Authorization/roleAssignments@2022-04-01' = [for rbacSp in rbacSecretOfficerUsers : if(!empty(rbacSp)) {
  scope: kv
  name: guid(kv.id, rbacSp, keyVaultSecretsOfficerRole)
  properties: {
    roleDefinitionId: keyVaultSecretsOfficerRole
    principalType: 'User'
    principalId: rbacSp
  }
}]

resource rbacCertsOfficerUser 'Microsoft.Authorization/roleAssignments@2022-04-01' = [for rbacSp in rbacCertOfficerUsers : if(!empty(rbacSp)) {
  scope: kv
  name: guid(kv.id, rbacSp, keyVaultCertsOfficerRole)
  properties: {
    roleDefinitionId: keyVaultCertsOfficerRole
    principalType: 'User'
    principalId: rbacSp
  }
}]
