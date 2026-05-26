---
short: Run, inspect, and iterate on your agent locally before deploying.
order: 15
---
# Develop: local inner-loop with `azd ai agent run`

Audience: an AI coding assistant helping a developer iterate on agent source before pushing a new deployed version. Every command in this topic runs against `localhost` -- no Foundry calls, no billing.

The loop:

1. Edit source.
2. `azd ai agent run` -- starts the agent server locally + opens Agent Inspector.
3. `azd ai agent invoke --local "<message>"` -- send a message and read the response.
4. Go to step 1.

When the loop produces something deploy-worthy, jump to the `deploy` topic.

---

## Start the agent locally

```bash
azd ai agent run
```

Important: Wait longer for the first time running this command before trying to invoke. Startup can take 30-60 seconds.

What this does:

1. Resolves the agent service from `azure.yaml` (auto-picks when only one service exists; otherwise pass the service name as a positional arg).
2. Detects the project type (Python, .NET, Node.js) from files in the service source dir.
3. Installs dependencies if needed (e.g. `uv pip install -e .` for Python; `npm install` for Node; `dotnet restore` for .NET).
4. Starts the agent in the foreground on `localhost:<port>` (default `8088`).
5. Opens Agent Inspector in your browser (unless `--no-inspector`).

`Ctrl+C` stops the agent and cleans up the stored local session id.

Flags:

* `--port <n>` / `-p <n>` -- override the listen port. Useful when 8088 is taken.
* `--start-command "<cmd>"` / `-c "<cmd>"` -- override both `azure.yaml` and auto-detect. Example: `--start-command "python app.py"`.
* `--no-inspector` -- skip opening Agent Inspector. Use in headless environments or when you want to drive `invoke --local` only.

Pass the service name when the project has multiple ai.agent services:

```bash
azd ai agent run my-agent
```

---

## Where the start command comes from

Resolution order (first non-empty wins):

1. `--start-command` flag.
2. `startupCommand` in the agent service config in `azure.yaml` (NOT `agent.yaml` -- this is azd service-level config).
3. Auto-detected from project type.

Example `azure.yaml` snippet:

```yaml
services:
  my-agent:
    project: src/my-agent
    language: py
    host: ai.agent
    config:
      startupCommand: "uvicorn app:app --host 0.0.0.0 --port 4001"
```

If detection fails and no override is set, `run` returns an error that names the project dir and tells you to set `--start-command` or `startupCommand`.

---

## Agent Inspector

Agent Inspector is a separate azd extension (`azure.ai.inspector`) that provides a web UI for poking at a running local agent. `azd ai agent run` installs it on first use, waits for the local agent to bind, then opens the UI in the default browser.

Skip the auto-open with `--no-inspector` when running in CI or over SSH.

---

## Invoke the local agent

```bash
azd ai agent invoke --local "hello, are you up?"
```

What `--local` changes vs. a remote invoke:

* Targets `http://localhost:<port>` instead of the Foundry endpoint.
* Skips the confirmation envelope (no billing, no remote mutation).
* `--version` is REJECTED (versions are a remote/deployed concept).
* Named-agent invocation is REJECTED -- when running locally you only have ONE agent in the foreground; passing a name is a flag error.

Other useful flags during local dev:

* `--protocol responses` (default) or `--protocol invocations` -- pick the wire format your agent code speaks.
* `--input-file request.json` / `-f request.json` -- send a file body instead of a string message (handy for structured/long payloads).
* `--new-session` -- drop the saved local session and start fresh. Local sessions are persisted per-agent so consecutive invokes reuse them by default.
* `--port <n>` -- when you started `run` on a non-default port.

---

## Common local-dev failure modes

| Symptom                                          | Likely cause                              | Fix                                                                 |
| ------------------------------------------------ | ----------------------------------------- | ------------------------------------------------------------------- |
| `could not connect to localhost:<port>`          | `run` not started, or wrong port          | Start `azd ai agent run`; pass `--port` to `invoke --local` if non-default |
| `could not detect project type in <dir>`         | Missing project marker file               | Set `startupCommand` in `azure.yaml` or pass `--start-command`      |
| `cannot use --local with a named agent`          | Named-agent invoke against localhost      | Drop the name from `invoke`; only one agent runs locally at a time  |
| `cannot use --version with --local`              | `--version` is remote-only                | Drop `--version`; or remove `--local` to invoke the deployed agent  |
| Inspector never opens                            | Headless env, OR extension install failed | Pass `--no-inspector`; or run `azd extension install azure.ai.inspector` |

---

## When to graduate to remote

Local dev validates code shape; remote dev validates infrastructure + identity + Foundry binding. Move to remote when:

* You have changed `agent.yaml` `model:`, `tools:`, `connections:`, or `protocols:` -- those only take effect on the deployed agent.
* You need to test against real Foundry connections (search indexes, Bing, MCP, A2A) that have no local mock.
* You are ready to publish a new immutable agent version.

Next step in that flow: see the `deploy` topic.

---

## What this topic does NOT cover

* `azd ai agent invoke` against the deployed agent -- see `operate`.
* Editing `agent.yaml` (model, tools, connections) -- see `extend`.
* Provisioning Azure resources -- see `deploy`.
