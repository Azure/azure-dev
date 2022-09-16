locals {
  tags             = { azd-env-name : var.environment_name }
  sha              = base64encode(sha256("${var.environment_name}${var.location}${data.azurerm_client_config.current.subscription_id}"))
  resource_token   = substr(replace(lower(local.sha), "[^A-Za-z0-9_]", ""), 0, 13)
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

# ------------------------------------------------------------------------------------------------------
# Deploy application insights
# ------------------------------------------------------------------------------------------------------
module "applicationinsights" {
  source           = "../../../../../../common/infra/terraform/applicationinsights"
  location         = var.location
  rg_name          = azurerm_resource_group.rg.name
  environment_name = var.environment_name
  workspace_id     = module.loganalytics.LOGANALYTICS_WORKSPACE_ID
  tags             = azurerm_resource_group.rg.tags
  resource_token   = local.resource_token
}

# ------------------------------------------------------------------------------------------------------
# Deploy log analytics
# ------------------------------------------------------------------------------------------------------
module "loganalytics" {
  source         = "../../../../../../common/infra/terraform/loganalytics"
  location       = var.location
  rg_name        = azurerm_resource_group.rg.name
  tags           = azurerm_resource_group.rg.tags
  resource_token = local.resource_token
}

# ------------------------------------------------------------------------------------------------------
# Deploy key vault
# ------------------------------------------------------------------------------------------------------
module "keyvault" {
  source                   = "../../../../../../common/infra/terraform/keyvault"
  location                 = var.location
  principal_id             = var.principal_id
  rg_name                  = azurerm_resource_group.rg.name
  tags                     = azurerm_resource_group.rg.tags
  resource_token           = local.resource_token
  access_policy_object_ids = [module.appserviceapi.APP_OBJECT_ID]
  secrets = [
    {
      name  = "AZURE-COSMOS-CONNECTION-STRING"
      value =  module.cosmos.AZURE_COSMOS_CONNECTION_STRING_KEY
    }
  ]
}

# ------------------------------------------------------------------------------------------------------
# Deploy cosmos
# ------------------------------------------------------------------------------------------------------
module "cosmos" {
  source         = "../../../../../../common/infra/terraform/cosmos"
  location       = var.location
  rg_name        = azurerm_resource_group.rg.name
  tags           = azurerm_resource_group.rg.tags
  resource_token = local.resource_token
}

# ------------------------------------------------------------------------------------------------------
# Deploy app service plan
# ------------------------------------------------------------------------------------------------------
module "appserviceplan" {
  source         = "../../../../../../common/infra/terraform/host/appserviceplan"
  location       = var.location
  rg_name        = azurerm_resource_group.rg.name
  tags           = azurerm_resource_group.rg.tags
  resource_token = local.resource_token
  os_type        = "Linux"
  sku_name       = "B1"
}

# ------------------------------------------------------------------------------------------------------
# Deploy app service web app
# ------------------------------------------------------------------------------------------------------
module "appserviceweb" {
  source         = "../../../../../../common/infra/terraform/host/appservice/appservicenode"
  location       = var.location
  rg_name        = azurerm_resource_group.rg.name
  resource_token = local.resource_token

  tags               = merge(local.tags, { azd-service-name : "web" })
  service_name       = "web"
  appservice_plan_id = module.appserviceplan.APPSERVICE_PLAN_ID
  app_settings = {
    "SCM_DO_BUILD_DURING_DEPLOYMENT"        = "false"
    "APPLICATIONINSIGHTS_CONNECTION_STRING" = module.applicationinsights.APPLICATIONINSIGHTS_CONNECTION_STRING
  }

  app_command_line = "pm2 serve /home/site/wwwroot --no-daemon --spa"
  node_version     = "16-lts"
}

# ------------------------------------------------------------------------------------------------------
# Deploy app service api
# ------------------------------------------------------------------------------------------------------
module "appserviceapi" {
  source         = "../../../../../../common/infra/terraform/host/appservice/appservicepython"
  location       = var.location
  rg_name        = azurerm_resource_group.rg.name
  resource_token = local.resource_token

  tags               = merge(local.tags, { "azd-service-name" : "api" })
  service_name       = "api"
  appservice_plan_id = module.appserviceplan.APPSERVICE_PLAN_ID
  app_settings = {
    "AZURE_COSMOS_CONNECTION_STRING_KEY"    = "AZURE-COSMOS-CONNECTION-STRING"
    "AZURE_COSMOS_DATABASE_NAME"            = module.cosmos.AZURE_COSMOS_DATABASE_NAME
    "SCM_DO_BUILD_DURING_DEPLOYMENT"        = "true"
    "AZURE_KEY_VAULT_ENDPOINT"              = module.keyvault.AZURE_KEY_VAULT_ENDPOINT
    "APPLICATIONINSIGHTS_CONNECTION_STRING" = module.applicationinsights.APPLICATIONINSIGHTS_CONNECTION_STRING
  }

  app_command_line = local.api_command_line
  python_version   = "3.8"
  identity = [{
    type = "SystemAssigned"
  }]
}