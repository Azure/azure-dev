# Resource-group-scoped resources for a microsoft.foundry service: the Foundry
# (AIServices) account, its project, model deployments, the optional container
# registry, and the developer role assignment.
#
# This is the Terraform equivalent of internal/synthesis/templates/main.bicep +
# modules/resources.bicep. It provisions the same resources and emits the same
# output contract (see outputs.tf), so the agent deploy + RBAC flow is
# unchanged on the Terraform path.

locals {
  # Deterministic token to vary resource names, mirroring the Bicep template's
  # uniqueString(subscription().id, resourceGroup().id, location[, salt]).
  # Terraform has no uniqueString; sha1 over the same inputs is stable across
  # re-provisions and keeps the cog-/cr prefixes from abbreviations.json.
  resource_token = substr(sha1(join("-", compact([
    var.subscription_id,
    var.resource_group_name,
    var.location,
    var.resource_token_salt,
  ]))), 0, 13)

  foundry_account_name    = "cog-${local.resource_token}"
  container_registry_name = "cr${local.resource_token}"

  # Foundry project name. When foundry_project_name is empty (e.g. the bicepless
  # default flow where AZURE_AI_PROJECT_NAME is unset), derive it from
  # environment_name, mirroring the Bicep provider's sanitizeFoundryName:
  # lowercase, non [a-z0-9-] -> '-', trim '-', cap at 32, pad to >= 3 chars.
  foundry_project_name = (
    var.foundry_project_name != "" ? var.foundry_project_name : local.derived_project_name
  )

  # Sanitize environment_name into a valid project name candidate.
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

  # Built-in role definition ids.
  # https://learn.microsoft.com/azure/role-based-access-control/built-in-roles
  cognitive_services_user_role_id = "a97b65f3-24c7-4388-baec-2e87135dc908"
}

resource "azurerm_resource_group" "this" {
  name     = var.resource_group_name
  location = var.location
  tags     = var.tags
}

# Foundry AIServices account. Uses azapi so allowProjectManagement and the
# disableLocalAuth/networkAcls body match the Bicep template exactly (the
# azurerm_cognitive_account resource does not expose allowProjectManagement).
resource "azapi_resource" "foundry_account" {
  type      = "Microsoft.CognitiveServices/accounts@2025-06-01"
  name      = local.foundry_account_name
  location  = azurerm_resource_group.this.location
  parent_id = azurerm_resource_group.this.id
  tags      = var.tags

  identity {
    type = "SystemAssigned"
  }

  body = {
    kind = "AIServices"
    sku = {
      name = "S0"
    }
    properties = {
      allowProjectManagement = true
      customSubDomainName    = local.foundry_account_name
      publicNetworkAccess    = "Enabled"
      disableLocalAuth       = true
      networkAcls = {
        defaultAction       = "Allow"
        virtualNetworkRules = []
        ipRules             = []
      }
    }
  }
}

# Model deployments. ARM throttles concurrent deployments on the same account,
# so they are created one at a time via the chain below (the Bicep template
# uses @batchSize(1) for the same reason).
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

# Foundry project. Created after all model deployments complete (the project
# does not reference them, so the dependency is declared explicitly to mirror
# the Bicep dependsOn).
resource "azapi_resource" "project" {
  type      = "Microsoft.CognitiveServices/accounts/projects@2025-06-01"
  name      = local.foundry_project_name
  location  = azurerm_resource_group.this.location
  parent_id = azapi_resource.foundry_account.id

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

# Grant the developer Cognitive Services User on the project so they can call
# the Foundry data-plane (chat/completions, agents API) from their machine.
resource "azurerm_role_assignment" "developer_cognitive_services_user" {
  count = var.principal_id == "" ? 0 : 1

  scope              = azapi_resource.project.id
  role_definition_id = "/subscriptions/${var.subscription_id}/providers/Microsoft.Authorization/roleDefinitions/${local.cognitive_services_user_role_id}"
  principal_id       = var.principal_id
  principal_type     = var.principal_type
}
