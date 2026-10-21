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

Install GitHub CLI, Azure CLI, and the Azure DevOps CLI extension on the machine
running the agent. Sign in with accounts authorized for the private tracking
repository and the Azure DevOps `azure-sdk` organization's `internal` project:

```powershell
gh auth login --hostname github.com
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
catalog observations. The renderer discards the prior snapshot, clears displayed
release data and form inputs, and closes pending dialogs. Failed refreshes hide
the previous release view until access can be verified again.

After the GitHub gate succeeds, Azure access requires an Azure CLI login, the
`azure-devops` extension, and successful discovery of exactly one
`azure-dev - cli` pipeline under `\azure-dev`, pointing to
`Azure/azure-dev` and `eng/pipelines/release-cli.yml`. No numeric definition ID is
hardcoded. A login alone does not establish permission to read or queue pipelines.

GitHub tracking remains usable when Azure access is missing; pipeline actions are
disabled and missing access is displayed rather than interpreted as success.

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
3. Refresh to discover the requested PR using its unique, non-sensitive body
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
   panel to explicitly recheck it, or select **Refresh**. It detects the merge
   automatically, turns the **Changelog** roadmap circle green, and offers
   **Preview publishing pipeline** directly in the top next-step panel to the
   driver. Observers see that the release is waiting for its driver; missing
   access is reported as a blocker. The panel also opens an existing-run dialog.
   Merge detection is a live observation, not a persisted completion attestation,
   and does not queue a pipeline or mark publication complete.
   **Check changelog PR** is hidden until a PR is linked, and after a publishing
   build is linked or tracking is closed; **Refresh** remains available.
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

The canvas loads state once when opened and updates after explicit canvas actions.
Use **Refresh** or **Check changelog PR** to check for external changes on demand.
The header stays pinned while scrolling, keeping **Refresh** visible. During a
refresh, the page content is hidden behind a loading spinner and status message;
it returns when the request finishes, including on failure. Reduced-motion
preferences disable the spinner's animation without hiding the status message.
There is no timed polling or refresh when switching back to the page, and no
background monitor when closed. Read-only
refresh never writes observations to the issue. A failed refresh hides previous
evidence; it does not replace missing information with successful defaults.

## Release roadmap

The roadmap is a connected line with five circles. Gray means pending or unknown,
green means the indicated milestone was reached, and red means a relevant failure
or PR closed without merging. Status text and symbols accompany the colors.
On narrow panels the line scrolls horizontally.

| Stop | Turns green when |
| --- | --- |
| Changelog | The tracked changelog PR is merged. |
| CI and signing | The linked run reaches a publishing approval in `PublishCLI` or the parallel `PublishInstallers` stage, or evidence shows it passed that approval. |
| Publishing | The run reaches the final `PublishVersionTxt` approval, or evidence shows it passed that gate. |
| WinGet package | An explicit local catalog check finds the expected `Microsoft.Azd` package version. Merging the WinGet PR is not enough. |
| Version increment | The linked run's generated `cli-version-increment-<buildId>` PR from `azure-sdk/azure-dev` to `Azure/azure-dev` main is merged. Review, approve, and merge it in GitHub. |

Approval observations come from the linked run's `Checkpoint.Approval` records,
correlated to their parent stages and current attempts. A queued stage or unrelated
check does not establish that an approval was reached. Rejected/canceled gates and
failed stages are red; skipped downstream stages are not treated as failures.
Missing access or evidence remains gray rather than implying success.
The canvas does not display a detailed pipeline job list. Open **Publishing build**
in the release summary or **Open pipeline** in the roadmap to inspect stages,
jobs, logs, and approvals in the browser.

**Check WinGet catalog** refreshes the local `winget` source, then reads the
available versions of the exact `Microsoft.Azd` package. It shows the latest
reported version, the expected package version read from the matching PR's
version manifest and checked against release metadata, and when the catalog was checked. The
expected **MSI/package version** is not the CLI version. A newer catalog version
does not substitute for the expected version: the expected version must actually
appear in the available-version list.

The catalog button appears after a matching WinGet PR is discovered, even while
ESRP or unrelated publishing jobs are unfinished, or when publishing reaches the
final approval. A matching PR is required before running the check. Version
increment's **Check version PR** appears after the preceding stages and local
catalog check complete (or metadata explicitly disables WinGet submission).
Completed stops and closed releases hide these buttons.

Catalog checks are explicit and local to the machine running the extension;
they do not write evidence to the tracking issue. **Refresh** rechecks PR and
pipeline state but does not invoke WinGet, and shows any previously checked
catalog result with its timestamp. Catalog observations reset when the extension
restarts and are not reused for a different run or expected version. A failed
catalog check clears the previous result and reports the error instead of
retaining a green stop. Missing WinGet, source-refresh failures, malformed output,
and PR search/access errors are reported explicitly. If WinGet submission is
disabled by metadata, the stop stays gray with a policy note.

The WinGet stop warns: **Do not approve next step until the local WinGet catalog
reports the expected version. Merging the PR is not enough.**
Approvals are still performed in Azure DevOps; the canvas neither grants them nor
technically blocks an authorized approver outside it. Version-increment PR
discovery may succeed earlier in parallel, but remains the last stop in the line.
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
Other team members see a read-only view. This convention does
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
`model.mjs` the shared state contract, `known-issues.mjs` the curated historical
incident summaries, `cli.mjs` CLI invocation, and `server.mjs` the loopback HTTP
boundary. Renderer assets are separate files. No credentials,
private logs, release state, or production destination settings are checked in.

Run offline tests from the repository root:

```powershell
node --test .github\extensions\azd-release\model.test.mjs .github\extensions\azd-release\roadmap.test.mjs .github\extensions\azd-release\release-service.test.mjs .github\extensions\azd-release\server.test.mjs .github\extensions\azd-release\changelog.test.mjs .github\extensions\azd-release\app.test.mjs
```

Reload extensions, inspect `azd-release`, inspect its canvas capabilities, open it,
and invoke `refresh`. Invalid open input and reserved `canvas.*` actions should
be rejected. The agent-facing `analyze_failure` action returns the investigation
prompt; only the confirmed iframe button sends a new agent turn. Mutating
operations are intentionally not exposed as agent-callable canvas actions.

Live queuing must be verified separately by an authorized release driver.
Offline tests do not establish Azure DevOps authentication, API permissions, or
successful production publication.
