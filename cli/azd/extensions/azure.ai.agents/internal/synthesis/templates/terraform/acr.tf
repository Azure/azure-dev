# Azure Container Registry for hosted agents that use docker. Wires the
# registry as a connection on the Foundry project so the project's managed
# identity can pull images. Gated on include_acr (set true by the eject step
# when any agent declares a docker: block in azure.yaml).
#
# This is the Terraform equivalent of internal/synthesis/templates/modules/acr.bicep.
# Premium SKU is intentional: Foundry recommends Premium so the registry can
# support content trust and geo-replication if enabled post-provision.

locals {
  # https://learn.microsoft.com/azure/role-based-access-control/built-in-roles
  acr_pull_role_id = "7f951dda-4ed3-4680-a7ca-43fe172d538d"

  # principalId of the Foundry project managed identity; receives AcrPull and
  # is the connection credential identity.
  project_principal_id = try(azapi_resource.project.output.identity.principalId, "")
}

resource "azurerm_container_registry" "this" {
  count = var.include_acr ? 1 : 0

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

# Grant the Foundry project's managed identity AcrPull on this registry so the
# hosted agent can pull images using the project identity.
resource "azurerm_role_assignment" "foundry_acr_pull" {
  count = var.include_acr ? 1 : 0

  scope              = azurerm_container_registry.this[0].id
  role_definition_id = "/subscriptions/${var.subscription_id}/providers/Microsoft.Authorization/roleDefinitions/${local.acr_pull_role_id}"
  principal_id       = local.project_principal_id
  principal_type     = "ServicePrincipal"
}

# Project-scoped connection so Foundry can resolve the registry by name.
# Pinned to 2025-04-01-preview: GA 2025-06-01 fails to resolve the
# projects/connections ContainerRegistry sub-resource (MissingApiVersionParameter).
resource "azapi_resource" "acr_connection" {
  count = var.include_acr ? 1 : 0

  type      = "Microsoft.CognitiveServices/accounts/projects/connections@2025-04-01-preview"
  name      = "${local.container_registry_name}-conn"
  parent_id = azapi_resource.project.id

  body = {
    properties = {
      category = "ContainerRegistry"
      target   = azurerm_container_registry.this[0].login_server
      authType = "ManagedIdentity"
      # RegistryIdentity auth requires both the identity client id (the project
      # principal) and the registry resource id.
      credentials = {
        clientId   = local.project_principal_id
        resourceId = azurerm_container_registry.this[0].id
      }
      isSharedToAll = true
      metadata = {
        ResourceId = azurerm_container_registry.this[0].id
      }
    }
  }

  depends_on = [azurerm_role_assignment.foundry_acr_pull]
}
