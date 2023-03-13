# 3/13/2023 - Commands Fit-and-Finish Proposal

## Design

Commands that interact with the core development workflow will be verb-based. This feels natural to the user as the user is building an application, provisioning infrastructure, and deploying their application to Azure -- which are all actions.

For commands that are of configuration and management in-nature, it is fine to add a noun for a subcommand. For example, `azd env <action>` and `azd config <action>`.

This means that the overall proposed design matches with what `azd` currently offers, and no changes need to occur.

## `azd` usage output

To help introduce user to the command layout, I propose slight changes to the current `azd` usage output (with UX input needed), that looks like the following:

```
Commands
  Develop an app
    init     	: Initialize a new application.
    provision	: Provision the Azure resources for an application.
    deploy   	: Deploy the application's code to Azure.
    up       	: Initialize application, provision Azure resources, and deploy your project with a single command.

  Monitor, test, and release your app
    monitor  	: Monitor a deployed application.
    pipeline 	: Manage and configure your deployment pipelines.

  Authenticate with Azure
    login    	: Log in to Azure.
    logout   	: Log out of Azure.

  Miscellaneous commands
    config   	: Manage azd configurations (ex: default Azure subscription, location).
    down     	: Delete Azure resources for an application.
    env      	: Manage environments.
    restore  	: Restore application dependencies.
    template 	: Find and view template details.
```

The commands are grouped by their scenarios, with most important scenarios at the top. It is not in alphabetical order, rather the order the user is expected to run the commands. If we added `package` for example, would be after `init`.

## Command changes

The following changes are proposed:

- `azd infra create` and `azd infra delete` should be removed, as the functionality already exists in `azd provision` and `azd down`, and users will find the duplication somewhat confusing.
- The `--output` flag should be moved to `experimental` (hidden and unsupported unless with feature toggle), with the exception of `azd auth token --output json` which must be supported due to credential handlers.
  - When the feature is hidden, the default output behavior of each command will apply.
  - This is done as `--output` does not have a documented contract. It prints out `stdout` in JSON, while `stderr` messages also contain JSON-enveloped messages. We might consider splitting this behavior in the future.

Discussion warranted:

- Should we offer `azd restore`? I'm curious if users actually find this useful. We currently use this in our `tasks.json`, which seems useful, but the immediate block directly references `npm`, which defeats the nice abstraction. Excerpt from `tasks.json` below:
  ```json
    {
    "label": "Restore Web",
    "type": "shell",
    "command": "azd restore --service web",
    "presentation": {
        "reveal": "silent"
    },
    "problemMatcher": []
    },
    {
        "label": "Web npm start",
        "detail": "Helper task--use 'Start Web' task to ensure environment is set up correctly",
        "type": "shell",
        "command": "npm run start"
    }
  ```
- Should `azd deploy` deploy only the current service, when the working directory is under a service directory? I personally think this is what users might expect, but I can see an argument against it since it may be implicit behavior.
- Should `azd deploy --service <service>` be moved to `azd deploy <service>`? I personally feel better about the latter design. We can still allow parsing for `--service`, but remove it in deprecated stage.  If in the future, `azd` supports deploying other things besides services, it could look like: `azd deploy --notAService <notAService name>`.
