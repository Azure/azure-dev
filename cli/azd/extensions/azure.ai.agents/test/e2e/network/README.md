# Foundry Private Networking — E2E Harness

Real Azure end-to-end validation for `host: microsoft.foundry` private
networking (the `network:` block: BYO VNet, create/reference subnets, own/
reference private DNS), plus the BYO-image agent lifecycle under a VNet.

> **Cost & creds:** This harness creates real Azure resources and incurs cost.
> Per the repo guidance, run the authenticated job from an **Azure DevOps
> pipeline** (or locally with `azd auth login`), not a public GitHub workflow.

## What it validates

| Scenario | Path | How it's verified | Azure cost |
|---|---|---|---|
| 1. Declarative `network:` | bicep-less (in-memory synth) | `azd provision --preview` what-if shape gate **+** the real provision in phase 3 (same code path) | none extra (what-if) |
| 2. Eject + edit | on-disk template + provision-time `${VAR}` | eject → what-if "no changes" against the live account → manual `infra/` edit delta | none extra (reuses phase 3 account) |
| 3. BYO image under VNet | full `init → provision → deploy → invoke` | one real network account, then `az` resource assertions + `agent invoke` | **one** account |

The whole matrix (subnet `create`/`reference` × DNS `own`/`reference`) is covered:
the `create+own` cell is the single real provision; the other three cells are
checked with what-if only.

## Why it's cheap

The long pole is creating a network-secured Cognitive Services account
(`publicNetworkAccess: Disabled` + private endpoint + DNS, ~8–15 min). The
harness creates that **once**. Everything else uses ARM what-if
(`azd provision --preview`), which creates nothing, and a shared BYO VNet that
is provisioned a single time and reused across cells.

Ordering is fast-fail: local gates → cheap shared VNet → what-if matrix → the
single expensive provision → deploy/invoke → teardown. A broken
template/parameter fails in seconds, not after a 15-minute provision.

## Prerequisites

- `az` (logged in), `azd` with the `ai agent` extension available, `jq`.
- The `azd ai agent init` build under test must support `--image` (BYO image);
  the harness preflight fails fast otherwise. Target subscription/region for the
  greenfield provision are passed via `AZURE_SUBSCRIPTION_ID` / `AZURE_LOCATION`
  (set by the harness from `SUBSCRIPTION_ID` / `ACCOUNT_LOCATION`), not init flags.
- A subscription with quota for a **westus** network-enabled Foundry account
  (hard requirement). Other regions may be used if westus hits capacity for a
  given resource — override `ACCOUNT_LOCATION`.
- The BYO image `…/echodual@sha256:…` must be pullable by the Foundry project's
  managed identity. The registry uses RBAC + ABAC; the harness grants `AcrPull`
  to the project MI post-provision (`grant_acr_pull`). If the grant needs an
  ABAC condition or the registry restricts network access, complete it manually
  and re-run phase 5.

## Usage

```bash
# from repo root, ensure the extension/CLI is built and on PATH first
export SUBSCRIPTION_ID=<sub-guid>
export ACCOUNT_LOCATION=westus        # hard requirement for the network account

cli/azd/extensions/azure.ai.agents/test/e2e/network/run-network-e2e.sh
```

Useful overrides:

| Var | Default | Purpose |
|---|---|---|
| `ACCOUNT_LOCATION` | `westus` | region of the network-enabled Foundry account |
| `IMAGE` | the echodual digest | BYO `--image` reference |
| `KEEP` | `false` | `true` skips teardown (inspect resources, then `azd down --purge` yourself) |
| `OUT_DIR` | `./azd-network-e2e-<ts>` | log directory |
| `RUN_ID` / `PREFIX` | timestamp | name uniqueness |

## Logs

All phases tee to `OUT_DIR/` (`00-context.txt`, `run.log`, `NN-*.log`,
`30-env-after-provision.txt`, `31-assert-resources.log`, `51-show.json`,
`52-invoke.log`). Share these for PR validation.

## Cleanup

Teardown runs on exit (unless `KEEP=true`): `azd down --force --purge` (purge is
required — otherwise the soft-deleted Cognitive account locks the name for ~48h)
and `az group delete` for the shared VNet/DNS resource groups. If a run is
interrupted, clean up manually:

```bash
azd down --force --purge            # from the project dir
az group delete -n <prefix>-vnet-rg --yes
az group delete -n <prefix>-dns-rg  --yes
```

## Files

- `run-network-e2e.sh` — orchestrator (phases 0–6).
- `assert-resources.sh` — live-topology `az` assertions (PE, DNS, delegation,
  `publicNetworkAccess: Disabled`).
- `lib.sh` — shared logging / assertion / `azure.yaml` mutation helpers.
