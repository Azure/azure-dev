# Installed evaluation scenario CI

This shared scenario workflow exercises the published evaluation and dataset
extensions through the actual `azd` host. It is separate from the immutable
[fork-only candidate gate](../../.github/workflows/eval-candidate-proof.yml).
Both preserve the canonical160 check identities and assertions. The candidate
gate retains its source pins and four jobs, with independent approval required
before either binary execution or source checkout.

## Coverage and limits

| Surface | Implemented coverage |
|---|---|
| Installed packages | Exact core version, extension runtime version JSON, archive checksums, extracted and installed binary digests |
| Practical offline authoring | Existing 160 actual commands on Linux and Windows, including static/turn/simulation authoring, custom config paths, dataset binding, JSON and non-interactive refusal |
| Configuration reproducibility | Six extra actual core commands prove separate `AZD_CONFIG_DIR` profiles do not overwrite or inherit each other's state |
| Cancellation arguments | Two extra actual commands reject multiple run IDs and unknown flags without state mutation |
| Interactive cancellation | **NOT RUN**. Argument refusal and closed stdin do not prove Ctrl+C or explicit Cancel behavior |
| Service-backed creation, deployment, evaluation, export | **BLOCKED / NOT RUN**. No approved existing CI identity, resource tuple, and operation budget are configured |
| Azure DevOps execution | **BLOCKED** until an explicitly authorized organization, project, repository connection, pipeline, and hosted capacity are provided |

A successful **offline** run is not a live Azure quality gate. With no service
plan, the manually requested `live` mode exits 3 and saves a blocked receipt. It
does not silently skip to success or accept a caller-supplied boolean as spend
authorization. Activating the restricted executor needs separately approved
existing identity/resource/budget wiring and bounded cleanup.
This contribution does not claim an authenticated service run.
It creates no identities, grants, runners, infrastructure, or paid generation.

Without a service plan, the live path is **status reporting only**:
`live_status()` builds a blocked receipt and the `live` subcommand exits 3.
An implementation-only follow-up adds a restricted
[`service.py`](../../eng/scripts/eval-scenario-ci/service.py) executor described
below. Its tests use mocked commands only; no service execution is claimed.

## Resolve Latest once, then freeze

[The shared runner](../../eng/scripts/eval-scenario-ci/scenario.py) first fetches
the approval manifest from an independently configured, immutable repository
revision. It then reads the feed's public GitHub `releases/latest` endpoint once
in a producer job. It
records the release ID, immutable tag, publication/resolution timestamps,
source commit, versions and metadata digests. It cross-checks GitHub asset
digests, `SHA256SUMS`, registry entries and publisher provenance, then requires
the whole execution tuple to match that independent approval before writing
`candidate.json`. Publisher-controlled hashes prove consistency, not trust.
The core version and its archive hashes remain pinned by the independent approval.
A changed core requirement fails explicitly rather than silently upgrading.

Both OS jobs consume the same frozen manifest artifact. They never query Latest,
but independently refetch the configured immutable approval and check the tuple
before constructing the installer or executing any downloaded binary. A producer
artifact's approval claim is not an authority and cannot replace this lookup.
Consumers also fetch the fixed-tag release metadata, registry, provenance and
checksum list without querying Latest. They verify the bytes, claimed digests,
release ID/publication time and complete source/archive tuple before constructing
the installer. Syntactically valid but forged metadata digests cannot enter a
successful receipt.
Unavailable fixed-tag APIs, rate limits or incomplete HTTP bodies also block
consumer verification. A `BLOCKED` receipt may therefore indicate unavailable
evidence, not necessarily a mismatched approval.
The original publisher's source declaration is retained as provenance, not
inferred from a filename. Each job verifies archive bytes, computes extracted
and installed executable digests, and checks actual version output. Extension
hashes come from the executable route persisted in the isolated
`config.json` (`extension.installed.<id>.path`), after checking the ID,
namespace, version and path containment, before invoking extension version
commands. The receipt rechecks that same route rather than reconstructing a
path from the archive entry point. A changed
future CLI contract fails the retained baseline; it is not silently waived.
The baseline command IDs are checked against the ordered, unique
[`checks.json`](../../eng/scripts/eval-candidate-proof/checks.json) contract,
not merely counted. The eight additional scenario IDs must also match their
exact ordered contract: six profile-isolation checks followed by the two
cancellation-argument refusals. Missing, duplicate, substituted or reordered
checks cannot produce a passing receipt.

### Independent repository approval

