# Design Note: Environments

## Metadata

- Author: @ellismg
- Date: 2022-07-28

## Overview

This document outlines what an environment is in `azd` today and outlines a change in model to remove the need to introduce the concept on the critical path for a user getting started with `azd` to manage their application.

## Current Design

The concept on an environment has been present in `azd` for a long while, but the way it has been used has changed over time leading to inconsistencies and a generally confusing model.

Every command in `azd` today runs in the context of an environment. The name of the environment can be provided by the `-e` flag which is passed to all `azd` commands and when not set, the *default environment* is used. The name of the currently selected environment is stored in `.azure/config.json` file, under the `defaultEnvironment`. The *default environment* can set with `azd env select [environment-name]`. If no environment name is passed and no default environment has been selected then the user is prompted to create a new environment before the operation can proceed.

Critically this means that every operation in `azd` requires interacting with an environment, and this it ends up being the first concept introduced when using `azd`.

During environment creation the user is prompted for the following pieces of information:

1. A Name for the environment.
2. An Azure location.
3. An Azure subscription.

Unlike an Azure location or subscription, an environment is a new idea that has no mapping to any existing Azure concepts.  We don't give a clear explanation of how you should pick and environment name or how that name influences the rest of the tool.  We simply ask for an environment name and then ask again if you given us an "invalid" name.

Names of environments are validated against the regular expression `[a-zA-Z0-9-\(\)_\.]{1,64}$` (this matches the regular expression used by ARM to validate names of ARM deployments).

The name of the environment is used to name a directory under `.azure` which contains files related to the environment. The main file in this folder is the `.env` file, which is used to store key/value pairs in the environment.

The values selected during initialization are stored in this `.env` file with well known key names:

- `AZURE_ENV_NAME`: The environment name.
- `AZURE_LOCATION`: The short location name (e.g. `eastus2`)
- `AZURE_SUBSCRIPTION_ID`: The id of the selected subscription (e.g. `faa080af-c1d8-40ad-9cce-e1a450ca5b57`)

In addition, the following value is added without any user interaction:

- `AZURE_PRINCIPAL_ID`: The id of the current principal authenticated to the `az` CLI.

All of these values must be present in an environment for it to be considered initialized.  If any are missing (for example, by editing the .env file) the user will be prompted the next time `azd` runs to configure these values. Note that this will require the user be logged into the `az` CLI since configuring these values requires the CLI (to list locations, subscriptions and the object id of the current principal).

Any time infrastructure is deployed (via `azd provision`) the outputs of the deployment are merged into the the `.env` file.  For example, if you have the following stanza in your root bicep file:

```bicep
output WEBSITE_URL string = resources.outputs.WEBSITE_URL
```

Then the value of `resources.outputs.WEBSITE_URL` will be written into the `.env` file with the key `WEBSITE_URL`. Loading this `.env` file allows 12-factor style applications to pull configuration information from their deployment (e.g., we use this in our templates to set values like `AZURE_COSMOS_DATABASE_NAME` and `AZURE_COSMOS_CONNECTION_STRING_KEY`). 

In addition, `azd` will pull values from the environment directly in a few cases (e.g., when using container applications, it expects that a `AZURE_CONTAINER_REGISTRY_ENDPOINT` value is set in the environment, which is the registry to log into) and write values into the environment (e.g., when using container applications, the name of the docker image to deploy is set in the environment with the key `SERVICE_<NAME>_IMAGE_NAME`)

Users can also add new or modify existing keys in the `.env` file by using `azd env set`. Today there is no way to delete existing keys.

Values set in the environment can be consumed by infrastructure by using environment string replacements in the corresponding `.parameters.json` files. The root infrastructure file for the ToDo application has the following parameters:

```bicep
@minLength(1)
@maxLength(64)
@description('Name of the the environment which is used to generate a short unique hash used in all resources.')
param name string

@minLength(1)
@description('Primary location for all resources')
param location string

@description('Id of the user or app to assign application roles')
param principalId string = ''
```

