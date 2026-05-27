---
name: azd-ai-skill
description: Set up, scaffold, configure, deploy, evaluate, and operate AI agents on Microsoft Foundry using the Azure Developer CLI (azd) and the azure.ai.agents extension. USE FOR azd ai agent, azd ai toolbox, foundry agent, agent.yaml, azure.yaml service config, hosted agent, deploying agents to Azure, running an agent locally, evaluating an agent, optimizing an agent, adding a tool to an agent, web search, code interpreter, file search, function tool, MCP server, OpenAPI tool, A2A peer agent, Azure AI Search RAG, Bing grounding, Bing Custom Search, toolbox, toolbox version, toolbox connection, connection, RemoteTool, CognitiveSearch, RemoteA2A, GroundingWithCustomSearch, OAuth2, UserEntraToken, AgenticIdentity, ProjectManagedIdentity, ApiKey, CustomKeys, model deployment, Foundry project endpoint. DO NOT USE FOR generic Azure CLI tasks unrelated to Foundry, or LLM application code that does not deploy to a Foundry hosted agent.
allowed-tools: ["azd", "azd ai agent", "azd ai project", "azd ai toolbox", "azd ai connection", "azd ai skill", "azd ai routine", "azd ai doc", "azd version", "azd extension list", "azd auth login", "azd config get defaults", "azd env get-values"]
---
# AZD AI skill

You're driving `azd` and the `azure.ai.agents` extension on behalf of a developer. This file is the router. Pull a topic on demand for the details.

## Defaults

* Add `--output json` and `--no-prompt` to `azd ai agent ...` commands so output is scriptable. **Do not** add `--output json` to `azd ai doc ...` -- doc commands print markdown either way. Read the topic body once; don't `grep` through it.
* Prefer `azd` over `az`. `azd` already knows the project endpoint (via `azd ai project show`) and the developer's subscription/location defaults (via `azd config get defaults` and `azd env get-values`). Only fall back to `az` after those come up empty AND the developer has been asked.
* Stop and ask the developer when a topic says "ask the developer" or when a write command exits 2 with a `confirmation_required` envelope.
* **Never** run `azd auth login` yourself. It opens a browser. Ask the developer.

## Start every session with

```bash
azd version --output json
azd extension list --output json     # must include azure.ai.agents and azure.ai.projects
azd auth login --check-status
azd ai project show --output json
azd ai agent show --output json
```

Branch on `show`'s `.status`:

* `active` / `deployed`, the developer wants to **diagnose or change remote state** -> `investigate` or `operate`.
* `active` / `deployed`, the developer wants to **add a new tool, toolbox, or connection** -> read `azd ai doc toolbox add`, `azd ai doc toolbox consume` (agent-side code patterns), and `azd ai doc connection add` / `manage`. **`azd deploy` does NOT create or update toolboxes for post-init projects** -- you must run `azd ai toolbox create` / `connection add` yourself, then set the `TOOLBOX_<NAME>_MCP_ENDPOINT` env var, update the agent code to wire the new tool, then `azd deploy` so the deployed container picks up the new env var and tool.
* `active` / `deployed`, the developer wants to **change agent code or local config** -> `configure` / `extend`, then `develop` to iterate locally before redeploying.
* `not_deployed` with `next_step.suggestions[]` -> run the suggested command. For a greenfield init, always start with `azd ai agent sample list --output json` to pick a `manifestUrl`, then `azd ai agent init -m <manifestUrl>`. Use `--from-code` only when the cwd already has hand-written agent source.
* Anything else -> `azd ai agent doctor --output json` and surface failing checks.

## Topics: agent workflow

```bash
azd ai doc agent <topic>
```

| Want to ...                                                  | Topic         |
| ------------------------------------------------------------ | ------------- |
| Pick a starting sample (any greenfield init)                 | `samples`     |
| Bootstrap a new agent project (`azd ai agent init`)          | `initialize`  |
| Run + iterate locally (`azd ai agent run`)                   | `develop`     |
| Edit `azure.yaml` service config (models, toolboxes, env)    | `configure`   |
| Edit on-disk `agent.yaml` (env vars, endpoint, card, runtime)| `extend`      |
| Provision, deploy, version, `.agentignore`                   | `deploy`      |
| Generate, run, iterate evals                                 | `evaluate`    |
| Invoke (billed), files, sessions, optimize, endpoint patches | `operate`     |
| Inspect state, sessions, logs, files, doctor                 | `investigate` |

List all: `azd ai doc agent`.

## Topics: connections

For everything connection-related (MCP, Azure AI Search, Bing, OpenAPI, A2A; auth types; credentials):

```bash
azd ai doc connection <topic>
```

