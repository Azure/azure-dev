---
short: Provision resources, deploy the agent, manage versions and endpoints.
order: 30
---
# Deploy: provision Foundry resources and push the agent

Audience: an AI coding assistant moving an agent from "scaffolded" or "locally tested" to "running on Foundry, responding to invocations". Every command below is documented for `--no-prompt` agent-friendly use.

The flow:

1. (One-time per env) `azd provision` -- creates the Foundry project, model deployments, connections, and supporting resources.
2. `azd deploy` -- packages the agent source / image and registers a new immutable agent version on the Foundry project.
3. Verify with `azd ai agent show --output json` and a smoke `azd ai agent invoke "..."`.

Subsequent code edits re-run step 2 only. Infra edits (new connections, new model deployments) need step 1 first.

---

## Step 1 -- Provision Azure resources

```bash
azd provision --no-prompt
```

What this does:

* Creates the Foundry project (if not present) and supporting Azure resources defined under `infra/`.
* Creates any project connections declared in `agent.yaml` (`resources:` entries with `kind: connection`). `${ENV_VAR}` placeholders in `credentials:` are resolved from the active azd env.
* Wires model deployments, AI Search, ACR, etc.
* If the project has `infra/layers/`, layers provision in parallel.

This is a core azd command, NOT an `azd ai agent` extension command. Output and error codes match core azd conventions.

Common failure modes:

| Error                                | Action                                                                 |
| ------------------------------------ | ---------------------------------------------------------------------- |
| `subscription quota exceeded`        | Ask the human to request quota; do not retry.                          |
| `credential creation failed`         | `azd auth login --check-status` and surface the result.                |
| Bicep deploy errors                  | Forward `error.details[]` verbatim and ask the human.                  |
| `missing required env value`         | `azd env set <KEY> <VAL>` for the named key; retry.                    |

Skip `azd provision` when the human gave you an existing `AZURE_AI_PROJECT_ENDPOINT` via `azd env set` -- in that case the agent extension uses the existing Foundry project as-is.

---

## Step 2 -- Deploy the agent

```bash
azd deploy --no-prompt
```

Single-service projects: agent name is auto-detected from `azure.yaml`.

Multi-service projects: deploy ONE service by passing its name:

```bash
azd deploy my-agent --no-prompt
```

`azd deploy` reads `agent.yaml`, packages the agent according to its deploy mode, uploads, and registers a new agent version on the Foundry project.

### Code deploy (`--deploy-mode code` at init time)

* ZIPs the agent source directory.
* Excludes files per `<service-dir>/.agentignore` (gitignore syntax; see "The `.agentignore` file" below).
* Uploads the ZIP to Foundry.
* Foundry builds the runtime image from your `runtime:` + `entryPoint:` declared in `agent.yaml` `codeConfiguration:`.
* `dependencyResolution: remote_build` (default) -- Foundry installs your requirements; `bundled` -- your build packs vendored dependencies into the ZIP.

### Container deploy (`--deploy-mode container` at init time -- the default)

* Builds a Docker image from the service's `Dockerfile`.
* OR (when `agent.yaml` `image:` is set on the `hosted` agent) reuses a pre-built image instead of building.
* Pushes to the ACR connection on the Foundry project.
* Registers the agent version pointing at the image tag.

In `--no-prompt` mode with `image:` set in `agent.yaml`, the default selection is "build from Dockerfile" -- pass `--deploy-mode image` at init time, or edit `azure.yaml` ahead of deploy, to skip the build.

### Versioning

Every successful `azd deploy` creates an immutable agent version.

* The new version is registered with an incrementing number (1, 2, 3...).
* Version 1 is the first deploy; subsequent re-deploys (even of identical bits) create a new version.
* Versions are not garbage-collected automatically -- they accumulate on the Foundry project until you prune them via the Foundry portal or API.
* If a deploy creates a version that is identical to the currently active one, the extension prints `Agent version <n> is already active.` and skips the poll.
* Each successful deploy writes `AGENT_<SVC>_NAME`, `AGENT_<SVC>_VERSION`, and `AGENT_<SVC>_<PROTO>_ENDPOINT` (one per declared protocol) to the active azd env.