And a corresponding `.parameters.json` file under the `infra` folder to tie these values to what was configured in the environment:

```json
{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
  "contentVersion": "1.0.0.0",
  "parameters": {
    "name": {
      "value": "${AZURE_ENV_NAME}"
    },
    "location": {
      "value": "${AZURE_LOCATION}"
    },
    "principalId": {
      "value": "${AZURE_PRINCIPAL_ID}"
    }
  }
}
```

During `azd provision` the following steps happen:

1. The `main.parameters.json` file is loaded, and evaluated in the current environment (so all the `${}` expressions are replaced with actual values).
2. The `main.bicep` file is loaded and all the deployment parameters are collected.
3. If any required parameters are missing, `azd` prompts for their values.
4. A new `main.parameters.json` file is written in the environment directory (`.azure/<env-name>/`) which contains the final configured values for the parameters.
5. An ARM Deployment is created to deploy the bicep template with the `main.parameters.json` file from step 4. The name of this deployment matches the environment name and the target subscription is pulled from the `AZURE_SUBSCRIPTION_ID` environment variable.
6. As described earlier, when the deployment completes, all outputs are merged in to the `.env` file for the environment.

By convention, our templates also tag resources with `azd-env-name` and there has been discussion about `azd` using that tag when running queries to discover resources. We've done something similar with `azd-service-name` and that pattern has worked out well for us and plays nicely in the infrastructure as code world.

This system allows you to create a multiple environments, each with a different name, and provision a unique copy of the infrastructure for the application.

## Current Challenges

The major issue we face with the current design is that we've introduced a new concept, an environment, and it is on the critical path of every `azd` invocation. We need to ensure this concept is simple to understand and worthwhile and clearly articulate how it relates to the the rest of the system (and ideally existing Azure concepts). It's not always clear how settings in the environment impact infrastructure or the rest of `azd` and what's based on convention and what is required by the tooling. There are also "tiers" of configuration values now - `AZURE_ENV_NAME`, `AZURE_LOCATION`, `AZURE_PRINCIPAL_ID` and `AZURE_SUBSCRIPTION_ID` are very different from other values. We also have a tension on how to describe what an environment name does. Our templates today use it to influence the names of some resources (usually the root resource group that is created) but that's simply a convention we've adopted and so we are wary of adding that as "help text" for an environment name.

The value of environments doesn't pay off until you want to start creating multiple copies of the infrastructure that backs your application, but the complexity cost is incurred as a soon as a user walks up to `azd`.

I think that with a few small tweaks to our model we can make it much easier to understand and describe - while at the same time allowing us to defer introduction of the concept until a user actually cares about the value an environment provides.

## Proposed Model

### Overview

As before, we still have the concept of an environment in our system.  All `azd` commands still accept an `-e` parameter to select what environment is used, and `azd env select` can be used to select the default environment. However, if no environment is selected, `azd` creates a new environment with the name `default`. In addition to a name every environment gets a unique id (a v4 UUID) assigned to it. These values are recorded in `.azure/<environment-name>/config.json`:

```json
{
  "id": "9fdfa455-2131-4faf-b2e3-8380aba97c96",
  "name": "default"
}
```

The above represents a fully initialized environment. Unlike today, creating an environment does not require user interaction and can be created without being logged into Azure.

As before, an environment name needs to be unique with a project context (i.e. the `.azure` folder) ignoring case differences (useful when persisting data to file systems which may not allow case differences).

### Deployment Parameters

Bicep Templates can contain parameters, which are configured per deployment. Other IaC providers have similar models (config for Pulumi, variables for Terraform). Since values for these parameters are provided on every deployment there is often a way to specify them from a file, for example for Bicep, a *deployment parameters* file (also known as a `.parameters.json` file) may be used to configure these values.

When `azd provision` is runs it loads the project wide configuration from the corresponding `.parameters.json` file in the infrastructure folder and merges with environment specific deployment parameters. If any required deployment parameters are missing the user is prompted for values and the responses are stored in the environment for future deployments.