Repository/pipeline maintainers must review the immutable tag, source, versions,
registry and archive hashes and select a full40-character commit containing
`eng/scripts/eval-candidate-proof/candidate.json`. The approval is fetched from
that exact revision, **not** from the PR checkout, a moving branch, the producer
artifact, dispatch input or Latest publisher.

GitHub uses its native repository identity plus the repository configuration
variable `AZD_SCENARIO_APPROVED_COMMIT`. Azure DevOps uses separately controlled
`ScenarioApprovalRepository` and `ScenarioApprovedCommit` variables. They map to
`AZD_SCENARIO_APPROVAL_REPOSITORY` and `AZD_SCENARIO_APPROVED_COMMIT` in both
producer and consumer processes. These are maintainer-controlled trust settings,
not queue-time user inputs. This contribution does not configure an upstream
approval or grant permission to set one.
The workflow and verifier themselves must run from a trusted reviewed ref.
Configuration variables are not protection against a malicious workflow that
changes its own verification code or overrides its environment.

For the explicitly authorized fork-only43 enrollment, the owner created
`m7md7sien/azure-dev`'s variable with revision
`e8d07ded9163562976a14b9eb9b61952829bd36e` after independent Main/Track1 approval.
Fresh verification at2026-09-24T10:28:58Z matched that exact value; the approval
manifest SHA256 was
`33e8fcb7ede80b36c8f3f3eb2615344e21dd8799eed6c5751871c63cbdd16a7a`.
This enrolls only the accepted43 tuple. It does not configure Azure/azure-dev,
Azure DevOps, future releases, service execution or a monetary/security grant.

The fixed-candidate workflow uses the same native approval variables. Each
binary job runs `candidate-approval` before invoking the low-level proof runner;
each source-race job obtains its checkout SHA only from `approve_candidate`
after the checked-in manifest matches the independently fetched approval.
Missing approval or changed source/core/archive pins produce an always-uploaded
`approval-status.json` with `BLOCKED / NOT RUN`. A passed admission receipt
means authorization matched, not that a binary or source test has run.
Direct use of the low-level `verify.py` runner requires the same explicit
admission step; the workflow supplies that step rather than treating checkout
content as an authority.

Missing/invalid approval configuration, a different Latest tag, changed hashes,
or a conflicting producer approval claim fails closed with nonzero status and
`approval-status.json` showing `BLOCKED / NOT RUN`. A future publisher release
cannot become executable just by supplying a self-consistent registry, checksum
list and provenance. It requires a newly reviewed immutable approval revision.
Independently fetched approvals and producer manifests reject duplicate JSON
keys recursively. Missing/corrupt producer files and metadata-byte substitutions
also preserve that blocked receipt before release lookup or binary work, as
applicable, rather than failing outside the evidence boundary.
Malformed or unavailable release metadata, duplicate metadata keys and checksum
conflicts also produce a blocked receipt. Consumers validate the complete
resolution schema, both platforms' archive identities, fixed metadata URLs,
registry digest, timestamps and exact JSON types before constructing the
installer. An incomplete producer artifact cannot launch even an approved
executable before discovering the missing evidence.
The native publication handoff and repository dispatch event do not constitute
that approval. A non-Latest candidate-pin commit can therefore leave the separate
Latest scenario blocked until approved configuration and the promoted release
match; fixed-candidate execution remains separate from Latest selection.

Hash consistency is not source-to-binary attestation. Running race tests on a
declared source SHA does not prove an archive was built from that source.
Before approving a new tuple, the independent authorizer must assess available
build attestation or reproducibility evidence. If automatic VCS metadata is
absent, record the provenance limitation and any explicit risk acceptance;
never label publisher build-input records as an independently verified stamp.
The harness authorizes exact reviewed bytes and does not invent missing build
provenance.

The subprocess environment is allowlisted, with fresh home, Azure and azd
configuration directories. User tokens, caches, GitHub tokens and pipeline
access tokens are not inherited by CLI subprocesses. After installation the
offline fixture uses a dead HTTP(S) proxy with loopback allowed for host gRPC.
This is not represented as a general-purpose network sandbox.

## GitHub Actions

[Workflow](../../.github/workflows/eval-scenario-ci.yml):

Pull requests changing the workflow or shared harness/manifest dependencies run
the Python `unittest` suite on Linux and Windows, without approval configuration.
This independent job downloads no candidate packages, invokes no candidate
binaries and makes no live Azure calls; process-lifetime tests use synthetic
Python children. Package resolution and installed-package execution are excluded
from pull requests. On eligible pushes and dispatches, resolution additionally
requires the harness tests to pass. The optional live service job also requires
both OS test jobs and the existing-environment preflight to succeed before it
can execute. All GitHub jobs that invoke Python explicitly select Python3.12.

