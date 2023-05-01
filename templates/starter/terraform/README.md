# Azure Developer CLI (azd) Terraform Starter

A starter template with [Terraform](https://aka.ms/azure-dev/terraform) as infrastructure provider for [Azure Developer CLI](https://learn.microsoft.com/en-us/azure/developer/azure-developer-cli/overview) (azd).

The following assets have been provided:

- Infrastructure-as-code (IaC) files under the `infra` directory that demonstrate how to provision resources and setup resource tagging for `azd`.
- A [dev container](https://containers.dev) configuration file under the `.devcontainer` directory that installs python, Node.js, and infrastructure tools by default. This can be readily used to create cloud-hosted developer environments such as [GitHub Codespaces](https://aka.ms/codespaces).
- Continuous deployment workflows for CI providers such as GitHub Actions under the `.github` directory, and Azure Pipelines under the `.azdo` directory that work for most use-cases.
