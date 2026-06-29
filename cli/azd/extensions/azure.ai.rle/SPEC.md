# RLE Environment Registration - OpenEnv Manifest
<!-- cspell:ignore openenv devrle -->

This spec defines the `azd ai rle` preview workflow for managing Reinforcement Learning Environment (RLE)
recipes using an OpenEnv-style manifest.

The goal is to move recipe/environment handling out of client-side SDK code and into a declarative manifest
that `azd` can use to initialize, run, invoke, and register RLE environments.

RLE environment registration follows a manifest-first sequence:

1. **Init** - User initializes an RLE session from an embedded default manifest or an existing `rle.yaml`.
2. **Run locally** - `azd` pulls and runs the environment Docker image locally.
3. **Invoke locally** - User interacts with the running OpenEnv runtime through a local shell.
4. **Deploy/register** - `azd` registers the environment image with the RLE control plane.

---

## Goals

- Provide a simple `azd ai rle` workflow for OpenEnv-style RLE environments.
- Represent environment recipes as an `rle.yaml` manifest.
- Support local validation before registering the environment.
- Keep training/client SDKs focused on training, not recipe/environment lifecycle management.
- Establish the azd command shape for future remote invoke, playground UI, and `azd provision` integration.

---

## Preview Scope

The preview focuses on existing Docker images. The manifest references images that Docker can already pull.

The preview uses explicit project selection during deployment:

```powershell
azd ai rle deploy --project-id <project-id>
```

Remote invocation, playground UI, and Foundry project provisioning are planned follow-up features.

---

## Manifest Contract

An RLE environment is described by an OpenEnv-style manifest.

```yaml
name: code_rl
description: Code RL OpenEnv environment.
template:
  name: code_rl
  kind: openenv
  local:
    image: devrle.azurecr.io/code_rl-env:latest
  environment:
    image: devrle.azurecr.io/code_rl-env:latest
```

| Field | Required | Description |
|---|---:|---|
| `name` | Yes | RLE session/environment name. Used for local folder/session state. |
| `description` | No | Human-readable environment description. |
| `template.name` | Yes | Template/environment name. Used as fallback when `name` is absent. |
| `template.kind` | Yes | Must be `openenv` for this workflow. |
| `template.local.image` | No | Docker image used for local run/invoke. |
| `template.environment.image` | Yes | Docker image registered with the RLE control plane. |

If `template.local.image` is omitted, local `run` and `invoke --local` use `template.environment.image`.

---

## CLI UX

### Step 1: Initialize an RLE Session

Default echo environment:

```powershell
azd ai rle init
cd .\echo_env
```

The default command uses an embedded manifest:

```yaml
name: echo_env
template:
  name: echo_env
  kind: openenv
  environment:
    image: devrle.azurecr.io/echo-rl:latest
```

Initialize from an existing manifest:

```powershell
azd ai rle init -m .\rle.yaml
```

`-m` accepts a local path or HTTPS/GitHub blob URL.

The session folder name is inferred from `name` or `template.name`.

---

### Step 2: Run Locally

```powershell
azd ai rle run
```

Behavior:

1. Loads `.azd-rle.json` and `rle.yaml`.
2. Resolves local image:
   - `template.local.image`, if present.
   - Otherwise `template.environment.image`.
3. Pulls the image with Docker.
4. Starts a local container.
5. Waits for `GET /health`.

Custom port:

```powershell
azd ai rle run --port 9000
```

The selected port is persisted in `.azd-rle.json`.

---

### Step 3: Invoke Locally

```powershell
azd ai rle invoke --local
```

The command opens an interactive OpenEnv shell.

```text
rle> health
rle> reset {"seed":0}
rle> step {"message":"hello"}
rle> state
rle> metadata
rle> schema
rle> exit
```

| Shell command | OpenEnv API |
|---|---|
| `health` | `GET /health` |
| `reset [json]` | `POST /reset` |
| `step <json-action>` | `POST /step` with `{ "action": <json-action> }` |
| `state` | `GET /state` |
| `metadata` | `GET /metadata` |
| `schema` | `GET /schema` |

---

### Step 4: Deploy/Register Environment

```powershell
azd ai rle deploy --project-id <project-id>
```

Deploy registers `template.environment.image` with the RLE control plane.

Request shape:

```json
{
  "name": "code_rl",
  "acrImagePath": "devrle.azurecr.io/code_rl-env:latest"
}
```