| Want to ...                                                  | Topic         |
| ------------------------------------------------------------ | ------------- |
| Mental model (declarative vs. pre-existing vs. imperative)   | `overview`    |
| Step-by-step recipes for common scenarios                    | `add`         |
| `category:` reference                                        | `categories`  |
| `authType:` + credentials + `PARAM_*` env-var rule           | `auth-types`  |
| Imperative CLI (`connection list / show / create / ...`)    | `manage`      |

## Topics: toolboxes

For grouping multiple tools under one MCP endpoint (`mcp`, `web_search`, `code_interpreter`, `azure_ai_search`, `openapi`, etc.):

```bash
azd ai doc toolbox <topic>
```

| Want to ...                                                  | Topic         |
| ------------------------------------------------------------ | ------------- |
| Mental model + the `azd ai toolbox` CLI surface              | `overview`    |
| Step-by-step recipes (MCP, AI Search, A2A, Bing Custom)      | `add`         |
| Connection categories + tool entry shapes                    | `tools`       |
| Agent-side runtime wiring (env var, MCP client, header)      | `consume`     |

## Topics: foundry skills

For managing **Foundry skills** -- versioned, project-scoped behavioral guidelines a Hosted agent downloads and injects as session instructions (the `azure.ai.skills` extension; `azd ai skill <verb>`). This is the Foundry skill **resource**, distinct from the embedded `azd-ai-skill` pack this router itself lives in:

```bash
azd ai doc skill <topic>
```

| Want to ...                                                  | Topic         |
| ------------------------------------------------------------ | ------------- |
| Mental model + versioning model + `azd ai skill` CLI surface | `overview`    |
| CLI reference (create / update / show / list / download / delete) | `manage`      |
| Cross-team / cross-project sharing via download              | `share`       |
| Wire downloaded SKILL.md into a Hosted agent + redeploy flow | `consume`     |

## Topics: routines

For managing **Foundry routines** -- trigger + action pairs that fire on a schedule, a one-shot timer, or an external event and invoke a deployed agent (the `azure.ai.routines` extension; `azd ai routine <verb>`). Routines are how a deployed agent gets billed work that fires on its own, as opposed to the on-demand `azd ai agent invoke` path:

```bash
azd ai doc routine <topic>
```

| Want to ...                                                  | Topic         |
| ------------------------------------------------------------ | ------------- |
| Mental model + trigger+action lifecycle + `azd ai routine` CLI surface | `overview`    |
| Trigger types reference (timer / recurring / github_issue)   | `triggers`    |
| Action types reference (agent-response / agent-invoke)       | `actions`     |
| CLI reference (create / update / show / list / delete / enable / disable + manifest format) | `manage`      |
| Manual dispatch + run history + debugging a failed run       | `dispatch`    |

## Resolving subscription, location, project ID

`azd ai project show --output json` only returns the **Foundry project endpoint** (plus its resolution source) -- it does NOT return subscription, tenant, location, or resource group. For those, try in order:

1. `azd config get defaults`
2. `azd env get-values`
3. Ask the developer.
4. Last resort, with explicit consent: `az account list --output json`.

For the **Foundry project ARM ID** (`--project-id`), FIRST ask the developer:

> "Do you want to create a new Foundry project, or use an existing one?"

* **New project** -- do NOT pass `--project-id`. `azd provision` will create the project. Proceed without it.
* **Existing project** -- ask the developer for the ARM resource ID and include this hint:
  > Open https://ai.azure.com -> Operate -> Admin -> select your project -> Copy the Resource ID.

Do NOT assume the developer has an existing project and jump straight to asking for an ID.
Don't shell out to `az cognitiveservices` or `az resource list` for the project ID -- they return the wrong resource shape.

## Confirmation envelope (exit 2)

Destructive or billed commands print JSON like this and exit 2 when run with `--no-prompt` and no `--force`:

```json
{ "status": "confirmation_required", "command": "...", "changes": [...], "confirmCommand": "... --force" }
```

Rules:

* Summarize `changes[]` for the developer in plain English.
* If their **immediately prior** turn named this exact action ("deploy", "yes delete it"), they've already consented -- re-run with `--force`.
* Otherwise, get explicit consent first. Never auto-append `--force`.
* Run `confirmCommand` exactly as printed.

For the full envelope shape, see `azd ai doc agent operate`.

## When to stop and ask

* `--project-id` when not provided -- but FIRST ask whether the developer wants a new project or an existing one (see "Resolving subscription, location, project ID" above). Only ask for the ID when they confirm they have an existing project.
* Picking a model deployment when multiple are available.
* Any `confirmation_required` envelope (unless prior turn already named it).
* Any nonzero exit from `auth login --check-status`, `provision`, or `deploy` that lacks a `next_step` block.
* Anything the developer flagged "ask first".
