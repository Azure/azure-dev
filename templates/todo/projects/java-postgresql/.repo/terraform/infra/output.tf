output "AZURE_POSTGRESQL_ENDPOINT" {
  value     = module.postgresql.AZURE_POSTGRESQL_FQDN
  sensitive = true
}

output "REACT_APP_WEB_BASE_URL" {
  value = module.web.URI
}

output "REACT_APP_API_BASE_URL" {
  value = var.useAPIM ? module.apimApi[0].SERVICE_API_URI : module.api.URI
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

output "AZURE_KEY_VAULT_ENDPOINT" {
  value     = module.keyvault.AZURE_KEY_VAULT_ENDPOINT
  sensitive = true
}