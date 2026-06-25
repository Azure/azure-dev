# azd ai agent — Demo Script (init → list → show)

A recording-ready script for a short screencast of the `azure.ai.agents` extension.
Each section has **[NARRATION]** (what to say) and **[RUN]** (what to type on screen).

- Target time: ~3–4 minutes
- Shell: PowerShell
- Pre-req: `azd auth login` already done, an existing Foundry project available

---

## 0. Setup (do this BEFORE recording — keep off-camera)

```powershell
# Clean, empty folder for the demo
New-Item -ItemType Directory -Force -Path "$HOME\azd-agent-demo" | Out-Null
Set-Location "$HOME\azd-agent-demo"

# Make output deterministic and clean for capture
$env:NO_COLOR = "1"          # stable text, no ANSI escapes
$env:AZURE_CORE_OUTPUT = "none"

# Confirm the extension is installed
azd extension list | Select-String "azure.ai.agents"
```

> Tip: Increase terminal font size and clear scrollback (`Clear-Host`) right before you hit record.

---

## 1. Intro (10–15s)

**[NARRATION]**
> "In this short demo I'll create a prompt-based AI agent with the Azure Developer CLI,
> then use the agent lifecycle commands to list it and inspect its status —
> all without leaving the terminal."

**[RUN]**
```powershell
Clear-Host
azd version
```

---

## 2. `azd ai agent init` (60–90s)

**[NARRATION]**
> "First, `azd ai agent init`. This scaffolds a new agent project: it walks me through
> picking a subscription and a Foundry project, selecting a model, and it writes an
> `azure.yaml`, an `agent.yaml` manifest, and the infrastructure to provision."

**[RUN]**
```powershell
azd ai agent init
```

**On-screen choices to make (call these out as you click):**
1. Agent type → **Prompt agent** (managed)
2. Subscription → your demo subscription
3. Foundry project → **Use an existing Foundry project** → pick your project
4. Model deployment → e.g. **gpt-4.1-mini**
5. Agent name → **my-demo-agent**

**[NARRATION] (while files generate)**
> "Notice it generated everything I need: the service definition, the agent manifest,
> and a Bicep template. Let me show the two key files."

**[RUN]**
```powershell
Get-Content azure.yaml
Get-Content agent.yaml
```

**[NARRATION]**
> "The `agent.yaml` is the heart of the agent — its kind, model, and the instructions
> that define its behavior."

---

## 3. Provision + deploy the agent (45–60s)

**[NARRATION]**
> "Now I'll run `azd up`. This provisions any required resources and then creates the
> agent on the managed Foundry harness."

**[RUN]**
```powershell
azd up
```

**[NARRATION] (when it finishes)**
> "Deployment succeeded. The agent is now live on my Foundry project.
> Let's use the lifecycle commands to confirm that."

---

## 4. `azd ai agent list` (30–40s)

**[NARRATION]**
> "`azd ai agent list` shows every agent on the project this environment is connected to,
> with its version and status."

**[RUN]**
```powershell
azd ai agent list
```

**[NARRATION]**
> "There's `my-demo-agent`, version 1, status active. The same project can host multiple
> agents and they all show up here."

---

## 5. `azd ai agent show` (40–60s)

**[NARRATION]**
> "To inspect a single agent, I use `azd ai agent show`. By default it prints a concise
> status table."

**[RUN]**
```powershell
azd ai agent show
```

**[NARRATION]**
> "Name, kind, version, status, and the harness endpoint. And because azd is built for
> automation, I can get the full object as JSON for scripting."

**[RUN]**
```powershell
azd ai agent show --output json
```

**[NARRATION]**
> "Here's the complete agent definition — the model, the instructions, the managed
> identity, and the version metadata — exactly what you'd pipe into another tool."

---

## 6. Wrap-up (10–15s)

**[NARRATION]**
> "And that's the core loop: `init` to scaffold, `azd up` to deploy, then `list` and `show`
> to manage your agents — a complete, terminal-first workflow for Azure AI agents.
> Thanks for watching."

**[RUN] (optional teardown, off-camera)**
```powershell
azd down --purge --force
```

---

## Quick command cheat-sheet (for the description / pinned comment)

```text
azd ai agent init               # scaffold a new agent project
azd up                          # provision + deploy the agent
azd ai agent list               # list agents on the project
azd ai agent show               # show status of the resolved agent (table)
azd ai agent show --output json # full agent object as JSON
```

## Recording tips

- Set `NO_COLOR=1` so captured text stays clean and copy-pasteable.
- Run each block once **before** recording to warm caches (first run can be slower).
- If a command is long-running, plan a jump-cut at the "creating prompt agent" step.
- Keep the window at a fixed size so zoom/crop is consistent across takes.