Control plane endpoint resolution:

| Source | Behavior |
|---|---|
| `RLE_ENDPOINT` | Used when set. |
| Default | `http://localhost:5000`. |

Example endpoint override:

```powershell
$env:RLE_ENDPOINT = "https://<rle-control-plane>"
```

---

## Local State

The extension persists local session state in `.azd-rle.json`.

Example:

```json
{
  "name": "code_rl",
  "localImage": "devrle.azurecr.io/code_rl-env:latest",
  "image": "devrle.azurecr.io/code_rl-env:latest",
  "port": 8088,
  "project": "<project-id>",
  "environmentId": "code-rl",
  "environmentVersion": "1"
}
```

State is used to:

- Remember the selected local port.
- Reuse project/environment registration details.
- Avoid requiring repeated manifest parsing for every command.

---

## OpenEnv API Mapping

The manifest describes the environment image. The running image must expose OpenEnv-compatible APIs.

| OpenEnv API | Required for |
|---|---|
| `GET /health` | `run`, local readiness check, shell `health` |
| `POST /reset` | shell `reset` |
| `POST /step` | shell `step` |
| `GET /state` | shell `state` |
| `GET /metadata` | shell `metadata` |
| `GET /schema` | shell `schema` |

---

## Validation Rules

| Rule | Error behavior |
|---|---|
| Missing `rle.yaml` and no initialized state | Ask user to run `azd ai rle init` or add `rle.yaml`. |
| Missing project on deploy | Require `--project-id <project-id>`. |
| Missing environment image on deploy | Require `template.environment.image`. |
| Missing local image on run/invoke | Require `template.local.image` or `template.environment.image`. |
| Docker pull fails | Tell user to ensure Docker can pull the image or set `template.local.image`. |
| OpenEnv health check fails | Surface local runtime startup failure. |
| Invalid JSON in shell command | Return command-specific JSON parsing guidance. |

---

## Example: Code RL

Manifest:

```yaml
name: code_rl
description: Code RL OpenEnv environment.
template:
  name: code_rl
  kind: openenv
  local:
    image: devrle.azurecr.io/code_rl-env:latest
  environment:
    image: devrle.azurecr.io/code_rl-env:latest
```

Run:

```powershell
azd ai rle init -m .\rle.yaml
cd .\code_rl
azd ai rle run --port 8088
azd ai rle invoke --local
```

Step example:

```text
rle> step {"code":"import sys\nnums=list(map(int, sys.stdin.read().split()))\nprint(sum(nums))\n"}
```

---

## Upcoming Features

### Remote Invoke

Future command:

```powershell
azd ai rle invoke
```

Expected behavior:

- Resolve registered environment from `.azd-rle.json`.
- Connect to remote RLE endpoint.
- Use the same OpenEnv shell commands as local invoke.

### Playground UI

Future experience:

```powershell
azd ai rle playground
```

Expected behavior:

- Launch a local or hosted UI for inspecting schema, reset/step behavior, metadata, and state.
- Support interactive testing without writing client code.

### `azd provision` Integration

Future flow:

```powershell
azd provision
```

Expected behavior:

- Choose an existing Foundry project or create a new one.
- Persist selected project information.
- Allow `azd ai rle deploy` without requiring `--project-id`.

---

## Open Design Questions

| # | Question | Context | Owner |
|---|---|---|---|
| RLE-1 | Should `azd ai rle deploy` remain a custom command or integrate into standard `azd deploy` later? | Current preview uses `azd ai rle deploy`; future azd alignment may prefer standard lifecycle hooks. | azd/RLE |
| RLE-2 | What is the final project identity model? | Preview requires `--project-id`; future `azd provision` should select/create Foundry project. | azd/RLE |
| RLE-3 | What is the remote invoke endpoint contract? | Local invoke maps directly to OpenEnv APIs. Remote invoke needs endpoint discovery and auth model. | RLE |
| RLE-4 | Should manifest support image build/push later? | Preview assumes images already exist. Future UX may include build/publish. | azd/RLE |

---

## Recommendation for Preview

Use the narrow command surface for initial review:

```powershell
azd ai rle init
azd ai rle init -m .\rle.yaml
azd ai rle run
azd ai rle invoke --local
azd ai rle deploy --project-id <project-id>
```

This keeps the preview focused on the core RLE environment lifecycle while leaving room for remote invoke,
playground UI, and `azd provision` integration.