The deployment parameters are stored in `config.json` under the `infra` section, segmented by module.

```json
{
  "id": "9fdfa455-2131-4faf-b2e3-8380aba97c96",
  "name": "default",
  "infra": {
    "main": { 
      "properties": {
         // ... the object that is set as the value of the "properties" key of a deploymentParameters file
      }
    }
  }
}
```

To save ourselves the trouble of designing our own format, we reuse the schema of a `deploymentParamters` file. One nice feature here is that system provides enough flexibility to express references to secrets in Azure Key Vault, which will be a useful feature for us down the line.

We create a new set of `azd infra config` commands with actions like `set`, `get`, `list`, `delete` to allow management of these parameters without having to edit the `config.json` file.

### Improved Deployment Configuration

Since we now operate directly on deployment template parameters, we can leverage existing template metadata (like the description of a parameter) to provide template specific help on how the parameter is used as well as validation.

For example, our ToDo application could be updated to have the following "name" parameter:

```bicep
@minLength(1)
@maxLength(64)
@description('The name used as a prefix for the resource group which holds all the resources for the ToDo application. The suffix `-rg` will automatically be added.')
param name string
```

Which provides template specific information on how the parameter is used. It also ensures that these templates behave nicely with other deployment tools (like creating a deployment from the portal or using `az` directly).

We leverage the existing metadata system for bicep templates and parameters to allow some `azd` specific metadata to be applied to properties:

```bicep
@description('The Azure location to use for all resources.')
@metadata({
    azd: {
        type: 'location'
    }
})
param location string
```

The `type` property of the `azd` metadata object denotes that this value should be set to an Azure location name (e.g. "westus2") and is used by `azd` to offer a "pick a location from this list of all Azure locations). This composes with other validation decorators that you can apply, for example `@allowed()` which can be used to restrict the set of locations offered to the user to pick from (for cases where a template uses services that are not deployed to all regions):

```bicep
@description('The Azure location to use for all resources.')
@allowed([
    'westus2'
    'centralus'
])
@metadata({
    azd: {
        type: 'location'
    }
})
param location string
```

To start, we support the following types:

`location`: The value should be the short name of an Azure location. All locations are supported by default, but the exiting `@allowed` may restrict this set.
`azd-deployment-context`: The value should be an `azd` deployment context object. This is a JSON object that contains information about the project which may be useful for values when deploying. It will be described in further detail later.

### Deployment Outputs

When a deployment completes, the outputs from the deployment are stored in the `config.json` file under the "outputs" key (a sibling of "properties"). As we did on inputs, we re-use the schema of an outputs property of an ARM Deployment to save ourselves the trouble of inventing a new format.  When supporting Pulumi and Terraform we'll have to build an adapter but the format should be flexible enough to support this.

### Environment Files

An explicit design goal is that all environment state is stored in single JSON file, which should provide flexibility in developing some longer term story around collaboration.

While no longer technically required, we continue produce a `.env` and `.parameters.json` in the `.azure\<environment-name>` folder use by external tooling. These are generated artifacts that are updated any time the authoritative values are updated in `config.json`.

The generated `.env` file no longer contains deployment parameters.

Now it is just the union of outputs from the deployment and any keys managed manually via `azd env set`. Since this file is regenerated each time a deployment happens, if a user removes an output from their `.bicep` file, the corresponding output will be removed from the `.env` file. 

### ARM Deployments

When using Bicep or ARM, deployments happen by creating an ARM Deployment object, which has a name that makes up part of it's identity. In addition, when preforming a subscription level deployment (the only form `azd` currently supports) you need to provide a location for where the deployment metadata is stored. When we support targeting resource groups, instead of a location we'll need a resource group name.

For now, we'll introduce a `target` property in the environment:

```json
{
  "id": "9fdfa455-2131-4faf-b2e3-8380aba97c96",
  "name": "default",
  "infra": {
    "main": { 
      "target": {
        "type": "arm",
        "subscriptionId": "faa080af-c1d8-40ad-9cce-e1a450ca5b57",
        "location": "westus2"
      }
    }
  }
}
```

