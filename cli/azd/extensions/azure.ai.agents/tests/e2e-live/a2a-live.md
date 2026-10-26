# Opt-in A2A live E2E — retain everything

<!-- cspell:ignore WORKROOT -->

`TestA2ALiveRetained` tests real azd **init → deploy → invoke** against an
**existing** Foundry project. It is separate from `TestTier2Live`: that older
runner tears resources down, and is deliberately not used here.

**Cost / retention:** two uniquely named agents and their versions/sessions are
created on every run. The test never invokes down, delete, stop, session cleanup,
resource cleanup, or artifact cleanup, even on failure. It never provisions a
project or deploys a model. Existing agent identities are not reused. Retained
agents/sessions may continue incurring charges; their lifecycle is the operator's
responsibility outside this test. Do not run the whole live suite to run this test.

No pipeline or scheduled execution is added. The ordinary package test skips the
live case. A separate opt-in variable (not the existing Tier 2 gate) is required.

## Prerequisites

- Linux/WSL and the Go 1.26 toolchain specified by the extension's `go.mod`.
- An explicit executable azd host with the matching locally built
  `azure.ai.agents` extension **already registered** in the selected azd config.
  This test does not build/install extensions or change shared configuration.
- Existing azd authentication and a logged-in Azure CLI (`az` on PATH). azd is
  used for init, deploy, and every invocation. Azure CLI `az rest` is used only
  for live definition/endpoint GETs and the prompt endpoint PATCH; it obtains its
  own AI-audience credentials without printing tokens. The two clients must be
  authenticated to the same tenant with access to the supplied project.
- An existing, network-accessible Foundry project in a region/subscription with
  hosted agents, Python 3.13 remote code builds, **native A2A 1.0**, and **prompt
  A2A translation** enabled. These capabilities are service-side prerequisites;
  the test does not enable subscription features. The planned validation target
  is a fresh **West US 2** project in subscription
  `1756abc0-3554-4341-8d6a-46674962ea19`; no resource ID is baked into the test.
- An existing chat model deployment usable by plain prompt agents. The operator
  supplies its deployment name, not a model catalog name. The test does not create
  or modify that deployment. The prompt must obey a fixed-answer instruction.
- RBAC allowing agent creation/code deploy, invocation, agent GET and endpoint
  PATCH. Internet access from the hosted remote builder to install the fixture's
  pinned Python packages; local Go dependencies must already be available or
  restored by the operator.

## Environment contract

| Variable | Contract |
| --- | --- |
| `AZD_E2E_A2A_LIVE` | Must be exactly `true`. Unset, `1`, or any other value skips. |
| `AZD_E2E_A2A_PROJECT_ID` | Required existing `/subscriptions/.../resourceGroups/.../providers/Microsoft.CognitiveServices/accounts/.../projects/...` ARM ID. Missing/blank skips before any command. Malformed input fails locally. |
| `AZD_E2E_A2A_MODEL_DEPLOYMENT` | Required existing model deployment name. Missing/blank skips. |
| `AZD_E2E_A2A_WORKROOT` | Required absolute artifact parent directory. A fresh `a2a-retained-*` child is created every run, never removed/reused. Missing/blank skips. Use a private directory **outside** the source config directories. |
| `AZD_E2E_AZD_PATH` | Required absolute executable azd host path. No fallback to PATH. Missing/blank skips; invalid path fails locally. |
| `AZD_CONFIG_DIR` | Optional source config; otherwise `~/.azd`. Copied to the new run's private config directory, so installed extensions/auth remain available without modifying the original. |
| `AZURE_CONFIG_DIR` | Optional source Azure CLI config; otherwise `~/.azure`. Likewise copied privately. |

The test sets `AZD_ALPHA_ENABLE_AGENTS_A2A=true` **only in child process
environments**, and switches it to `false` for feature-off cases. It also sets
non-TTY, no-color, and telemetry-disabled child environments. It never persists
preview opt-in in azure.yaml or user config. Caller-supplied process-only request
attribution is inherited unchanged; set it inline when launching the test if
required by your validation procedure. Do not put attribution into fixtures,
project files, or shared config.

## Run

From `cli/azd/extensions/azure.ai.agents`, after the operator has supplied the
project/model/binary paths:

```bash
AZD_E2E_A2A_LIVE=true \
AZD_E2E_A2A_PROJECT_ID="$PROJECT_ARM_ID" \
AZD_E2E_A2A_MODEL_DEPLOYMENT="$MODEL_DEPLOYMENT" \
AZD_E2E_A2A_WORKROOT="$PRIVATE_ARTIFACT_ROOT" \
AZD_E2E_AZD_PATH="$AZD_BINARY" \
  go test -mod=readonly ./tests/e2e-live \
    -run '^TestA2ALiveRetained$' -count=1 -timeout 90m -v
```

Use `-count=1`: cached test success is not live verification. Do not set the
unrelated `AZURE_AI_AGENTS_E2E_LIVE` gate or select `TestTier2Live`.

Safe offline assertion/gate check (does not execute azd or contact Azure):

```bash
AZD_E2E_A2A_LIVE=false go test -mod=readonly ./tests/e2e-live \
  -run '^TestA2ALive' -count=1 -timeout 3m -v
```

## What is asserted

1. Copy `testdata/a2a-hosted/native-echo-v1/` into a fresh artifact project and
   initialize existing source with `--src`, `--protocol a2a`, `--project-id`,
   `--deploy-mode code`, `--runtime python_3_13`, `--entry-point main.py`, and
   `--dep-resolution remote_build`. Inspect generated hosted definition,
   code configuration, native `a2a: 1.0.0` implementation declaration, and public
   A2A-only endpoint configuration.
