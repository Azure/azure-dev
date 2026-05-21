# Azure AI Inspector Extension (Preview)

Browser-based inspector UI for locally running Foundry agents, packaged as a standalone
azd extension.

## What it does

`azd ai inspector launch` starts a small local HTTP server that:

1. Serves an embedded single-page application in your default browser.
2. Proxies the SPA's HTTP and Server-Sent Event (SSE) traffic to a locally running
   Foundry agent (`localhost:<port>`, default `8088`).
3. Mirrors the SSE chunks to your terminal so you can watch progress without
   focusing the browser.
4. Surfaces "Fix with AI" clicks from the SPA as `[fix-with-ai]` lines on stderr.

The inspector targets a **local** agent only. The agent must already be running
on the target port (for example, started via `azd ai agent run`).

Third-party components redistributed with the embedded SPA are listed in
`THIRD_PARTY_NOTICES.md`.

## Usage

```bash
# Launch with defaults (UI on :8087, target agent on localhost:8088)
azd ai inspector launch

# Custom ports
azd ai inspector launch --port 9000 --inspector-port 9001

# Seed an explicit session/conversation (otherwise the SPA mints fresh UUIDs)
azd ai inspector launch --session-id <uuid> --conversation-id <uuid>
```

| Flag | Default | Purpose |
| --- | --- | --- |
| `--port` | `8088` | Localhost port of the agent the inspector targets. |
| `--inspector-port` | `8087` | Port the inspector UI listens on. |
| `--session-id` | _(SPA mints UUID)_ | Optional explicit session ID for the SPA. |
| `--conversation-id` | _(SPA mints UUID)_ | Optional explicit conversation ID for the SPA. |

## Local Development

### Prerequisites

Install the developer kit extension if you don't already have it:

```bash
azd ext install microsoft.azd.extensions
```

> **Note**: If you encounter an error about the extension not being in the registry,
> verify you have the default source configured:
>
> ```bash
> azd ext source list
> ```
>
> If missing, add it:
>
> ```bash
> azd ext source add -n azd -t url -l "https://aka.ms/azd/extensions/registry"
> ```

### Building and Installing Locally

1. **Navigate to the extension directory**:

   ```bash
   cd cli/azd/extensions/azure.ai.inspector
   ```

2. **Initial setup** (first time only):

   ```bash
   azd x build
   azd x pack
   azd x publish
   ```

3. **Install the extension from your local registry**:

   ```bash
   azd ext install azure.ai.inspector
   ```

4. **For subsequent development** (after initial setup):

   ```bash
   azd x watch
   ```

   This automatically watches for file changes, rebuilds, and installs updates locally.

   Or for manual builds:

   ```bash
   azd x build
   ```

   This builds and automatically installs the updated extension.

> [!NOTE]
> The `pack` and `publish` steps are only required for the first time setup. For ongoing
> development, `azd x watch` or `azd x build` handles all updates automatically.

### Smoke Test

In one terminal, start a local Foundry agent on the target port (for example,
`azd ai agent run`).

In a second terminal:

```bash
azd ai inspector launch
```

Your default browser should open to `http://localhost:8087`. Send a message from the
SPA and watch the SSE chunks mirror in the second terminal.
