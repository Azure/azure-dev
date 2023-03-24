locals {
  tags                         = { azd-env-name : var.environment_name, spring-cloud-azure : true }
  sha                          = base64encode(sha256("${var.environment_name}${var.location}${data.azurerm_client_config.current.subscription_id}"))
  resource_token               = substr(replace(lower(local.sha), "[^A-Za-z0-9_]", ""), 0, 13)
  psql_custom_username         = "CUSTOM_ROLE"
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
  source           = "../../../../../../common/infra/terraform/core/monitor/applicationinsights"
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
  source         = "../../../../../../common/infra/terraform/core/monitor/loganalytics"
  location       = var.location
  rg_name        = azurerm_resource_group.rg.name
  tags           = azurerm_resource_group.rg.tags
  resource_token = local.resource_token
}

# ------------------------------------------------------------------------------------------------------
# Deploy PostgreSQL
# ------------------------------------------------------------------------------------------------------
module "postgresql" {
  source         = "../../../../../../common/infra/terraform/core/database/postgresql"
  location       = var.location
  rg_name        = azurerm_resource_group.rg.name
  tags           = azurerm_resource_group.rg.tags
  resource_token = local.resource_token
  client_id      = var.client_id
}

# ------------------------------------------------------------------------------------------------------
# Deploy app service plan
# ------------------------------------------------------------------------------------------------------
module "appserviceplan" {
  source         = "../../../../../../common/infra/terraform/core/host/appserviceplan"
  location       = var.location
  rg_name        = azurerm_resource_group.rg.name
  tags           = azurerm_resource_group.rg.tags
  resource_token = local.resource_token
}

# ------------------------------------------------------------------------------------------------------
# Deploy app service web app
# ------------------------------------------------------------------------------------------------------
module "web" {
  source         = "../../../../../../common/infra/terraform/core/host/appservice/appservicenode"
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
}

# ------------------------------------------------------------------------------------------------------
# Deploy app service api
# ------------------------------------------------------------------------------------------------------
module "api" {
  source         = "../../../../../../common/infra/terraform/core/host/appservice/appservicejava"
  location       = var.location
  rg_name        = azurerm_resource_group.rg.name
  resource_token = local.resource_token

  tags               = merge(local.tags, { "azd-service-name" : "api" })
  service_name       = "api"
  appservice_plan_id = module.appserviceplan.APPSERVICE_PLAN_ID

  pg_custom_role_name_with_aad_identity = local.psql_custom_username
  pg_aad_admin_user = module.postgresql.AZURE_POSTGRESQL_ADMIN_USERNAME
  pg_database_name = module.postgresql.AZURE_POSTGRESQL_DATABASE_NAME
  pg_server_fqdn = module.postgresql.AZURE_POSTGRESQL_FQDN

  app_settings = {
    "SCM_DO_BUILD_DURING_DEPLOYMENT"        = "true"
    "APPLICATIONINSIGHTS_CONNECTION_STRING" = module.applicationinsights.APPLICATIONINSIGHTS_CONNECTION_STRING
    "AZURE_POSTGRESQL_URL"                  = "jdbc:postgresql://${module.postgresql.AZURE_POSTGRESQL_FQDN}:5432/${module.postgresql.AZURE_POSTGRESQL_DATABASE_NAME}?sslmode=require"
    "AZURE_POSTGRESQL_USERNAME"             = local.psql_custom_username
    "JAVA_OPTS"                             = "-Djdk.attach.allowAttachSelf=true"
  }

  app_command_line = ""

  identity = [{
    type = "SystemAssigned"
  }]
}

# ------------------------------------------------------------------------------------------------------
# Passwordless setting
# ------------------------------------------------------------------------------------------------------
module "psql-passwordless" {
  source         = "../../../../../../common/infra/terraform/core/security/passwordless/postgresql"

  pg_custom_role_name_with_aad_identity     =   local.psql_custom_username
  pg_aad_admin_user                         =   module.postgresql.AZURE_POSTGRESQL_ADMIN_USERNAME
  pg_database_name                          =   module.postgresql.AZURE_POSTGRESQL_DATABASE_NAME
  pg_server_fqdn                            =   module.postgresql.AZURE_POSTGRESQL_FQDN
  hosting_service_aad_identity              =   module.api.IDENTITY_PRINCIPAL_ID
}

# ------------------------------------------------------------------------------------------------------
# Deploy app service apim
# ------------------------------------------------------------------------------------------------------
module "apim" {
  count                     = var.useAPIM ? 1 : 0
  source                    = "../../../../../../common/infra/terraform/core/gateway/apim"
  name                      = "apim-${local.resource_token}"
  location                  = var.location
  rg_name                   = azurerm_resource_group.rg.name
  tags                      = merge(local.tags, { "azd-service-name" : var.environment_name })
  application_insights_name = module.applicationinsights.APPLICATIONINSIGHTS_NAME
  sku                       = "Consumption"
}

# ------------------------------------------------------------------------------------------------------
# Deploy app service apim-api
# ------------------------------------------------------------------------------------------------------
module "apimApi" {
  count                    = var.useAPIM ? 1 : 0
  source                   = "../../../../../../common/infra/terraform/core/gateway/apim-api"
  name                     = module.apim[0].APIM_SERVICE_NAME
  rg_name                  = azurerm_resource_group.rg.name
  web_front_end_url        = module.web.URI
  api_management_logger_id = module.apim[0].API_MANAGEMENT_LOGGER_ID
  api_name                 = "todo-api"
  api_display_name         = "Simple Todo API"
  api_path                 = "todo"
  api_backend_url          = module.api.URI
}