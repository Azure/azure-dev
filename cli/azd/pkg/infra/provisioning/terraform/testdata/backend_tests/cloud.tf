terraform {
  required_version = ">= 1.1.7"
  cloud {
    hostname     = "app.terraform.io"
    organization = "my-org"
    workspaces {
      name = "my-workspace"
    }
  }
}
