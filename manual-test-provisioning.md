# Manual test plan — bicepless `microsoft.foundry` feature

End-to-end manual test plan for the **bicepless Foundry** feature in the
`azure.ai.agents` extension. Covers the shipped commits:

| Commit | Feature |
|---|---|
| `7226fd640` | In-memory bicep synthesizer |
| `afbb9975f` | `microsoft.foundry` provisioning provider |
| `19609f4cc` | `azd ai agent init --infra` (eject) |
| `94226a74e` | Compile and deploy on-disk Bicep when `./infra/` exists |
| `5ae5b45e2` | `azd provision --preview` via ARM what-if |
| `59ed6991e` | `Parameters()` on the on-disk path (regression fix) |

Three phases:

- **Phase A** — smoke (no Azure, ~5 min). Confirms install + dispatch + refusal paths.
- **Phase B** — `--infra` eject + on-disk provision (no Azure, ~3 min). Confirms the spec's E2E "edit `main.bicep` → next provision applies the edit" criterion using fixtures.
- **Phase C** — live deploy (real subscription, ~5–10 min). Confirms end-to-end ARM, preview, and `--force` destroy.

This doc is a developer aid; it's not committed to the repo as a public reference.

> **Auto-test alternative**: the OpenCode agent at `.opencode/agents/test-bicepless.md` automates Phase A and the no-Azure part of Phase B. Invoke it with `@test-bicepless` for a regression run. The `azd ai agent init` command is intentionally NOT covered there (interactive only); the auto-test starts from a pre-written `azure.yaml`.

---

## Important: `host:` vs `infra.provider:`

`azure.yaml` has two **separate** routing keys that the provider relies on:

| Key | Value used today | What it routes to |
|---|---|---|
| `services.<name>.host` | `azure.ai.agent` | The extension's **service-target** (handles `azd deploy`) |
| `infra.provider` | `microsoft.foundry` | The extension's **provisioning provider** (handles `azd provision`) |

If you set `host: microsoft.foundry`, azd-core will reject the project with
`service host 'microsoft.foundry' for service '...' is unsupported` — that
host name isn't claimed by any service-target today.

The provider's accepted host list lives in
`internal/project/provisioning_provider.go` as `FoundryServiceHosts` and
currently contains a single value: `azure.ai.agent`.

### Terminology drift to be aware of

The spec (`spec/bicepless-foundry/spec.md`) uses `resourceId:` to mean
"this is a brownfield project, use the existing Foundry project." The
shipped code uses `endpoint:` for the same concept. They map to the
same `CodeBrownfieldNotSupported` refusal today (greenfield only).
Treat the two terms as synonyms when reading the spec.

---

## Prerequisites

```powershell
# Tool versions
azd version                                           # >= 1.25.4 (needs provisioning-provider capability)
az --version                                          # for one-time provider registration
go version                                            # 1.26.x
& "$env:USERPROFILE\.azd\bin\bicep.exe" --version    # 0.44.1 (auto-downloaded by the on-disk path)
```

One-time, per subscription (skip if already done):

```powershell
az login
az account set --subscription "<your-test-subscription-id>"
az provider register --namespace Microsoft.CognitiveServices
az provider register --namespace Microsoft.ContainerRegistry  # only if you'll test docker:
```

Grab your AAD object id (needed for the developer role assignment in bicep):

```powershell
az ad signed-in-user show --query id -o tsv
```

---

## Phase A — Smoke test (no Azure)

Goal: confirm the dev build installs, azd discovers the provider, and the refusal paths fire as designed.

### A1. Install the dev build over the registry build

```powershell
cd C:\Users\zhihuan\source\azure-dev\cli\azd\extensions\azure.ai.agents
azd x build
```

Expected: ends with `SUCCESS: Build completed successfully!`. Verify:

```powershell
azd extension list --installed | Select-String "azure.ai.agents"
```

### A2. Scaffold a test project

