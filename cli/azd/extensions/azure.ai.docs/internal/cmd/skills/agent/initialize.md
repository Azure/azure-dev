---
short: Bootstrap a new Foundry agent project end-to-end.
order: 10
---
# Initialize: bootstrap a Microsoft Foundry agent project with azd

Audience: an AI coding assistant driving the `azd ai agent` extension on behalf of a developer. Every command below is safe to run from a script.

The path through this topic is linear:

1. Verify identity and context.
2. Verify what (if anything) is already deployed.
3. Branch into `azd ai agent init`, `azd init`, `azd provision`, or `azd deploy` based on what step 2 reports.

---

## Step 1 -- Verify the Foundry project endpoint

ALWAYS run this BEFORE any other agent command, even read-only ones. It tells you which Foundry project endpoint the rest of the workflow will target, and which cascade level supplied it (so you know whether to trust it or prompt the developer to set it).

```bash
azd ai project show --output json
```

Success payload:

```json
{
  "endpoint": "https://contoso.services.ai.azure.com/api/projects/myproj",
  "source": "azdEnv",
  "sourceDetail": "azd env",
  "azdEnv": "dev",
  "setAt": "",
  "fromLegacyAgentsConfig": false
}
```

`source` is one of `flag`, `azdEnv`, `globalConfig`, or `foundryEnv`. `setAt` is only meaningful when `source == "globalConfig"`. `fromLegacyAgentsConfig` is true only on the one-time migration run that read from the removed `azd ai agent project set` legacy key.

Exit codes: `0` on success. A nonzero exit means no endpoint could be resolved from any cascade level -- the error suggests `azd ai project set <endpoint>`. If the developer hasn't initialized an agent project yet, branch to `azd ai agent init` (Step 3a) instead of asking them to set an endpoint by hand.

This command does NOT return subscription, tenant, location, resource group, or Foundry account name. For those, see the next section.

### Resolving subscription / location

If you need a subscription or location (e.g. to seed `--project-id` or a `provision` location), keep using `azd` -- do NOT shell out to `az`:

1. `azd config get defaults` -- returns the user-level azd defaults as JSON: `{ "location": "...", "subscription": "..." }`. These are the same defaults the interactive prompts seed.
2. `azd env get-values` -- the active azd environment's variables (look for `AZURE_SUBSCRIPTION_ID`, `AZURE_LOCATION`, `AZURE_AI_PROJECT_ENDPOINT`).
3. Ask the human.
4. Last resort: `az account list --output json` -- only after 1-3 are exhausted AND the human has approved the shell-out. Users who picked `azd` typically did so to avoid juggling two CLIs.

---

## Step 2 -- Verify what's already deployed

```bash
azd ai agent show --output json
```

Two possible shapes. Branch on `.status`.

`status: "not_deployed"` -- no agent yet. The payload includes a `next_step` block telling you exactly what to run next:

```json
{
  "agent": null,
  "status": "not_deployed",
  "service": "echo",
  "next_step": {
    "suggestions": [
      {
        "command": "azd ai project show --output json",
        "description": "Inspect identity, subscription, and project context."
      },
      {
        "command": "azd deploy",
        "description": "Deploy agent service \"echo\"."
      }
    ]
  }
}
```

`status: "active"` (or any other API status) -- the agent is deployed. You will receive the full agent record:

```json
{
  "id": "agent-id",
  "name": "echo",
  "version": "3",
  "status": "active",
  "agent_endpoints": {
    "Responses": "https://contoso.services.ai.azure.com/api/projects/myproj/agents/echo/endpoint/protocols/openai/responses?api-version=..."
  },
  "playground_url": "https://ai.azure.com/..."
}
```

Either way, this command exits 0. Branch on the payload, never on the exit code.

---

## Step 3a -- Initialize a new agent project

First, decide which path you are on. This decision drives every remaining flag.

| User is ... | Signal                                                              | Source flag                  |
| ----------- | ------------------------------------------------------------------- | ---------------------------- |
| Greenfield  | Empty workspace, only a bootstrap stub, or wants a starter          | `-m <manifestUrl>` (default) |
| Brownfield  | The cwd already contains hand-written agent source the user owns    | `--from-code`                |

The interactive picker (no `-m`, no `--from-code`) is for human-driven flows only. NEVER use it under `--no-prompt`.

### Decision: new Foundry project or existing?

