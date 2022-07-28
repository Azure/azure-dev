# Design Note: Environments

## Metadata

- Author: @ellismg
- Date: 2022-07-27

## Overview

This document outlines what an environment is in `azd` today and outlines a change in model to remove the need to introduce the concept on the critical path for a user getting started with `azd` to manage their application.

## Current Design

The concept on an environment has been present in `azd` for a long while, but the way it has been used has changed over time. In addition, the ties between an environment name and provisioned resources has changed over time.

Every command in `azd` today runs in the context of an environment. The name of the environment can be provided by the `-e` flag which is passed to all `azd` commands and when not set, the *default environment* is used. The name of the currently selected environment is stored in `.azure/config.json` file, under the `defaultEnvironment`. The *default environment* can set with `azd env select [environment-name]`. If no environment name is passed and no default environment has been selected then the user is prompted to create a new environment before the operation can proceed.

During environment creation the user is prompted for the following pieces of information:

1. A Name for the environment.
2. An Azure location.
3. An Azure subscription.

Names of environments are validated against the regular expression `[a-zA-Z0-9-\(\)_\.]{1,64}$` (this matches the regular expression used by ARM to validate names of ARM deployments).

The name of the environment is used to name a directory under `.azure` which contains files related to the environment. The main file in this folder is the `.env` file, which is used to store key/value pairs in the environment.

The values selected during initialization are stored in this `.env` file with well known key names:

- `AZURE_ENV_NAME`: The environment name.
- `AZURE_LOCATION`: The short location name (e.g. `eastus2`)
- `AZURE_SUBSCRIPTION_ID`: The id of the selected subscription (e.g. `faa080af-c1d8-40ad-9cce-e1a450ca5b57`)

In addition, the following value is added without any user interaction:

- `AZURE_PRINCIPAL_ID`: The id of the current principal authenticated to the `az` CLI.

All of these values must be present in an environment for it to be considered initialized.  If any are missing (for example, by editing the .env file) the user will be prompted the next time `azd` runs to configure these values. Note that this will require the user be logged into the `az` CLI since configuring these values requires the CLI (to list locations, subscriptions and the object id of the current principal).

In addition to these values, any time infrastructure is deployed (via `azd provision`) the outputs of the deployment are merged into the the `.env` file.  For example, if you have the following stanza in your root bicep file:

```bicep
output WEBSITE_URL string = resources.outputs.WEBSITE_URL
```

Then the value of `resources.outputs.WEBSITE_URL` will be written into the `.env` file with the key `WEBSITE_URL`. Loading this `.env` file allows 12-factor style applications to pull configuration information from their deployment (e.g., we use this in our templates to set values like `AZURE_COSMOS_DATABASE_NAME` and `AZURE_COSMOS_CONNECTION_STRING_KEY`). 

In addition, `azd` will pull values from the environment directly in a few cases (e.g., when using container applications, it expects that a `AZURE_CONTAINER_REGISTRY_ENDPOINT` value is set in the environment, which is the registry to log into) and write values into the environment (e.g., when using container applications, the name of the docker image to deploy is set in the environment with the key `SERVICE_<NAME>_IMAGE_NAME`)

Users can also add keys into the `.env` file by using `azd env set`.

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
5. An ARM Deployment is created to deploy the bicep template using the `main.parameters.json` file from step 4. The name of this deployment matches the environment name (and the regular expression we use the validate environment names matches theirs.) and the target subscription is whatever is configured for `AZURE_SUBSCRIPTION_ID`.
6. As described earlier, when the deployment completes, all outputs are merged in to the `.env` file for the environment.

By convention, our templates also tag resources with `azd-env-name` and there has been discussion about `azd` using that tag when running queries to discover resources. We've done something similar with `azd-service-name` (container apps, again) and that pattern has worked out well for us and plays nicely in the infrastructure as code world.

This system allows you to create a multiple environments, each with a different name, and provision a unique copy of the infrastructure for the application.

## Current Challenges

The major issue we face with the current design is that we've introduced a new concept, an environment, and it is on the critical path of every `azd` invocation. We need to ensure this concept is simple to understand and worthwhile and clearly articulate how it relates to the the rest of the system (and ideally existing Azure concepts). It's not always clear how settings in the environment impact infrastructure or the rest of `azd` and what's based on convention and what is required by the tooling. There are also "tiers" of configuration values now - `AZURE_ENV_NAME`, `AZURE_LOCATION`, `AZURE_PRINCIPAL_ID` and `AZURE_SUBSCRIPTION_ID` are very different from other values. We also have a tension on how to describe what an environment name does. Our templates today use it to influence the names of some resources (usually the root resource group that is created) but that's simply a convention we've adopted and so we are wary of adding that as "help text" for an environment name.

The value of environments doesn't pay off until you want to start creating multiple copies of the infrastructure that backs your application, but the complexity cost is incurred as a soon as a user walks up to `azd`.

I think that with a few small tweaks to our model we can make it much easier to understand and describe - while at the same time allowing us to defer introduction of the concept until a user actually cares about the value an environment provides.