```powershell
# Requires the maintainer-selected immutable approval revision in repository settings.
gh workflow run eval-scenario-ci.yml --repo m7md7sien/azure-dev `
  --ref m7md7sien-evaluation-github-actions-proof -f mode=offline
```

The main and dedicated validation branches' scenario files and shared harness/manifest dependency
changes also run it once the repository has configured its approval revision.
Automatic pushes in an unenrolled repository do not schedule the resolver and
are **NOT RUN**, not passing test evidence. Explicit manual/repository dispatch
still fails closed when approval is missing. The fixed personal feed/source
scope is intentional test infrastructure, not automatic trust in its publisher;
upstream maintainers must approve the complete immutable tuple before opting in.
A candidate-pin change is a configuration-validation round,
not proof of a newly promoted Latest release; the receipt must still identify
the release actually selected. A release publisher
can invoke the same dispatch after publishing/promoting a new package.
The shared offline jobs do not restrict execution to the validation fork.
After adoption on `Azure/azure-dev` main, manual or repository dispatch uses
the same offline entry point there. The `azd-bugbash-published` event requires
the workflow on the selected repository's default branch; GitHub does not
deliver it to a workflow present only on a validation branch.
**Automatic release-event delivery is not activated or proven by this YAML.**
There is no idle release watcher or scheduled repeat-run loop.

Publication continuation currently uses a **native handoff**, not a GitHub
release trigger. The publisher sends the verified public Latest tag, source,
registry digest and provenance to the sole CI owner. The owner deduplicates by
that immutable identity plus the authorized quality round, reuses an already
queued/completed matching run, and dispatches only when a meaningful new round
is needed. The Latest42 handoff was covered by
[run 35951992378](https://github.com/m7md7sien/azure-dev/actions/runs/35951992378)
at `bca776eecd1f3d697b524d3d2ed321999425aae7`, which resolved public 42 once;
the later handoff did not create a second dispatch. This operational receipt
does not establish an automatic webhook or schedule.

## Azure DevOps

[Pipeline](../../eng/pipelines/eval-scenario-ci.yml) uses the same resolver and
runner, a single frozen manifest, and Linux/Windows Microsoft-hosted images.
It has no PR/continuous trigger and creates no service connection or secret
variables; the optional service path references values supplied by its operator.
Both offline resolution and the optional live path select Python3.12 and run
the shared harness unit suite before invoking their executor. A failing test
step prevents the live service step from running.
The producer and consumers also require the independent approval variables
described above; missing values do not fall back to the checked-out manifest.
Register/queue it only in a user-authorized target with existing approved
repository access and capacity. The documented `azure-sdk/internal` pipeline
location is not an execution grant, and this standalone YAML is not a request
to use its shared pools or bypass its production pipeline policies.
The symbolic variables in this YAML do not enforce who can edit or override
them, and its plain live job does not establish a native environment approval.
Before activation, the owner must supply permission-restricted, non-queue-time
approval configuration and a protected deployment/approval boundary appropriate
to the actual pipeline. No variable group, environment or protection policy is
created or claimed as configured by these files.

No Azure DevOps run URL can be reported until such a target is supplied and
an actual run finishes. Local YAML checks are not an Azure DevOps execution.

## Implemented service sequence, not activated

The optional `service_plan` GitHub input or `servicePlan` Azure DevOps parameter
selects the restricted executor. Empty input still produces blocked evidence.
Nonempty input is not authorization: the executor refuses before any command
unless its exact plan SHA256 matches an externally supplied approved digest,
the provider/run/revision match, and approval expires within 24 hours.
The plan's `ciIdentity` must also exactly match native execution identity:
GitHub repository name/ID, workflow ref/SHA, job and run attempt; Azure DevOps
collection URI/ID, project ID, repository ID/provider, definition ID, job ID
and attempt. Missing or mismatched platform variables block execution before
binary verification or command-driver creation. These are native CI context
values, not caller-supplied workflow inputs.
This is input/context validation, not cryptographic CI attestation, OIDC
bootstrap, or native approval enforcement; protected provider configuration and
explicit activation approval are still required.

On GitHub, the live request first performs a read-only metadata check for the
**already existing** environment named by repository variable
`AZD_SCENARIO_LIVE_ENVIRONMENT`. A missing plan/name, unavailable or denied API,
unknown environment, or absent required-reviewer protection returns nonzero
with a durable `BLOCKED` receipt. Only the API-confirmed name is passed to the
service job's native `environment` binding. No environment is created or
configured by this workflow. The preflight receives only a read-only GitHub
workflow token; Azure identity/plan secrets are referenced only in the protected
service job after the native gate. Offline jobs do not need live configuration.
Mock/schema checks prove the wiring, not actual deployment approval or live
activation.

The existing `static-evaluation` mode retains this sequence:

1. Verify approved core/extension executable digests in the supplied isolated
   **CI service-auth** configuration. The profile must contain exactly the two
   approved evaluation/dataset extensions for this mode; each persisted ID/namespace/version and relative
   execution path is bound to its contained resolved executable and that file
   is hashed. The persisted path is authoritative, including legitimate custom
   locations; an approved conventional filename elsewhere is not sufficient.
   Windows records must name an explicit `.exe` file rather than rely on
   executable-extension lookup.
   Then require `auth status` to report the
   exact approved service-principal client ID. A native AI-scoped token is
   obtained privately to compare its client/tenant claims with the plan; this
   is identity matching of the trusted broker response, not independent JWT
   signature verification. No login, credential copying, IAM, or service
   connection is created, and token output is never logged or saved.
2. Download one explicitly pinned, pre-existing single-file dataset version.
   Require its approved content digest and exactly one completed query/response
   row. No dataset/evaluator publication is allowed.
3. Create a unique owned static evaluation using a registered dataset reference
   and a built-in evaluator. Its generated config has no local dataset source,
   agent target, trace source, or simulation block.
4. Invoke run start once, wait for the returned run ID under the returned eval
   ID, and require terminal completion. Export that exact run through the real
   `run output export` command and require its sole item's `run_id` and
   `datasource_item` to match the owned run and the approved downloaded row,
   without boolean/numeric coercion, plus a successful one-row result-count
   assertion.
   All five `result_counts` fields (`total`, `passed`, `failed`, `errored`,
   `skipped`) must be present as JSON integers. The one-row proof requires
   `total: 1`, `passed: 1`, and explicit zeroes for the other three; missing,
   null or coerced values are not evidence of zero. A non-completed run is an
   execution failure, incomplete counters are insufficient evidence, and valid
   counts that miss the assertion are a completed quality breach. None records
   `quality: PASS`, and owned cleanup remains required in every case.
5. Delete only the returned owned evaluation and its runs. Cleanup uses the
   existing client's exact `DELETE /openai/v1/evals/{id}` contract rather than
   the CLI's ID-to-name fallback. It obtains an AI-scoped token from the already
   verified CI service identity in memory, never logs or saves that response,
   forbids redirects, and treats only success or ID-not-found as complete.
   There is no name search, pre-check race, or retry. If create returned an
   ambiguous outcome, do not retry the POST or guess an identity to delete;
   report manual reconciliation required.

### Owned prompt version and manual dataset

The `owned-prompt-evaluation` mode adds executable code, not a status-only
placeholder. It uses the same protected entry and validation above, then:

1. Validate an approved local UTF-8 JSONL file before identity or resource
   actions. It must have no byte-order mark and contain exactly one object with
   nonempty plain-text `query` and `ground_truth` values. Its raw SHA256 must
   match the approved plan. No dataset generation runs.
2. Generate a unique owned agent name and require its exact GET to return404.
   A present agent is never adopted or edited. Submit one minimal prompt-version
   POST using the existing approved model deployment and instructions, with
   `draft: false` and no tools, hosted, voice or A2A configuration.
3. Preserve the actual returned `name`, string `version` and nonempty opaque
   `id` independently. An observed `name:version` ID is not a contractual format;
   GUID-style IDs are valid. No version1/latest value is invented. An ambiguous POST or missing identity
   is not retried and triggers manual reconciliation rather than guessed deletion.
4. Create a unique owned dataset using
   `azd ai dataset create <name> --from-file <copied-row.jsonl> --version <version>`.
   Confirm the returned name/version, download that exact version and recheck
   the approved bytes before creating an evaluation.
5. Run the existing evaluation create/run/wait/export sequence with
   `target: {type: agent, name: <owned-name>}`. The published067 config has no
   `target.version` field and its builder supplies no explicit agent version.
   This path creates only one version under a fresh owned name and records its
   identity; it does **not** claim an explicit version pin on the evaluation wire.
6. Attempt cleanup for every confirmed owned resource in dependency order:
   evaluation ID, dataset name/version, then agent name/version. All cleanup
   calls share one deadline. A failure in one cleanup does not prevent attempts
   on the others, and primary/cleanup errors are retained separately.

The prompt contract is the public AI Projects2.7.0
[`AgentsOperations.create_version`/`delete_version` API][agent-operations],
with a [pinned public reference][agent-operations-pinned]:
`POST /agents/{name}/versions?api-version=v1`, body
`{"definition":{"kind":"prompt","model":"<existing-deployment>","instructions":"<approved-text>"},"draft":false}`.
Version cleanup targets only
`DELETE /agents/{returned-name}/versions/{returned-version}?api-version=v1`,
never a name search, whole-agent delete, force cascade or guessed latest.
Empty successful deletion is handled without JSON decoding; unsupported or
failed deletion is reported, not broadened to whole-agent cleanup. A nonempty
JSON `null`/array/scalar is not an empty success; a nonempty deletion object
must confirm the exact returned name/version and `deleted: true`.

Dataset create/delete flags and JSON identities are verified against
[the exact public067 command source][dataset-command-contract].
`delete <returned-name> --version <returned-version> --force` removes only that
registered version. Physical blob retention, empty agent-container retention and
stopped billing are not proven by logical version deletion. The executor records
those limitations explicitly. Historical live prompt creation/whole-owned-agent
cleanup informed contract discovery, but does not count as new version-delete,
pipeline or package execution proof.

The owned mode replaces `datasetName` with `datasetFile`, and additionally
requires `agentModel` and `agentInstructions`. `datasetVersion` and
`datasetSha256` remain mandatory. Supplying the existing-dataset name in this
mode is rejected rather than ignored. Its exact `authorizedOperations` set adds
`agent-version-create`, `agent-version-delete`, `dataset-create`, and
`dataset-delete` to the existing five operations.

### Existing-agent CLI smoke, not deployment

The separate `existing-agent-cli-evaluation` mode exercises the actual
`azure.ai.agents` command through `azd`, then evaluates its returned text.
It does **not** deploy, update or delete the existing hosted agent. The
owned v1 prompt-version mode above remains a distinct REST sequence, not
agents-extension deployment proof.

This mode requires an independently approved installed agents executable in
addition to core, evaluation and dataset. The profile must contain exactly
those three approved extension routes, versions and executable hashes; it must
be exclusive to the job and have no existing `extensions.ai-agents` state.
No moving registry or producer input authorizes those bytes. Staging compatible
packages, their dependencies and native authentication is an activation
prerequisite, not something this executor silently installs or waives.
The separate agents owner's current local source
`b185546784fa83ff4eb7b0934888dc843c244ddf` requires core `>=1.34.2`.
Do not reuse the offline public43 core1.33.0 pin for that agents artifact.
That local agents build is not published or independently approved by this
scenario contribution; a compatible core/agents/evaluation/dataset tuple
must be explicitly selected and approved before activation.

1. Verify the service identity, all installed bytes and all three runtime
   extension versions. Agents `version` emits `Version`, `Commit` and `Build Date`
   text, not JSON; validate its exact version without passing an output-format
   flag. Evaluation and dataset version commands retain their JSON assertions.
   Validate one approved manual query/ground-truth row.
2. Create one uniquely named owned session on the approved existing hosted
   agent through `POST /agents/{name}/endpoint/sessions?api-version=v1`.
   Require the returned `agent_session_id` to match the submitted unique ID
   and `version_indicator` to confirm the approved `version_ref`/`agent_version`.
   An ambiguous create is not retried or deleted by guessing.
3. Write the approved query into a private JSON input file and execute
   `azd ai agent invoke --agent-endpoint <invocations-endpoint> --input-file <file> --session-id <returned-id> --timeout <seconds> --debug-latency=false --no-prompt --output raw`.
   Do not add a positional agent name, `--protocol`, or `--version`: the endpoint
   selects the protocol and the already-created session binds the version.
   The agents command does not support JSON output for invoke.
4. Parse the captured raw HTTP response privately. Require one synchronous
   HTTP200 response with a single JSON content type, no error envelope, and a
   nonempty string in the approved response field. Reject SSE, HTTP202/LRO,
   duplicate JSON keys and malformed/empty output. Exit0 alone is not success.
5. Combine that actual response with the approved query/ground-truth row, create
   one uniquely named dataset version, download and hash-check those exact
   derived bytes, then use the static evaluation create/run/wait/export flow.
   This does not invoke the agent again during evaluation.
6. Attempt deletion of the owned eval ID, dataset version and returned session
   ID, sharing one cleanup deadline. Session deletion uses only
   `DELETE /agents/{approved-name}/endpoint/sessions/{returned-id}?api-version=v1`,
   accepting success or404 without decoding an empty body. Remove and read back
   only the initially absent `extensions.ai-agents` local namespace. Preserve
   the rest of the caller's isolated auth profile. Retain primary and every
   cleanup failure separately. Never delete the existing agent or its versions.

The additional plan fields are `datasetFile`, `agentName`, `agentVersion`,
`agentInputField` and `agentResponseField`, replacing `datasetName`.
Input/response field names are simple top-level JSON property names, not
expressions. The raw input JSONL SHA256 remains `datasetSha256`; the generated
response-row hash is recorded as `invokedDatasetSha256`.
`versions` adds `azure.ai.agents`; `binarySha256` adds its executable digest.
The exact operation set adds `agent-session-create`, `agent-invoke`,
`agent-session-delete`, `dataset-create` and `dataset-delete` to the five
static-evaluation operations. One inference, one row and one evaluation run
are fixed execution bounds, not monetary enforcement.

The shared entry point remains `service.py --plan <approved-json> --output <new-directory>`.
`service-status.json` retains `status`, `execution`, `planSha256`, native run
identity, command timing/output hashes, `quality`, exact owned identities,
`remoteCleanup`, `datasetCleanup`, `sessionCleanup`, `agentStateCleanup` and
local `cleanup`. `agentCliInvocation.status` is `NOT RUN`, `FAIL` or `PASS`;
its success is independent of evaluation quality and cleanup.
Exit0 requires the complete selected lifecycle and cleanup, exit3 means
pre-execution `BLOCKED / NOT RUN`, and exit1 means failed started work.
Mock results are not real GitHub/Azure DevOps service-run evidence.

The command/session contract was checked against the repository's
[`invoke.go`](../../cli/azd/extensions/azure.ai.agents/internal/cmd/invoke.go),
[`config_store.go`](../../cli/azd/extensions/azure.ai.agents/internal/cmd/config_store.go)
and [session operations](../../cli/azd/extensions/azure.ai.agents/internal/pkg/agents/agent_api/operations.go).
The actual agents surface has no `ai agent create` command. Core
`azd deploy <service> --no-prompt` requires a separately validated approved
project, provider/artifact tuple and deployment/teardown contract. Prompt
agents use a different service path and support whole-agent, not per-version,
CLI deletion. That core-deployment stage is **NOT IMPLEMENTED**, not an alias
for this smoke or the v1 REST mode. Whole-agent cleanup would require explicit
ownership and deletion authorization; no shared-agent teardown is inferred.

### Executor implementation matrix

All service rows below are default-off and **actual execution NOT RUN** for this
contribution. Unit/mock coverage is not native approval or live acceptance.

| Operation | `executorImplemented` and verified entry | Mock coverage | GitHub Actions / Azure DevOps wiring |
| --- | --- | --- | --- |
| Static workflow | Yes: `service.lifecycle`, existing registered row | Existing success, refusal, result and cleanup tests | Both call `service.py --plan`; GitHub additionally binds an existing protected environment |
| Agent version create | Yes: `owned_prompt_lifecycle`, public v1 prompt-version POST | Existing-name refusal, exact minimal body, returned version9, ambiguous/missing identity | Both select `owned-prompt-evaluation` through the approved plan |
| Existing-agent CLI smoke | Yes: `existing_agent_cli_lifecycle`, real `ai agent invoke` raw-output contract | Version-bound session, actual response-to-dataset/eval binding, raw/error/SSE/LRO controls, exact session/local-state cleanup | Both select `existing-agent-cli-evaluation` through the approved plan; actual service execution is NOT RUN |
| Core agent deployment | **No**: supported core command is `azd deploy <service> --no-prompt`, not an agent-create subcommand | No approved complete project/provider/deploy/whole-owned-agent-cleanup contract or deployed-artifact proof | NOT IMPLEMENTED; not interchangeable with v1 prompt version creation or existing-agent smoke |
| Agent infrastructure deploy | **No**: no separate deploy command is part of this minimal prompt-version contract | No hosted/deployment proof | Not wired. `azd deploy` with an agent service would require an approved code/image artifact, service definition, compute/registry/infra and teardown contract; hosted/A2A/voice infrastructure is outside this scope |
| Manual dataset create | Yes: published `ai dataset create --from-file --version` | Exact copied bytes/returned version, uncertain write, round-trip mismatch | Both use the same owned-mode executor |
| Eval create | Yes: published `ai eval create --from-file` | Exact target/catalog, unique name, returned eval ID | Both use the same executor |
| Run/wait | Yes: published `run start --no-wait`, `run show --wait` | Returned ID binding, terminal status, one command, failure paths | Both use the same executor |
| Export | Yes: published `run output export --format json --output-file -` | Exact item/run/approved-row binding; wrong/null shapes and numeric types | Both use the same executor |
| Owned cleanup | Yes, scoped identities: eval ID DELETE, dataset version CLI DELETE, agent-version API DELETE | No guessed identity, empty response, one shared budget, all cleanup attempts and retained primary failures | Both use the same executor; no broad project/agent/infrastructure deletion |

The real subprocess driver uses fixed argument lists, `--no-prompt`, the command's supported output format,
per-command timeouts and a total observation deadline. Cleanup receives its own
bounded command budget even after that deadline; the token request and ID-only
HTTP operations and dataset-version deletion share it. Provider job limits are 30 minutes, covering the maximum
15-minute observation plus 10-minute cleanup and setup overhead. These are
observation/time bounds, not monetary enforcement. Service payloads are parsed
in memory but not uploaded: receipts contain command timing, output digests,
owned IDs and known assertions. The project endpoint and pre-existing dataset
name/version are redacted from public command receipts without changing the
actual command arguments.

Both the service driver and the offline proof runner use the shared
[`owned_process.py`](../../eng/scripts/eval-candidate-proof/owned_process.py)
lifetime boundary. On POSIX, the launcher starts a new process group; on Windows,
it is assigned to a non-breakaway job before its private launch request is sent.
Launcher exit is observed independently of stdout/stderr EOF, so a child holding
inherited pipes cannot keep working until the command timeout after its parent
exits. Remaining owned members are terminated before bounded output draining
and resource/workspace cleanup; the original exit code and output are retained.
POSIX signals the original process group and reaps the launcher; it does not
assert that a deliberately detached group/session is contained or perform a
Windows-style group-empty check. Windows additionally verifies its job drains.
Local regressions start a real child heartbeat and verify that activity stops
before return, including inherited-pipe parent exit. A missing executable
returns127, and POSIX permission-denied launch returns126; Windows launch errors
retain127. Both use a fixed error rather than a Python traceback or private
path. A primary timeout remains a timeout if
process cleanup also fails; command receipts retain secondary
`processCleanupErrors` rather than losing the `timedOut` outcome.
This is owned-process cleanup with platform-specific limits, not a
general-purpose sandbox, remote cancellation confirmation or proof that billing
stopped.

Authenticated HTTP runs in a short-lived owned Python transport process.
The parent enforces one remaining absolute deadline across pipe writes/reads,
connection, headers, body consumption and child exit, and terminates/reaps only
that owned process on timeout or a communication failure. A slowly
arriving body cannot extend the cleanup window through socket-read timeouts.
Tokens and payloads travel only through private captured pipes, not command
arguments, environment variables, files or public receipts. Response metadata
is limited to one MiB as a harness safety bound, not an Azure API limit.
Local regressions stall input, mocked headers/body and process exit, and cover
a crashing child with private diagnostics. They verify owned-process cleanup
without any network or authentication operation.
Client termination does not establish that a remote POST stopped or was
rolled back, so ambiguous outcomes remain failures requiring reconciliation.

The plan must contain all fields checked by `validate_plan`: provider, run ID,
workflow revision, expiry, approval reference, resource owner, client/tenant IDs,
existing project endpoint, approved binary digests and extension versions,
registered dataset name/version/content digest, built-in evaluator/judge,
owned `ci-` prefix, one-run/one-row bounds, command/observation timeouts, and
explicit authorization for download/create/run/export/delete. Unknown fields
and duplicate JSON keys at any nesting level are rejected before commands run.
`AZD_SCENARIO_LIVE_APPROVAL_SHA256` and
`AZD_SCENARIO_LIVE_AUTH_CONFIG` must come from an already-authorized provider
configuration; they are not populated by this contribution. Azure DevOps maps
the corresponding `ScenarioLiveApprovalSha256` and `ScenarioLiveAuthConfig`
variables. The supplied auth directory belongs to its caller and is not
removed by the executor; its lifecycle and tenant authorization must be reviewed
as part of provider activation.
The provider must securely stage the per-run approved plan and existing auth
configuration before selecting this path; that bootstrap is not implemented
by these jobs. Supplying a service plan with offline mode is an error, not an
ignored input.
The shipped jobs cannot become a live executor merely by committing a plan and
setting `service_plan`: that file cannot contain its own workflow commit and
future run ID, and the jobs do not stage authenticated profiles or approved
executables. A protected, run-bound delivery/bootstrap step must be added and
authorized first. Until then, the service entry points are deliberately
unreachable as a successful hosted lifecycle, not an activated feature.
Missing or malformed plans and unavailable pre-execution inputs produce
`BLOCKED / NOT RUN` with exit3. Once command execution starts, command, assertion
and cleanup failures are `FAIL`, not a new prerequisite block.

**A budget number is not enforcement.** The plan additionally requires a
reference to an externally verified service-side budget control. This code
cannot validate or enforce that monetary control, and its plan fields are not
a substitute for native approval. Timeouts, one-row limits and deletion do not
prove billing stopped. No approved tuple/control exists for this work, so
runtime activation remains blocked.
The wrapper bounds its own command submissions, not hidden SDK retries or
service-side billing. Those behaviors and the monetary control must be verified
before activation; a one-command/one-row assertion is not a spending fence.

Infrastructure deployment, generated data, authentication bootstrap and verified
service-side monetary enforcement remain **NOT IMPLEMENTED**. Prompt-version
creation and manual dataset creation are implemented as described above; missing
runtime identity is an execution block, not a claim those code paths are absent.
No service execution, hosted/A2A/voice deployment, or complete live E2E pass is
claimed.

[agent-operations]: https://learn.microsoft.com/en-us/python/api/azure-ai-projects/azure.ai.projects.operations.agentsoperations?view=azure-python
[agent-operations-pinned]: https://github.com/MicrosoftDocs/azure-docs-sdk-python/blob/7b985631332617f11a4d7e965fa06021f7cf52b0/docs-ref-autogen/azure-ai-projects/azure.ai.projects.operations.AgentsOperations.yml
[dataset-command-contract]: https://github.com/m7md7sien/azure-dev/blob/067b2fd5622494db2dc6ac3a2e9bc19e871ed9e7/cli/azd/extensions/azure.ai.dataset/internal/cmd/dataset.go

## Local reproduction

Use a new output directory for every attempt:

```powershell
python -m unittest discover -s eng\scripts\eval-scenario-ci -p "test_*.py" -v
# Set these only to an independently reviewed repository and immutable revision.
$env:AZD_SCENARIO_APPROVAL_REPOSITORY = "<reviewed-owner/repository>"
$env:AZD_SCENARIO_APPROVED_COMMIT = "<reviewed-full-commit-sha>"
python eng\scripts\eval-scenario-ci\scenario.py resolve --output evidence\candidate.json
python eng\scripts\eval-scenario-ci\scenario.py offline `
  --manifest evidence\candidate.json --output evidence\windows
```

