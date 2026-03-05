# Changes PRD: Azure AI Agents Extension

This document describes the requirements for each feature added in the current set of uncommitted changes to the `azd ai agent` extension.

---

## 1. Initialize from Template (`init --template`)

### Overview

Add a new initialization path that scaffolds an AI agent project from a GitHub template repository, as an alternative to the existing manifest-based (`--manifest`) initialization.

### Requirements

- The `init` command must accept a `--template` (`-t`) flag specifying a GitHub template repository.
- `--template` and `--manifest` must be mutually exclusive. The CLI must reject invocations that provide both.
- Template URL resolution must support the following input formats:
  - Full GitHub URL (e.g., `https://github.com/owner/repo`)
  - Full URL with branch (e.g., `https://github.com/owner/repo/tree/my-branch`)
  - Shorthand `owner/repo` (resolved to `https://github.com/owner/repo`)
  - Bare repository name (resolved to `https://github.com/Azure-Samples/{repo}`)
- Download must use shallow `git clone` as the primary method, with automatic fallback to the GitHub REST API (tree endpoint + raw file download) if `git` is unavailable or the clone fails.
- When authenticated GitHub access is available via `GITHUB_TOKEN` or `GH_TOKEN` environment variables, those credentials must be used for API calls to avoid rate limiting.

### Behavior by Project State

- **No existing azd project**: Download the entire template repository (including `infra/` files) into the current directory. Present the user with a list of files to be created; `infra/` files are collapsed into a single summary line (e.g., `infra/ (+13 files)`) for readability. If any local files would collide, offer three choices: overwrite, skip existing files, or cancel.
- **Existing project with `--infra` flag**: Download only the `infra/` directory from the template into the current project. Error if the template has no `infra/` directory.
- **Existing project without `--infra`**: Download the template to a temp directory, locate the agent manifest, and copy only the agent source directory into `src/<agent-name>/` (or the path specified by `--src`).

### Agent Manifest Discovery

- Search for agent manifests in the template in this priority order: root, then `src/*/`, then recursive walk.
- Recognized filenames in priority order: `agent.yaml`, `agent.yml`, `agent.manifest.yaml`, `agent.manifest.yml`.
- Error if no agent manifest is found.

### Post-Scaffold Flow

- Prompt the user for an agent name (defaulting to the name in the manifest).
- Create an azd environment named `<agent-name>-dev`.
- Process any manifest parameters using defaults where available.
- Prompt for model configuration with three options:
  - **Deploy a new model from the catalog**: prompt for Azure subscription, location, and model selection.
  - **Select an existing model deployment from a Foundry project**: prompt for subscription, list Foundry projects in that subscription, let the user pick a project, then list its deployments and let the user pick one.
  - **Skip model configuration**.
- Write the processed `agent.yaml` back with a YAML language server schema annotation.
- Register the agent as a service in `azure.yaml` with default container resource settings.
- If the user chose to deploy a new model, include `azd provision` in the next-steps guidance so the model is provisioned before local development.

### Infrastructure Flag (`--infra`)

- A new `--infra` boolean flag on the `init` command.
- When used with `--template` on an existing project, copies only the `infra/` directory from the template.
- On a new project (full scaffold), `infra/` files are always included by default; the `--infra` flag is not required.

---

## 2. Deferred Azure Prompts in Manifest Init (`init --manifest`)

### Overview

Restructure the manifest-based initialization flow so that Azure subscription, location, and credential prompts are deferred until actually needed, rather than being required upfront.

### Requirements

- Environment creation must no longer require subscription and location. The `init --manifest` flow must create the azd environment first, then lazily prompt for subscription, credential, and location only when a downstream operation needs them (e.g., querying a Foundry project, processing model resources, or downloading from a registry URL).
- If `--project-id` is provided, the subscription ID must be extracted from it and used without prompting.
- Tenant lookup must be attempted from the subscription if no tenant is set.
- Environment variables (`AZURE_TENANT_ID`, `AZURE_SUBSCRIPTION_ID`, `AZURE_LOCATION`) must be persisted to the azd environment as each value is resolved.

---

## 3. Simplified Container Settings

### Overview

Remove interactive prompts for container memory, CPU, and replica settings during initialization.

### Requirements

- The `init` command must no longer prompt users for container memory, CPU, minimum replicas, or maximum replicas.
- Default values must always be used for container settings.
- This applies to both the manifest and template initialization flows.

---

## 4. Local Development Server (`dev`)

### Overview

Add a command that runs the agent locally for interactive development.

### Requirements

- New command: `azd ai agent dev`.
- Accept the following flags:
  - `--src` (`-s`): project source directory (default: current directory).
  - `--port` (`-p`): port to listen on (default: 8088).
  - `--name` (`-n`): agent service name from `azure.yaml` (for multi-service projects).
  - `--start-command` (`-c`): explicit startup command override.
