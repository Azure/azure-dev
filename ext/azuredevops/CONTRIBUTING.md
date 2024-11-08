# Contributing guide

## Prerequisites

Learn about [Azure devops custom tasks](https://learn.microsoft.com/azure/devops/extend/develop/add-build-task?view=azure-devops). 

## Submitting A Change

We use a fork based workflow. Here are simple steps:

1. Fork `azure/azure-dev` on GitHub.
2. Create a branch named `<some-description>` (e.g. `fix-123` for a bug fix or `add-deploy-command`) in your forked Git
   repository.
3. Push the branch to your fork on GitHub.
4. Open a pull request in GitHub.

Here is a more [in-depth guide to forks in GitHub](https://guides.github.com/activities/forking/).

As part of CI validation, we run a series of live tests which provision and deprovision Azure resources. For external
contributors, these tests will not run automatically, but someone on the team will be able to run them for your PR on your
behalf.

## Build

- From `setupAzd` folder:
  - Run `npm install` 
  - Run `tsc`
- Run `tfx extension create --manifest-globs vss-extension.json` to create the extension.

## Testing

From `setupAzd` folder, run `npm test`

## Release

- Update `setupAzd/task.json` with the `version` number.
- Update `vss-extension.json` with the `version` to release.
- Run the `build` steps to produce the `vsix` release artifact.
- Follow [publish steps](https://learn.microsoft.com/azure/devops/extend/develop/add-build-task?view=azure-devops#5-publish-your-extension) to update the Marketplace.
