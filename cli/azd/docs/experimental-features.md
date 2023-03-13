# Experimental Features

The Azure Developer CLI includes a `feature toggle` which allows the cli to ship `in-preview` ( _a.k.a. experimental_ ). This document provides the feature design for it.

## Main requirement

As a customer, I can explicitly ask `azd` to unblock and show any _experimental_ functionality. The features becoming available are not finalized, so they can be changed or removed in future releases.

The strategy to ask `azd` to `toggle` between modes is:

- Enable _all_ experimental features: `azd config set experimental on`
- Enable _specific_ experimental feature: `azd config set experimental.name.feature`
- Disable _all_ experimental features: `azd config unset experimental`
- Disable _specific_ experimental feature: `azd config unset experimental.name.feature`

## Experimental features

The Azure Developer CLI offers the following type of features that can be toggled using experimental modes:

1. New Command or sub-command
    - The command becomes available to be run just like any other command.
    - The command is designed exactly as how it would be released.
      - There is no suffix or prefix to the command.
      - There is no `experimental` command group which experimental commands live under.
    - A `warning note` should be displayed then the command is run with a note like:
      > You are running an experimental command. It can be changed or removed in the future.

1. New Flags for command
    - Follows the same rules from `1)`

1. New Field for `azure.yaml`
    - There won't be changes to the `yaml-schema`. This means, setting fields for in-preview features will be syntax highlighted as an error. Customer should expect and ignore those errors.
    - Similar to 1) and 2), when experimental fields are set within azure.yaml, there will be a warning message displayed while azd loads the `azure.yaml`.

> Note: The Azure Developer CLI will never use the experimental toggling for breaking-changes like:
> - Renaming existing commands or flags.
> - Updating yaml-schema.
> - Changing the existing behavior for any flow.
>
> An experimental feature must be an **add-on only** to the current flows.

## Announcing in-preview features

The Azure Developer CLI can list available experimental features running:

> azd config list-experiments
### Changelog notes

Customer can refer to the release notes to discover changes for the list of experiments. There next sections will be exposed to the changelog:

- Experimental features added.
- Experimental features promoted to GA.
- Experimental features updated.
  - This section should cover updated and removed.
