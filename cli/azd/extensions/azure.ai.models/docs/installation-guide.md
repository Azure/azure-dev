# Azure AI Models Extension — Installation & Usage Guide

## Prerequisites

- An Azure AI Foundry project with a Cognitive Services account
- Azure subscription with appropriate permissions

### Install Azure Developer CLI (azd)

If you don't have `azd` installed, follow the instructions at [https://aka.ms/azd-install](https://aka.ms/azd-install).

After installation, restart your terminal and verify:

```bash
azd version
```

For other installation methods, see the [official docs](https://learn.microsoft.com/azure/developer/azure-developer-cli/install-azd).

---

## Step 0: Authenticate with Azure

```bash
azd auth login
```

Sign in with your Azure credentials. This is required for both installing the extension and using it.

---

## Step 1: Verify Extension is Available

Confirm the extension appears in the list of available extensions:

```bash
azd ext list
```

You should see `azure.ai.models` in the output.

![Verify Model Extension Available](images/02-Verify-model-ext-available.jpg)

---

## Step 2: Install the Extension

```bash
azd ext install azure.ai.models
```

![Install the Model Extension](images/03%20-%20install%20the%20model%20extension.jpg)

---

## Step 3: Verify Installation

```bash
azd ai models -h
```

You should see the help output with available commands: `init`, `custom`, and `version`.

![Verify Extension is Installed](images/04%20-%20verify%20extension%20is%20installed.jpg)

---

## Step 4: Authenticate (if not already done)

Ensure you are logged in before running any model commands:

```bash
azd auth login
```

---

## Step 5: Use Custom Model Commands

You can use the custom model commands by providing `--project-endpoint` and `--subscription (-s)` flags directly.

### 5.1 List Custom Models

```bash
azd ai models list --project-endpoint <project_endpoint> -s <subscription_id>
```

**Example:**

```bash
azd ai models list --project-endpoint "https://my-account.services.ai.azure.com/api/projects/my-project" -s "8861a79b-1234-5678-abcd-1234567890ab"
```

![List Custom Models](images/06.1.jpg)

### 5.2 Create a Custom Model

```bash
azd ai models create --project-endpoint <project_endpoint> -s <subscription_id> --name <model-name> --source <local_path_or_remote_url>
```

**Example with local source:**

```bash
azd ai models create --project-endpoint "https://my-account.services.ai.azure.com/api/projects/my-project" -s "8861a79b-1234-5678-abcd-1234567890ab" --name my-model --source "D:\models\my-weights" --base-model "FW-DeepSeek-v3.1"
```

**Example with remote source:**

> **⚠️ Important:** When using a remote blob URL with a SAS token, the `&` characters in the
> URL are interpreted by the command shell as command separators. You have two options:
>
> **Option A:** Escape each `&` with `^` in cmd.exe:
> ```cmd
> azd ai models create --name my-model --source "https://account.blob.core.windows.net/container/path?sv=2026-02-06^&sp=rl^&sig=..."
> ```
>
> **Option B (Recommended):** Save the URL in a file and use `--source-file`:
> ```bash
> echo https://account.blob.core.windows.net/container/path?sv=2026-02-06&sp=rl&sig=... > source_url.txt
> azd ai models create --name my-model --source-file source_url.txt
> ```

![Create Custom Model](images/06.2.jpg)

### 5.3 Show Custom Model Details

```bash
azd ai models show --project-endpoint <project_endpoint> -s <subscription_id> --name <model-name>
```

### 5.4 Delete a Custom Model

```bash
azd ai models delete --project-endpoint <project_endpoint> -s <subscription_id> --name <model-name>
```

Use `--force` to skip the confirmation prompt:

```bash
azd ai models delete --project-endpoint <project_endpoint> -s <subscription_id> --name <model-name> --force
```

---

## Step 6: Initialize Project (Optional — Recommended)

Instead of passing `--project-endpoint` and `-s` on every command, you can run `azd ai models init` to
configure your project once. This stores the project endpoint and subscription in the
azd environment so all subsequent commands pick them up automatically.

```bash
azd ai models init
```

The init command will interactively guide you through:

1. Creating an azd project (if needed)
2. Creating an azd environment
3. Selecting your Azure subscription
4. Selecting a resource group
5. Selecting your AI Foundry project

After init, you can run commands without `--project-endpoint` and `-s`:

```bash
azd ai models list
azd ai models create --name my-model --source ./model-weights/
azd ai models show --name my-model
azd ai models delete --name my-model
```

You can also provide flags during init to skip interactive prompts:

```bash
azd ai models init --project-endpoint "https://my-account.services.ai.azure.com/api/projects/my-project" -s "8861a79b-1234-5678-abcd-1234567890ab"
```

![Init Command](images/07.jpg)

---

## Quick Reference

| Command | Description |
|---------|-------------|
| `azd ai models init` | Set up project, environment, and Azure context |
| `azd ai models list` | List all custom models |
| `azd ai models list --connection <connection> --account <account>` | List models in a connected Fireworks account |
| `azd ai models create --name <n> --source <path>` | Upload and register a model |
| `azd ai models show --name <n>` | Show model details |
| `azd ai models delete --name <n>` | Delete a model |

### Common Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--project-endpoint` | `-e` *(deprecated)* | Foundry project endpoint URL |
| `--subscription` | `-s` | Azure subscription ID |
| `--name` | `-n` | Model name |
| `--version` | | Model version (default: "1") |
| `--output` | `-o` | Output format: `table` or `json` |
| `--force` | `-f` | Skip confirmation prompts |
| `--source` | | Local path or remote URL for model files |
| `--source-file` | | File containing the source URL |
| `--base-model` | | Base model architecture tag |
| `--connection` | | Saved Fireworks project connection (list only) |
| `--account` | | Fireworks account ID (list only) |

### List models through a Fireworks connection

```bash
azd ai models list \
  --project-endpoint "https://my-account.services.ai.azure.com/api/projects/my-project" \
  --connection fireworks --account my-fireworks-account --output json
```

The connection must target `https://api.fireworks.ai` and contain either an API key
or a Custom Keys `Authorization: Bearer <key>` credential. The signed-in Azure
identity needs permission to read the connection's credentials. The key is read
at runtime; it is not included in command output.

This command follows all Fireworks listing pages and returns a JSON array with
`name`, `kind`, and `state`, or a table with those columns. Without `--connection`,
`list` continues to list registered Foundry model assets. `--connection` and
`--account` must be supplied together and cannot be combined with `--source-job-id`.
Use an account ID, not an `accounts/...` resource path.
Listing does not import, register, or deploy a model.
