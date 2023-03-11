# Feature Toggle

The Azure Developer CLI includes a `feature toggle` which allows the cli to ship `in-preview` ( _a.k.a. experimental_ ). This document provides the feature design for it.

## Main requirement

As a customer, I can explicitly ask `azd` to unblock and show any _experimental_ functionality. The features becoming available are not finalized, so they can be changed or removed in future releases.

The strategy to ask `azd` to `toggle` between modes is:

> azd config set experimental on

and

> azd config set experimental off

## Experimental features

When toggling experimental mode `on`, the Azure Developer CLI includes the next type of features:

1. Command or sub-command
    - The command becomes available to be run just like any other command.
    - Customers are **not** expected to know when a command or sub-command is experimental by just typing the command. This means:
      - There are not suffix or prefix for a in-preview command.
      - There is not sub-grouping command for all experimental commands.
    - A `warning note` should be displayed then the command is run with a note like:
      > You are running an experimental command. It can be changed or removed in the future.

1. Flags for command
    - Follows the same rules from `1)`

1. Field for `azure.yaml`
    - There won't be changes to the `yaml-schema`. This means, setting fields for in-preview features will be syntax highlighted as an error. Customer should expect and ignore those errors.
    - Similar to 1) and 2), when experimental fields are set within azure.yaml, there will be a warning message displayed while azd loads the `azure.yaml`.

> Note: The Azure Developer CLI will never use the experimental toggling for breaking-changes like:
> - Renaming existing commands or flags.
> - Updating yaml-schema.
> - Changing the existing behavior for any flow.
>
> An experimental feature must be an **add-on only** to the current flows.

## Announcing in-preview features

As mentioned before, when experimental mode is enabled, the only way a customer is expected to know when one of the experimental features is used, is by getting a warning note after invoking the feature. This means:

- The is not way for azd to show a list of experimental features available.
- User can not individually select which features to activate. All the experiments are enabled from the same switch.

### Changelog notes

Customer can refer to the release notes to discover changes for the list of experiments. There next sections will be exposed to the changelog:

- Experimental features added.
- Experimental features promoted to GA.
- Experimental features updated.
  - This section should cover updated and removed.

### Experiment feature tracker

Additionally to the changelog, there will be a github issue to follow the list of experimental features for each release.
