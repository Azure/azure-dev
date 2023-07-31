output "AZURE_POSTGRESQL_DATABASE_NAME" {
  value     = azurerm_postgresql_flexible_server_database.database.name
  sensitive = true
}

output "AZURE_POSTGRESQL_FQDN" {
  value = azurerm_postgresql_flexible_server.psql_server.fqdn
}

output "AZURE_POSTGRESQL_SPRING_DATASOURCE_URL" {
  value = "jdbc:postgresql://${azurerm_postgresql_flexible_server.psql_server.fqdn}:5432/${azurerm_postgresql_flexible_server_database.database.name}?sslmode=require"
}

output "AZURE_POSTGRESQL_USERNAME" {
  value = local.psqlUserName
}

output "AZURE_POSTGRESQL_PASSWORD" {
  value     = random_password.password[1].result
  sensitive = true
}