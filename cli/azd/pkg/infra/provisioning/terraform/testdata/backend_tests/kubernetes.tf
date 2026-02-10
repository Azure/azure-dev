terraform {
  required_version = ">= 1.1.7"
  backend "kubernetes" {
    secret_suffix    = "state"
    config_path      = "~/.kube/config"
  }
}
