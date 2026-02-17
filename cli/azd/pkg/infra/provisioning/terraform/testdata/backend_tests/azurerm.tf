terraform {
  required_version = ">= 1.1.7"
  backend "azurerm" {
    resource_group_name  = "rg-terraform-state"
    storage_account_name = "tfstate"
    container_name       = "tfstate"
    key                  = "terraform.tfstate"
  }
}
