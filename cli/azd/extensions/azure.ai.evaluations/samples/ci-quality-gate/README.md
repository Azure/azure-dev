# Evaluation quality gate for CI

A working example of using `azd ai eval` as a pull-request quality gate: every
change to the agent is scored against a curated dataset, and the build fails if
quality regresses.

Copy [`.github/workflows/eval-quality-gate.yml`](.github/workflows/eval-quality-gate.yml)
into your own repository. Nothing here runs from the sample's location.

## What it does

```text
pull request
  -> install azd + the eval extensions (pinned)
  -> sign in with a federated credential
  -> azd ai eval create          reconcile datasets, evaluators, eval group
  -> azd ai eval run start       score the agent, gate on the result
  -> export results as an artifact, pass or fail
```

## What is in the sample

| Path | Purpose |
| --- | --- |
| `azure.yaml` | Foundry project, a hosted triage agent, and the eval service |
| `evals/azure.eval.yaml` | One eval over one dataset, using built-in evaluators |
| `evals/datasets/support-golden.jsonl` | 10 curated support questions |
| `src/triage-agent/main.py` | A deliberately small agent |
| `.github/workflows/eval-quality-gate.yml` | The gate |

Built-in evaluators only, on purpose. A gate with fewer moving parts is a gate
people leave switched on.

## Setup

### 1. Federated credential — no stored secrets

The workflow uses azd's own GitHub federated login, so there is no client
secret anywhere, and no third-party login action:

```bash
azd auth login \
  --client-id "$AZURE_CLIENT_ID" \
  --tenant-id "$AZURE_TENANT_ID" \
  --federated-credential-provider github
```

On the app registration, add a federated credential whose subject matches the
workflow's trigger. For pull requests from the same repository that is
`repo:<owner>/<repo>:pull_request`. A credential scoped only to
`refs/heads/main` will not match a pull-request run, which is the usual reason
a first attempt fails with an authentication error rather than a quality one.

### 2. Permissions

The identity needs to create datasets, evaluators and evaluation runs on the
Foundry project — it is publishing resources, not just reading results. The
built-in role normally used for this is **Azure AI Developer**, assigned at
project scope. Confirm against your tenant's policy before relying on it;
role names and the exact scope required are the thing most likely to differ in
your environment.

### 3. Repository variables

All four are **variables, not secrets** — none of them is a credential:

| Variable | Value |
| --- | --- |
| `AZURE_CLIENT_ID` | App registration client id |
| `AZURE_TENANT_ID` | Tenant id |
| `AZURE_SUBSCRIPTION_ID` | Subscription containing the project |
| `AZURE_ENV_NAME` | azd environment the workflow selects |

## Choosing a gate

`--fail-on` takes one of two forms:

| Value | Fails when |
| --- | --- |
| `any-failure` | Any row fails, **including rows nothing could grade** |
| `pass-rate=<0..1>` | The scored pass rate falls below the threshold |

**Read this before picking one.** `pass-rate` is measured over the rows that
were actually scored — rows that errored or were skipped are outside the
denominator. A run where almost everything errored can therefore report a high
pass rate from the handful of rows that survived. The CLI prints the count that
did not score next to the rate for exactly this reason; do not gate on the rate
alone without looking at it.

`any-failure` does not have that hole, because ungraded rows count against the
run. It is the right choice for a small curated dataset like this one. On a
large or noisy dataset it will fire constantly and the gate will be turned off
within a week, which is worse than having no gate.

A starting point that survives contact with reality:

- fewer than ~20 curated rows → `any-failure`
- larger or noisier datasets → `pass-rate`, set from a few baseline runs on
  `main` rather than from a round number

LLM-judged scores are not deterministic. Two runs over identical rows can
disagree at the margin, so a threshold set exactly at your observed pass rate
will flake. Leave headroom.

## What a failing gate looks like

The workflow distinguishes two failures that are easy to confuse:

> **Quality gate breached** — the evaluation completed and missed the
> threshold. This is a regression. The `evaluation-results` artifact has the
> per-row detail.

> **Evaluation could not run** — the run did not complete, so no quality
> conclusion can be drawn. An infrastructure or configuration failure.

That distinction is the reason the workflow classifies the outcome itself
rather than relying on the exit code. **azd collapses an extension's exit
code**, so a gate breach and a broken run both surface as exit 1. A workflow
that keys off the numeric code will not be able to tell them apart — whether
results came back is what separates them.

## Cost

Every run spends model calls twice: once invoking the agent per row, and again
for each LLM judge scoring that row. This sample has 10 rows and 2 evaluators,
so a full run is roughly 10 agent calls plus 20 judge calls.

`max_samples` is a workflow input rather than a hardcoded number so cost is a
dial, not a rewrite. Lower it on busy repositories and raise it on the branches
that matter. Scoring a 500-row dataset on every pull request is how an
evaluation gate becomes a line item somebody asks about.

## Extending this

**A custom rubric.** Add an `evaluators:` entry pointing at a rubric file and
reference it from the eval. This is the natural next step once the built-in
evaluators stop saying something specific enough about your product.

**Conversation-level evaluation.** Not covered here. Multi-turn simulation is
still being stabilized; add it once it ships.

**Acceptance tests against the live service.** This workflow is also the
closest thing the repository has to a harness for them. The extension's
`ci-test.ps1` states that the `live`, `hero` and `cli` suites are
*type-checked only, never executed*, because they need a live Foundry project
and credentials — which is precisely what this workflow already has. Running
them would mean adding a step that executes the tagged suites with the
project endpoint from the authenticated environment. That is not done here,
deliberately: it needs a decision about which subscription pays for it and how
often it runs.

## Known caveats

- Pinned to `azure.ai.evaluations` and `azure.ai.dataset` `1.0.0-beta.1`. Both
  are beta; re-pin deliberately rather than floating, so a result can be
  attributed to your change and not to a tool that moved underneath it.
- The `evaluation-results` artifact contains model outputs for the rows you
  evaluated. If your dataset contains anything sensitive, the artifact does
  too — set a retention policy accordingly.
