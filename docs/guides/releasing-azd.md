# Tracking and recovering an azd core release

Use this runbook to prepare, locate, track, and troubleshoot a **core CLI** release given an expected version. It is for contributors and AI agents with authorized access to the Azure DevOps `azure-sdk` organization and `internal` project. Extension releases have separate pipelines and are outside this runbook.

The [changelog-generation skill](../../.github/skills/changelog-generation/SKILL.md) prepares changelog and version files; it explicitly does **not** publish releases or track pipeline completion. A merged changelog PR, a public GitHub release, and a successful end-to-end release are different milestones.

## Safety and sources of truth

- Start with read-only investigation. Get explicit operator approval before queuing builds, retrying jobs, changing credentials, or publishing anything. Do not bypass environment approvals or substitute account keys for missing data-plane permissions.
- Use the pipeline run's **source revision and release metadata**, not today's `main`, the local checkout's version, or a guessed Windows package version. The version-increment job can advance `main` while publication is still running.
- Fetch failed job logs first. Keep run, task, log, and service correlation IDs in the approved internal incident record. Public issues should contain only the necessary sanitized symptoms, public references, and mitigation. Do not copy credentials, signed URLs, private support conversations, full logs, or unrelated private data into public issues or documentation.
- Inspect existing published state before any retry. Some steps are not idempotent: the GitHub release check rejects an existing tag/release, and the WinGet submission does not explicitly resume an existing ESRP operation.
- Report **product publication** separately from **pipeline health**. Manual recovery can complete publication without making the original failed pipeline green.

The entry point is [`eng/pipelines/release-cli.yml`](../../eng/pipelines/release-cli.yml). Its build, signing, and [publishing templates](../../eng/pipelines/templates/stages/publish.yml) define the graph. Re-read them at the selected run's source revision before interpreting that run.

## Find the release and establish its identity

Use authorized Agency Azure DevOps tools where available. Azure CLI with the `azure-devops` extension is an alternative; the following examples use PowerShell 7. Run `az login` if needed. If `az extension show --name azure-devops` reports it missing, install it with `az extension add --name azure-devops`.

```powershell
$org = 'https://dev.azure.com/azure-sdk'
$project = 'internal'
$version = '1.36.0' # Replace with the expected CLI version.

# Discover the definition instead of assuming a permanent numeric ID.
az pipelines list --organization $org --project $project `
  --name 'azure-dev - cli' --query '[].{id:id,name:name,path:path}' -o json

# Enter the definition ID returned above.
$definitionId = [int](Read-Host 'Pipeline definition ID')
az pipelines build list --organization $org --project $project `
  --definition-ids $definitionId --branch main --reason manual --top 20 `
  --query '[].{id:id,status:status,result:result,sourceVersion:sourceVersion,queueTime:queueTime}' -o json
```

If a run URL is supplied, use its `buildId` directly. Otherwise inspect recent candidates, including running and completed failures. Widen the bounded search if needed; do not select a run just because it is newest.

```powershell
$buildId = [int](Read-Host 'Selected publishing build ID')
az pipelines build show --id $buildId --organization $org --project $project `
  --query '{id:id,reason:reason,status:status,result:result,sourceBranch:sourceBranch,sourceVersion:sourceVersion,templateParameters:templateParameters}' -o json

az pipelines runs artifact list --run-id $buildId --organization $org --project $project `
  --query '[].name' -o json
$metadataPath = Join-Path ([IO.Path]::GetTempPath()) ([IO.Path]::GetRandomFileName())
az pipelines runs artifact download --run-id $buildId --artifact-name release-metadata `
  --path $metadataPath --organization $org --project $project
if ($LASTEXITCODE -ne 0) { throw 'Could not download release metadata.' }
$metadata = Get-Content (Join-Path $metadataPath 'metadata.json') -Raw | ConvertFrom-Json
if ($metadata.cliVersion -ne $version) { throw 'This build is for a different CLI version.' }
$metadata
```

Confirm repository, branch, source SHA, manual publishing intent (`DoPublish=true`), and metadata together. `DoPublish` alone does not identify the version. If metadata is not available yet, inspect the source version and build progress; report identity as provisional rather than inventing values.

Metadata includes `cliVersion`, `msiVersion`, `isPublishingGa`, `submitWingetPackage`, `submitChocoPackage`, and `submitBrewFormula`. For 1.36.0, the Windows/package version was **1.36.100**, not 1.36.0. [`Get-MsiVersion.ps1`](../../eng/scripts/Get-MsiVersion.ps1) defines the conversion, but the selected run's metadata is authoritative.

Use `git show <source-sha>:eng/pipelines/templates/stages/publish.yml` and the referenced step templates to inspect historical behavior. Fetch an unavailable revision through normal repository access if necessary.

## Inspect progress and produce a roadmap

