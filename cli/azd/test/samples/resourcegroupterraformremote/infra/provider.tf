#Set the terraform required version, and Configure the Azure Provider.Use remote storage

# Configure the Azure Provider
terraform {
  required_version = "~> v1.1.7"
  backend "azurerm" {
  }
  required_providers {
    azurerm = {
      version = "~>3.18.0"
      source  = "hashicorp/azurerm"
    }
    azurecaf = {
      source  = "aztfmod/azurecaf"
      version = "~>1.2.15"
    }
  }
}

provider "azurerm" {
  features {}
}