BEFORE running `azd ai agent init`, you MUST ask the human:

> "Do you want to create a new Foundry project, or use an existing one?"

**New project** -- the human has no Foundry project yet (or wants a fresh one). Do NOT pass `--project-id`. Let `azd provision` create the project later (Step 3c). Proceed directly to the Greenfield or Brownfield init below, omitting `--project-id`.

**Existing project** -- the human already has a Foundry project they want to target. Ask them for the project's ARM resource ID: "Open the Foundry portal at https://ai.azure.com -> Operate -> Admin -> select your project -> Copy the Resource ID." Pass this value as `--project-id`. Do NOT shell out to `az cognitiveservices ...` to discover it.

Do NOT assume the human has an existing project. Do NOT skip this question and jump straight to asking for an ID.

### Greenfield: start from a curated sample (the common case)

Run `azd ai agent sample list` first (see the `samples` topic) to fetch a `manifestUrl` from the curated catalog. Do NOT guess or hand-author a manifest URL.

```bash
# 1. Discover a manifest URL
azd ai agent sample list --featured-only --language python --output json

# 2a. Init for a NEW project (no --project-id; azd provision will create it)
azd ai agent init --no-prompt \
  -m "<manifestUrl-from-sample-list>"

# 2b. Init targeting an EXISTING project
azd ai agent init --no-prompt \
  --project-id "<projectResourceId>" \
  -m "<manifestUrl-from-sample-list>"
```

`-m` accepts a URL or a local path; the value comes from the `manifestUrl` field of `azd ai agent sample list --output json`.

### Brownfield: existing agent source (rare)

ONLY use `--from-code` when the workspace already contains hand-written agent source the user wants lifted into a hosted Foundry agent.

```bash
# New project (no --project-id)
azd ai agent init --no-prompt \
  --from-code \
  --deploy-mode code \
  --runtime python_3_13 \
  --entry-point app.py

# Existing project
azd ai agent init --no-prompt \
  --project-id "<projectResourceId>" \
  --from-code \
  --deploy-mode code \
  --runtime python_3_13 \
  --entry-point app.py
```

`--runtime` and `--entry-point` are required with `--deploy-mode code --no-prompt`. `--deploy-mode container` (the default) builds from `Dockerfile`.

Full flag set:

- `-m, --manifest <url-or-path>` -- agent manifest source (greenfield default). Mutually exclusive with `--from-code`. Get candidates from `azd ai agent sample list --output json` (the `manifestUrl` field).
- `--from-code` -- use the code in cwd as the agent source. BROWNFIELD ONLY -- requires hand-written agent source already in the workspace. Mutually exclusive with `-m`. Do NOT pass this just because `--no-prompt` complains about a missing source; pick a sample with `-m` instead.
- `-p, --project-id <resourceId>` -- Foundry project ARM ID. ONLY pass this when the human confirmed they have an existing project. See "Decision: new Foundry project or existing?" above. Do NOT shell out to `az cognitiveservices ...` to discover it.
- `--agent-name <name>` -- Foundry agent name written to `agent.yaml`. Reusing a name creates a new version of the existing agent.
- `--model <name>` -- model id (e.g. `gpt-4.1-mini`). Defaults to `gpt-4.1-mini`. Mutually exclusive with `--model-deployment` (`--model-deployment` wins if both are given).
- `-d, --model-deployment <name>` -- name of an existing model deployment on the Foundry project. Only valid when paired with `--project-id`.
- `--deploy-mode container|code` -- defaults to `container` in `--no-prompt`. `container` builds from `Dockerfile`; `code` ZIPs the source and Foundry builds the runtime.
- `--runtime <id>` -- e.g. `python_3_13`, `python_3_14`, `dotnet_10`, `node_22`. REQUIRED with `--deploy-mode code --no-prompt`.
- `--entry-point <file>` -- e.g. `app.py`, `MyAgent.dll`, `dist/index.js`. REQUIRED with `--deploy-mode code --no-prompt`.
- `--dep-resolution remote_build|bundled` -- defaults to `remote_build`. Only relevant for code deploy.
- `--protocol <name>` (repeatable) -- e.g. `responses`, `invocations`.
- `-s, --src <dir>` -- where to download the agent definition (defaults to `src/<agent-id>`).
- `--force` -- required together with `--no-prompt` when init would otherwise need confirmation (e.g. an input manifest already lives inside the generated `src` tree).
- `--no-prompt` -- refuses interactive prompts; flags must supply every required value, otherwise the command emits a structured `validation` error that names the missing flag.
- `-o, --output json` -- machine-readable progress (when supported).

