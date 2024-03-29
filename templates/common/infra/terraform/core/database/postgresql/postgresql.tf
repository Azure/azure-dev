locals {
  psqlUserName = "psqluser"
}

terraform {
  required_providers {
    azurerm = {
      version = "~>3.97.1"
      source  = "hashicorp/azurerm"
    }
    azurecaf = {
      source  = "aztfmod/azurecaf"
      version = "~>1.2.24"
    }
  }
}
# ------------------------------------------------------------------------------------------------------
# Deploy PostgreSQL Server
# ------------------------------------------------------------------------------------------------------
resource "azurecaf_name" "psql_name" {
  name          = var.resource_token
  resource_type = "azurerm_postgresql_flexible_server"
  random_length = 0
  clean_input   = true
}

resource "random_password" "password" {
  count            = 2
  length           = 32
  special          = true
  override_special = "_%@"
}

resource "azurerm_postgresql_flexible_server" "psql_server" {
  name                   = azurecaf_name.psql_name.result
  location               = var.location
  resource_group_name    = var.rg_name
  tags                   = var.tags
  version                = "12"
  administrator_login    = var.administrator_login
  administrator_password = random_password.password[0].result
  zone                   = "1"

  storage_mb = 32768

  sku_name = "GP_Standard_D4s_v3"
}


resource "azurerm_postgresql_flexible_server_firewall_rule" "firewall_rule" {
  name             = "AllowAllFireWallRule"
  server_id        = azurerm_postgresql_flexible_server.psql_server.id
  start_ip_address = "0.0.0.0"
  end_ip_address   = "255.255.255.255"
}

resource "azurerm_postgresql_flexible_server_database" "database" {
  name      = var.database_name
  server_id = azurerm_postgresql_flexible_server.psql_server.id
  collation = "en_US.utf8"
  charset   = "utf8"
}

resource "azurerm_resource_deployment_script_azure_cli" "psql-script" {
  name                = "psql-script-${var.resource_token}"
  resource_group_name = var.rg_name
  location            = var.location
  version             = "2.40.0"
  retention_interval  = "PT1H"
  cleanup_preference  = "OnSuccess"
  timeout             = "PT5M"

  environment_variable {
    name              = "PSQLADMINNAME"
    value             = azurerm_postgresql_flexible_server.psql_server.administrator_login
  }
  environment_variable {
    name              = "PSQLADMINPASSWORD"
    value             = random_password.password[0].result
  }
  environment_variable {
    name              = "PSQLUSERNAME"
    value             = local.psqlUserName
  }
  environment_variable {
    name              = "PSQLUSERPASSWORD"
    value             = random_password.password[1].result
  }
  environment_variable {
    name              = "DBNAME"
    value             = var.database_name
  }
  environment_variable {
    name              = "DBSERVER"
    value             = azurerm_postgresql_flexible_server.psql_server.fqdn
  }

  script_content = <<-EOT

      apk add postgresql-client

      cat << EOF > create_user.sql
      CREATE ROLE "$PSQLUSERNAME" WITH LOGIN PASSWORD '$PSQLUSERPASSWORD';
      GRANT ALL PRIVILEGES ON DATABASE $DBNAME TO "$PSQLUSERNAME";
      EOF

      psql "host=$DBSERVER user=$PSQLADMINNAME dbname=$DBNAME port=5432 password=$PSQLADMINPASSWORD sslmode=require" < create_user.sql
  EOT

  depends_on = [ azurerm_postgresql_flexible_server_database.database ]
}