The runner never edits the checked-in candidate manifest. Existing output or
manifest paths are refused to avoid overwriting evidence.
A failed resolution also reserves its evidence directory through
`approval-status.json`. Even after prerequisites recover, use a new output
directory rather than placing a successful manifest beside the old blocked
receipt.

## Evidence

Each offline artifact includes `candidate.json`, `commands.json`,
`results.json`, `summary.md`, `live-status.json`, synthetic authored YAML and
strict state/binding receipts. `results.json` records provider/run identity,
the shared manifest digest, baseline/additional check counts, exact source,
runtime versions, archive/installed byte comparisons, and cleanup status.
The overall receipt is not marked `PASS` until owned temporary cleanup finishes;
Windows sharing violations receive bounded filesystem retries, not ignored errors.
Command receipts include UTC `startedAt`/`finishedAt`, monotonic
`durationSeconds`, the actual `timeoutSeconds`, and partial sanitized output
when a subprocess times out. Current candidate and scenario receipts use
`PASS`/`FAIL`; archived earlier candidate receipts retain their original
lowercase status values and are not rewritten.
GitHub artifacts
are retained for 14 days; preserve approved immutable receipts in the release
ledger rather than assuming artifacts last forever.

URLs in captured CLI output lose user information, query and fragment credentials.
Service filesystem/process exceptions use fixed diagnostics rather than raw
exception text or absolute filenames; runtime diagnostics retain the exception
class and numeric error code when present. This applies to
preflight, primary runtime failures, resource cleanup and local cleanup receipts
as well as terminal errors; URL sanitization alone is not filesystem redaction.
Only explicitly selected synthetic evidence is uploaded, never home folders,
auth caches, environment dumps, or downloaded credential material.
Resolver consistency checks are not a cryptographic publisher signature.