```powershell
$test = "$env:TEMP\foundry-prov-test"
Remove-Item -Recurse -Force $test -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path $test | Out-Null
cd $test

@'
name: foundry-prov-test

services:
  app:
    host: azure.ai.agent
    deployments:
      - name: gpt-4.1-mini
        model:
          format: OpenAI
          name: gpt-4.1-mini
          version: "2025-04-14"
        sku:
          capacity: 10
          name: GlobalStandard
    agents:
      - name: prompt-agent
        kind: prompt
        instructions: You are a test agent.

infra:
  provider: microsoft.foundry
'@ | Set-Content azure.yaml -Encoding utf8

azd env new dev
```

### A3. Confirm preview reaches the provider

`azd provision --preview` is now fully implemented (commit `5ae5b45e2`).
It calls ARM what-if and emits the diff summary via progress callbacks
so users see it even though azd-core's adapter still drops the
structured `Summary` field (gap B1 from the audit).

Without `AZURE_SUBSCRIPTION_ID` set, the provider will refuse with a
clean `missing_azure_subscription` error — this is the no-Azure smoke
that confirms dispatch + lazy credential resolution:

```powershell
azd provision --preview --no-prompt 2>&1 | Select-Object -Last 6
```

| If you see... | Then... |
|---|---|
| `AZURE_SUBSCRIPTION_ID is required but not set in azd environment "dev"` | Provider reachable; lazy `ensureCredential` short-circuits before any network call. **Phase A success.** Ready for Phase C once env values are set. |
| `Reauthentication required` (`AADSTS9002313`) | Your azd auth token expired. Run `azd auth login --tenant-id <tenant>` and retry. |
| `service host 'azure.ai.agent' for service 'app' is unsupported` | Dev build didn't reinstall. Re-run `azd x build` and verify `azd extension list --installed`. |
| `failed resolving IaC provider 'microsoft.foundry'` | Manifest didn't update — re-run `azd x build` and check `extension.yaml` has both the `provisioning-provider` capability and the `providers[]` entry. |
| `extension does not support provisioning-provider capability` | azd host is older than 1.25.4. Run `winget upgrade Microsoft.Azd`. |

### A4. Test the brownfield short-circuit

```powershell
@'
name: foundry-prov-test

services:
  app:
    host: azure.ai.agent
    endpoint: https://existing.services.ai.azure.com/api/projects/foo
    deployments:
      - name: gpt-4.1-mini
        model:
          format: OpenAI
          name: gpt-4.1-mini
          version: "2025-04-14"
        sku:
          capacity: 10
          name: GlobalStandard

infra:
  provider: microsoft.foundry
'@ | Set-Content azure.yaml -Encoding utf8

azd provision --no-prompt 2>&1 | Select-Object -Last 10
```

**Expected error:**

```
endpoint: is set on the foundry service; existing-project (brownfield)
provisioning is not supported yet
```

with code `brownfield_not_supported`.

The same refusal must fire on the on-disk path too (handled by the
`rejectBrownfield` helper in `foundry_provisioning_provider.go` after
the on-disk detection).

### A5. Test the missing-service error

```powershell
@'
name: foundry-prov-test
services:
  app:
    host: staticwebapp
    project: src/app
infra:
  provider: microsoft.foundry
'@ | Set-Content azure.yaml -Encoding utf8

azd provision --no-prompt 2>&1 | Select-Object -Last 10
```

> `host: staticwebapp` is used instead of `containerapp` so the test
> doesn't require Docker locally. Either host kind triggers the same
> foundry "no matching service" check; SWA is the lower-friction
> fixture for an environment that may not have Docker installed.

**Expected error:**

```
no service in azure.yaml has host in [azure.ai.agent]
```

with code `provisioning_service_not_found` (category: `Dependency`).

### A6. Test `--infra` refusal: `./infra/` already exists

Reset `azure.yaml` to the good A2 content, then:

```powershell
@'
name: foundry-prov-test

services:
  app:
    host: azure.ai.agent
    deployments:
      - name: gpt-4.1-mini
        model:
          format: OpenAI
          name: gpt-4.1-mini
          version: "2025-04-14"
        sku:
          capacity: 10
          name: GlobalStandard
    agents:
      - name: prompt-agent
        kind: prompt
        instructions: You are a test agent.

infra:
  provider: microsoft.foundry
'@ | Set-Content azure.yaml -Encoding utf8

New-Item -ItemType Directory -Force -Path infra | Out-Null
azd ai agent init --infra --no-prompt 2>&1 | Select-Object -Last 6
```

