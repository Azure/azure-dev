# Provider configuration for a microsoft.foundry service provisioned with
# Terraform. Emitted on disk by `azd ai agent init --infra=terraform` and
# consumed by azd-core's built-in Terraform provider at `azd provision`.
#
# azd-core injects ARM_SUBSCRIPTION_ID (from AZURE_SUBSCRIPTION_ID) and relies
# on the azurerm/azapi providers' default Azure CLI auth, so no ARM_* client
# credentials are required for local development. subscription_id is also wired
# explicitly from main.tfvars.json so the configuration is self-describing.

terraform {
  required_version = ">= 1.1.7, < 2.0.0"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 4.0"
    }
    azapi = {
      source  = "Azure/azapi"
      version = "~> 2.0"
    }
  }
}

provider "azurerm" {
  subscription_id = var.subscription_id
  features {}
}

provider "azapi" {
  subscription_id = var.subscription_id
}
