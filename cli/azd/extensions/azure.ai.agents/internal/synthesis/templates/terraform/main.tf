# Foundry (AIServices) account, its project, and model deployments.

locals {
  # Resource group name. Falls back to rg-{environment_name} when not provided.
  resource_group_name = (
    var.resource_group_name != "" ? var.resource_group_name : "rg-${local.derived_rg_suffix}"
  )
  derived_rg_suffix = trimsuffix(
    substr(trim(coalesce(var.environment_name, "env"), "-."), 0, 87),
    "-",
  )

  # Token to keep globally-unique resource names stable across re-provisions.
  resource_token = substr(sha1(join("-", compact([
    var.subscription_id,
    local.resource_group_name,
    var.location,
    var.resource_token_salt,
  ]))), 0, 13)

  foundry_account_name = local.byo_enabled ? var.byo_onboarding.accountName : "cog-${local.resource_token}"

  # Project name. Falls back to a sanitized environment_name (3-32 chars) when
  # not provided.
  foundry_project_name = (
    var.foundry_project_name != "" ? var.foundry_project_name : local.derived_project_name
  )

  sanitized_env_name = trim(
    replace(lower(coalesce(var.environment_name, "")), "/[^a-z0-9-]/", "-"),
    "-",
  )
  # substr errors when length exceeds the string, so only truncate when needed.
  capped_env_name = (
    length(local.sanitized_env_name) > 32
    ? substr(local.sanitized_env_name, 0, 32)
    : local.sanitized_env_name
  )
  derived_project_name = (
    length(local.capped_env_name) >= 3
    ? local.capped_env_name
    : (local.capped_env_name == "" ? "foundryproject" : "${local.capped_env_name}prj")
  )

}

resource "azurerm_resource_group" "this" {
  count    = local.byo_rg_supplied ? 0 : 1
  name     = local.resource_group_name
  location = var.location
  tags     = local.byo_enabled ? local.byo_tags : var.tags
}

data "azurerm_resource_group" "existing" {
  count = local.byo_rg_supplied ? 1 : 0
  name  = local.resource_group_name
}

locals {
  resource_group_id = local.byo_rg_supplied ? data.azurerm_resource_group.existing[0].id : azurerm_resource_group.this[0].id
}

# azapi is used so allowProjectManagement can be set (not exposed by
# azurerm_cognitive_account).
resource "azapi_resource" "foundry_account" {
  type                      = "Microsoft.CognitiveServices/accounts@2026-07-15-preview"
  name                      = local.foundry_account_name
  location                  = var.location
  parent_id                 = local.resource_group_id
  tags                      = local.byo_enabled ? local.byo_tags : var.tags
  schema_validation_enabled = false

  identity {
    type         = var.agent_hosting.enabled ? "SystemAssigned, UserAssigned" : "SystemAssigned"
    identity_ids = var.agent_hosting.enabled ? [local.hosting_identity_id] : []
  }

  body = {
    kind = "AIServices"
    sku = {
      name = "S0"
    }
    properties = merge({
      allowProjectManagement = true
      customSubDomainName    = local.foundry_account_name
      publicNetworkAccess    = "Enabled"
      disableLocalAuth       = true
      networkAcls = {
        defaultAction       = "Allow"
        virtualNetworkRules = []
        ipRules             = []
      }
      }, var.agent_hosting.enabled ? {
      agentHostingConfigurations = [{
        hostingType                         = var.agent_hosting.hostingType
        name                                = var.agent_hosting.name
        clusterResourceId                   = local.hosting_cluster_id
        hostingManagementIdentityResourceId = local.hosting_identity_id
        storageAccountResourceId            = local.hosting_storage_id
        workloadIdentityResourceId          = local.workload_identity_id
      }]
    } : {})
  }
  depends_on = [azapi_resource.byo_grant]
}

# Created one at a time; ARM throttles concurrent deployments on one account.
resource "azurerm_cognitive_deployment" "model" {
  for_each = { for d in var.deployments : d.name => d }

  name                 = each.value.name
  cognitive_account_id = azapi_resource.foundry_account.id

  model {
    format  = each.value.model.format
    name    = each.value.model.name
    version = each.value.model.version
  }

  sku {
    name     = each.value.sku.name
    capacity = each.value.sku.capacity
  }
}

# Created after all model deployments complete.
resource "azapi_resource" "project" {
  type      = "Microsoft.CognitiveServices/accounts/projects@2025-06-01"
  name      = local.foundry_project_name
  location  = var.location
  parent_id = azapi_resource.foundry_account.id
  tags      = local.byo_enabled ? local.byo_tags : {}

  identity {
    type = "SystemAssigned"
  }

  body = {
    properties = {
      description = "${local.foundry_project_name} Project"
      displayName = local.foundry_project_name
    }
  }

  response_export_values = ["identity.principalId"]

  depends_on = [azurerm_cognitive_deployment.model]
}
