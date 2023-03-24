output "AZURE_POSTGRESQL_DATABASE_NAME" {
  value     = azurerm_postgresql_flexible_server_database.database.name
  sensitive = true
}

output "AZURE_POSTGRESQL_FQDN" {
  value = azurerm_postgresql_flexible_server.psqlServer.fqdn
}

output "AZURE_POSTGRESQL_SPRING_DATASOURCE_URL" {
  value = "jdbc:postgresql://${azurerm_postgresql_flexible_server.psqlServer.fqdn}:5432/${azurerm_postgresql_flexible_server_database.database.name}?sslmode=require"
}

output "AZURE_POSTGRESQL_ADMIN_USERNAME" {
  value = azurerm_postgresql_flexible_server_active_directory_administrator.aad_admin.principal_name
}