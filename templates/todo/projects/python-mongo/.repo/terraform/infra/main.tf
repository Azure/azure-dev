locals {
  tags             = { azd-env-name : var.name }
  api_command_line = "gunicorn --workers 4 --threads 2 --timeout 60 --access-logfile \"-\" --error-logfile \"-\" --bind=0.0.0.0:8000 -k uvicorn.workers.UvicornWorker todo.app:app"
}
# ------------------------------------------------------------------------------------------------------
# Deploy resource Group
# ------------------------------------------------------------------------------------------------------
resource "azurecaf_name" "rg_name" {
  name          = var.name
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
}

# ------------------------------------------------------------------------------------------------------
# Deploy application insights
# ------------------------------------------------------------------------------------------------------
module "applicationinsights" {
  source         = "../../../../../../common/infra/terraform"
  location       = var.location
  rg_name        = azurerm_resource_group.rg.name
  env_name       = var.name
  workspace_id   = azurerm_log_analytics_workspace.workspace.id
  tags           = azurerm_resource_group.rg.tags
  resource_token = random_string.resource_token.result
}
