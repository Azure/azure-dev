# ------------------------------------------------------------------------------------------------------
# Deploy cosmos db account
# ------------------------------------------------------------------------------------------------------
resource "azurecaf_name" "db_acc_name" {
  name          = local.resource_token
  resource_type = "azurerm_cosmosdb_account"
  random_length = 0
  clean_input   = true
}

resource "azurerm_cosmosdb_account" "db" {
  name                            = azurecaf_name.db_acc_name.result
  location                        = azurerm_resource_group.rg.location
  resource_group_name             = azurerm_resource_group.rg.name
  offer_type                      = "Standard"
  kind                            = "MongoDB"
  enable_automatic_failover       = false
  enable_multiple_write_locations = false
  mongo_server_version            = "4.0"
  tags                            = local.tags

  capabilities {
    name = "EnableServerless"
  }

  consistency_policy {
    consistency_level = "Session"
  }

  geo_location {
    location          = azurerm_resource_group.rg.location
    failover_priority = 0
    zone_redundant    = false
  }
}

# ------------------------------------------------------------------------------------------------------
# Deploy cosmos mongo db and collections
# ------------------------------------------------------------------------------------------------------
resource "azurerm_cosmosdb_mongo_database" "mongodb" {
  name                = "Todo"
  resource_group_name = azurerm_cosmosdb_account.db.resource_group_name
  account_name        = azurerm_cosmosdb_account.db.name
}

resource "azurerm_cosmosdb_mongo_collection" "todolist" {
  name                = "TodoList"
  resource_group_name = azurerm_cosmosdb_account.db.resource_group_name
  account_name        = azurerm_cosmosdb_account.db.name
  database_name       = azurerm_cosmosdb_mongo_database.mongodb.name
  shard_key           = "_id"


  index {
    keys   = ["_id"]
    unique = true
  }
}

resource "azurerm_cosmosdb_mongo_collection" "todocollection" {
  name                = "TodoItem"
  resource_group_name = azurerm_cosmosdb_account.db.resource_group_name
  account_name        = azurerm_cosmosdb_account.db.name
  database_name       = azurerm_cosmosdb_mongo_database.mongodb.name
  shard_key           = "_id"

  index {
    keys   = ["_id"]
    unique = true
  }
}