# Following an azd core release in the release canvas

The **azd release** canvas is a repository-scoped GitHub Copilot app extension in
`.github/extensions/azd-release/`. Ask the agent to **open the azd release canvas**.
It guides core CLI releases from changelog preparation through publication and
follow-ups. It is not an azd CLI extension and does not handle extension releases.

Operational guidance comes from the [core release runbook PR](https://github.com/Azure/azure-dev/pull/10566).
Once that PR is merged, prefer the maintained `docs/guides/releasing-azd.md` runbook.
The canvas does not copy incident recovery scripts or grant permission to publish.

## Set up access

Use a Copilot app version with SDK extension canvas support and Node.js 22 or later.
The app discovers this project extension automatically; after editing it, reload
extensions. The SDK is supplied by the app, so no package installation is needed.

The canvas is for teammates with access to private release tracking, not a public
release-management service.

**Following saved progress requires only GitHub CLI** on the machine running the
agent, signed in with access to the private tracking repository:

```powershell
gh auth login --hostname github.com
```

Install Azure CLI and the Azure DevOps CLI extension only when you need live
pipeline reads or actions. Sign in with access to the Azure DevOps `azure-sdk`
organization's `internal` project:

```powershell
az login --allow-no-subscriptions
az extension add --name azure-devops
```

Reopen the app if its process predates a CLI installation or PATH change. Prefer
the GitHub CLI credential store: the runtime strips sensitive environment variables
from extension processes, and this extension does not request token forwarding.
The canvas neither installs extensions nor initiates login automatically.

Checking the local WinGet catalog also requires WinGet on the Windows machine
running the canvas extension. Other hosts can follow the release and PR links,
but must use a Windows host for this check. Resolve any WinGet source agreements
interactively in a terminal; the canvas does not accept them automatically.

**Private GitHub access is the first gate**, before any Azure CLI or Azure DevOps
command. Every refresh verifies GitHub identity, that `Azure/azure-dev-pr` is
private with issues enabled, and permission to read its issues. GitHub CLI calls
target `github.com`, not an unrelated `GH_HOST` setting. If proof fails (including
missing CLI/login, insufficient token permissions, SSO authorization, or network
errors), the canvas shows **Team access required**. It does not query Azure DevOps,
load release state or local changelog requests, check WinGet, or start an agent.
Release controls stay hidden. Once access is restored, **Refresh** retries the gate.

Backend actions also require fresh private-repository proof; hiding controls is
not the authorization mechanism. Loss of access clears local queue previews and
package-readiness observations. The renderer discards the prior snapshot, clears displayed
release data and form inputs, and closes pending dialogs. Failed refreshes hide
the previous release view until access can be verified again.

Opening the canvas or selecting **Refresh** stops at GitHub: it loads tracking
and saved observations without invoking Azure CLI, querying pipeline or public PR
status, or rechecking packages. Local preparation requests can be displayed
without rediscovering their PRs. A teammate without Azure login sees the same
saved roadmap as the driver, with the original observation times and authors.

**Refresh live data & share** is the driver's explicit live-read action; other
viewers see **Refresh live data (local)**. Azure access for live pipeline reads requires an Azure CLI login, the
`azure-devops` extension, and successful discovery of exactly one
`azure-dev - cli` pipeline under `\azure-dev`, pointing to
`Azure/azure-dev` and `eng/pipelines/release-cli.yml`. No numeric definition ID is
hardcoded. A login alone does not establish permission to read or queue pipelines.

On a saved view, the Azure badge says **not queried**, not authentication failed.
Pipeline actions are disabled until live access is checked. If an explicit live
read fails, missing access is reported rather than interpreted as success.

This is an application guard, not protection for the publicly available source.
GitHub and Azure DevOps permissions remain the actual service-side boundaries.
Repository/org/project/pipeline identifiers and public runbook links in the source
are configuration metadata, not credentials. Do not commit private issue contents,
real release/run state, logs, tokens, signed URLs, or user-local request artifacts.

## Start and follow a release

1. Select **New release**, enter only the CLI version, then select **Create
   release**. No changelog PR is required yet. This creates
   `[azd-release] <version>` in the private `Azure/azure-dev-pr` repository with the
   `azd-release` label, creating that label if necessary. No pipeline is queued.
   Creation is blocked if another tracking issue is open or the version already
   has a tracking issue. The new entry is selected immediately; creating tracking
   does not start changelog preparation.
2. Inside that tracked release, the **Changelog** setup is visible only while no
   changelog PR is linked and the release remains open. The driver can choose
   **New isolated session** (the default) or **This session**, then **Prepare
   changelog with agent**. This sends an explicit request to the current agent to
   run the [changelog-generation skill](../../.github/skills/changelog-generation/SKILL.md)
   or create an isolated child session for it. The skill's review/approval steps
   remain in the agent conversation; the current agent provides the child session
   link. If session creation is unavailable, the agent reports the limitation
   rather than silently using the current checkout. A pending or discovered request
   hides the preparation button to avoid launching duplicate work.
3. Select **Refresh live data** to discover the requested PR using its unique, non-sensitive body
   marker. Requests are shown only for the selected version in the initiating
   conversation. Select **Link this PR**, or enter an existing changelog PR number
   and select **Verify and link changelog PR**. Linking verifies the public repo,
   target branch, open/merged state, and matching CLI, SDK, and first dated
   changelog versions at the PR head. It records a driver-authored event in the
   private issue, so other viewers and sessions see the same linked PR. Discovery
   alone never writes tracking state. GitHub search indexing can delay discovery;
   entering the existing PR number also works from a different conversation.
   Preparation/linking controls disappear once a PR is linked. Observers cannot
   prepare or link PRs. Neither action queues publishing.

   Anyone with access can open the canvas to discover the active release or select
   a previous one. Discovery prefers the newest open labeled issue, then the newest
   closed issue. Multiple open issues produce a warning; select the intended one
   explicitly. An agent can also open `{ "issueNumber": 123 }` to select a specific
   private tracking issue.
4. After merging the changelog PR, select **Check changelog PR** in the next-step
   panel to explicitly recheck it, or select **Refresh live data**. It detects the merge
   automatically, turns the **Changelog** roadmap circle green, and offers
   **Preview publishing pipeline** directly in the top next-step panel to the
   driver. Observers see that the release is waiting for its driver; missing
   access is reported as a blocker. The panel also opens an existing-run dialog.
   The driver shares the merge observation for teammates, but it is not a
   completion attestation and does not queue a pipeline or mark publication complete.
   **Check changelog PR** is hidden until a PR is linked, and after a publishing
   build is linked or tracking is closed; both refresh actions remain available.
   Inspect the organization, project, pipeline definition, repository, branch,
   full source SHA, and parameters, then type the confirmation to queue.
   The preview expires after five minutes and is single-use. Changes to `main`
   invalidate it. The source must contain the merged PR and matching CLI, SDK,
   and first dated changelog versions. Choose **Live** (the default) or **Playback**
   under **Test recording mode** before previewing. Playback uses recorded test
   responses instead of live Azure validation and can shorten release validation.
   Both modes still publish a real release: playback is not a dry run.
   Queue parameters are `DoPublish=true` and the selected `AzureRecordMode=live`
   or `AzureRecordMode=playback`; ordinary Azure DevOps approvals still apply.
   The selected mode is displayed in the confirmation and bound to its single-use
   preview ticket. To change it, cancel and preview again.
5. If a pipeline was started manually, select **Link an existing publishing run**
   and enter its build ID in the dialog, then **Verify and link existing run**.
   The canvas verifies pipeline,
   repository, branch, manual publishing intent, source version, changelog ancestry,
   and release metadata when available. Missing metadata is explicitly provisional.
6. Follow the five-stop roadmap and open the pipeline in the browser for detailed
   progress. **Known previous issues** below the roadmap links to recorded incidents
   and summarizes their historical workarounds. These are references, not diagnoses
   or automatic recovery actions.
7. Verify final publication, assets, package channels, tags, markers, and follow-ups
   using the runbook before closing the private tracking issue directly in GitHub.
   The canvas no longer displays independent public checks, manual milestone forms,
   or a completion form, and no longer fetches public release/tag/marker checks on
   refresh. Existing recorded evidence is preserved. A green roadmap does not
   independently establish that all publication requirements are met; publication
   and the original pipeline result can differ after approved manual recovery.

The canvas loads saved state once when opened and updates after explicit actions.
Use **Refresh** to pick up another driver's shared observations and tracking
changes. Use **Refresh live data**, **Check changelog PR**, or **Check version PR**
to query external status on demand. The header stays pinned while scrolling,
keeping both refresh buttons visible. During a saved-state refresh, page content
is hidden behind a loading spinner and status message;
it returns when the request finishes, including on failure. Reduced-motion
preferences disable the spinner's animation without hiding the status message.
There is no timed polling or refresh when switching back to the page, and no
background monitor when closed. Saved-state refresh never writes to the issue.
A failed saved-state refresh hides previous evidence until private access can be
verified. A failed live read retains historical observations with stale/failed
labels and separate attempt times; it never substitutes successful defaults.

## Shared observations and freshness

The private tracking issue is also a shared display cache. The first authorized
driver must select **Refresh live data & share** to populate it. Existing tracking
issues need no migration, but show **no shared snapshot** until a genuine live
read has been saved. Package gates need a separate **Check package readiness**.

| Action | Reads and shared effects |
| --- | --- |
| Open / Refresh | Fresh private GitHub authorization, tracking, and saved observations only. No Azure login, live status queries, or snapshot writes. |
| Refresh live data / PR checks, driver | Read current PR/pipeline evidence, revalidate identity and tracking, and append a sanitized observation comment to the private issue. No pipeline or publication mutations. |
| Refresh live data / PR checks, observer | The same live reads, but local display only; never writes the driver's issue snapshots. Refresh returns to the shared view. |
| Check package readiness | Fresh Azure run verification and independent package checks. Drivers share sanitized gates; observers retain local results. WinGet refreshes the checking machine's catalog. |
| Preview, link, queue, or analyze | Perform their own current authorization and identity checks. Saved snapshots are never proof of permission or current run validity. |

Expand **Saved observations and freshness** to see each section's observation
time, observing GitHub user, shared/local origin, stale status, and latest failed
attempt. Changelog, run metadata, timeline, WinGet PR, version PR, and package gates
have independent receipts. Refreshing pipeline status does not make an older
package check or failed PR read appear new. Shared WinGet evidence describes the
driver's machine, not the current viewer's local catalog.

Observations become stale after ten minutes, evaluated when rendering a response;
there is no freshness timer. Older pipeline/PR evidence remains visible as
historical information. Expired or failed package checks become unknown, not
ready. Errors retain the previous successful value and its original timestamp
while recording a separate failed-attempt timestamp. A snapshot write failure is
reported explicitly, not claimed as shared success or automatically retried.
Recheck immediately before release approvals.

Observation comments are append-only `azd-release-observation` JSON blocks,
separate from authoritative `azd-release-state` and `azd-release-event` data.
Schema version 1 binds sections to the issue, CLI version, changelog PR, and
linked build, with pipeline-dependent sections additionally bound to source SHA.
The observing user comes from the comment's GitHub author, not an authored
permission field. Only issue-author snapshots are accepted. Per-section timestamps
prevent a slower, older write from replacing a newer observation. Malformed or
unsupported blocks produce a warning without erasing valid earlier observations.
Human discussion, the issue body, queue reservations, and milestone attestations
are not overwritten.

Stored fields are deliberately compact: normalized statuses, validated
identifiers/versions, distribution flags, and PR identities. URLs are reconstructed
from validated identities. Raw logs, timeline names, previous-attempt diagnostics,
free-form errors, gate diagnostics, credentials, signed URLs, permissions, and
queue-preview tickets are not saved. Failure analysis fetches the live timeline again
and log references after Azure verification instead of using cached diagnostics.
Append-only snapshots add issue comments; opening still reads the issue's
paginated history, but no longer repeats slow live service queries.

## Release roadmap

The roadmap is a connected line with five circles. Gray means pending or unknown,
green means the indicated milestone was reached, and red means a relevant failure
or PR closed without merging. Status text and symbols accompany the colors.
On narrow panels the line scrolls horizontally.

| Stop | Turns green when |
| --- | --- |
| Changelog | The tracked changelog PR was observed merged. |
| CI and signing | The linked run reaches a publishing approval in `PublishCLI` or the parallel `PublishInstallers` stage, or evidence shows it passed that approval. |
| Publishing | The run reaches the final `PublishVersionTxt` approval, or evidence shows it passed that gate. |
| Package readiness | An explicit, fresh check confirms every required distribution gate below for the exact release versions. A merged PR, successful submission, or disabled submission is not enough. |
| Version increment | The linked run's generated `cli-version-increment-<buildId>` PR from `azure-sdk/azure-dev` to `Azure/azure-dev` main is merged. Review, approve, and merge it in GitHub. |

Approval observations come from the linked run's `Checkpoint.Approval` records,
correlated to their parent stages and current attempts. A queued stage or unrelated
check does not establish that an approval was reached. Rejected/canceled gates and
failed stages are red; skipped downstream stages are not treated as failures.
Missing evidence remains gray rather than implying success. Saved statuses are
last-known observations, with freshness displayed separately; they do not assert
that the services are currently reachable.
The canvas does not display a detailed pipeline job list. Open **Publishing build**
in the release summary or **Open pipeline** in the roadmap to inspect stages,
jobs, logs, and approvals in the browser.

### Required gates before announcing a version

**Check package readiness** checks all five distribution gates independently.
Results appear in the **Package readiness before version.txt** cards, with
expected versions, ready/blocked/unknown states, public evidence links, and a
timestamp. The button updates this section and the roadmap without hiding or
refreshing the full canvas. Checks remain available after a successful check so
the operator can recheck immediately before approval.

| Gate | Required evidence |
| --- | --- |
| WinGet | Refresh the local `winget` source and find the exact `Microsoft.Azd` MSI/package version in its available-version list. A newer version or merged PR does not substitute. |
| Chocolatey | The exact `azd` MSI/package version has `IsApproved=true`, passing validation, and passing installation verification in public feed metadata. Submission or exact-version query success alone is insufficient. |
| Homebrew cask | Public `Azure/homebrew-azd` main has the exact CLI version, expected versioned macOS/Linux download definitions, and active checksums for all four architecture keys in `Casks/azd.rb`. |
| Homebrew formula | The same checks for `Formula/azd.rb`, including all four platform/architecture download URLs with active checksum declarations in their corresponding branches. |
| Standalone, MSI, DEB and RPM | The public, non-draft GA GitHub release contains all 11 expected assets, including the Windows ARM64 alpha archive. Each corresponding immutable, versioned public-storage URL responds to HEAD with the same positive size. |

The expected **MSI/package version** comes from verified run metadata and is not
the CLI version. A WinGet PR is useful context but is not required to perform the
check. Chocolatey and Homebrew checks work without installing their CLIs. Missing
WinGet on a non-Windows host leaves that gate unknown while the other checks
still run. Public reads have timeouts and size bounds; unavailable services,
malformed metadata, unexpected definitions, and download size mismatches never
count as ready. These checks inspect distribution evidence: they do not install
packages, execute Homebrew definitions, download/checksum-test binaries, or prove
an individual customer's installation will succeed.
Homebrew checks recognize the tap's generated download layout without executing
Ruby. Commented-out checksums do not count; unsupported layouts remain unknown
and require inspection rather than being treated as ready.

Checking is available to authorized drivers and observers after an explicit live
refresh verifies access and a GA publishing run is linked, even if that run has failed or unrelated jobs are
unfinished. Preview releases require separately reviewed policy and are not
declared ready by this checker. Disabled submission flags do not waive customer
upgrade readiness: all five gates are still required. Version-increment
**Check version PR** remains an independent follow-up and does not authorize
marker updates. Closed or abandoned releases hide the interactive check buttons.

Package checks never request moderation, resubmit packages, queue pipelines,
publish, or update version markers. They refresh the local WinGet source and,
for the driver, save compact observations to the private issue. **Refresh** loads
saved gates without rerunning them; **Refresh live data** refreshes pipeline and
PR status but does not renew package timestamps. Shared observations survive
extension restarts and are bound to the issue, run, source, CLI version, and MSI
version. Local-only observer results do not survive restarts or a saved-state
refresh. Expiration is reevaluated on refresh; there is no background timer.
Access loss clears local data and hides shared observations, but does not delete
issue comments. Starting a new package check clears prior displayed success;
failed reads are shown as unknown, independently of other channels.

Chocolatey is a direct package submission, not a PR workflow. Its package-page
Review section and linked verifier report provide moderation evidence. A failed
verifier can reflect a feed outage before installation, not an MSI defect. If
verification or approval requires action by an account the release team does not
control, document public evidence in an issue and coordinate with that account
owner or Chocolatey operators; the canvas does not assume the driver can rerun
verification. See [Chocolatey's moderation guidance](https://docs.chocolatey.org/en-us/community-repository/moderation/).
The pipeline's existing `Wait-ChocoPackage.ps1` only checks exact-version
`choco info` output, so this canvas deliberately applies a stronger approval and
verification gate.

**Do not approve version.txt or advertise a new version while any required gate
is blocked or unknown.** All-green package evidence is not release permission:
review pipeline health, approvals, the runbook, and any recovery policy separately.
The canvas neither changes pipeline gate conditions nor technically blocks an
authorized approver outside it. Version-increment PR discovery may succeed in
parallel, but remains the last stop in the line.
Neither a green roadmap nor merged follow-up PRs automatically close tracking.
The original seven evidence categories remain compatible with existing tracking
issues, but their controls are not shown. Final publication must still be verified
according to the runbook before the driver closes the issue in GitHub.

### Abandoned or partially published releases

Closing tracking does not make pending or failed roadmap stops green. If the
driver abandons a release after some publication succeeded, document what remains
available, failed channels, unchanged version markers, recovery attempts, and the
successor plan in the private issue. Record a driver-authored comment containing:

```html
<!-- azd-release-event
{"schema":1,"type":"outcome","status":"partial","successorVersion":"1.37.1"}
-->
```

Use the actual successor version, or omit `successorVersion` if none is planned.
Then close the issue as **not planned**, not completed. The canvas displays
**Irregular release** and **Partially completed - abandoned**, preserves linked
runs and existing evidence, and disables further driver actions even if the issue
is reopened. This outcome is shared through GitHub, not local panel state.
Tracking closure does not cancel a pipeline, unpublish artifacts, or start the
successor release. External channels can still finish publishing independently.
Closed issues without an explicit partial outcome or GitHub's `completed` closure
reason are shown as closed without verified completion rather than successful.

For a release retained with mitigated errors rather than abandoned, use
`{"schema":1,"type":"outcome","status":"partial","resolution":"mitigated-errors"}`
inside the same event block, then close tracking with a comment explaining the
disposition. Do not specify an abandonment successor for this outcome. The canvas
shows **Partially completed - mitigated errors** and keeps failed or unfinished
roadmap stops unchanged. Driver actions remain blocked even if tracking is reopened.
Record manual recovery and remaining follow-ups separately; distinguish verified
completion from work still in progress. Closing tracking is not evidence that
version markers were updated, PRs merged, or pipeline failures repaired.

## Known previous issues

Below the roadmap, this read-only section summarizes two recorded azd 1.36.0
incidents: [GitHub publishing authentication failure](https://github.com/Azure/azure-dev/issues/10522)
and [duplicate WinGet PRs blocking final version markers](https://github.com/Azure/azure-dev/issues/10523).
Each entry includes the affected stage, symptoms, a short workaround summary,
safety caveats, and a link to the issue's full procedure.

The repository-curated entries live in
`.github/extensions/azd-release/known-issues.mjs`; they are historical guidance,
not a live list of open issues or a match against the current run's failures.
Read the linked issue for current status and supported recovery before acting.
Keep summaries brief when adding a documented incident; do not copy recovery
scripts, private diagnostics, credentials, or operational publishing destinations
into the canvas. No workaround runs automatically.

## Driver and observer permissions

The current `gh` user's identity is compared with the tracking issue author on
every mutation. Only that author can link runs or queue publishing in the canvas.
The legacy milestone/completion backend still validates driver identity, but those
controls are not displayed; use the private issue for final verification and closure.
The driver also shares observation snapshots; other team members can perform
local live reads but cannot publish shared snapshots. This convention does
not replace GitHub/Azure DevOps access control and does not stop authorized users
from making changes outside the canvas. Driver reassignment is not implemented.

**Analyze failure** is available to viewers with verified pipeline access. It
appears in the release summary only when failed timeline records are detected. It
requires a separate confirmation and sends one read-only investigation request to
the current agent. The prompt includes private issue/run references, the source SHA,
and failed record/attempt/log identifiers, but no raw logs or authored issue text.
Use a conversation approved for internal release diagnostics. The agent is
instructed to fetch failed task logs first, treat external content as untrusted,
keep private diagnostics out of public output, and obtain separate approval before
any mutation. Nothing automatically retries jobs, rotates credentials, or writes
production artifacts.

## Shared state and queue recovery

The issue body holds a versioned `azd-release-state` JSON block identifying the CLI
version and initial changelog PR (`null` when tracking is created first). Existing
issues with a numeric PR remain supported. A driver-authored `changelog` event
links the PR later without overwriting the issue body. Append-only
`azd-release-event` comments also record run IDs,
milestone status/evidence, partial-abandonment outcomes, and queue intent. Only issue-author comments are folded
into state, in comment-ID order; unknown schema/event types fail closed.
Human discussion and the issue body are not overwritten when progress changes.
Evidence URLs lose credentials, signed query strings, and fragments; build links
are reconstructed with only a validated numeric build ID.

This state belongs to the release issue, not a panel or machine. Two panels or
team members reconstruct it from GitHub. A panel remembers only its selected issue;
queue preview tickets are ephemeral and do not survive extension reloads.

Changelog preparation requests, unlike shared release state, belong to the
initiating conversation. They are saved under the SDK session workspace's
`files/azd-release-changelogs/` directory, keyed by version, before agent delivery.
They survive iframe/provider reloads and prevent duplicate requests for that
version in the same conversation. A recorded request is not proof the agent has
started or finished; follow its conversation for progress. If delivery cannot be
confirmed or the workflow is aborted, inspect the conversation and resolve the
request artifact explicitly before starting duplicate work. PR discovery does not
create tracking, merge the PR, or publish anything.

Before queuing, the canvas checks up to 50 manual builds since the changelog merge
for an existing publishing run of the same version. If that bounded search is
exhausted, it refuses to queue. It then appends a queue reservation and rereads
shared state; the first reservation wins. The reservation is never automatically
cleared after a failed or ambiguous queue request. If a queue response or tracking
write fails, find the run in Azure DevOps and link its existing build ID.
Do not delete the reservation and blindly queue again.

GitHub issue comments and Azure DevOps queuing do not form a distributed
transaction. Reservations coordinate canvas clients but cannot serialize an
external/manual queue request, compensate for service consistency issues, or
guarantee exactly-once publishing. If a reservation exists but no build was ever
created, use an operator-reviewed investigation before changing tracking state.
Retries and recovery remain agent-assisted rather than one-click mutations.

## Develop and verify

The entry point wires the SDK; `release-service.mjs` contains the workflow,
`model.mjs` the authoritative state contract, `observations.mjs` the independent
shared-display schema and freshness projection, `known-issues.mjs` the curated historical
incident summaries, `cli.mjs` CLI invocation, and `server.mjs` the loopback HTTP
boundary. Renderer assets are separate files. No credentials,
private logs, release state, or production destination settings are checked in.

Run offline tests from the repository root:

```powershell
node --test .github\extensions\azd-release\*.test.mjs
```

Reload extensions, inspect `azd-release`, inspect its canvas capabilities, open it,
and invoke `refresh`. Invalid open input and reserved `canvas.*` actions should
be rejected. The agent-facing `analyze_failure` action returns the investigation
prompt; only the confirmed iframe button sends a new agent turn. Authoritative
release-state changes and pipeline mutations are intentionally not exposed as agent-callable canvas
actions. `refresh` is GitHub-only. `refresh_live` and `check_packages` explicitly
disclose their observation-comment writes when run by the driver.

Live queuing must be verified separately by an authorized release driver.
Offline tests do not establish Azure DevOps authentication, API permissions, or
successful production publication.
