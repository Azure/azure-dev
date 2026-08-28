# Release an emergency hotfix

Use this process to release a small, urgent patch without including unrelated changes from `main`.
The core CLI release pipeline supports manual releases from branches other than `main`.

Follow the Azure SDK
[hotfix branching policy](https://azure.github.io/azure-sdk/policies_repobranching.html#hotfix-branches)
unless this guide gives azd-specific instructions.

## Before you start

Use the standard release process when changes can wait for the next planned release. Use a hotfix only
when delaying the fix presents more risk than releasing an out-of-band patch.

The standard hotfix process services the current stable release. The release pipeline updates the
global `release/latest` channel and, for a stable version, the global `release/stable` channel. Contact
the engineering systems team before servicing an older release line because publishing it through the
standard pipeline can move those channels backward.

Choose the next patch version. For example, use `1.32.1` to hotfix `1.32.0`. Confirm that the
`azure-dev-cli_1.32.0` source release exists and that `1.32.1` has not already been released.

## Create the shared hotfix branch

An authorized maintainer creates the short-lived `hotfix/azd-1.32.1` branch in `Azure/azure-dev`
from the immutable `azure-dev-cli_1.32.0` release tag.

Do not create the branch from `main`. The release tag is the exact source used to build the released
version.

Use the normal fork and pull request workflow for changes to the shared hotfix branch.

## Apply and review the fix

Cherry-pick only the approved fix commits:

```bash
git cherry-pick <commit-sha>
```

Resolve conflicts against the released code, not against current behavior on `main`. Run the build,
tests, and other validation that cover the changed code.

Review the complete hotfix range before preparing the release. The comparison view for the hotfix
pull request should contain only the intended fixes and release preparation.

The hotfix branch must not include unrelated changes from `main`.

## Prepare the patch version

Keep these three version surfaces synchronized:

1. Add a `## 1.32.1 (YYYY-MM-DD)` section at the top of `cli/azd/CHANGELOG.md`. Include only the
   changes in this hotfix and preserve the existing `1.32.0` section.
2. Set `cli/version.txt` to `1.32.1`.
3. Set `Version` in `cli/azd/pkg/azdext/version.go` to `1.32.1`.

Do not run `eng/scripts/Update-CliVersion.ps1 -NewVersion` for this step. That command is designed to
replace the latest unreleased changelog heading during a normal release. A hotfix starts from an
already released tag, so replacing the heading would incorrectly relabel the previous release notes.

Open a pull request from your fork branch to `Azure/azure-dev:hotfix/azd-1.32.1`, not to `main`.
Merge only after required validation passes and the release owner confirms the commit range.

## Run the release

After the pull request merges, manually queue the Azure DevOps pipeline defined by
`eng/pipelines/release-cli.yml`.

1. Select `hotfix/azd-1.32.1` as the branch.
2. Set the `DoPublish` parameter to `true`.
3. Add the pipeline variable `Skip.IncrementVersion` with the value `true`. The hotfix branch is
   short-lived and must not receive the normal next-minor development-version pull request.
4. Keep the default live Azure record mode unless the release owner has a specific reason to change
   it.
5. Review the selected branch, version, and publish setting before starting the run.

The `PublishCLI` deployment uses the `package-publish` environment gate. Complete the required
approval and monitor every publishing job. The release tag is created at the exact commit identified
by `Build.SourceVersion` for the selected branch.

## Verify and clean up

Do not delete the hotfix branch until all release outputs are available:

- GitHub release and CLI tag: `azure-dev-cli_1.32.1`
- Go module tag: `cli/azd/v1.32.1`
- Packages in WinGet, Chocolatey, and Homebrew
- `azd` installation from the stable and latest channels

Verify that both tags point to the commit built from `hotfix/azd-1.32.1`.

If the fix was cherry-picked from `main`, no forward-port pull request is needed. If the fix exists
only on the hotfix branch, create a new working branch from current `main` and cherry-pick only the
fix commits. Do not copy the hotfix version or changelog preparation back to `main`.

After the release is verified and any required forward-port pull request is open, delete the shared
hotfix branch and its fork branch. The release tags retain the exact released source.