We should have some way to manage the target (`infra target config`?) and at a minimum it should be easy to understand what subscription or subscription/resource group will be used for deployment (`infra target show`?) For now, these values will be prompted for during the initial deployment (just like other required deployment parameters). The required settings to configure a target will not be uniform across infrastructure providers.  When we introduce resource group level deployments we will do by adding the resource group name as a required piece of target configuration (and remove location).

As a UX concern, we may need to handle the "location" parameter in a clever way to reuse whatever location is being set for the template. Ideally we will not have to ask separate questions for "where do you want to store your resources" and "where do you want to store your deployment metadata" even though these are individually configurable.

Note that the target **does not** contain information about a deployment name. Today the name we use for the deployment matches the environment name. We will change this to instead include the *project name* (stored in the name property of `azure.yaml` and the *environment id* to ensure uniqueness. We should also include some per deployment entropy (or perhaps just a timestamp) such that each deployment gets its own deployment object. This should help with some problems we face today where re-using the same deployment object can confuse our status reporting logic, and provides a better view of deployment history.  

Since deployment names are limited to 64 characters, we will need someway to truncate names if we hit this limit.  We should bias towards keeping as much entropy as possible in the strings to ensure uniqueness at a given scope. We should tag these deployment objects with the full environment ID for querying later.

## User Impacting Changes

- We no longer add `AZURE_ENV_NAME`, `AZURE_LOCATION`, `AZURE_SUBSCRIPTION_ID` or `AZURE_PRINCIPAL_ID` to the `.env` file.
- We no longer support environment variable substitution in the `.parameters.json` file in the `infra` folder, it is used just to configure static values.
- In our templates we remove the `.parameters.json` files since they are no longer used (all values are user configured during first deployment). (*note: We can choose to keep these files if we think their existence provides value in our templates as a learning tool*)
- We prompt and validate deployment parameters using the metadata applied in the bicep template.

We can figure out how to rolls these changes out in a smooth enough way (for example, we could take the existence of a environment reference like `${FOO}` in a `.parameters.json` to mean we were on the "old" plan and `azd` can warn and do something sensible (or point folks at an article on how to upgrade).

## Open Questions

> How do we express that a deployment parameter should correspond to the environment id or environment name. Do we introduce types like `azd-env-id` and `azd-env-name` for our metadata on parameters? Should we instead have something like `azd-context` and that passes in a JSON object with well known keys like `id` and `name`?  Is there value in making it easy to flow the environment name (which now will often just be the string "default" into the infrastructure? Why would you use it vs `azd-env-id`?) 

Let's experiment with a context object that will contain the environment id, as well as image ids for containers we've built and go from there 

> Related to the above - how do we denote the "this parameter corresponds to the name of the of the image to deploy". In the older model we wrote a name with a well known key into the environment and then had the `.parameters.json` file wire it up to the deployment parameter. What do we want to do in this new world (where that doesn't work as well since we don't evaluate a `.parameters.json` file in an environment)? Something in an `azd-context` object?  Another `azd-image-name` type?

We'll have a services object in the context which will have keys for each service (mirroring `azure.yaml`) and it will contain an `imageId` property.

> How does this impact `azd env refresh` - What does refreshing an environment mean?  

`env refresh` fetches the latest deployment object for the environment and updates `properties` and `outputs` sections of the `config.json` file for the `main` module. As a consequence of changing `config.json`, the `.env` and `.parameters.json` files are also regenerated.  (*note: I would honestly prefer if we just got rid of this command for now, I don't think we have a great understanding of what this tries to solve and the value it provides today is quite limited*)

- [ ] What is the impact to deployments via GitHub (during `pipeline config` we try to set enough environment variables via secrets so that `azd init` "just works" but that depends heavily on the existing relationship between deployment parameters and environment variables). The whole persistance logic in `pipeline config` is very ad-hoc today and is a result of us not having a clear story here.