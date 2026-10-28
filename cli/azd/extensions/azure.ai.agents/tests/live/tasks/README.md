# Hosted task inspection fixture (opt-in live test)

A minimal Python Invocations agent that creates **real** Foundry task records using
`@multi_turn_task`, with no LLM, model deployment, ACR, or application database.
Each accepted invocation starts a bounded sleep (default 60 seconds, maximum 180),
then returns from the task handler. The record changes from `in_progress` to
`suspended` with `suspension_reason: run_completion`; it is not deleted. Three
invocations provide records for pagination tests. IDs are caller supplied and are
returned exactly, including the per-turn input ID.

**Cloud costs apply. Do not run automatically.** Obtain explicit authorization,
create a NEW uniquely named/tagged resource group, and retain all resources and
records on success **and** failure. The original live validation subscription has
an inherent three-day TTL policy. This fixture has no cleanup command or hook.
Do not use a shared Foundry project for these tests.

## Deployment

Use current azd core and matching `azure.ai.agents` / `azure.ai.projects`
extensions. Isolate `AZD_CONFIG_DIR` and `DOCKER_CONFIG`; keep the existing
`AZURE_CONFIG_DIR` for Azure CLI authentication. Configure azd to use Azure CLI
authentication in the isolated config (`auth.useAzCliAuth=true`) when appropriate.
Never switch the global Azure subscription. Set the subscription explicitly on
all `az` commands and in the project's `AZURE_SUBSCRIPTION_ID` environment value.

1. Check region availability (the original run used `eastus2`). Provision a new
   tagged `AIServices` S0 account with `allowProjectManagement: true`, system
   identity, a unique custom subdomain, and a new system-identity Foundry project.
   No model, registry, capability host, or monitoring resource is required.
   The operator needs Foundry data-plane access, inherited or explicitly granted;
   if RBAC/quota blocks deployment, report it rather than selecting shared resources.
2. Copy this directory to a fresh temporary **template** directory. Use a separate
   empty deployment directory so template adoption cannot recursively copy azd
   configs, credentials, or logs. Select a unique service key and matching `name:`
   in the copied `azure.yaml`.
3. From the empty deployment directory, adopt the template against the new project:

   ```bash
   AZURE_DEV_USER_AGENT=microsoft_foundry_skill azd ai agent init \
     -t "$TEMPLATE/azure.yaml" --project-id "$NEW_PROJECT_ARM_ID" --no-prompt
   ```

   Follow the printed project-directory path. Set `AZURE_SUBSCRIPTION_ID` and
   `AZURE_LOCATION` there using `azd env set`. Inspect `azd env get-values` to ensure
   the project ID, endpoint, tenant, and resource group all refer to the NEW group.
   If local extension/core versions differ, follow installed command help rather
   than adding unsupported flags. A partially completed init can be repaired with
   the matching projects extension's `azd ai project add --project-id ...`.
4. Check `codeConfiguration` remains present, then deploy:

   ```bash
   AZURE_DEV_USER_AGENT=microsoft_foundry_skill azd deploy "$AGENT" --no-prompt
   AZURE_DEV_USER_AGENT=microsoft_foundry_skill azd ai agent show --output json
   ```

   Expect an active version and an Invocations protocol endpoint. Do not generate
   an evaluation suite: this is a synthetic CLI fixture, not a model-quality test.

## Seed and capture records

`seed.py` uses only Python's standard library and an already authenticated `az`.
It obtains a token in memory with an explicit subscription, sends exactly three
POST requests, captures exact task IDs, reads each record, and polls for
`suspended` for at most `delay + 45` seconds. It never writes access tokens or
request headers, never deletes tasks, and requires a **new** output directory.

```bash
python3 seed.py \
  --subscription "$SUBSCRIPTION_ID" \
  --project-endpoint "$NEW_PROJECT_ENDPOINT" \
  --agent "$AGENT" --prefix "act-task-UNIQUE" \
  --delay-seconds 90 --output "$SCRATCH/seed-evidence"
```

Optionally pass `--session-id` from an existing invocation to route all requests
to the same session. With a dedicated agent, a single session is simplest.
`identities.json` contains exact task and input IDs; `tasks-initial.json` and
`tasks-suspended.json` contain the lifecycle evidence. While the delay runs, the
parent live verifier can exercise the Tasks CLI, including payload opt-in and
pagination. The helper does not exercise the Tasks CLI itself.

The fixture's POST returns HTTP 202 after registering the background task. Some
azd versions automatically poll `/invocations/{id}` after 202; this intentionally
minimal fixture does not implement that invocation-status API. Use `seed.py` or a
single direct authenticated HTTP POST instead of waiting for `azd ai agent
invoke` to finish polling. A 202 body proves acceptance, not task completion.

Re-invoking a suspended task with a **new** `input_id` resumes the same chain.
Re-invoking while it is running returns 409. Do not delete records to reset a
run; use a new unique prefix instead. The endpoint accepts only synthetic IDs
and a bounded delay, not arbitrary tools, prompts, or code.

## Verify the built Tasks CLI

After deployment and initial seeding, run the opt-in verifier against the **built**
CLI and extension, using the same isolated `AZD_CONFIG_DIR`. The selected environment
must not override `AZD_ALPHA_ENABLE_AGENTS_LONG_RUNNING`: the verifier toggles the
process switch to check both rejection and enabled behavior.

```bash
python3 verify.py --azd "$BUILT_AZD" \
  --project-directory "$DEPLOYED_PROJECT_DIRECTORY" \
  --project-endpoint "$NEW_PROJECT_ENDPOINT" --agent "$AGENT" \
  --subscription "$SUBSCRIPTION_ID" --session-id "$SESSION_ID" \
  --output "$SCRATCH/cli-verification"
```

The verifier checks gate-off list/show, default project resolution, exact-ID show,
explicit targeting outside a checkout, missing-agent validation, two-page opaque
cursor traversal, status/session/source/tag/error filters, repeated AND tags,
attachment omission, metadata tables, not-found/invalid-input errors, and a missing
environment. It then invokes the synthetic fixture three more times and observes
an active lease and the same exact task ID after suspension. Each new helper run
uses a unique prefix. This test assumes the dedicated project contains only the
healthy synthetic fixture tasks; it is not suitable for an arbitrary project.

`summary.json` records passed check names. Evidence contains synthetic task data,
including identifiers and payloads, and stays in the requested scratch directory;
do not commit it or publish it to shared logs. Neither helper provisions or deletes
resources, and all records/resources remain after success or failure. Region,
permissions, feature availability, and the tested `api-version=v1` contract must
still be confirmed before release; the published TypeSpec currently describes a
different specification version.

## Local smoke test

Install `requirements.txt` in a scratch virtual environment and run `main.py`
from a scratch copy of the fixture. The host defaults to port 8088 (`PORT` can
override it). POST `{"task_id":"local-example","input_id":"turn1",
"delay_seconds":1}` to `/invocations`. Local task storage is file-backed; it is
not evidence of hosted task persistence. No credentials/model are needed locally.