### Manifest parameters (`parameters:` block)

A sample manifest may declare a `parameters:` block whose values get substituted into `{{name}}` placeholders elsewhere in the manifest (typically inside `resources[].credentials:` or template `environment_variables:`).

```yaml
parameters:
  github_pat:
    secret: true
    description: GitHub PAT (ghp_... or github_pat_...)
  region:
    description: Default Azure region
    default: eastus2
```

Interactive init prompts the developer for each parameter. Under `--no-prompt`, init uses the `default:` when present and FAILS for any required parameter without a default; secret parameters ALWAYS fail under `--no-prompt`. Init does not currently expose a `--param key=value` flag.

When the target manifest declares parameters:

1. Fetch the manifest (`curl <manifestUrl>`) and read its `parameters:` block before running init.
2. **Ask the developer** for each value. Surface the `description:` so they understand what is being requested. Don't echo `secret: true` values back to chat.
3. Drop `--no-prompt` from your init invocation and let the developer answer the prompts in their terminal. This is the only deterministic way to feed values into init today.

If you genuinely cannot reach the developer (fully autonomous flow with no chat channel), make a best-effort pass:

* For each declared parameter, set its eventual deploy-time env var if not already set. Credential parameters land in `azure.yaml` as `${PARAM_<UPPER_CONN_NAME>_<UPPER_KEY_PATH>}` -- see `azd ai doc connection auth-types` for the naming rule. Run `azd env get-values` first; only `azd env set PARAM_<...> "<placeholder>"` for names that are missing so you don't overwrite a value the developer already provided.
* Init itself will still fail if any required parameter lacks a default -- surface that failure to the developer rather than masking it. Don't fall back to `--from-code` to dodge the parameter prompts; that picks a completely different scaffolding path.

Manifests with no `parameters:` block (e.g. the basic echo sample) work directly under `--no-prompt`.

Init writes files into the working directory. There is no confirmation envelope on init -- it's a non-destructive create. Files written:

- `azure.yaml` (or appends a new ai.agent service to an existing one)
- `<service-dir>/agent.yaml`
- `<service-dir>/.agentignore` (code-deploy only; controls ZIP packaging)

After init, re-run Step 1 + Step 2 to confirm the new state. For the ON-DISK shape of `agent.yaml`, see the `extend` topic.

---

## Step 3b -- The workspace already has azure.yaml but no agent service

The `--help` preamble of `azd ai agent` will tell you this case. Use the same init invocation as Step 3a. The new service is appended to `azure.yaml`.

---

## Step 3c -- Service exists, no Foundry project endpoint

You need Azure resources provisioned. This is NOT an `azd ai agent` command -- use core azd:

```bash
azd provision --no-prompt
```

After provision succeeds, re-run Step 1; `endpoint` should populate. Full deploy lifecycle (provision + deploy + verify) lives in the `deploy` topic.

---

## Step 3d -- Provisioned but not deployed

```bash
azd deploy --no-prompt
```

After deploy succeeds, `azd ai agent show --output json` will return the agent record (Step 2's "active" shape). At that point the `develop`, `configure`, `extend`, `evaluate`, `operate`, and `investigate` topics all become applicable.

---

## Common error codes

When any command exits 1, the stderr JSON has a `code` field. The codes you're most likely to see during initialize:

- `not_logged_in` / `login_expired` -- run `azd auth login`, then retry.
- `missing_project_endpoint` -- the 5-level cascade produced nothing. Either run `azd provision` or `azd env set AZURE_AI_PROJECT_ENDPOINT <url>` if you have an endpoint to inject.
- `project_not_found` -- the working directory has no azure.yaml. Move to the project root or run Step 3a.
- `azd_client_failed` -- the azd host itself is not running. Surface to the human.

Any unfamiliar `code` value is safe to surface verbatim to the human.

---

## Diagnostics

When something doesn't add up, run the full health check:

```bash
azd ai agent doctor --output json
```

`status: "fail"` checks include a `suggestion` field. Each check is independent -- fix one, re-run doctor, iterate. Exit code is `0` if at least one check passed and none failed; `1` if any failed; `2` if all were skipped (e.g. no project detected).
