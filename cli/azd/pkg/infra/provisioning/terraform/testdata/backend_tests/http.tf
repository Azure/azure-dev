terraform {
  required_version = ">= 1.1.7"
  backend "http" {
    address = "https://api.example.com/terraform/state"
    lock_address = "https://api.example.com/terraform/lock"
    unlock_address = "https://api.example.com/terraform/unlock"
  }
}
