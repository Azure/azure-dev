param keyVaultName string

@description('An array of Service Principal IDs')
#disable-next-line secure-secrets-in-params //Disabling validation of this linter rule as param does not contain a secret.
param rbacSecretUserSps array = []

@description('An array of Service Principal IDs')
#disable-next-line secure-secrets-in-params //Disabling validation of this linter rule as param does not contain a secret.
param rbacSecretOfficerSps array = []

@description('An array of Service Principal IDs')
param rbacCertOfficerSps array = []

@description('An array of Service Principal IDs')
param rbacCryptoUserSps array = []

@description('An array of Service Principal IDs')
param rbacCryptoOfficerSps array = []

@description('An array of Service Principal IDs')
param rbacCryptoServiceEncryptSps array = []

@description('An array of Service Principal IDs')
param rbacKvContributorSps array = []

@description('An array of Service Principal IDs')
param rbacAdminSps array = []

@description('An array of User IDs')
param rbacCryptoOfficerUsers array = []

@description('An array of User IDs')
#disable-next-line secure-secrets-in-params //Disabling validation of this linter rule as param does not contain a secret.
param rbacSecretOfficerUsers array = []

@description('An array of User IDs')
param rbacCertOfficerUsers array = []

@description('An array of User IDs')
param rbacAdminUsers array = []

var keyVaultAdministratorRole = subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '00482a5a-887f-4fb3-b363-3b7fe8e74483')
var keyVaultContributorRole = subscriptionResourceId('Microsoft.Authorization/roleDefinitions', 'f25e0fa2-a7c8-4377-a976-54943a77a395')
var keyVaultSecretsUserRole = subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '4633458b-17de-408a-b874-0445c86b69e6')
var keyVaultSecretsOfficerRole = subscriptionResourceId('Microsoft.Authorization/roleDefinitions', 'b86a8fe4-44ce-4948-aee5-eccb2c155cd7')
var keyVaultCertsOfficerRole = subscriptionResourceId('Microsoft.Authorization/roleDefinitions', 'a4417e6f-fecd-4de8-b567-7b0420556985')
var keyVaultCryptoUserRole = subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '12338af0-0e69-4776-bea7-57ae8d297424')
var keyVaultCryptoOfficerRole = subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '14b46e9e-c2b7-41b4-b07b-48a6ebf60603')
var keyVaultCryptoServiceEncrpytionRole = subscriptionResourceId('Microsoft.Authorization/roleDefinitions','e147488a-f6f5-4113-8e2d-b22465e65bf6')

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

resource rbacCryptoUserSp 'Microsoft.Authorization/roleAssignments@2022-04-01' = [for rbacSp in rbacCryptoUserSps : if(!empty(rbacSp)) {
  scope: kv
  name: guid(kv.id, rbacSp, keyVaultCryptoUserRole)
  properties: {
    roleDefinitionId: keyVaultCryptoUserRole
    principalType: 'ServicePrincipal'
    principalId: rbacSp
  }
}]

resource rbacCryptoServiceEncryptionSp 'Microsoft.Authorization/roleAssignments@2022-04-01' = [for rbacSp in rbacCryptoServiceEncryptSps : if(!empty(rbacSp)) {
  scope: kv
  name: guid(kv.id, rbacSp, keyVaultCryptoServiceEncrpytionRole)
  properties: {
    roleDefinitionId: keyVaultCryptoServiceEncrpytionRole
    principalType: 'ServicePrincipal'
    principalId: rbacSp
  }
}]

resource rbacKvContributorSp 'Microsoft.Authorization/roleAssignments@2022-04-01' = [for rbacSp in rbacKvContributorSps : if(!empty(rbacSp)) {
  scope: kv
  name: guid(kv.id, rbacSp, keyVaultContributorRole)
  properties: {
    roleDefinitionId: keyVaultContributorRole
    principalType: 'ServicePrincipal'
    principalId: rbacSp
  }
}]

resource rbacCryptoOfficerSp 'Microsoft.Authorization/roleAssignments@2022-04-01' = [for rbacSp in rbacCryptoOfficerSps : if(!empty(rbacSp)) {
  scope: kv
  name: guid(kv.id, rbacSp, keyVaultCryptoUserRole)
  properties: {
    roleDefinitionId: keyVaultCryptoOfficerRole
    principalType: 'ServicePrincipal'
    principalId: rbacSp
  }
}]

resource rbacAdminSp 'Microsoft.Authorization/roleAssignments@2022-04-01' = [for rbacSp in rbacAdminSps : if(!empty(rbacSp)) {
  scope: kv
  name: guid(kv.id, rbacSp, keyVaultAdministratorRole)
  properties: {
    roleDefinitionId: keyVaultAdministratorRole
    principalType: 'ServicePrincipal'
    principalId: rbacSp
  }
}]

resource rbacCryptoOfficerUser 'Microsoft.Authorization/roleAssignments@2022-04-01' = [for rbacSp in rbacCryptoOfficerUsers : if(!empty(rbacSp)) {
  scope: kv
  name: guid(kv.id, rbacSp, keyVaultCryptoOfficerRole)
  properties: {
    roleDefinitionId: keyVaultCryptoOfficerRole
    principalType: 'User'
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

resource rbacAdminUser 'Microsoft.Authorization/roleAssignments@2022-04-01' = [for rbacSp in rbacAdminUsers : if(!empty(rbacSp)) {
  scope: kv
  name: guid(kv.id, rbacSp, keyVaultAdministratorRole)
  properties: {
    roleDefinitionId: keyVaultAdministratorRole
    principalType: 'User'
    principalId: rbacSp
  }
}]