- Startup command resolution order: `--start-command` flag, then `startupCommand` from the agent service entry in `azure.yaml`, then auto-detection based on project type.
- Project type auto-detection must support:
  - Python: detected by `pyproject.toml`, `requirements.txt`, or `main.py`. Default command: `python main.py`.
  - .NET: detected by `*.csproj`. Default command: `dotnet run`.
  - Node.js: detected by `package.json`. Default command: `npm start`.
- Error with a descriptive message listing supported types if detection fails and no explicit command is given.
- Automatic dependency installation before starting:
  - Python: prefer `uv` (create `.venv` with Python >=3.12, install via `uv pip install`). Fall back to `pip` if `uv` is not installed.
  - Node.js: `npm install`.
  - .NET: no dependency installation needed.
- For Python projects with a `.venv` directory, resolve the `python` binary to the venv-local path (platform-aware: `Scripts/python.exe` on Windows, `bin/python` on Unix).
- Set the `PORT` environment variable to the configured port.
- Load all key-value pairs from the current azd environment into the child process environment so the agent has access to Azure service configuration (e.g., `AZURE_AI_PROJECT_ENDPOINT`).
- Forward stdout, stderr, and stdin from the agent process to the terminal.
- Handle Ctrl+C gracefully: forward the interrupt signal to the child process, wait for it to exit, and suppress the "signal: interrupt" error message.

---

## 5. Agent Invocation (`invoke`)

### Overview

Add a command to send messages to a running agent, locally or on Azure AI Foundry.

### Requirements

- New command: `azd ai agent invoke [message]`.
- Accept the following flags:
  - `--message` (`-m`): alternative to positional argument for the message text.
  - `--remote` (`-r`): target Foundry instead of localhost.
  - `--name` (`-n`): remote agent name. Providing `--name` implies `--remote`.
  - `--port`: local server port (default: 8088).
  - `--session` (`-s`): explicit session ID override.
  - `--new-session`: force a new session, discarding saved conversation state.
  - `--account-name` (`-a`): Cognitive Services account name.
  - `--project-name` (`-p`): AI Foundry project name.
- Default message is "hello" if neither positional argument nor `--message` is provided.

### Local Invocation

- POST to `http://localhost:{port}/responses` with `{"input": "<message>"}`.
- Timeout: 120 seconds.
- If the local server is unreachable, error with guidance to start the agent via `azd ai agent dev`.

### Remote Invocation

- Auto-resolve agent name from `azure.yaml` and the azd environment if `--name` is not provided.
- Require an agent name (error if unresolvable).
- POST to the Foundry Responses API endpoint (`{endpoint}/openai/responses?api-version=...`).
- Request body must include:
  - `input`: the message text.
  - `agent`: `{"name": "<agent>", "type": "agent_reference"}`.
  - `store`: `false`.
  - `session_id`: a persistent session ID (see Session Management below).
  - `conversation`: `{"id": "<conversation-id>"}` if a conversation is available.
- Authenticate using a bearer token obtained via Azure Developer CLI credentials with `https://ai.azure.com/.default` scope.
- Timeout: 120 seconds.
- Print the APIM request/trace ID from the response header when available.

### Session Management

- Sessions must be persisted per agent in `.foundry-agent.json`.
- On first invocation for an agent, generate a random 25-character alphanumeric session ID and save it.
- Subsequent invocations for the same agent reuse the saved session ID.
- `--session` overrides the saved session for one invocation.
- `--new-session` discards the saved session, generates a new one, and saves it.

### Conversation Management (Remote Only)

- Conversations enable multi-turn memory on Foundry.
- On first remote invocation for an agent, create a new conversation via the Foundry Conversations API (`POST {endpoint}/openai/conversations`).
- Persist the conversation ID per agent in `.foundry-agent.json`.
- Subsequent remote invocations reuse the saved conversation ID.
- `--new-session` also resets the conversation.
- If conversation creation fails, proceed without it (multi-turn memory is disabled) and print a warning.

### Response Parsing

- Extract `output_text` items from the `output` array in the response and print each as `[<label>] <text>`.
- If no `output_text` items are found, pretty-print the full JSON response.
- Detect and report agent-level failures (status "failed" with error code and message).
- Detect and report server-level errors (top-level "code" and "message" fields).

---

## 6. Agent Deployment (`deploy`)

### Overview

Add a convenience command that deploys the agent service to Azure AI Foundry.

### Requirements

- New command: `azd ai agent deploy`.
- Accept a `--service` flag to specify the agent service name from `azure.yaml`.
- If `--service` is not provided, auto-detect the first `azure.ai.agent` service from `azure.yaml`.
- Execute `azd deploy --service <name>` as a subprocess, forwarding stdout, stderr, and stdin.
- Report the detected service name to stderr before running.

