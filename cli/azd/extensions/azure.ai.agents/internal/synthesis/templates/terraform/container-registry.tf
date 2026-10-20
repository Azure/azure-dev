# Azure Container Registry for hosted agents that use docker, wired as a
# connection on the Foundry project so the project identity can pull images.
# Premium SKU is required for Foundry registry connections.

locals {
  container_registry_name = "cr${local.resource_token}"

  # https://learn.microsoft.com/azure/role-based-access-control/built-in-roles
  acr_pull_role_id = var.container_registry_abac ? "b93aa761-3e63-49ed-ac28-beffa264f7ac" : "7f951dda-4ed3-4680-a7ca-43fe172d538d"

  project_principal_id = try(azapi_resource.project.output.identity.principalId, "")
  reuse_registry       = var.container_registry_resource_id != ""
  registry_id          = local.reuse_registry ? var.container_registry_resource_id : azurerm_container_registry.this[0].id
  registry_endpoint    = local.reuse_registry ? data.azapi_resource.prepared_registry[0].output.properties.loginServer : azurerm_container_registry.this[0].login_server
  registry_name        = local.reuse_registry ? element(reverse(split("/", var.container_registry_resource_id)), 0) : local.container_registry_name
}

data "azapi_resource" "prepared_registry" {
  count                  = local.reuse_registry ? 1 : 0
  type                   = "Microsoft.ContainerRegistry/registries@2023-07-01"
  resource_id            = var.container_registry_resource_id
  response_export_values = ["properties.loginServer"]
}

resource "azurerm_container_registry" "this" {
  count               = local.reuse_registry ? 0 : 1
  name                = local.container_registry_name
  resource_group_name = azurerm_resource_group.this.name
  location            = azurerm_resource_group.this.location
  tags                = var.tags
  sku                 = "Premium"
  admin_enabled       = false

  identity {
    type = "SystemAssigned"
  }

  public_network_access_enabled = true
  zone_redundancy_enabled       = false
}

# Grants the Foundry project identity AcrPull so hosted agents can pull images.
resource "azurerm_role_assignment" "foundry_acr_pull" {
  scope              = local.registry_id
  role_definition_id = "/subscriptions/${split("/", local.registry_id)[2]}/providers/Microsoft.Authorization/roleDefinitions/${local.acr_pull_role_id}"
  principal_id       = local.project_principal_id
  principal_type     = "ServicePrincipal"
}

# Project connection so Foundry can resolve the registry by name.
# Pinned to 2025-04-01-preview: GA 2025-06-01 cannot resolve the
# projects/connections ContainerRegistry sub-resource (MissingApiVersionParameter).
resource "azapi_resource" "acr_connection" {
  type      = "Microsoft.CognitiveServices/accounts/projects/connections@2025-04-01-preview"
  name      = "${local.registry_name}-conn"
  parent_id = azapi_resource.project.id

  body = {
    properties = {
      category = "ContainerRegistry"
      target   = local.registry_endpoint
      authType = "ManagedIdentity"
      credentials = {
        clientId   = local.project_principal_id
        resourceId = local.registry_id
      }
      isSharedToAll = true
      metadata = {
        ResourceId = local.registry_id
      }
    }
  }

  depends_on = [azurerm_role_assignment.foundry_acr_pull]
}
