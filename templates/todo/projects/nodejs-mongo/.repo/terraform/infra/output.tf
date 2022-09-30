output "AZURE_COSMOS_CONNECTION_STRING_KEY" {
  value = local.cosmos_connection_string_key
}

output "AZURE_COSMOS_DATABASE_NAME" {
  value = module.cosmos.AZURE_COSMOS_DATABASE_NAME
}

output "AZURE_KEY_VAULT_ENDPOINT" {
  value     = module.keyvault.AZURE_KEY_VAULT_ENDPOINT
  sensitive = true
}

output "REACT_APP_WEB_BASE_URL" {
  value = "https://${module.appserviceweb.DEFAULT_HOST_NAME}"
}

output "REACT_APP_API_BASE_URL" {
  value = "https://${module.appserviceapi.DEFAULT_HOST_NAME}"
}

output "AZURE_LOCATION" {
  value = var.location
}

output "APPLICATIONINSIGHTS_CONNECTION_STRING" {
  value     = module.applicationinsights.APPLICATIONINSIGHTS_CONNECTION_STRING
  sensitive = true
}

output "REACT_APP_APPLICATIONINSIGHTS_CONNECTION_STRING" {
  value     = module.applicationinsights.APPLICATIONINSIGHTS_CONNECTION_STRING
  sensitive = true
}
