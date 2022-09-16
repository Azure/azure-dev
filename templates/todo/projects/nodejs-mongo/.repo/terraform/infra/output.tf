output "AZURE_COSMOS_CONNECTION_STRING_KEY" {
  value     = module.cosmos.AZURE_COSMOS_CONNECTION_STRING_KEY
  sensitive = true
}

output "AZURE_COSMOS_DATABASE_NAME" {
  value = module.cosmos.AZURE_COSMOS_DATABASE_NAME
}

output "AZURE_KEY_VAULT_ENDPOINT" {
  value     = module.keyvault.AZURE_KEY_VAULT_ENDPOINT
  sensitive = true
}

output "REACT_APP_WEB_BASE_URL" {
  value = "https://${module.appserviceweb.APP_DEFAULT_HOST_NAME}"
}

output "REACT_APP_API_BASE_URL" {
  value = "https://${module.appserviceapi.APP_DEFAULT_HOST_NAME}"
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
