# `azd coding-agent` Extension

This azd extension makes it easy to take an existing GitHub repo and add in support for the Copilot Coding Agent to access Azure.

This includes:

- Creating a managed identity, specifically for use by Copilot, allowing finer control over permissions.
- Automatically updating the Github `copilot` environment to use that credential.
- Creating a copilot-setup-steps.yml that sets up the Copilot Coding Agent environment.

## Installing

```shell
# first, you'll need to enable extensions
azd config set alpha.extensions on

# now we can install the coding-agent
azd extension install azure.coding-agent
```

Or, if you want to upgrade to the latest:

```shell
azd extension upgrade azure.coding-agent
```

## Usage

```
azd coding-agent config
```