**Expected error:** `` `./infra/` already exists `` with code `infra_eject_exists`
and suggestion *"to regenerate from azure.yaml, delete the infra directory and run the command again"*.

### A7. Test `--infra` refusal: conflicting arguments

Remove the `./infra/` dir again:

```powershell
Remove-Item -Recurse -Force infra
azd ai agent init --infra -m ./somefile.yaml --no-prompt 2>&1 | Select-Object -Last 6
```

**Expected error:** `` `--infra` on an existing project runs eject only and does not accept a positional path, -m/--manifest, or --src ``
with code `infra_eject_conflicting_arguments`.

---

## Phase B — `--infra` eject + on-disk provision (no Azure)

Goal: verify the spec's E2E criterion — *"edit `main.bicep` → next provision applies the edit"* — without needing a live subscription.

Use the good A2 `azure.yaml` (no `endpoint:`, with deployments, no
`./infra/`).

### B1. Eject

```powershell
azd ai agent init --infra --no-prompt 2>&1 | Select-Object -Last 12
```

**Expected output:**

```
Generating infrastructure files from azure.yaml...

  Created infra/abbreviations.json
  Created infra/main.bicep
  Created infra/main.parameters.json
  Created infra/modules/acr.bicep

Future provisions will read from ./infra/.

Next steps:
  azd provision    Apply changes
```