---

## 7. List Deployed Agents (`list`)

### Overview

Add a command that lists all deployed agents in the Foundry project.

### Requirements

- New command: `azd ai agent list`.
- Accept the following flags:
  - `--account-name` (`-a`): Cognitive Services account name.
  - `--project-name` (`-p`): AI Foundry project name.
  - `--output` (`-o`): output format, either `json` or `table` (default: `table`).
- Table format must show columns: NAME, VERSION, IMAGE, URI.
- In table format, mark the currently active agent (from `.foundry-agent.json`) with an arrow marker.
- Truncate image strings longer than 50 characters with a leading ellipsis.
- Print "No agents found." if the project has no deployed agents.
- JSON format must pretty-print the full agent list response.

---

## 8. Delete Agent (`delete`)

### Overview

Add a command that permanently deletes a deployed agent.

### Requirements

- New command: `azd ai agent delete`.
- Accept the following flags:
  - `--name` (`-n`): name of the agent to delete (required).
  - `--account-name` (`-a`): Cognitive Services account name.
  - `--project-name` (`-p`): AI Foundry project name.
- Prompt for confirmation before deleting (user must type "y" or "yes"). Skip confirmation if `--no-prompt` is set globally.
- Deletion is permanent and removes the agent and all its versions.
- On successful deletion, clean up the agent's session and conversation state from `.foundry-agent.json`.

---

## 9. Auto-Resolution for `show` and `monitor`

### Overview

Make the `--name` and `--version` flags optional on the `show` and `monitor` commands by auto-resolving them from the project context.

### Requirements

- The `--name` and `--version` flags on `show` and `monitor` must no longer be marked as required.
- If either flag is omitted, attempt to resolve the value from the `azure.yaml` project configuration and the azd environment. Specifically, look for environment variables `AGENT_{SERVICE_KEY}_NAME` and `AGENT_{SERVICE_KEY}_VERSION` where `SERVICE_KEY` is the uppercased, underscore-delimited version of the service name.
- If auto-resolution fails and the flag was not provided, error with a message directing the user to provide the flag or deploy the agent first.

---

## 10. Local State File (`.foundry-agent.json`)

### Overview

Introduce a project-level local state file to persist agent context across CLI invocations.

### Requirements

- File name: `.foundry-agent.json`, located in the project root.
- Contents:
  - `agent_name`: the currently active agent name.
  - `sessions`: a map of agent name to session ID.
  - `conversations`: a map of agent name to conversation ID.
- Used by: `invoke` (session/conversation persistence), `list` (active agent marker), `delete` (cleanup), `show`/`monitor` (agent name resolution).
- Reading a missing or malformed file must return an empty context (no error).

---

## 11. Error Code Additions

### Requirements

- Add the following error codes to the structured error system:
  - `invalid_args`: for mutually exclusive flag violations and similar argument validation errors.
  - `template_download_failed`: for failures downloading a template repository.
  - `agent_yaml_not_found`: when no agent manifest is found in a template.

---

## 12. Manifest File Naming Updates

### Requirements

- All user-facing error messages and help text that previously referenced `agent.yaml/agent.yml` must also mention `agent.manifest.yaml` as a recognized manifest file name.
- The message about a file at the repository root must use the generic term "agent manifest file" instead of the literal filename `agent.yaml`.

---

## 13. Preserve Existing AgentDefinition during Init from Code

### Overview

When running `azd ai agent init` (no flags, init-from-code path) in a project that has no `azure.yaml` but already has an `agent.yaml` containing an AgentDefinition, the existing agent definition file must be preserved rather than overwritten.

### Requirements

- Before writing a new `agent.yaml`, the init-from-code flow must check whether an `agent.yaml` or `agent.yml` already exists in the source directory.
- Detection logic: the file is an AgentDefinition if it has a `kind` field (e.g., `prompt`, `hosted`, `workflow`) and does **not** have a `template` field (which indicates an AgentManifest).
- If an existing AgentDefinition is detected:
  - The file must **not** be overwritten.
  - The agent name from the existing definition must be used as the default in the agent name prompt.
  - Model configuration and environment setup must still proceed normally so that azd environment variables are configured.
  - The agent must still be registered as a service in `azure.yaml`.
  - A "next steps" message must be printed informing the developer to:
    1. Update their `agent.yaml` environment variables to reference azd environment values (e.g., `AZURE_AI_PROJECT_ENDPOINT`, `AZURE_AI_MODEL_DEPLOYMENT_NAME`).
    2. Update their code to use these environment variables if needed.
    3. Verify the Docker image and container settings in `azure.yaml` match their project.
    4. Run `azd up` to provision and deploy.
- If no existing AgentDefinition is found, or the file is an AgentManifest, the existing behavior (create and write a new `agent.yaml`) applies unchanged.