If the Foundry project ALREADY has an agent with the same name from a previous (non-azd) workflow, you will see a one-time warning that re-deploying will create a new version of that existing agent. This is informational -- the deploy still proceeds.

---

## Step 3 -- Verify the deployment

```bash
azd ai agent show --output json
```

Expect `"status": "active"` (or `"deployed"` when the version is fully registered) and an `agent_endpoints` map keyed by protocol label.

Smoke-test the agent:

```bash
azd ai agent invoke "hello, are you up?" --output json
```

Anything other than a `completed` response status warrants a follow-up:

```bash
azd ai agent doctor --output json
```

Endpoint URLs are also written to `AGENT_<SVC>_<PROTO>_ENDPOINT` env vars. Read them with `azd env get-values` when an external tool needs to hit the deployed agent directly.

---

## The `.agentignore` file

`azd ai agent init` writes a default `<service-dir>/.agentignore` for code-deploy projects. The file controls which files are EXCLUDED from the deploy ZIP. Syntax matches `.gitignore`.

Default exclusions (from the bundled template):

```
# azd tooling files
agent.yaml
agent.manifest.yaml
azure.yaml
.agentignore

# Security / secrets
.env
.env.*
.azure/
.git/

# Python
__pycache__/
.venv/
venv/
*.pyc
*.pyo
.mypy_cache/
.pytest_cache/

# .NET
bin/
obj/
*.user
*.suo
.vs/

# Node
node_modules/

# Docker (not used in code deploy)
Dockerfile
.dockerignore
```

Important quirks:

* Only the ROOT `.agentignore` is read -- subdirectory `.agentignore` files are ignored (unlike `.gitignore`).
* To force-include a file that defaults exclude, use negation: `!path/to/file`.
* The file ITSELF is excluded by default, so editing it is safe -- the edit does not bloat the ZIP.

---

## Endpoint and card edits (no new version)

When the ONLY change is to `agent.yaml`'s `agentEndpoint:` or `agentCard:` blocks, skip `azd deploy` and use the in-place patch:

```bash
azd ai agent endpoint update --dry-run    # preview
azd ai agent endpoint update --force      # apply
```

This updates the agent record without creating a new immutable version. Idempotent: re-running with the same `agent.yaml` is a no-op. See `operate` for the confirmation envelope flow.

---

## Multi-environment deploys

`azd deploy` targets the active azd environment. Switch first if the active env is wrong:

```bash
azd env list
azd env select prod
azd deploy --no-prompt
```

Each env has its own `AGENT_<SVC>_*` vars, so `show` / `invoke` after a switch read the correct deployed endpoint.

---

## Common deploy failure modes

| Error                              | Action                                                                  |
| ---------------------------------- | ----------------------------------------------------------------------- |
| `missing_project_endpoint`         | Run `azd provision`, or `azd env set AZURE_AI_PROJECT_ENDPOINT <url>`.  |
| `invalid_agent_manifest`           | `azd ai agent doctor --output json`; fix the named field in agent.yaml. |
| `invalid_connection`               | Inspect with `azd ai agent connection show <name> --output json`.       |
| Docker daemon not running          | Start Docker / Podman; or switch to code deploy if appropriate.         |
| ACR push 403                       | The Foundry project's RBAC is missing `AcrPush` for your identity.      |
| Agent version poll times out       | The image is still being built; retry `azd ai agent show` after a minute. |

---

## What this topic does NOT cover

* Scaffolding the project -- see `initialize`.
* Editing `agent.yaml` -- see `extend`.
* Inspecting deployed state -- see `investigate`.
* Invoking, eval, optimize after deploy -- see `operate` and `evaluate`.