```powershell
az devops invoke --organization $org --area build --resource timeline `
  --route-parameters project=$project buildId=$buildId `
  --query "records[?type=='Stage' || type=='Job' || result=='failed'].{id:id,parentId:parentId,type:type,name:name,state:state,result:result,attempt:attempt,logId:log.id,previousAttempts:previousAttempts}" -o json

# Select a failed task's log ID from the timeline.
$logId = [int](Read-Host 'Failed task log ID')
az devops invoke --organization $org --area build --resource logs `
  --route-parameters project=$project buildId=$buildId logId=$logId `
  --query value -o tsv
```

Follow `previousAttempts` with the `timelineId` route parameter to inspect prior attempts. A stage attempt number does not prove that every publishing task ran that many times. Distinguish the first failure, blocked dependents, expected conditional skips, and incidental warnings.

Build an evidence-backed status table for the requested version:

| Milestone | Evidence and completion criteria |
|---|---|
| Release preparation | Changelog PR approved and merged; dated changelog, CLI version, and SDK constant agree at the release source revision. Use the changelog skill for this phase, not for pipeline recovery. |
| Release validation and build | `ValidateReleaseMetadata`, `BuildAndTest`, and applicable installer validation/checks have acceptable results; release metadata and expected artifacts exist. Inspect warnings rather than silently equating `succeededWithIssues` with clean success. |
| Signing and release artifacts | Applicable `Sign` jobs and release packaging succeed; expected signed assets are available. Check the actual dependency graph rather than assuming all displayed stages run sequentially. |
| GitHub and storage publication | `PublishCLI` / `Publish_Release` succeeds; `azure-dev-cli_<version>` is public with required assets, the matching `cli/azd/v<version>` module tag exists, and expected storage artifacts are present. Resolve annotated tags before comparing commit identities. |
| Distribution channels | Inspect `Publish_Choco`, `Prepare_WinGet` -> `Publish_WinGet`, `Publish_Brew`, and applicable installer/shield jobs. Verify external package publication, not merely submission or PR creation. Use metadata flags to explain channels not applicable to a prerelease. |
| Release follow-ups | Track `Increment_Version` and `Publish_Docs_PR`, including the generated PRs and their disposition. A new preview version on `main` does not establish package publication. |
| Final version markers | `PublishVersionTxt` / `ReleaseGate` -> `Upload_Version_Txt` checks WinGet/Chocolatey availability and updates `release/latest/version.txt`, plus `release/stable/version.txt` for GA. Verify public marker contents independently. |

For each row record **complete / running / blocked / failed / not started / not applicable / unknown**, an evidence link or ID, and the next action with its owner and prerequisites. State missing access explicitly. Refresh external state before recommending a mutation.

Useful independent checks:

```powershell
gh release view "azure-dev-cli_$version" --repo Azure/azure-dev `
  --json tagName,isDraft,isPrerelease,publishedAt,url,assets
gh api "repos/Azure/azure-dev/git/ref/tags/cli/azd/v$version"

$packageVersion = $metadata.msiVersion
gh pr list --repo microsoft/winget-pkgs --state all `
  --search "Microsoft.Azd $packageVersion in:title" `
  --json number,title,state,mergedAt,url --limit 20
