# Installed evaluation scenario CI

This fork-validation workflow exercises the published evaluation and dataset
extensions through the actual `azd` host. It is separate from the immutable
[candidate gate](../../.github/workflows/eval-candidate-proof.yml): it does not
modify that gate's 160 checks, fixture contract, source pins, or four jobs.

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

[The shared runner](../../eng/scripts/eval-scenario-ci/scenario.py) reads the
feed's public GitHub `releases/latest` endpoint once in a producer job. It
records the release ID, immutable tag, publication/resolution timestamps,
source commit, versions and metadata digests. It cross-checks GitHub asset
digests, `SHA256SUMS`, registry entries and publisher provenance before writing
`candidate.json`. Core 1.33.0 and its reviewed archive hashes remain pinned.
A changed core requirement fails explicitly rather than silently upgrading.

Both OS jobs consume the same frozen manifest artifact. They never query Latest.
The original publisher's source declaration is retained as provenance, not
inferred from a filename. Each job verifies archive bytes, computes extracted
and installed executable digests, and checks actual version output. A changed
future CLI contract fails the retained baseline; it is not silently waived.
The baseline command IDs are checked against the ordered, unique
[`checks.json`](../../eng/scripts/eval-candidate-proof/checks.json) contract,
not merely counted.

The subprocess environment is allowlisted, with fresh home, Azure and azd
configuration directories. User tokens, caches, GitHub tokens and pipeline
access tokens are not inherited by CLI subprocesses. After installation the
offline fixture uses a dead HTTP(S) proxy with loopback allowed for host gRPC.
This is not represented as a general-purpose network sandbox.

## GitHub Actions

[Workflow](../../.github/workflows/eval-scenario-ci.yml):

```powershell
gh workflow run eval-scenario-ci.yml --repo m7md7sien/azure-dev `
  --ref m7md7sien-evaluation-github-actions-proof -f mode=offline
```

The dedicated branch's scenario files and shared harness/manifest dependency
changes also run it. A candidate-pin change is a configuration-validation round,
not proof of a newly promoted Latest release; the receipt must still identify
the release actually selected. A release publisher
can invoke the same dispatch after publishing/promoting a new package. The
`azd-bugbash-published` repository event is declared for future adoption on the
fork's default branch; GitHub does not deliver it to a workflow present only on
this validation branch.
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
It has no PR/continuous trigger and no service connection or secret variables.
Register/queue it only in a user-authorized target with existing approved
repository access and capacity. The documented `azure-sdk/internal` pipeline
location is not an execution grant, and this standalone YAML is not a request
to use its shared pools or bypass its production pipeline policies.

No Azure DevOps run URL can be reported until such a target is supplied and
an actual run finishes. Local YAML checks are not an Azure DevOps execution.

## Implemented service sequence, not activated

The optional `service_plan` GitHub input or `servicePlan` Azure DevOps parameter
selects the restricted executor. Empty input still invokes the blocked reporter.
Nonempty input is not authorization: the executor refuses before any command
unless its exact plan SHA256 matches an externally supplied approved digest,
the provider/run/revision match, and approval expires within 24 hours.

The implemented sequence is:

1. Verify approved core/extension executable digests in the supplied isolated
   **CI service-auth** configuration. The profile must contain exactly the two
   approved extensions; each persisted ID/namespace/version and relative
   execution path must match the approved package entry point, and the actual
   resolved file that azd will execute is hashed. An approved conventional
   filename elsewhere is not sufficient.
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
   `run output export` command and require one row plus a successful one-row
   result-count assertion.
5. Delete only the returned owned evaluation and its runs. Cleanup uses the
   existing client's exact `DELETE /openai/v1/evals/{id}` contract rather than
   the CLI's ID-to-name fallback. It obtains an AI-scoped token from the already
   verified CI service identity in memory, never logs or saves that response,
   forbids redirects, and treats only success or ID-not-found as complete.
   There is no name search, pre-check race, or retry. If create returned an
   ambiguous outcome, do not retry the POST or guess an identity to delete;
   report manual reconciliation required.

The real subprocess driver uses fixed argument lists, `--no-prompt`, JSON,
per-command timeouts and a total observation deadline. Cleanup receives its own
bounded command budget even after that deadline; the token request and ID-only
HTTP delete share it. Provider job limits are 30 minutes, covering the maximum
15-minute observation plus 10-minute cleanup and setup overhead. These are
observation/time bounds, not monetary enforcement. Service payloads are parsed
in memory but not uploaded: receipts contain command timing, output digests,
owned IDs and known assertions. The project endpoint and pre-existing dataset
name/version are redacted from public command receipts without changing the
actual command arguments.

The plan must contain all fields checked by `validate_plan`: provider, run ID,
workflow revision, expiry, approval reference, resource owner, client/tenant IDs,
existing project endpoint, approved binary digests and extension versions,
registered dataset name/version/content digest, built-in evaluator/judge,
owned `ci-` prefix, one-run/one-row bounds, command/observation timeouts, and
explicit authorization for download/create/run/export/delete. Unknown fields
are rejected. `AZD_SCENARIO_LIVE_APPROVAL_SHA256` and
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

**A budget number is not enforcement.** The plan additionally requires a
reference to an externally verified service-side budget control. This code
cannot validate or enforce that monetary control, and its plan fields are not
a substitute for native approval. Timeouts, one-row limits and deletion do not
prove billing stopped. No approved tuple/control exists for this work, so
runtime activation remains blocked.
The wrapper bounds its own command submissions, not hidden SDK retries or
service-side billing. Those behaviors and the monetary control must be verified
before activation; a one-command/one-row assertion is not a spending fence.

Agent creation/deployment, dataset creation, generated data, authentication
bootstrap, and verified service-side monetary enforcement remain **NOT
IMPLEMENTED**. They require additional pinned agent artifacts and verified
resource/lifecycle/cost contracts. The restricted static sequence must not be
reported as complete agent lifecycle E2E or a live test pass.

## Local reproduction

Use a new output directory for every attempt:

```powershell
python -m unittest discover -s eng\scripts\eval-scenario-ci -p "test_*.py" -v
python eng\scripts\eval-scenario-ci\scenario.py resolve --output evidence\candidate.json
python eng\scripts\eval-scenario-ci\scenario.py offline `
  --manifest evidence\candidate.json --output evidence\windows
```

The runner never edits the checked-in candidate manifest. Existing output or
manifest paths are refused to avoid overwriting evidence.

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
Only explicitly selected synthetic evidence is uploaded, never home folders,
auth caches, environment dumps, or downloaded credential material.
Resolver consistency checks are not a cryptographic publisher signature.
