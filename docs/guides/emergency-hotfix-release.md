# Release an emergency hotfix

Use this process to release a small, urgent patch for the latest stable release without including
unrelated changes from `main`.
The core CLI release pipeline supports manual releases from branches other than `main`.

Follow the Azure SDK
[hotfix branching policy](https://azure.github.io/azure-sdk/policies_repobranching.html#hotfix-branches)
unless this guide gives azd-specific instructions.

## Before you start

Use the standard release process when changes can wait for the next planned release. Use a hotfix only
when delaying the fix presents more risk than releasing an out-of-band patch.

This procedure supports only the latest stable release. Do not use it to service an older version.
The release pipeline updates the global `release/latest` and `release/stable` channels, including
the public `version.txt` used for upgrade discovery. Publishing an older version can suppress upgrade
notifications or direct users to a version older than the latest release.

Older-version servicing requires a separate publishing procedure, agreed with the engineering systems
team, that preserves the global channels and upgrade metadata and accounts for package-manager
publication. It is outside the scope of this guide.

Choose the next patch version. The examples below assume `1.32.0` is the latest stable release and
use `1.32.1` for the hotfix. Substitute the actual latest stable version and next patch version.
Confirm that the source release exists and that the target patch version has not already been released.

## Create the shared hotfix branch

An authorized maintainer creates the short-lived `hotfix/azd-1.32.1` branch in `Azure/azure-dev`
from the immutable `azure-dev-cli_1.32.0` release tag.

Do not create the branch from `main`. The release tag is the exact source used to build the released
version.

Use the normal fork and pull request workflow for changes to the shared hotfix branch.

Azure Pipelines loads its templates from the selected hotfix branch. Confirm that the branch's
`eng/pipelines/templates/steps/publish-cli.yml` passes `--target "$(Build.SourceVersion)"` to
`gh release create`. If the source release tag predates that safeguard, include the same pipeline
change in the hotfix pull request. Without it, GitHub creates the release tag from `main` instead of
the commit that produced the hotfix artifacts.

Have the engineering systems team review whether the branch's release infrastructure is compatible
with current build and publishing requirements. Infrastructure changes since the source release may
require additional backports from `main`, including changes under `eng/`. Review and validate those
backports with the product fixes; do not assume that adding the target argument alone is sufficient
or replace the entire `eng/` directory without that review.

## Apply and review the fix

Cherry-pick only the approved fix commits:

```bash
git cherry-pick <commit-sha>
```

Resolve conflicts against the released code, not against current behavior on `main`. Run the build,
tests, and other validation that cover the changed code.

Review the complete hotfix range before preparing the release. The comparison view for the hotfix
pull request should contain only the intended fixes, required release-infrastructure backports, and
release preparation.

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
approval and monitor every publishing job. Immediately before approving publication, reconfirm with
the release owner that the source release is still the latest stable release and the target version
is unused. Coordinate other releases to avoid concurrent publication. If a newer stable release has
shipped while the hotfix was being prepared, stop and prepare the fix against that release instead.
The release tag is created at the exact commit identified by `Build.SourceVersion` for the selected
branch.

`Skip.IncrementVersion=true` only suppresses the next-development-version pull request. It does not
skip `PublishVersionTxt` or prevent updates to public upgrade metadata. Skipping `PublishVersionTxt`
alone would not make older-version servicing safe: `PublishCLI` also uploads release artifacts to the
global latest and stable channels. Keep the normal publication stages for a latest-stable hotfix.

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