```

For WinGet, inspect the merged manifest under `manifests/m/Microsoft/Azd/<msiVersion>/` and the PR's publish result. For Chocolatey, check the exact `azd` package version and approval. For Homebrew, inspect the matching formula/casks and publication job in `Azure/homebrew-azd`. Package files, URLs, and versions must match the selected release.

```powershell
$base = 'https://azuresdkartifacts.z5.web.core.windows.net/azd/standalone/release'
foreach ($channel in @('latest', 'stable')) {
    $value = (Invoke-WebRequest "$base/$channel/version.txt" -TimeoutSec 60).Content.Trim()
    Write-Host "$channel : $value"
}
```

For a GA release both markers should match the target at completion; a prerelease must not move `stable`. If a newer release has legitimately advanced a marker, do not overwrite it or call the earlier release incomplete solely on that basis. Correlate historical publication evidence.

## Incident: expired GitHub PAT during release publishing

Tracking issue: [#10522](https://github.com/Azure/azure-dev/issues/10522).

During 1.36.0, **Create GitHub Release and upload artifacts** failed with:

```text
HTTP 401: Bad credentials (https://api.github.com/repos/Azure/azure-dev/releases)
HTTP 401: Bad credentials (https://api.github.com/repos/Azure/azure-dev/releases/tags/azure-dev-cli_1.36.0)
Try authenticating with:  gh auth login -h github.com
```

The release operator confirmed an expired pipeline credential. Logs establish authentication failure, not the specific cause of every future 401.

**Working mitigation:** ask the authorized pipeline owners to investigate authentication and, if necessary, rotate the credential through the approved secret-management process. After they confirm resolution, inspect partial release state, obtain approval, and rerun only the failed jobs/steps on the existing run where safe. The 1.36.0 retry passed after credential rotation. Do not start a duplicate full release or log into the agent interactively just because `gh` suggests it.

At the incident revision, [`publish-cli.yml`](../../eng/pipelines/templates/steps/publish-cli.yml) sets `GH_TOKEN` from a legacy PAT for GitHub release checks, creation/upload, and module tagging. The environment variable already being named `GH_TOKEN` does not mean it contains a GitHub App token.

**Permanent work:** coordinate with pipeline owners to migrate publishing to short-lived GitHub App tokens using the shared [GitHub App login template](../../eng/common/pipelines/templates/steps/login-to-github.yml). Validate the change in the actual deployment-job environment. Check #10522 and the current source before prescribing rotation; update this runbook when the migration lands.

## Incident: duplicate ESRP WinGet PR blocks final markers

Tracking issue: [#10523](https://github.com/Azure/azure-dev/issues/10523). Related onboarding: [#9953](https://github.com/Azure/azure-dev/issues/9953).

During the 1.36.0 release, two WinGet PRs were created for Microsoft.Azd **1.36.100**:

- [microsoft/winget-pkgs#453073](https://github.com/microsoft/winget-pkgs/pull/453073) merged and its publishing pipeline succeeded.
- [microsoft/winget-pkgs#453074](https://github.com/microsoft/winget-pkgs/pull/453074) was closed as a duplicate.

Publishing tracked the closed duplicate as a rejection. `PublishCLI` failed and its dependent `PublishVersionTxt` stage was skipped, leaving installer version markers on 1.35.1 despite package publication.

**Unknown:** why two PRs were created and whether it will recur. Do not attribute the duplicate to retries without additional evidence. This mitigation is not a root-cause fix.

**Do not blindly retry WinGet submission.** [`publish-cli-winget.yml`](../../eng/pipelines/templates/steps/publish-cli-winget.yml) submits to ESRP without an existing-package/merged-PR pre-check or explicit resume ID. A retry re-enters submission; service-side deduplication is not guaranteed by this template. `SubmitWingetPackage` is reloaded from the metadata artifact, so overriding a queue-time variable is not a verified bypass.

Escalate to the publishing service's support channel through the task's support link. Share diagnostic identifiers and logs only through approved internal support channels; public reports can link both public PRs and describe the sanitized failure. Inspect the existing package and get a supported reconciliation procedure before retrying.

### Approved marker-only recovery

For 1.36.0, the release operator successfully used a one-off script to perform only the missing version-marker publication. Both public markers were independently checked as **1.36.0** on October 16, 2026. The original pipeline still recorded failure.

[#10523 contains a recovery template and a 1.36.0 example](https://github.com/Azure/azure-dev/issues/10523). The template reads versions and channel flags from the selected run's release metadata, requires explicit run and storage configuration, and defaults to dry run. Obtain configuration from the authorized pipeline owners rather than publishing internal operational values. It supports **GA releases only** and rejects prerelease metadata. The historical version-specific script is not maintained in the repository.

Before approving any future marker-only recovery:

1. Verify the original run's version, signed/published release assets, tags, distribution channels, and metadata. Confirm that marker publication is the only outstanding release action; a failed submission alone is insufficient evidence.
2. Verify the exact GitHub release asset, merged and published WinGet package, and approved Chocolatey package. Check other required channels and follow-up jobs separately; the issue's template does not prove their state.
3. Inspect both current markers and refuse downgrades. Preserve GA/prerelease channel policy. Do not assume CLI and Windows/package versions are identical.
4. Require explicit operator approval and approved, least-privilege data-plane access to the intended publishing destination. The recovery template requires PowerShell 7 and Azure CLI. Obtain the destination and access requirements from pipeline owners; do not request broader permissions or use account keys to bypass missing access.
5. Use a reviewed, release-appropriate script or a narrowly scoped recovery pipeline with normal approvals. Keep dry-run default, conditional ETag writes, already-current skips, explicit errors, and independent public verification. The two uploads are not atomic; report partial completion and do not roll back blindly.
6. Record recovery evidence and remaining pipeline failure in the approved internal incident/run record. Keep public updates limited to the release version, verified public availability, and sanitized resolution. Do not misreport a manual publication as a successful pipeline retry.

Creating and checking in a reusable recovery utility would require a separate reviewed change with version/metadata validation, channel-policy handling, and offline failure-path tests. This runbook does not introduce such a tool or authorize production writes.

## Recommended future skill boundary

Keep `changelog-generation` focused on release-note/version preparation. A separate proposed **`azd-release`** skill should accept an expected core version and optional build URL/ID, read this runbook, discover and correlate the correct publishing run, then produce the evidence-backed roadmap above from preparation through final public markers and follow-up PRs.

Its default should be read-only status and diagnosis. It should delegate changelog preparation to the existing skill only when needed, distinguish product completion from pipeline health, refresh state on explicit follow-up, link these incident issues, and require approval before retries or publishing. No background monitoring, credential rotation, automatic recovery, or production side effects should be implied by a status request.

This skill is a recommendation, **not implemented** by this documentation change. Keep operational instructions in this runbook so humans and future skills share one maintained source rather than copying incident procedures into multiple prompts.
