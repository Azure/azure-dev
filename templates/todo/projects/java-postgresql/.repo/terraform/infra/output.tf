output "AZURE_POSTGRESQL_ENDPOINT" {
  value     = module.postgresql.AZURE_POSTGRESQL_FQDN
  sensitive = true
}

output "REACT_APP_WEB_BASE_URL" {
  value = module.web.URI
}

output "REACT_APP_API_BASE_URL" {
  value = module.api.URI
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
