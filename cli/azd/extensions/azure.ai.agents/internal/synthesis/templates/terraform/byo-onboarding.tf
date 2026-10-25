locals {
  byo_enabled          = var.byo_onboarding != null
  byo_rg_supplied      = local.byo_enabled ? var.byo_onboarding.resourceGroupSupplied : false
  byo_tags             = merge(var.tags, { "azd-byoaks-owner" = local.byo_enabled ? var.byo_onboarding.owner : "" })
  hosting_identity_id  = local.byo_enabled ? azapi_resource.byo_identity["hosting"].id : var.agent_hosting.hostingManagementIdentityResourceId
  workload_identity_id = local.byo_enabled ? azapi_resource.byo_identity["workload"].id : var.agent_hosting.workloadIdentityResourceId
  hosting_storage_id   = local.byo_enabled ? var.byo_onboarding.storageAccountResourceId : var.agent_hosting.storageAccountResourceId
  hosting_cluster_id   = local.byo_enabled ? var.byo_onboarding.clusterResourceId : var.agent_hosting.clusterResourceId
  byo_registry_role = local.byo_enabled ? (
    var.byo_onboarding.registryABAC ? "b93aa761-3e63-49ed-ac28-beffa264f7ac" : "7f951dda-4ed3-4680-a7ca-43fe172d538d"
  ) : ""
  byo_grants = local.byo_enabled ? merge({
    cluster_reader = {
      scope     = var.byo_onboarding.clusterResourceId
      principal = azapi_resource.byo_identity["hosting"].output.properties.principalId
      role      = "/subscriptions/${split("/", var.byo_onboarding.clusterResourceId)[2]}/providers/Microsoft.Authorization/roleDefinitions/acdd72a7-3385-48ef-bd42-f606fba81ae7"
    }
    cluster_extension = {
      scope     = var.byo_onboarding.clusterResourceId
      principal = azapi_resource.byo_identity["hosting"].output.properties.principalId
      role      = "/subscriptions/${split("/", var.byo_onboarding.clusterResourceId)[2]}/providers/Microsoft.Authorization/roleDefinitions/85cb6faf-e071-4c9b-8136-154b5a04f717"
    }
    federation = {
      scope     = azapi_resource.byo_identity["workload"].id
      principal = azapi_resource.byo_identity["hosting"].output.properties.principalId
      role      = var.byo_onboarding.federatedIdentityRoleDefinitionId
    }
    storage = {
      scope     = var.byo_onboarding.storageAccountResourceId
      principal = azapi_resource.byo_identity["workload"].output.properties.principalId
      role      = "/subscriptions/${split("/", var.byo_onboarding.storageAccountResourceId)[2]}/providers/Microsoft.Authorization/roleDefinitions/ba92f5b4-2d11-453d-a403-e96b0029c9fe"
    }
    }, var.byo_onboarding.kubeletRoleAssignmentId == "" ? {
    kubelet = {
      scope     = var.byo_onboarding.registryResourceId
      principal = var.byo_onboarding.kubeletObjectId
      role      = "/subscriptions/${split("/", var.byo_onboarding.registryResourceId)[2]}/providers/Microsoft.Authorization/roleDefinitions/${local.byo_registry_role}"
    }
  } : {}) : {}
}

resource "azapi_resource" "byo_identity" {
  for_each = local.byo_enabled ? {
    hosting  = var.byo_onboarding.hostingIdentityName
    workload = var.byo_onboarding.workloadIdentityName
  } : {}
  type                   = "Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31"
  name                   = each.value
  parent_id              = local.resource_group_id
  location               = var.location
  tags                   = local.byo_tags
  body                   = {}
  response_export_values = ["properties.principalId"]
}

resource "azapi_resource" "byo_storage" {
  count     = local.byo_enabled ? (var.byo_onboarding.storageSupplied ? 0 : 1) : 0
  type      = "Microsoft.Storage/storageAccounts@2023-05-01"
  name      = var.byo_onboarding.storageName
  parent_id = local.resource_group_id
  location  = var.location
  tags      = local.byo_tags
  body = {
    kind = "StorageV2"
    sku  = { name = var.byo_onboarding.storageSku }
    properties = {
      minimumTlsVersion        = "TLS1_2"
      supportsHttpsTrafficOnly = true
      allowBlobPublicAccess    = false
      allowSharedKeyAccess     = false
    }
  }
}

resource "azapi_resource" "byo_registry" {
  count     = local.byo_enabled ? (var.byo_onboarding.registrySupplied ? 0 : 1) : 0
  type      = "Microsoft.ContainerRegistry/registries@2023-07-01"
  name      = var.byo_onboarding.registryName
  parent_id = local.resource_group_id
  location  = var.location
  tags      = local.byo_tags
  body = {
    sku = { name = "Premium" }
    properties = {
      adminUserEnabled    = false
      publicNetworkAccess = "Enabled"
    }
  }
}

resource "azapi_resource" "byo_grant" {
  for_each  = local.byo_grants
  type      = "Microsoft.Authorization/roleAssignments@2022-04-01"
  name      = uuidv5("11fb06fb-712d-4ddd-98c7-e71bbd588830", "${each.value.scope}-${each.value.principal}-${each.value.role}")
  parent_id = each.value.scope
  body = {
    properties = {
      principalId      = each.value.principal
      principalType    = "ServicePrincipal"
      roleDefinitionId = each.value.role
      description      = var.byo_onboarding.owner
    }
  }
  depends_on = [azapi_resource.byo_storage, azapi_resource.byo_registry]
}