2. Deploy using **azd deploy**, GET the live agent, and verify its native
   implementation and endpoint protocols. Invoke explicit wire version **1.0**,
   both azd-composed plain message and complete JSON-RPC input file, using raw
   HTTP output. Require JSON-RPC success, a **completed Task**, task identity,
   and exact artifact text `AZD_A2A_NATIVE_ECHO_OK`.
3. Change **only the retained local hosted definition** to `a2a: 0.3.0`, do
   **not** deploy it, and invoke explicit 1.0 again through project-based azd
   resolution. A completed native Task proves local compatibility advice did
   not suppress the remote request. Developer advisory logging is normally
   hidden without `--debug`; the test intentionally does not enable debug or
   require the diagnostic on stderr. **Do not redeploy this stale project.**
4. Initialize a distinct `--kind prompt` agent with `--model-deployment`,
   deterministic `--instructions`, and the existing `--project-id`; verify it
   has no native implementation protocols/code configuration. Deploy it using
   **azd deploy**, not a hand-authored API create request.
5. Enable only the prompt's **public endpoint** protocols using the existing
   Foundry partial-update contract:
   `PATCH /agents/{new-prompt-name}?api-version=v1`, body
   `{"agent_endpoint":{"protocols":["responses","a2a"]}}`.
   This is the same API shape used by the extension's `PatchAgent` helper.
   The current prompt schema rejects `agentEndpoint`, and the CLI
   `endpoint update` is hosted-only, so neither is misused to pretend the prompt
   implements native A2A. GET and verify the persisted public endpoint and prompt
   definition. An unsupported PATCH is a **failure**, not a skip/fallback.
6. Invoke prompt A2A **0.3 and 1.0**, each with plain-message and raw JSON-RPC-file
   requests. Parse JSON-RPC `result`/`error`, not HTTP success alone:
   - **Generated 0.3 plain messages:** the builder omits the entire
     `configuration`, retaining the protocol's non-blocking defaults. Accept a
     Task with a nonempty string identity and state `submitted` or `working`
     without requiring an answer artifact. This proves **request acceptance
     only**, not completion or the deterministic execution marker.
   - **All completed Tasks and agent-role Messages**, including generated 0.3
     results, must carry exact answer text `AZD_A2A_PROMPT_TRANSLATION_OK` in
     artifacts or Message parts, never an echoed user message/history.
   - **0.3 files** retain caller-owned `blocking: true` and
     `acceptedOutputModes: ["text/plain"]`; they require a completed Task answer
     or agent-role Message with the marker, never a pending snapshot. **1.0**
     retains `returnImmediately: false` and the same strict marker checks;
     native 1.0 still requires a completed Task with its native marker.
7. Disable the preview in the invocation process only: legacy message and file
   requests must still succeed over 0.3 without a version flag. The generated
   message has the same acceptance-only pending allowance as preview-on 0.3;
   the file retains its blocking configuration and strict answer checks. Explicit
   `--a2a-version 1.0` must be rejected with a disabled-feature diagnostic.

No JSON-RPC error or Task in `failed`, `canceled`, `rejected`, or any state other
than the explicitly allowed states passes, even if it contains the marker. There
is no polling or resubmission to turn an accepted pending snapshot into completion.

The fixture uses the actual **Python A2A SDK 1.2.2** (not the incompatible 0.3
API). Its route/executor pattern follows the retained proven reference
`/home/m/work/artifacts/a2a-westus2-20261020/project/src/compat-native`:
`/a2a`, root, and `/invocations/a2a` JSON-RPC POST routes, `/health`, `/readiness`,
`/agent_card`, and both root/prefixed well-known agent cards. It performs no model
or external calls and returns its fixed marker as a completed Task artifact.
The native and prompt markers are deliberately different and never appear in
user request text. SDK internals also require the pinned `sse-starlette` runtime
package for the route implementation, even though this test uses non-streaming
JSON-RPC only.

## Artifacts, bounds, and failure interpretation

The test prints the new run directory before invoking any CLI. It preserves:

- `hosted-project/` and `prompt-project/`, their azure.yaml files, environments,
  generated files and session state. The hosted azure.yaml ends **deliberately
  stale**; the deployed version remains 1.0.
- Numbered `*.command.json` records (binary, arguments, working directory,
  preview state, duration, success), `*.stdout.txt`, and `*.stderr.txt`.
- Complete version-specific `*.request.json` files and
  `prompt-endpoint-patch.json`.
- `azd-config-private/` and `azure-config-private/` containing copied credentials
  and any resulting local state. **These directories are sensitive; never
  publish the entire artifact tree.** Share reviewed/redacted logs only.

Logs do not include the process environment, token acquisition output, request
Authorization headers, or the full azd environment. Known token/secret environment
values, Bearer values and JWTs are redacted defensively. Debug output is never
requested. The private config copies still contain credentials by design.

All commands have bounded timeouts: init 5 minutes, hosted deploy 15 minutes,
prompt deploy 10 minutes, invoke 4 minutes, endpoint operations 1 minute; the
whole test has a 75-minute context. **There are no retries**, including for cold
starts. A service-level JSON-RPC error, translation failure, authorization error,
unsupported feature, timeout, or wrong answer is immediately visible as a failure;
the driver never reclassifies generic 500/503 responses as readiness. Inspect the
retained evidence before deciding whether to launch a new, separately named run.
A successful deploy followed by a failed invoke is not a successful live E2E.
A passing generated 0.3 invocation with a pending Task establishes acceptance
only; completed-marker evidence comes from completed Task/Message results and
the strict blocking-file and 1.0 cases, not from that pending snapshot.

This test was added and verified offline; no cloud execution is implied by its
presence. Live validation must be explicitly authorized and run by the operator.