Verify the four files are on disk and that `azure.yaml` was **not**
mutated (per RFC #8065 §Eject Command):

```powershell
Get-ChildItem -Recurse infra | ForEach-Object { $_.FullName.Substring((Resolve-Path .).Path.Length + 1) }
Get-Content azure.yaml | Select-String -Pattern "infra:|provider:"
```

`azure.yaml` should still contain only the original `infra.provider:
microsoft.foundry` line — no new keys added.

### B2. Confirm preview detects the on-disk template

```powershell
azd provision --preview --no-prompt 2>&1 | Select-Object -Last 8
```

**Expected:** the preview attempts to compile `./infra/main.bicep` via
`bicep.Cli` (auto-downloaded to `~/.azd/bin/bicep` if missing), then
refuses at the credential step with `AZURE_SUBSCRIPTION_ID is required`.
Look for a line like `Compiling on-disk Bicep templates...` in the
output — that confirms the on-disk detection fired.

If you see the embedded path instead (no compile line) → `./infra/`
detection broke; check that `main.bicep` exists at the expected path.

### B3. Edit `main.bicep`; provision must re-compile

```powershell
# Append a no-op comment to force a content change
Add-Content infra/main.bicep "`n// edited at $(Get-Date -Format o)"
azd provision --preview --no-prompt 2>&1 | Select-Object -Last 8
```

Same expected output as B2 — the change should NOT affect the preview
result (it's just a comment) but should NOT be silently ignored either
(prior bug: pre-`94226a74e` the provider always used the embedded ARM
JSON regardless of `./infra/`).

### B4. Refuse on existing infra (re-eject)

```powershell
azd ai agent init --infra --no-prompt 2>&1 | Select-Object -Last 4
```

**Expected error:** `` `./infra/` already exists `` (`infra_eject_exists`).

---

## Phase C — Live deploy (real Azure)

Goal: end-to-end smoke against your test subscription. **Will create real
resources and incur cost** (Foundry account is essentially free, GPT
deployment is free until used, ACR is ~$5/mo if you include it).

### C1. Restore the good azure.yaml + clear on-disk infra

```powershell
Remove-Item -Recurse -Force infra -ErrorAction SilentlyContinue
@'
name: foundry-prov-test

services:
  app:
    host: azure.ai.agent
    deployments:
      - name: gpt-4.1-mini
        model:
          format: OpenAI
          name: gpt-4.1-mini
          version: "2025-04-14"
        sku:
          capacity: 10
          name: GlobalStandard
    agents:
      - name: prompt-agent
        kind: prompt
        instructions: You are a test agent.

infra:
  provider: microsoft.foundry
'@ | Set-Content azure.yaml -Encoding utf8
```

### C2. Set required env values + pre-create the resource group

First, **pick a region with quota**. `eastus2` and `northcentralus` are
frequently over their `gpt-4.1-mini GlobalStandard` quota on shared test
subscriptions. Survey before picking:

```powershell
# Lists current usage vs quota for each region. The "Used" column is
# already-allocated TPM (thousands); pick a region with comfortable
# headroom over the 5,000-TPM-thousand default limit. Skip with
# the documented westus3 default if you don't want to survey.
$candidates = @("westus3","swedencentral","eastus","westeurope","eastus2","northcentralus")
foreach ($r in $candidates) {
    $u = az cognitiveservices usage list -l $r --query "[?name.value=='OpenAI.GlobalStandard.gpt4.1-mini'].{used:currentValue,limit:limit}" -o tsv 2>$null
    "{0,-16} {1}" -f $r, ($u -replace "`t","/")
}
```

Then set the env values (default `westus3` works on most subs):

```powershell
$sub  = az account show --query id -o tsv
$loc  = "westus3"                                    # change after the survey above if needed
$oid  = az ad signed-in-user show --query id -o tsv
$rg   = "rg-foundry-prov-test-$($env:USERNAME)-$loc" # region-suffixed to avoid cross-region reuse

azd env set AZURE_SUBSCRIPTION_ID  $sub
azd env set AZURE_LOCATION         $loc
azd env set AZURE_RESOURCE_GROUP   $rg
azd env set AZURE_PRINCIPAL_ID     $oid
azd env set AZURE_AI_PROJECT_NAME  "fdtest-$($env:USERNAME)"
```

**Pre-create the resource group.** ARM what-if (used by
`azd provision --preview` in C3) returns 404 if the RG doesn't yet
exist. `azd provision` itself creates the RG on first use, but
preview won't — create it manually before C3:

```powershell
az group create -n $rg -l $loc -o table
```

Sanity check:

```powershell
azd env get-values
```

### C3. Run preview (now with a real diff summary)

```powershell
azd provision --preview 2>&1 | Tee-Object -FilePath preview.log
```

**Expected output sections** (emitted via the progress callback per
the B1 workaround for gap B1):

```
What-if status: Succeeded
Total changes: N
  Create: N
Affected resources:
  + Create Microsoft.CognitiveServices/accounts/cog-<token>
  + Create Microsoft.CognitiveServices/accounts/cog-<token>/projects/fdtest-<user>
  + Create Microsoft.CognitiveServices/accounts/cog-<token>/deployments/gpt-4.1-mini
  ...
```

If you see an empty body (no `What-if status: ...` line) → the
progress workaround didn't fire. Check that `Preview` made it past
`ensureCredential` (look in `--debug` output).

### C4. Run provision

```powershell
azd provision 2>&1 | Tee-Object -FilePath provision.log
```

**Expected sequence:**

1. `Foundry deployment in progress` heartbeats every ~5s
2. Resource group `rg-foundry-prov-test-<user>` appears in the portal
3. CognitiveServices account `cog-<token>` created
4. Project `fdtest-<user>` created under the account
5. GPT-4.1-mini deployment provisioned
6. Role assignment for your AAD object id created on the project
7. Outputs written back to azd environment

After completion, verify outputs:

```powershell
azd env get-values |
  Select-String "AZURE_AI_PROJECT_ID|AZURE_AI_ACCOUNT_NAME|FOUNDRY_PROJECT_ENDPOINT|AZURE_OPENAI_ENDPOINT"
```

All four should be populated.

Verify the deployment was tagged with the template source (new in commit `94226a74e`):

```powershell
az deployment group show -g $rg -n "azd-foundry-dev" --query "tags"
```

Expected: `azd-provision-template-source` = `embedded` (this run used
the in-memory synthesizer).

### C5. Verify in Azure

```powershell
$accountName = (azd env get-value AZURE_AI_ACCOUNT_NAME)
az cognitiveservices account show -n $accountName -g $rg `
  --query "{name:name,kind:kind,sku:sku.name,location:location,subdomain:properties.customSubDomainName}"
az resource list -g $rg --query "[].{name:name,type:type}" -o table
```

Expected: see the CognitiveServices account, the `accounts/projects` child,
the `accounts/deployments/gpt-4.1-mini` deployment, and a role assignment.

### C6. Idempotent re-run

```powershell
azd provision 2>&1 | Select-String -Pattern "deployment in progress|complete"
```

**Expected:** completes with no new resources created (ARM incremental mode).
Deployment record `azd-foundry-dev` updates.

### C7. On-disk re-deploy (eject + edit + provision)

```powershell
# Eject the now-deployed project
azd ai agent init --infra --no-prompt 2>&1 | Select-Object -Last 8

# Edit main.bicep (no-op comment is enough to prove dispatch)
Add-Content infra/main.bicep "`n// on-disk reprovision test"

# Re-provision. Watch for the "Compiling on-disk Bicep templates..." progress line.
azd provision 2>&1 | Tee-Object -FilePath provision-ondisk.log
Select-String -Path provision-ondisk.log -Pattern "Compiling on-disk|on-disk template"
```

Verify the deployment record now has `azd-provision-template-source` = `ondisk_bicep`:

```powershell
az deployment group show -g $rg -n "azd-foundry-dev" --query "tags.\"azd-provision-template-source\""
```

### C8. ACR variant (optional, +1 min)

Edit `azure.yaml` to add a `docker:` block to the agent:

```yaml
    agents:
      - name: prompt-agent
        kind: hosted
        project: src/prompt-agent
        docker:
          path: Dockerfile
          remoteBuild: true
```

```powershell
azd provision 2>&1 | Select-String -Pattern "acr|registry|deployment in progress"
```

**Expected:** ACR module deploys this time; new env vars:

```powershell
azd env get-values |
  Select-String "AZURE_CONTAINER_REGISTRY_ENDPOINT|AZURE_AI_PROJECT_ACR_CONNECTION_NAME"
```

### C9. `azd down --force` actually deletes the RG (new in `afbb9975f` + reviewer fix)

```powershell
azd down --force 2>&1 | Select-Object -Last 15
```

**Expected:** progress line `Deleting resource group rg-foundry-prov-test-<user>...`
followed by `Foundry deployment complete`. Verify the RG is gone:

```powershell
az group exists -n $rg
# expected: false
```

Without `--force` the destroy refuses (also new behavior — prior to the
review fix it silently leaked resources):

```powershell
# Re-provision first to have something to delete
azd provision
azd down 2>&1 | Select-Object -Last 6
```

**Expected error:** `microsoft.foundry destroy will delete resource group ... --force is required`
with code `destroy_requires_force`.

### C10. Final teardown

```powershell
az group delete -n $rg --yes --no-wait
azd env delete dev --no-prompt 2>$null
```

---

## Failure modes

| Symptom | Likely cause | Fix |
|---|---|---|
| `failed resolving IaC provider 'microsoft.foundry'` | Dev build not installed, or `extension.yaml` not loaded | `azd extension list --installed`; rerun `azd x build` |
| `extension does not support provisioning-provider capability` | azd host < 1.25.4 | `winget upgrade Microsoft.Azd` |
| `service host 'azure.ai.agent' for service 'app' is unsupported` | Dev build not installed (old registry version still active) | Same as above |
| `AZURE_SUBSCRIPTION_ID is required` | env value not set | `azd env get-values` then `azd env set ...` |
| `AADSTS9002313: Reauthentication required` | azd auth token expired (NOT a provider bug) | `azd auth login --tenant-id <tenant>` |
| `tenant_lookup_failed` | not logged in to azd | `azd auth login --tenant-id <tenant>` |
| `Microsoft.CognitiveServices subscription is not registered` | One-time provider registration missed | `az provider register --namespace Microsoft.CognitiveServices` |
| `RegionDoesNotAllowProvisioning` / capacity errors | Region doesn't have GPT-4.1-mini GlobalStandard quota | Re-run the C2 region survey; change `AZURE_LOCATION` to `westus3`, `eastus`, `swedencentral`, or `westeurope`. Avoid `eastus2` and `northcentralus` on shared test subscriptions (frequently over quota). |
| `azd provision --preview` returns `404 ResourceGroupNotFound` | ARM what-if needs the RG to pre-exist; only full `azd provision` creates the RG on demand | Pre-create the RG before C3: `az group create -n $rg -l $loc` (now documented in C2) |
| `ondisk_bicep_compile_failed` | User's `main.bicep` is malformed | The bicep CLI's own error message is embedded in the error; fix and retry. |
| `arm_what_if_failed: InsufficientQuota` | Preview detected a real quota issue | This is the preview doing its job — fix the quota or change region |
| Heartbeat forever, no resources appear | Check `provision.log` for ARM error; check Azure Activity Log on the RG | `az monitor activity-log list -g $rg --query "[?level=='Error']"` |
| Brownfield error didn't fire (A4) | YAML edit didn't take | `Get-Content azure.yaml` to verify `endpoint:` is present |
| Preview body is empty (just header + footer, no diff lines) | Progress workaround didn't fire OR azd-core fixed gap B1 and is now rendering Summary directly | Check `--debug` to confirm `Compiling on-disk Bicep templates...` (on-disk path) or `Computing deployment plan...` (embedded path) appeared |
| `destroy_requires_force` | Running `azd down` without `--force` (correct behavior since `afbb9975f` + reviewer fix) | Re-run with `azd down --force` if you actually want to delete the RG |

---

## What this plan does NOT prove

- The synthesized parameters drive a Foundry data-plane (agents/toolboxes/connections) reconciliation. That's `azd deploy`, not `azd provision`.
- Brownfield (`endpoint:` set) provisioning works. It's intentionally rejected today.
- The provider survives concurrent `azd provision` calls or partial failures mid-deploy. Out of scope for v1.
- Full interactive `azd ai agent init` flow. That's interactive-only; covered separately.
- Schema validation, service-graph invariants, deploy-mode invariant, env-var resolution at synth time (spec §Validation Pipeline steps 1–4). Deferred per spec.
- Telemetry fields `init.infra_flag`, `provision.synthesis_source` (deferred — extension has no telemetry plumbing yet; the deployment tag `azd-provision-template-source` covers the in-portal view).

## Known limitations (post-feature-complete)

### `Summary` field on `ProvisioningPreviewResult` is dropped by azd-core's adapter

The structured `Summary` we return from `Preview` is silently dropped
by `cli/azd/internal/grpcserver/external_provisioning_provider.go`
(gap B1 from the audit). The workaround is to emit the same summary
line-by-line via the `progress` callback — that's what users see in
their terminal today. When azd-core extends the proto with a
`repeated changes` field, the workaround becomes a confirmation line
and the structured Summary will render properly.

### Spec §Validation Pipeline steps 1–4 not implemented

The synthesizer doesn't run JSON schema validation, service-graph
invariants, deploy-mode-XOR-runtime checks, or env-var resolution at
synthesis time. Step 5 (brownfield consistency) is done via the
`endpoint:` field (spec calls it `resourceId:` — terminology drift).
All four deferred items error gracefully downstream (bad YAML errors
out at parse, missing services error out at the dependency check,
etc.), so the practical impact is the error message quality rather
than correctness.

### Telemetry fields not emitted

Spec calls for `init.infra_flag` and `provision.synthesis_source` to
be emitted as telemetry. The extension doesn't have telemetry
plumbing yet. The deployment tag `azd-provision-template-source`
gives an equivalent signal that's visible in the Azure portal.

## Cleanup checklist

Before closing the test session:

- [ ] Resource group deleted (`az group list -o table | findstr foundry-prov-test`)
- [ ] azd env deleted (`azd env list`)
- [ ] `$env:TEMP\foundry-prov-test` deleted (`Remove-Item -Recurse -Force $env:TEMP\foundry-prov-test`)
</content>
</invoke>