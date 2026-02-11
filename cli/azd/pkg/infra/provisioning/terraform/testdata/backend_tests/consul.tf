terraform {
  required_version = ">= 1.1.7"
  backend "consul" {
    address = "consul.example.com"
    path    = "terraform/state"
  }
}
