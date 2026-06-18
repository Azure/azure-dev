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
| 3. BYO image under VNet | `deploy → invoke` on the provisioned account | `agent invoke` (gated `RUN_DEPLOY=true`) | none extra (reuses phase 3 account) |

The whole matrix (subnet `create`/`reference` × DNS `own`/`reference`) is covered:
the `create+own` cell is the single real provision; the other three cells are
checked with what-if only.

### `--image` is not required for phases 0–4

The project is **hand-authored** (an `azure.yaml` fixture with the foundry
service, `network:` block, and an agent entry using `image:`), so phases 0–4
run against the current branch **without** the BYO-image init UX
(`azd ai agent init --image`, PR 8689). `image:` makes the synthesizer set
`includeAcr=false`, matching BYO image, so no ACR is created at provision.

**Phase 5 (deploy + invoke) is gated behind `RUN_DEPLOY=true`** because it needs
the deploy-time pre-built-image short-circuit from PR 8689 (`AZD_AGENT_SKIP_ACR`
consumption in `service_target_agent.go`). Without it, a headless `azd deploy`
defaults to "build" and fails for a BYO image. Run phases 0–4 today; enable
phase 5 once PR 8689 (via PR 8643 landing on `huimiu/foundry-azure-yaml`) is in
your build.

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

- `az` (logged in), `azd` with the `ai agent` extension available (for the eject
  step `azd ai agent init --infra`), `jq`.
- A subscription with quota for a **westus** network-enabled Foundry account
  (hard requirement). Other regions may be used if westus hits capacity for a
  given resource — override `ACCOUNT_LOCATION`.
- A current `azd x` developer tool. Phase 0 refreshes the dev extension from the
  **current source** (`azd x build` → `pack` → `publish` → `extension install`)
  so the run tests your code, not a stale installed build. This registers the
  `provisioning-provider` capability + the `microsoft.foundry` provider. If your
  installed `azd x` is old (it silently drops the capability), rebuild it first;
  otherwise `azd provision` fails with `extension does not support
  provisioning-provider capability`. Set `SKIP_EXT_REFRESH=true` to reuse the
  already-installed extension.
- For the gated deploy phase only (`RUN_DEPLOY=true`): use an ABAC-enabled ACR
  image that is pullable by the Foundry project's managed identity. The harness
  can build `~/agents/echo-dual` into an ABAC-enabled ACR with `BUILD_IMAGE=true`.
  The build command intentionally uses caller authentication for source registry
  access:
  `az acr build ... --source-acr-auth-id [caller]`.
- The caller that queues the ACR Task must receive **`Container Registry
  Repository Writer`** on the ABAC ACR so the build can push the image. The
  harness grants this before running `az acr build --source-acr-auth-id [caller]`.
- The project MI must receive the ABAC-aware **`Container Registry Repository
  Reader`** role (exact Azure role name; not the legacy `AcrPull`). The harness
  grants this role in `grant_acr_pull` and sets `AZD_AGENT_SKIP_ACR=true` (the
  BYO-image deploy signal). If the registry requires a narrower ABAC condition,
  complete the grant manually and re-run phase 5.
- Because the account is intentionally private (`publicNetworkAccess: Disabled`),
  phase 5 deploy/invoke must run from a host that can resolve and reach the
  private endpoint. Running from the public internet fails with `403 Public
  access is disabled. Please configure private endpoint.`

## Usage

```bash
# from repo root, ensure the extension/CLI is built and on PATH first
export SUBSCRIPTION_ID=<sub-guid>
export ACCOUNT_LOCATION=westus        # hard requirement for the network account

cli/azd/extensions/azure.ai.agents/test/e2e/network/run-network-e2e.sh
```

Phases 0–4 run by default (no deploy). To also run phase 5 and build the
`~/agents/echo-dual` image into an ABAC-enabled ACR:

```bash
RUN_DEPLOY=true BUILD_IMAGE=true \
  cli/azd/extensions/azure.ai.agents/test/e2e/network/run-network-e2e.sh
```

For manual investigation, keep all created test resources in one RG and skip
teardown:

```bash
RUN_DEPLOY=true BUILD_IMAGE=true KEEP=true TARGET_RG=<single-test-rg> \
  cli/azd/extensions/azure.ai.agents/test/e2e/network/run-network-e2e.sh
```

Useful overrides:

| Var | Default | Purpose |
|---|---|---|
| `ACCOUNT_LOCATION` | `westus` | region of the network-enabled Foundry account |
| `RUN_DEPLOY` | `false` | `true` runs phase 5 (deploy + invoke); needs PR 8689 |
| `MAX_PHASE` | `6` | stop after phase N (e.g. `2` for the cheap VNet + what-if gates) |
| `SKIP_EXT_REFRESH` | `false` | `true` skips the phase-0 dev-extension rebuild/reinstall |
| `BUILD_IMAGE` | `false` | `true` builds `ECHO_DUAL_DIR` into an ABAC-enabled ACR before fixtures are generated |
| `ECHO_DUAL_DIR` | `~/agents/echo-dual` | source directory for the phase-5 agent image |
| `ACR_NAME` / `ACR_RG` | derived from `PREFIX` / VNet RG | target ACR used by `BUILD_IMAGE=true` |
| `IMAGE` | the echodual digest or built tag | BYO image (in `agent.yaml`); pulled only in phase 5 |
| `TARGET_RG` | unset | optional single RG for VNet, DNS, ACR, and the real Foundry env |
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
