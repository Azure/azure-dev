# Evaluation scenario CI

The [GitHub Actions workflow](../../.github/workflows/eval-scenario-ci.yml) runs
unauthenticated installed-package acceptance on Windows and Linux. It installs
the latest released `azd` and the latest `azure.ai.evaluations` and
`azure.ai.dataset` packages from the canonical `azd` registry, checks the installed
versions and source, starts both command routes, and runs the offline evidence
and script tests. It has no Azure secrets, subscription, OIDC permission, or
live evaluation job.

Both CI surfaces use
[`install_packages.ps1`](../../eng/scripts/eval-scenario-ci/install_packages.ps1).
Resolved versions are recorded in job output; these checks intentionally track
releases rather than fixing a historical package version.

## Internal live pipeline

[`eng/pipelines/eval-scenario-live.yml`](../../eng/pipelines/eval-scenario-live.yml)
is a separate, default-off Azure DevOps pipeline. It uses the repository's 1ES
template and `AzureCLI@2` service-connection pattern. It runs only when manually
queued in the `internal` project from `refs/heads/main` with `runLive=true`.
There are no push, PR, or scheduled triggers.

An internal pipeline administrator must register this YAML, explicitly authorize
the pipeline on an approved test service connection, and configure maintainer
approval/checks on that connection before use. The `serviceConnection` parameter
defaults to the existing repository convention, `azure-sdk-tests`; this file
does not create or authorize a connection. Provide these pipeline variables:

| Variable | Purpose |
|---|---|
| `EVAL_SCENARIO_PROJECT_ENDPOINT` | Existing Foundry project endpoint; configure as secret |
| `EVAL_SCENARIO_LOCATION` | Location for the existing test resource group |
| `EVAL_SCENARIO_RESOURCE_GROUP` | Existing, approved test resource group |

The subscription is read from the approved service connection's active Azure CLI
session, not from GitHub or a checked-in subscription ID. `auth.useAzCliAuth`
keeps execution inside the same `AzureCLI@2` task; `keepAzSessionActive` maintains
the federated session for the run.

The scenario copies the checked-in fixture to temporary storage, reconciles it
with `azd up --no-prompt`, starts `ci-f1-quality` asynchronously, reads `run_id`,
reattaches with `--wait --fail-on pass-rate=0.8`, and exports that exact run.
This is **real Azure execution**, not a fake subscription: it publishes a
dataset/evaluation, incurs service costs, and creates an empty nested ARM
deployment as a reconciliation marker. Administrators own the dedicated test
resources and their retention; this pipeline does not delete remote assets.
Do not queue it against production resources.

## Evidence and failure contract

The evaluator threshold lives in
[`f1-parameters.json`](../../eng/scripts/eval-scenario-ci/fixture/live/f1-parameters.json).
The evaluation includes that file with `$ref`, and the live runner passes the
same value to the sanitizer. The runner's single quality-gate value is passed
both to `--fail-on` and to `--quality-gate-threshold`.

Only a completed run whose aggregate counts agree with all reviewed sample
verdicts can produce publishable evidence. `total` must cover passed, failed,
errored, and skipped counts. This two-row fixture additionally requires exactly
two scored rows and no errored, skipped, or unaccounted rows. Pass rate is
`passed / total`, matching the CLI.

The artifact contains only aggregate counts, thresholds, and the reviewed
`query`, `response`, `ground_truth`, numeric `score`, and `passed` fields.
Duplicate JSON properties, malformed JSON/JSONL, non-finite numbers, fixture
drift, and inconsistent results fail closed. A validated threshold breach
publishes sanitized evidence and fails the job; a CLI failure that disagrees
with the evidence is an operational failure and cannot authorize publication.
Raw command output and the temporary project are removed in `finally`; the
pipeline also removes its isolated azd configuration.

Run the offline checks locally from the repository root (Python 3.12+ and
PowerShell 7 are required; the script tests replace Azure/azd commands with
in-process fakes and never authenticate):

```powershell
python -m unittest discover -s eng/scripts/eval-scenario-ci -p "test_*.py" -v
```

Checking in the pipeline is not evidence of a successful live run. Registration,
service-connection approval, test-resource configuration, and a maintainer-
approved internal run remain separate operational requirements.
