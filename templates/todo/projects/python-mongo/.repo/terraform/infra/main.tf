locals {
  tags             = { azd-env-name : var.environment_name }
  api_command_line = "gunicorn --workers 4 --threads 2 --timeout 60 --access-logfile \"-\" --error-logfile \"-\" --bind=0.0.0.0:8000 -k uvicorn.workers.UvicornWorker todo.app:app"
}
# ------------------------------------------------------------------------------------------------------
# Deploy resource Group
# ------------------------------------------------------------------------------------------------------
resource "azurecaf_name" "rg_name" {
  name          = var.environment_name
  resource_type = "azurerm_resource_group"
  random_length = 0
  clean_input   = true
}

resource "azurerm_resource_group" "rg" {
  name     = azurecaf_name.rg_name.result
  location = var.location

  tags = local.tags
}

resource "random_string" "resource_token" {
  length  = 10
  upper   = false
  lower   = true
  numeric = false
  special = false

  keepers = {
    env      = var.environment_name,
    location = azurerm_resource_group.rg.location,
    subid    = data.azurerm_client_config.current.subscription_id
  }
}

# ------------------------------------------------------------------------------------------------------
# Deploy application insights
# ------------------------------------------------------------------------------------------------------
module "applicationinsights" {
  source           = "../../../../../../common/infra/terraform/applicationinsights"
  location         = var.location
  rg_name          = azurerm_resource_group.rg.name
  environment_name = var.environment_name
  workspace_id     = azurerm_log_analytics_workspace.workspace.id
  tags             = azurerm_resource_group.rg.tags
  resource_token   = random_string.resource_token.result
}
