# Setting up a pipeline configuration manually

The Azure Developer CLI provides the command `azd pipeline config` to automatically take care of:

1. Setting up a local git repository and a git remote for it.
1. Creating a `Service Principal` and assigning a role to it (`Contributor` by default).
1. Creating `federated credential` or `client secrets` for the `Service principal`.
1. Finding an existing git repo or creating a new one. (GitHub or Azure DevOps).
1. Configuring the git repo to use the created `Service Principal` to authenticate to Azure.
1. Creating a pipeline definition.

This command **must** be executed by someone who has a `Contributor` role, in order to create the service principal with the given role.
The next steps can be used to manually configure a pipeline without a `Contributor` role, for example, by using an existing service principal.

## Setting up the local repository

There a few ways to get the initial project, even before thinking about the pipeline and how to set it up. For example:

- `azd init -t path-to-git-repo`: This method will pull the source code from the specified repository and initialize a local git repo within the current folder. There won't be any `git remote` set in this case, but a local git branch `main` is created and all the code is staged. 

- `git clone path-to-git-repo`: In this case, git will create a new folder with the content of the repository and with all the existing branches from the repository. It will also set a `git remote` (origin) to the source git repository.

- `zip download`: From the repository website, we can download a zip file containing all the source code and extract it to any folder. In this case, there won't be a local git repository.

- `fork and clone`: Similar to `git clone`, but in this case, two `git remote` are created. `origin` will be pointing to the forked repository and typically `upstream` will be pointing to the original repository.

Depending on what approach you used to start your project, there's a few steps you need to follow before setting up the pipeline.

### azd init -t path-to-git-repo

This is usually the simplest way, as the git repository is only local. The next step is to decide where the source will be pushed, for example, GitHub or Azure DevOps. Go and manually create the repository you will use and then run the next command from your local project `git remote add origin path-to-the-repo`.

You can verify the remote was set correctly running `git remote -v`.

At this point, you can continue on [Set up remote repository](#set-up-remote-repository)

### git clone path-to-git-repo

There are at least two reasons why you might use `git clone`. 

1. You have previously set up your GitHub or Azure DevOps repository and you are trying to add `azd` support for it. In this case, you own the repo that you clone and you will be pushing your source updates to this repo. If this is the case, your remote `origin` is correctly set and you can move on to [Set up remote repository](#set-up-remote-repository).

1. You found a repository that you want to try, for example, from [Azure-Samples](https://github.com/Azure-Samples) and you clone it locally. In this case, the remote `origin` is pointing to the original repository and you won't be able to push changes to this repo. You might want to submit PR changes to the repo, but if you are thinking on setting up a pipeline and your own repository, first you need to update the remote origin. One option is to remove the remote origin with `git remote remove origin`. This will disconnect the repo from where it was cloned and you will be in a state where you can follow the steps from [azd init -t path-to-git-repo](#azd-init--t-path-to-git-repo).

### zip download

After extracting the code, you will need to run `git init -b main` to create a new local git repository with an initial branch `main`. Then you can follow the steps from [azd init -t path-to-git-repo](#azd-init--t-path-to-git-repo).

> Note: This approach can introduce some issue on Windows, as some file's information might be stored within the .git folder and be lost after the zip download (which does not include the .git folder). 

### fork and clone

A fork and clone usually means that you have created your own repository but you still want to track what happens on the original repo. If you are not really planning to keep an eye to the main repository, you can optionally remove the remote `upstream` (or whatever is the name for the main repo) leaving only the `origin` remote. Since your remote is correctly set, you can move on to [Set up remote repository](#set-up-remote-repository)

## Set up remote repository

At this point, you local git repository has the right remote (named `origin`), and your source code might either never been pushed to the `origin` or you might have pushed it before. In either case, before the GitHub actions or Azure DevOps pipelines can run successfully, they need to be configured to use a service principal to communicate with Azure.

### Define the authentication for the pipeline

The sample pipelines included within the `todo` templates provide support for using a service principal with a federated credential (GitHub only) or with client secrets. Depending on `secrets` you set, the pipeline will use one or the other.

#### Federated Credential with GitHub

You need to set `AZURE_CLIENT_ID` and `AZURE_TENANT_ID` secret for GitHub to use a federated credential.
You can use the [github cli](https://cli.github.com) to set secrets for your GitHub repository.

The `client id` and `tenant id` are the ones from a `Service Principal` which has configured a federated credential for the GitHub repository you are using. You can follow [these steps](https://learn.microsoft.com/azure/active-directory/workload-identities/workload-identity-federation-create-trust?pivots=identity-wif-apps-methods-azp#github-actions) to set up the federated credential for the app registration (Service principal).

#### Client Secret with GitHub

You need to set the secret `AZURE_CREDENTIALS` which is a json file that includes:

```json
{
    "clientId": "xxxxx",
    "clientSecret": "xxxx",
    "tenantId": "xxxx",
}
```

You can follow [these steps](https://github.com/marketplace/actions/azure-login#configure-a-service-principal-with-a-secret) to set a secret value for your service principal. Or you might receive this values for an `existing service principal` which you want to use for the pipeline.

#### Client Secret with Azure DevOps

The Azure DevOps pipelines from the `azd` samples depend on the task `AzureCLI@2` which uses the Azure CLI to provide authentication. This requires the creation of an `azure service connection` within the Azure DevOps project where the pipeline will be also hosted. The name you use for this service connection must be used with the pipeline definition. For example, the `azd` pipeline samples use the name `azconnection` because `azd` automatically creates a service connection using that name.

For reusing an existing connection, you would only need to set the name of the existing connection within the pipeline, for the input `azureSubscription` like:

```yml
- task: AzureCLI@2
    inputs:
      azureSubscription: service-connection-name
```

The service connection from Azure DevOps is equivalent to the Service Principal used on GitHub, but the service connection must be first created within Azure DevOps. Learn more about creating service connections for Azure DevOps [here](https://learn.microsoft.com/azure/devops/pipelines/library/service-endpoints?view=azure-devops&tabs=yaml).

### Azd environment configuration

After setting up the authentication strategy for the pipeline, the next step is to tell `azd` about the environment. This is also set by using secrets for both GitHub and Azure DevOps. You need to set:

- `AZURE_ENV_NAME`
- `AZURE_LOCATION`
- `AZURE_SUBSCRIPTION_ID`

If you have run `azd provision` locally, you can get these values by running `azd env get-values`.

## Create Azure DevOps pipeline

For Azure DevOps, you need to manually create the pipeline by using a yml definition. You can follow [this these](https://learn.microsoft.com/azure/devops/pipelines/create-first-pipeline?view=azure-devops&tabs=java%2Ctfs-2018-2%2Cbrowser) to learn how to use the existing yml definition.

This is not required on GitHub, as the GitHub actions is automatically created as soon as the yml file is pushed into an specific folder.

## Push your changes

At this point, everything should be ready for you to push your changes to start a new pipeline or you can also manually start the flow from GitHub or Azure DevOps.
