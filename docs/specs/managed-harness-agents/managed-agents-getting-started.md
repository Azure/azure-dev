# Managed (Harness) Agents — Getting Started

Audience: early-access customers evaluating managed prompt agents on Microsoft Foundry. This guide shows two ways to create and call a managed agent: the **azd CLI** (`azd ai agent`) and the **Python SDK** (`azure-ai-projects`).

A managed agent (a "prompt agent" with `harness=ghcp`) declares only a model and instructions. Foundry provisions and runs the Brain+Hand sandbox for you — there is no container to build and no code to host. Agents live on a Foundry project and are invoked through the OpenAI-shape **Responses** API.

---

## Prerequisites

- An Azure subscription and a Foundry **project** (an `Microsoft.CognitiveServices/accounts/projects` resource, kind `AIServices`).
- A model deployment in that project (e.g. `gpt-4.1-mini`).
- `azd auth login` / `az login` access to the subscription.

You will need the project endpoint and a model deployment name:

- `AZURE_AI_PROJECT_ENDPOINT` = `https://<account>.services.ai.azure.com/api/projects/<project>`
- `AZURE_AI_MODEL_DEPLOYMENT_NAME` = e.g. `gpt-4.1-mini`

---

## Option A — azd CLI

### 1. Install

```powershell
# Install azd
winget install microsoft.azd

# Install the azd extensions developer extension
azd extension install microsoft.azd.extensions

# Add the dev registry for the bug bash
azd extension source add --name MHA-dev --type url --location https://raw.githubusercontent.com/kshitij-microsoft/azure-dev/refs/heads/kchawla/azd-managed-harness/cli/azd/extensions/registry.json

# Install the agents extension from that registry
azd extension install azure.ai.agents --source MHA-dev

# Sign in
azd auth login
```

### 2. Initialize a managed agent

```powershell
azd ai agent init
```

When prompted, choose **Prompt agent** (managed), pick your subscription and existing Foundry project, choose a model deployment, and name the agent. This scaffolds:

- `agent.yaml` — `kind: managed`, the model, and the instructions.
- `azure.yaml` — a service entry (`host: azure.ai.agent`) with a `promptAgent` block.

### 3. Deploy, list, show, invoke

```powershell
# Provision (if needed) and create the agent on the project
azd up

# List managed agents on the project
azd ai agent list

# Show status of the resolved agent
azd ai agent show

# Send a message
azd ai agent invoke "hello, what is your name?"
```

`list`/`show`/`invoke`/`delete` resolve the same Foundry project the agent was created on. `azd down` removes the agent along with the project resources.

---

## Option B — Python SDK

### 1. Install

In your virtual environment:

```powershell
pip install azure-ai-projects==2.3.0a20260625001 --extra-index-url https://pkgs.dev.azure.com/azure-sdk/public/_packaging/azure-sdk-for-python/pypi/simple
pip install azure-identity python-dotenv
```

Set the endpoint and model (env vars or a `.env` file):

```text
AZURE_AI_PROJECT_ENDPOINT=https://<account>.services.ai.azure.com/api/projects/<project>
AZURE_AI_MODEL_DEPLOYMENT_NAME=gpt-4.1-mini
```

### 2. Create the client

```python
import os
from dotenv import load_dotenv
from azure.identity import DefaultAzureCredential
from azure.ai.projects import AIProjectClient
from azure.ai.projects.models import PromptAgentDefinition, AgentHarness

load_dotenv()

endpoint = os.environ["AZURE_AI_PROJECT_ENDPOINT"]
model_name = os.environ["AZURE_AI_MODEL_DEPLOYMENT_NAME"]

credential = DefaultAzureCredential()
project_client = AIProjectClient(endpoint=endpoint, credential=credential, allow_preview=True)
```

### 3. Create a managed agent version

The `harness=AgentHarness.GHCP` field routes the agent to the managed (GHCP) runtime instead of the default prompt-agent runtime.

```python
agent_name = "my-managed-agent"

created = project_client.agents.create_version(
    agent_name=agent_name,
    definition=PromptAgentDefinition(
        model=model_name,
        instructions="You are a helpful assistant.",
        harness=AgentHarness.GHCP,
    ),
    description="Prompt agent running on the GHCP managed runtime.",
    metadata={"sample": "agent_harness"},
)
print(created)
```

### 4. Invoke via the Responses API

Reference the agent by name + version through the OpenAI-compatible client:

```python
openai_client = project_client.get_openai_client()

response = openai_client.responses.create(
    input=[{"role": "user", "content": "Generate the python code to print the OS and execute it."}],
    store=False,
    extra_body={"agent_reference": {"name": "my-managed-agent", "version": "1", "type": "agent_reference"}},
)
print(response)
```

---

## What the response looks like

Invocations stream Server-Sent Events from the project data-plane:

```text
POST https://<account>.services.ai.azure.com/api/projects/<project>/openai/v1/responses
content-type: text/event-stream
x-agent-session-id: ses_...

event: response.created
data: {"type":"response.created","response":{"model":"gpt-5.4","status":"in_progress", ...}}
event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"..."}
...
event: response.completed
data: {"type":"response.completed", ...}
```

The Brain plans the turn and the Hand sandbox executes any tools/code; only `response.output_text.delta` events carry user-visible text. The `x-agent-session-id` header identifies the session for follow-up turns.

---

## Quick reference

| Step | CLI | SDK |
|---|---|---|
| Create agent | `azd ai agent init` + `azd up` | `agents.create_version(..., harness=AgentHarness.GHCP)` |
| Invoke | `azd ai agent invoke "..."` | `openai_client.responses.create(..., extra_body={agent_reference})` |
| List / show | `azd ai agent list` / `show` | `agents.list()` / `agents.get_version(...)` |
| Endpoint | from `azure.yaml` / env | `AZURE_AI_PROJECT_ENDPOINT` |

Both paths target the same Foundry project; an agent created via the SDK is visible to the CLI and vice versa.
