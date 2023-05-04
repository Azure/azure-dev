# Azure Developer CLI (azd) Bicep Starter

A starter blueprint for getting your application up on Azure using [Azure Developer CLI](https://learn.microsoft.com/en-us/azure/developer/azure-developer-cli/overview) (azd). Add your application code, write Infrastructure as Code assets in [Bicep](https://aka.ms/bicep) to get your application up and running quickly.

The following assets have been provided:

- Infrastructure-as-code (IaC) files under the `infra` folder that demonstrate how to provision resources and setup resource tagging for `azd`.
- A [dev container](https://containers.dev) configuration file under the `.devcontainer` directory that installs infrastructure tooling by default. This can be readily used to create cloud-hosted developer environments such as [GitHub Codespaces](https://aka.ms/codespaces).
- Continuous deployment workflows for CI providers such as GitHub Actions under the `.github` directory, and Azure Pipelines under the `.azdo` directory that work for most use-cases.

## Next Steps

### Step 1: Add application code

Initialize the service source code projects anywhere under the current directory. Ensure that all source code projects can be built successfully.

> Note: For `function` services, it is recommended to initialize the project using the provided [quickstart tools](https://learn.microsoft.com/en-us/azure/azure-functions/functions-get-started).

Once all service source code projects are building correctly, update `azure.yaml` to reference the source code projects.

Run `azd package` to validate that all service source code projects can be built and packaged locally.

### Step 2: Provision Azure resources

Update or add Infrastructure as Code (IaC) files to provision the relevant Azure resources. This can be done incrementally, as the list of [Azure resources](https://learn.microsoft.com/en-us/azure/?product=popular) are explored and added. As an example, a reference library that contains all of the Bicep modules used by the azd templates can be found [here](https://github.com/Azure-Samples/todo-nodejs-mongo/tree/main/infra/core). All Azure resources available in Bicep format can be found [here](https://learn.microsoft.com/en-us/azure/templates/).

Run `azd provision` whenever you want to ensure that changes made are applied correctly and work as expected.

### Step 3: Tie in application and infrastructure

There are certain pieces required that help tie-in application and infrastructure together. These are pieces that help expose your application to other Azure resources, or help allow your application to take advantage of Azure capabilities. For more details, see [below](#additional-details). This includes the following:

1. Set up application settings for the code running in Azure to reference other Azure resources.
1. Set up managed identities to allow the code running in Azure to access other Azure resources.
1. If you are using Key Vault, set up required secrets in KeyVault that then can be referenced by your application settings, with the use of managed identities.
1. Configure settings on the hosting platform to match your application's needs. This may include networking, security options, or more advanced configuration that helps you take advantage of Azure capabilities.

Run `azd up` whenever you need to validate both infrastructure and application code changes.
Run `azd deploy` if you simply need to validate application code changes.

### Step 4: Up to Azure

Run `azd up` to run the end-to-end infrastructure provisioning (`azd provision`) and deployment (`azd deploy`) flow. Visit the service endpoints listed to see your application up-and-running!

## Additional Details

The following section examines different concepts that involved in tying in application and infrastructure.

### Application settings

In general, for backend services, application settings should be managed in Azure. Typically, the service host allows application settings to be defined.

- For `appservice` and `function`, application settings should be defined on the IaC resource for the targeted host.
- For `aks`, the application settings is managed with deployment manifests under the `<service>/manifests` folder.  Example from our reference templates [here](https://github.com/Azure-Samples/todo-nodejs-mongo-aks/tree/main/src/api/manifests).

### Managed identities

[Managed identities](https://learn.microsoft.com/en-us/azure/active-directory/managed-identities-azure-resources/overview) allows you to secure communication between services. This is done without having the need for you to manage any credentials.

### KeyVault Secrets

[Azure KeyVault](https://learn.microsoft.com/en-us/azure/key-vault/general/overview) allows you to store secrets securely. Your application can access these secrets securely through the use of managed identities.

### Host-specific configuration

For `appservice`, the following host configuration are often modified:

- Language runtime
- Exposed port from the running container (if running a web application service)
- Allowed origins for CORS (Cross-Origin Resource Sharing) protection (if running a web application service)
- The run command to execute for the application. This may be used as an optimization when the default Oryx run command is not sufficient for your needs.
