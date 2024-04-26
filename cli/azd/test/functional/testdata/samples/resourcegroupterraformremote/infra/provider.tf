#Set the terraform required version, and Configure the Azure Provider.Use remote storage

# Configure the Azure Provider
terraform {
  required_version = ">= 1.1.7, < 2.0.0"
  backend "azurerm" {
  }
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

provider "azurerm" {
  skip_provider_registration = "true"
  features {}
}
