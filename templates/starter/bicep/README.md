# Azure Developer CLI (azd) Bicep Starter

A starter blueprint for getting your application up on Azure using [Azure Developer CLI](https://learn.microsoft.com/en-us/azure/developer/azure-developer-cli/overview) (azd). Add your application code, write Infrastructure as Code assets in [Bicep](https://aka.ms/bicep) to get your application up and running quickly.

The following assets have been provided:

- Infrastructure-as-code (IaC) files under the `infra` folder that demonstrate how to provision resources and setup resource tagging for `azd`.
- A [dev container](https://containers.dev) configuration file under the `.devcontainer` directory that installs infrastructure tooling by default. This can be readily used to create cloud-hosted developer environments such as [GitHub Codespaces](https://aka.ms/codespaces).
- Continuous deployment workflows for CI providers such as GitHub Actions under the `.github` directory, and Azure Pipelines under the `.azdo` directory that work for most use-cases.