## Proposed Model

As before, we still have the concept of an environment in our system.  All `azd` commands still accept an `-e` parameter to select what environment is used, and `azd env select` can be used to select the default environment. However, if no environment is selected, `azd` creates a new environment with the name `default`. In addition to a name every environment gets a unique id (a v4 UUID) assigned to it. These values are recorded in `.azure/<environment-name>/config.json`:

```json
{
  "id": "9fdfa455-2131-4faf-b2e3-8380aba97c96",
  "name": "default"
}
```

Deployment parameters are also stored in this file, under an "infra" key (segmented by module):

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

When `azd provision` is run it combines the `.parameters.json` file with the values in `config.json` (with the settings in `config.json` overriding `.parameters.json` settings) and then checks for any missing required configuration and prompts for values. We can leverage existing template metadata (like the description of a parameter) to provide template specific help on how the parameter is used as well as validation.

For example, our ToDo application could be updated to have the following "name" parameter:

```bicep
@minLength(1)
@maxLength(64)
@description('The name used as a prefix for the resource group which holds all the resources for the ToDo application. The suffix `-rg` will automatically be added.')
param name string
```

Which provides template specific information on how the parameter is used. It also ensures that these templates behave nicely with other deployment tools (like creating a deployment from the portal or using `az` directly).

We also allow some `azd` specific metadata to be applied to properties:

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
`current-principal-id`: The value should be the id of the authenticated principal.

Configuration values (including the environment name and id) are **not** stored in the `.env` file.

Deployment outputs are also stored in the `config.json` file under the "outputs" key (a sibling of "properties") and updated on each deployment. They are written to the `.env` file as before, as well.

The intermediate `.parameters.json` files are no longer written in the `.azure\<environment-name>` directory instead they are written in a temporary directory (and when we stop depending on `az` for deployments will not be persisted to disk)

When creating names for deployments, we name them using the combination of the environment name and ID (e.g. `default-1539e0b8-ac32-440c-ae1f-d131512563dd`), if this would generate a deployment name which is too long, we truncate the environment name but keep the full id). This ensures the deployment names don't clash between two unrelated projects even if they have the same environment name (which will now be common, since we have a default value and no longer force the user into picking a new environment).

We also need to understand the "target" of the deployment. Today, this is simply the subscription ID (since we only support subscription level deployments) and a location to store deployment metadata (which we infer from `AZURE_LOCATION`) but when we start to support resource level deployments this will change to be a subscription and resource group name. Terraform and Pulumi also support targeting multiple subscriptions in a single deployment operation (by creating multiple providers) so I imagine this target will become provider specific over time (I am guessing that for Pulumi and Terraform the target is more tied to a backend but I'm not 100% sure).

For now, we'll introduce a `target` property:

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

## User Impacting Changes

- We no longer add `AZURE_ENV_NAME`, `AZURE_LOCATION`, `AZURE_SUBSCRIPTION_ID` or `AZURE_PRINCIPAL_ID` to the `.env` file.
- We no longer evaluate the `.parameters.json` file in the context on an environment, it is used just to configure static values.
- In our templates we remove the `.parameters.json` files since they are no longer used.
- We prompt and validate deployment parameters using the metadata applied in the bicep template.


We can figure out how to rolls these changes out in a smooth enough way (for example, we could take the existence of a environment reference like `${FOO}` in a `.parameters.json` to mean we were on the "old" plan and `azd` can warn and do something sensible)

## Open Questions

- [ ] How do we express that a deployment parameter should correspond to the environment id or environment name. Do we introduce types like `azd-env-id` and `azd-env-name` for our metadata on parameters? Should we instead have something like `azd-context` and that passes in a JSON object with well known keys like `id` and `name`?  Is there value in making it easy to flow the environment name (which now will often just be the string "default" into the infrastructure? Why would you use it vs `azd-env-id`?)

- [ ] Related to the above - how do we denote the "this parameter corresponds to the name of the of the image to deploy". In the older model we wrote a name with a well known key into the environment and then had the `.parameters.json` file wire it up to the deployment parameter. What do we want to do in this new world (where that doesn't work as well since we don't evaluate a `.parameters.json` file in an environment)? Something in an `azd-context` object?  Another `azd-image-name` type?

- [ ] We'd prefer not to prompt for the location property of a subscription level deployment, since it's a somewhat difficult concept to wrap ones head around and we don't want it on the critical path. In the past inferred this from a `location` parameter on the deployment, does that still make sense (my gut says yes?)

- [ ] How does this impact `azd env refresh` - What does refreshing an environment mean?  I think it means sync the `properties` and `outputs` sections of the `config.json` file for the `main` module with their values from the most recent infrastructure deployment?

- [ ] What is the impact to deployments via GitHub (during `pipeline config` we try to set enough environment variables via secrets so that `azd init` "just works" but that depends heavily on the existing relationship between deployment parameters and environment variables). The whole persistance logic in `pipeline config` is very ad-hoc today and is a result of us not having a clear story here.