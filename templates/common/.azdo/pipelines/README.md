# Azure DevOps Pipeline Configuration

This document will show you how to configure an Azure DevOps pipeline that uses the Azure Developer CLI. This can be configured by running the  `azd pipeline config --provider azdo` command.

You will find a default Azure DevOps pipeline file in `./.azdo/pipelines/azure-dev.yml`. It will provision your Azure resources and deploy your code upon pushes and pull requests.

You are welcome to use that file as-is or modify it to suit your needs.

## Create or Use Existing Azure DevOps Organization

To run a pipeline in Azure DevOps, you'll first need an Azure DevOps organization. You must create an organization using the Azure DevOps portal here: https://dev.azure.com.

Once you have that organization, copy and paste it below, then run the commands to set those environment variables.

```bash
export AZURE_DEVOPS_ORG_NAME="<Azure DevOps Org Name>"
```

This can also be set as an Azure Developer CLI environment via the command:

```bash
azd env set AZURE_DEVOPS_ORG_NAME "<Azure DevOps Org Name>"
```
> AZURE_DEVOPS_ORG_NAME: The name of the Azure DevOps organization that you just created or existing one that you want to use.

## Create a Personal Access Token

The Azure Developer CLI relies on an Azure DevOps Personal Access Token (PAT) to configure an Azure DevOps project. The Azure Developer CLI will prompt you to create a PAT and provide [documentation on the PAT creation process](https://aka.ms/azure-dev/azdo-pat).


```bash
export AZURE_DEVOPS_EXT_PAT=<PAT>
```
> AZURE_DEVOPS_EXT_PAT: The Azure DevOps Personal Access Token that you just created or existing one that you want to use.

## Invoke the Pipeline configure command

By running `azd pipeline config --provider azdo` you can instruct the Azure Developer CLI to configure an Azure DevOps Project and Repository with a deployment Pipeline.

## Conclusion

That is everything you need to have in place to get the Azure DevOps pipeline running. You can verify that it is working by going to the Azure DevOps portal (https://dev.azure.com) and finding the project you just created.



