# Proposal 1: Azure AI Custom Models Extension

## Overview

This document outlines a **focused** extension (`azure.ai.models`) that handles **custom models only** in Phase 1. This approach keeps the scope narrow, avoids complexity of handling two model registries, and delivers value faster.

## Current Implementation Status

| Command | Status | Description |
|---------|--------|-------------|
| `azd ai models init` | ✅ Implemented | Initialize project, environment, and Azure context |
| `azd ai models custom create` | ✅ Implemented | Upload (via AzCopy) + register model |
| `azd ai models custom list` | ✅ Implemented | List all custom models |
| `azd ai models custom show` | ✅ Implemented | Show model details |
| `azd ai models custom delete` | ✅ Implemented | Delete model with confirmation |

## Command Structure

The command structure uses clear **entity keywords** to indicate which entity the user is working with:

```
azd ai models <entity> <action> [options]
```

| Entity Keyword | Entity | Example |
|----------------|--------|--------|
| `custom` | Custom Model | `azd ai models custom create` |
| `custom deployments` | Custom Model Deployment | `azd ai models custom deployments create` |
| `base` | Base Model | `azd ai models base list` |
| `base deployments` | Base Model Deployment | `azd ai models base deployments create` |

## Scope

**This extension focuses exclusively on custom model management in Phase 1.**

| In Scope (Phase 1) — ✅ Implemented | Out of Scope (Future Phases) |
|----------|--------------|
| `azd ai models init` | Custom Model Deployments (Phase 2) |
| `azd ai models custom create` | Base Models (Phase 3) |
| `azd ai models custom list` | Base Model Deployments (Phase 4) |
| `azd ai models custom show` | |
| `azd ai models custom delete` | |

## Entities & Operations

There are **4 distinct entities** in the Azure AI model ecosystem. This extension focuses on **Custom Model** only.

### Entity Overview

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                                                                                 │
│   ┌───────────────────────────────────┐   ┌───────────────────────────────────┐ │
│   │          BASE MODEL               │   │         CUSTOM MODEL              │ │
│   │     (Model Catalog)               │   │     (FDP Custom Registry)         │ │
│   ├───────────────────────────────────┤   ├───────────────────────────────────┤ │
│   │ • Pre-trained models (GPT-4, etc.)│   │ • User-uploaded model weights     │ │
│   │ • Managed by Azure                │   │ • Stored in FDP data store        │ │
│   │ • Read-only for users             │   │ • User manages lifecycle          │ │
│   │                                   │   │                                   │ │
│   │ Operations:                       │   │ Operations:                       │ │
│   │   • List ✓                        │   │   • Create (Upload + Register) ✓  │ │
│   │   • Show ✓                        │   │   • List ✓                        │ │
│   │                                   │   │   • Show ✓                        │ │
│   │                                   │   │   • Delete ✓                      │ │
│   └───────────────────────────────────┘   └───────────────────────────────────┘ │
│                    │                                       │                    │
│                    │              Deploy                   │                    │
│                    ▼                                       ▼                    │
│   ┌───────────────────────────────────┐   ┌───────────────────────────────────┐ │
│   │     BASE MODEL DEPLOYMENT         │   │    CUSTOM MODEL DEPLOYMENT        │ │
│   │     (Inference Endpoint)          │   │    (Inference Endpoint)           │ │
│   ├───────────────────────────────────┤   ├───────────────────────────────────┤ │
│   │ • Deployed instance of base model │   │ • Deployed instance of custom     │ │
│   │ • Inference endpoint              │   │ • Inference endpoint              │ │
│   │                                   │   │                                   │ │
│   │ Operations:                       │   │ Operations:                       │ │
│   │   • Deploy ✓                      │   │   • Deploy ✓                      │ │
│   │   • List ✓                        │   │   • List ✓                        │ │
│   │   • Show ✓                        │   │   • Show ✓                        │ │
│   │   • Delete ✓                      │   │   • Delete ✓                      │ │
│   └───────────────────────────────────┘   └───────────────────────────────────┘ │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### Entity Details

#### 1. Custom Model

User-uploaded model weights registered in FDP custom registry.

| Operation | Supported | Command | Description |
|-----------|:---------:|---------|-------------|
| **Create** | ✅ | `azd ai models custom create` | Upload weights + register model |
| **List** | ✅ | `azd ai models custom list` | List all custom models |
| **Show** | ✅ | `azd ai models custom show` | View model details |
| **Delete** | ✅ | `azd ai models custom delete` | Remove model and weights |

#### 2. Custom Model Deployment

Deployed instance of a custom model for inference.

| Operation | Supported | Command | Description |
|-----------|:---------:|---------|-------------|
| **Deploy** | ✅ | `azd ai models custom deployments create` | Deploy custom model |
| **List** | ✅ | `azd ai models custom deployments list` | List deployments |
| **Show** | ✅ | `azd ai models custom deployments show` | View deployment details |
| **Delete** | ✅ | `azd ai models custom deployments delete` | Remove deployment |

#### 3. Base Model

Pre-trained models available in Azure AI model catalog (GPT-4, Llama, Mistral, etc.).

| Operation | Supported | Command | Description |
|-----------|:---------:|---------|-------------|
| **Create** | ❌ | - | Not allowed (Azure-managed) |
| **List** | ✅ | `azd ai models base list` | View available models |
| **Show** | ✅ | `azd ai models base show` | View model details |
| **Delete** | ❌ | - | Not allowed (Azure-managed) |

#### 4. Base Model Deployment

Deployed instance of a base model for inference.

| Operation | Supported | Command | Description |
|-----------|:---------:|---------|-------------|
| **Deploy** | ✅ | `azd ai models base deployments create` | Deploy base model |
| **List** | ✅ | `azd ai models base deployments list` | List deployments |
| **Show** | ✅ | `azd ai models base deployments show` | View deployment details |
| **Delete** | ✅ | `azd ai models base deployments delete` | Remove deployment |

### This Extension's Focus (Phase 1)

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                                                                                 │
│   Entity                    │ Phase 1           │ Future Phases                 │
│   ──────────────────────────┼───────────────────┼─────────────────────────────  │
│   Custom Model              │ ✅ Full support   │                               │
│   Custom Model Deployment   │ ❌ (use Azure CLI)│ ✅ Phase 2                    │
│   Base Model                │ ❌ (use Portal)   │ ✅ Phase 3                    │
│   Base Model Deployment     │ ❌ (use Azure CLI)│ ✅ Phase 4                    │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

## Key Concepts

### Custom Models Only

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         Custom Model Lifecycle                              │
│                                                                             │
│   ┌──────────────────────────────────────────┐      ┌──────────────────┐   │
│   │        Phase 1 (This Extension)         │      │  Phase 2 (Future) │   │
│   │                                          │      │                  │   │
│   │   ┌──────────────┐    ┌──────────────┐   │      │ ┌──────────────┐ │   │
│   │   │   Upload     │    │   Register   │   │      │ │   Deploy     │ │   │
│   │   │   Weights    │───►│   Model      │───┼─────►│ │   Model      │ │   │
│   │   │              │    │              │   │      │ │              │ │   │
│   │   └──────────────┘    └──────────────┘   │      │ └──────────────┘ │   │
│   │                                          │      │                  │   │
│   │   azd ai models custom create            │      │ azd ai models    │   │
│   │   azd ai models custom list              │      │ custom deployments│   │
│   │                                          │      │ create           │   │
│   └──────────────────────────────────────────┘      └──────────────────┘   │
│                                                                             │
│   Until Phase 2: Use Azure CLI for deployment                               │
│   az cognitiveservices account deployment create ...                        │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Create Command: 3-Step Process

The `create` command performs three sequential steps internally:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                     Upload + Register Flow (3 Steps)                        │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ STEP 1: Get Writable SAS URL                                        │   │
│  │         CLI ────► FDP API: POST /datastore/upload/initialize        │   │
│  │         CLI ◄──── FDP API: { sas_uri, upload_id, expires_at }       │   │
│  │         Duration: ~1-2 seconds                                      │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    ▼                                        │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ STEP 2: Upload Model Weights via AzCopy                ⚠️ LONG      │   │
│  │         Source: Local path OR remote URL                            │   │
│  │         Destination: Azure Blob Storage (via SAS)                   │   │
│  │                                                                     │   │
│  │         ⏱️ Duration: Minutes to HOURS depending on file size        │   │
│  │         ┌─────────────────────────────────────────────────────┐     │   │
│  │         │ File Size    │ Est. Time @ 100 MB/s │ @ 50 MB/s     │     │   │
│  │         ├──────────────┼──────────────────────┼───────────────┤     │   │
│  │         │ 1 GB         │ ~10 seconds          │ ~20 seconds   │     │   │
│  │         │ 10 GB        │ ~2 minutes           │ ~3 minutes    │     │   │
│  │         │ 50 GB        │ ~8 minutes           │ ~17 minutes   │     │   │
│  │         │ 100 GB       │ ~17 minutes          │ ~33 minutes   │     │   │
│  │         │ 500 GB       │ ~1.4 hours           │ ~2.8 hours    │     │   │
│  │         └─────────────────────────────────────────────────────┘     │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    ▼                                        │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ STEP 3: Register Model in FDP Registry                              │   │
│  │         CLI ────► FDP API: POST /custom-models                      │   │
│  │         CLI ◄──── FDP API: { name, status, created_at }             │   │
│  │         Duration: ~1-2 seconds                                      │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

**Critical Considerations for Step 2 (Long-Running Upload):**

| Concern | Solution |
|---------|----------|
| **Network interruption** | AzCopy auto-retries; show resume hint on failure |
| **SAS token expiration** | Request SAS with sufficient lifetime based on file size |
| **User cancellation (Ctrl+C)** | Graceful handling, show resume instructions |
| **Progress visibility** | Real-time progress bar with speed, ETA, percentage |
| **Large file support** | AzCopy handles multi-GB files with chunked upload |
```

### Two Distinct Entities

| Entity | Description | Storage | Operations |
|--------|-------------|---------|------------|
| **Binaries/Weights** | Raw model files uploaded by user | FDP Data Store (Blob) | Upload |
| **Custom Models** | Registered models in custom registry | FDP Custom Registry | Register, List, Show, Delete |

## Commands

### `init` - Initialize AI Models Project

Sets up an azd environment and configures the Azure AI Foundry project connection.

```bash
azd ai models init [--project-endpoint <endpoint>] [-s <subscription>] [-p <resource-id>] [-n <env-name>]
```

**Flow:**
1. Ensures azd project is initialized (runs `azd init --minimal` if needed)
2. Creates or selects an azd environment
3. Configures Azure context — prompts interactively for any missing values:
   - Subscription → Resource Group → Foundry Project
4. Stores all settings as environment variables

**Environment Variables Set:**
| Variable | Description |
|----------|-------------|
| `AZURE_TENANT_ID` | Azure AD tenant ID |
| `AZURE_SUBSCRIPTION_ID` | Azure subscription ID |
| `AZURE_RESOURCE_GROUP_NAME` | Resource group name |
| `AZURE_ACCOUNT_NAME` | Cognitive Services account name |
| `AZURE_PROJECT_NAME` | Foundry project name |
| `AZURE_LOCATION` | Azure region |
| `AZURE_PROJECT_ENDPOINT` | Constructed project endpoint URL |

**Flags:**

| Flag | Required | Description |
|------|----------|-------------|
| `--subscription, -s` | No | Azure subscription ID |
| `--project-endpoint` | No | Foundry project endpoint URL |
| `--project-resource-id, -p` | No | ARM resource ID of the Foundry project |
| `--environment, -n` | No | Name of the azd environment to use |

**Output:**
```
$ azd ai models init

Let's get your project initialized.
Let's create a new azd environment for your project.

SUCCESS: AI models project initialized!

  Environment:    dev
  Subscription:   8861a79b-122e-4733-b9f0-bb521b0268ce
  Resource Group: rg-myproject

You can now use commands like:
  azd ai models custom list
  azd ai models custom create --name <model-name> --model <path>
```

### Write Operations

```bash
# Upload weights AND register model in one step
azd ai models custom create --name <model-name> --source <local-path-or-url> [options]

# Delete a custom model
azd ai models custom delete --name <model-name> [--force]
```

### Read Operations

```bash
# List all custom models in the registry
azd ai models custom list [--output table|json]

# Show details of a specific custom model
azd ai models custom show --name <model-name> [--output table|json]
```

## Command Details

### `create` - Upload and Register Custom Model

Combines the upload and register steps into a single user-friendly command.

```bash
azd ai models custom create --name my-model --source ./model-weights/ --base-model FW-DeepSeek-v3.1
```

**Workflow:**
1. Verify AzCopy is available (auto-download to `~/.azd/bin/azcopy` if not found)
2. Request upload SAS from Foundry API (POST `startPendingUpload`)
3. Upload weights via AzCopy with real-time progress bar
4. Register model in custom model registry (PUT)
5. Return success with model details

**Flags:**

| Flag | Required | Description |
|------|----------|-------------|
| `--name, -n` | Yes | Model name in registry |
| `--source` | Yes* | Local file/directory path or remote blob URL |
| `--source-file` | No | File containing the source URL (for URLs with `&` characters) |
| `--version` | No | Model version (default: "1") |
| `--description` | No | Human-readable description |
| `--base-model` | No | Base model architecture tag (e.g., FW-DeepSeek-v3.1) |
| `--azcopy-path` | No | Explicit path to azcopy binary |
| `--project-endpoint` | No | Override project endpoint (reads from env if not set) |
| `--subscription, -s` | No | Override subscription ID |

*Either `--source` or `--source-file` is required.

**Note on remote URLs:** When using a blob URL with SAS token as source, the `&` characters
are interpreted by the shell. Use `--source-file` to provide a file containing the URL:

```bash
echo "https://account.blob.core.windows.net/container/path?sv=...&sig=..." > source_url.txt
azd ai models custom create --name my-model --source-file source_url.txt
```

**Output:**
```
$ azd ai models custom create --name my-model --source ./model-weights/ --base-model FW-DeepSeek-v3.1

  Using azcopy: C:\Users\user\.azd\bin\azcopy.exe

Creating custom model: my-model (version 1)

✓ Upload location ready
  Blob URI: https://storage.blob.core.windows.net/container

Step 2/3: Uploading model files...
  Source: ./model-weights/

  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 100.0% (12.8 GB / 12.8 GB) | 53.0 MB/s | Elapsed: 4m 28s | ETA: done
  Completed in 4m 28s

✓ Upload complete

✓ Model registered successfully!

──────────────────────────────────────────────────
  Name:        my-model
  Version:     1
  Created:     2026-02-14T10:30:00Z
──────────────────────────────────────────────────
```

### `list` - List Custom Models

```bash
azd ai models custom list [--output table|json]
```

**Output (table):**
```
$ azd ai models custom list

NAME             VERSION    CREATED                  CREATED BY
my-model         1          2026-02-14T10:30:00Z     user@contoso.com
test-model       1          2026-02-13T08:15:00Z     user@contoso.com

2 custom model(s) found
```

**Output (json):**
```bash
azd ai models custom list --output json
```

### `show` - Show Custom Model Details

```bash
azd ai models custom show --name my-model [--version 1] [--output table|json]
```

**Flags:**

| Flag | Required | Description |
|------|----------|-------------|
| `--name, -n` | Yes | Model name |
| `--version` | No | Model version (default: "1") |
| `--output, -o` | No | Output format: table, json (default: table) |

**Output:**
```
$ azd ai models custom show --name my-model

Custom Model: my-model
──────────────────────────────────────────────────

General:
  Name:         my-model
  Version:      1
  Description:  My fine-tuned model

System Data:
  Created:       2026-02-14T10:30:00Z
  Created By:    user@contoso.com
  Last Modified: 2026-02-14T10:35:00Z

Storage:
  Blob URI: https://storage.blob.core.windows.net/container

Tags:
  baseArchitecture: FW-DeepSeek-v3.1
──────────────────────────────────────────────────
```

To deploy this model, use Azure CLI:
  az cognitiveservices account deployment create \
    -g <resource-group> -n <account-name> \
    --deployment-name llama-7b-deploy \
    --model-name llama-7b --model-version "1" \
    --model-format safetensors --sku-name "Standard"
```

### `delete` - Delete Custom Model

```bash
azd ai models custom delete --name my-model [--version 1] [--force]
```

**Output:**
```
$ azd ai models custom delete --name my-model

Delete custom model 'my-model' (version 1)? This action cannot be undone.
Type the model name to confirm: my-model

✓ Model 'my-model' (version 1) deleted
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--name, -n` | Model name (required) |
| `--version` | Model version (default: "1") |
| `--force, -f` | Skip confirmation prompt |

## Architecture

### Project Endpoint Resolution

Custom commands resolve the project endpoint using a 3-tier priority:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                     Project Endpoint Resolution                             │
│                                                                             │
│   Priority 1: Explicit --project-endpoint flag                              │
│              └─► Use directly, highest priority                             │
│                                                                             │
│   Priority 2: azd environment variables                                     │
│              └─► Read AZURE_PROJECT_ENDPOINT from current environment       │
│              └─► Or construct from AZURE_ACCOUNT_NAME + AZURE_PROJECT_NAME  │
│                                                                             │
│   Priority 3: Lightweight interactive prompt                                │
│              └─► Subscription → Resource Group → Foundry Project            │
│              └─► No azd project/env creation (unlike full init)             │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### High-Level Flow (3 Steps for Create)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                                                                             │
│   ┌─────────┐         STEP 1: Get SAS           ┌─────────────┐            │
│   │         │ ────────────────────────────────► │             │            │
│   │   CLI   │    POST /datastore/upload/init    │   FDP API   │            │
│   │         │ ◄──────────────────────────────── │             │            │
│   └─────────┘    { sas_uri, upload_id }         └─────────────┘            │
│        │                                                                    │
│        │                                                                    │
│        │  STEP 2: Upload via AzCopy (⚠️ LONG-RUNNING)                       │
│        │                                                                    │
│        │    ┌─────────────────────────────────────────────────────────┐    │
│        │    │  Source                      Destination                │    │
│        │    │  ┌─────────────────┐        ┌─────────────────────┐    │    │
│        │    │  │ Local File      │        │ Azure Blob Storage  │    │    │
│        │    │  │ ./model.safetensors │──►│ (via SAS URI)       │    │    │
│        │    │  └─────────────────┘        └─────────────────────┘    │    │
│        │    │         OR                                              │    │
│        │    │  ┌─────────────────┐        ┌─────────────────────┐    │    │
│        │    │  │ Remote URL      │        │ Azure Blob Storage  │    │    │
│        │    │  │ https://...     │───────►│ (via SAS URI)       │    │    │
│        │    │  └─────────────────┘        └─────────────────────┘    │    │
│        │    │                                                         │    │
│        │    │  Progress: ━━━━━━━━━━━━━━━━━━━ 67% | 125 MB/s | ETA 2m │    │
│        │    └─────────────────────────────────────────────────────────┘    │
│        │                                                                    │
│        │                                                                    │
│   ┌─────────┐         STEP 3: Register          ┌─────────────┐            │
│   │         │ ────────────────────────────────► │             │            │
│   │   CLI   │    POST /custom-models            │   FDP API   │            │
│   │         │ ◄──────────────────────────────── │             │            │
│   └─────────┘    { name, status }               └─────────────┘            │
│                                                         │                   │
│                                                         ▼                   │
│                                                 ┌─────────────┐            │
│                                                 │   Custom    │            │
│                                                 │   Model     │            │
│                                                 │   Registry  │            │
│                                                 └─────────────┘            │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Components

| Component | Responsibility |
|-----------|----------------|
| **FDP Client** | REST client for FDP API (Step 1: get SAS, Step 3: register) |
| **AzCopy Manager** | Ensure AzCopy available, execute uploads with progress (Step 2) |
| **Progress Reporter** | Display upload progress, speed, ETA during long uploads |

### Handling Long-Running Uploads (Step 2)

Since uploads can take **minutes to hours**, we need robust handling:

#### SAS Token Lifetime

Request SAS lifetime based on estimated upload duration:

```go
func calculateRequiredSASLifetime(fileSize int64, avgSpeedMBps float64) time.Duration {
    estimatedSeconds := float64(fileSize) / (avgSpeedMBps * 1024 * 1024)
    // Add 50% buffer + 30 min minimum
    lifetime := time.Duration(estimatedSeconds*1.5) * time.Second
    if lifetime < 30*time.Minute {
        lifetime = 30 * time.Minute
    }
    if lifetime > 24*time.Hour {
        lifetime = 24 * time.Hour // Cap at 24 hours
    }
    return lifetime
}
```

| File Size | Est. Upload Time | Recommended SAS Lifetime |
|-----------|------------------|--------------------------|
| < 10 GB   | < 5 min          | 30 minutes               |
| 10-50 GB  | 5-20 min         | 1 hour                   |
| 50-200 GB | 20-70 min        | 2 hours                  |
| 200+ GB   | 1+ hours         | 4 hours                  |

#### Progress Reporting

Real-time progress during long uploads:

```
Uploading model: llama-70b.safetensors (68.5 GB)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 34% (23.3 GB / 68.5 GB)
Speed: 142 MB/s | Elapsed: 2m 45s | ETA: 5m 20s
```

#### Interruption Handling

```
$ azd ai models custom create --source ./large-model.bin --name my-model

Uploading model: large-model.bin (120 GB)
━━━━━━━━━━━━━━━━━ 28% (33.6 GB / 120 GB)
Speed: 98 MB/s | Elapsed: 5m 42s | ETA: 14m 38s

^C  Interrupted!

Upload interrupted at 28% (33.6 GB uploaded).

To resume, run the same command again:
  azd ai models custom create --source ./large-model.bin --name my-model

Note: A new SAS token will be obtained, but AzCopy will attempt to resume 
from where it left off using its journal files.
```

#### Source Types

The `--source` flag supports:

| Source Type | Example | Notes |
|-------------|---------|-------|
| **Local file** | `./model.safetensors` | Most common |
| **Absolute path** | `/data/models/llama.bin` | |
| **Remote URL** | `https://huggingface.co/.../model.bin` | AzCopy can pull directly |

For remote URLs, AzCopy performs a server-to-server copy when possible, avoiding download to local machine.

### User Engagement During Long Uploads

> ⚠️ **Critical Design Constraint**: The `upload` command performs BOTH upload AND registration. Since registration is invoked by the client after upload succeeds, **the terminal must remain open** until the entire operation completes.

#### The Problem

```
┌─────────────────────────────────────────────────────────────────────────────┐
│  Upload can take HOURS for large models (50GB+ files)                       │
│                                                                             │
│  User starts upload ──► Upload runs ──► ??? ──► Registration                │
│                              │                        │                     │
│                              │                        │                     │
│                        Takes 1-4 hours          Only happens if             │
│                                                 client still running        │
│                                                                             │
│  Risk: User closes terminal, loses SSH, laptop sleeps, etc.                 │
│        = Upload completes but model is NOT registered (orphaned blob)       │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

#### Design Options Considered

| Option | Description | Pros | Cons |
|--------|-------------|------|------|
| **A. Foreground** ✅ | Keep terminal open, show progress | Simple, user sees progress | Terminal must stay open |
| **B. Background mode** | `--background` flag, poll for status | User can close terminal | Complex, needs state storage |
| **C. Detached upload** | Upload only, register separately | Decoupled steps | More commands, user may forget |

#### v1 Decision: Option A (Foreground)

**Rationale:**
- Simplest implementation
- User has clear visibility into progress
- If terminal closes, user restarts (no complex recovery logic)
- Future versions can add background mode if needed

Keep the upload in foreground but **keep user engaged** with:

1. **Rich progress display** - Speed, ETA, percentage, elapsed time
2. **Periodic status updates** - Every 30s, show a status line even if no change
3. **Clear warnings** - At start, warn user about expected duration
4. **Interruption recovery** - If interrupted, show clear resume instructions

**Initial Warning:**
```
$ azd ai models custom create --source ./llama-70b.safetensors --name my-llama

⚠️  Large file detected: 68.5 GB
    Estimated upload time: 8-15 minutes (depending on network speed)
    
    IMPORTANT: Keep this terminal open until upload completes.
    The model will be registered automatically after upload.
    
    Press Enter to continue or Ctrl+C to cancel...
```

**During Upload (keep user engaged):**
```
Uploading model: llama-70b.safetensors
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 34% (23.3 GB / 68.5 GB)
Speed: 142 MB/s | Elapsed: 2m 45s | ETA: 5m 20s

[2m 45s] Still uploading... 34% complete
[3m 15s] Still uploading... 38% complete  
[3m 45s] Still uploading... 42% complete
```

**On Completion:**
```
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 100% (68.5 GB / 68.5 GB)
✓ Upload complete (8m 23s)

Registering model...
✓ Model registered: my-llama

Model Details:
  Name:     my-llama
  Format:   safetensors
  Size:     68.5 GB
  Status:   Ready for deployment
```

#### What Happens If Terminal Closes? (v1 Behavior)

**Simple approach: Restart from scratch.**

| Scenario | State | Recovery |
|----------|-------|----------|
| Closed during Step 1 (get SAS) | Nothing uploaded | Re-run command |
| Closed during Step 2 (upload) | Partial/complete blob | Re-run command (starts fresh) |
| Closed after Step 2, before Step 3 | Orphaned blob | Re-run command (starts fresh) |

**v1 Design Decision:**
- No resume capability
- No orphan recovery needed (FDP service handles cleanup automatically)
- User simply re-runs the command from the beginning
- Previous partial/orphaned uploads are cleaned up by FDP service (TTL-based)

```
$ azd ai models custom create --source ./llama-70b.safetensors --name my-llama

⚠️  Previous upload may exist but was not registered.
    Starting fresh upload...

Uploading model: llama-70b.safetensors
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 0% (0 GB / 68.5 GB)
```

> **Future Enhancement**: Resume capability and orphan recovery can be added in v2 if needed.

### AzCopy Integration

AzCopy is **mandatory** for uploads. Auto-downloaded to `~/.azd/bin/azcopy` if not found.

| Platform | Download URL |
|----------|--------------|
| Windows x64 | `https://aka.ms/downloadazcopy-v10-windows` |
| Linux x64 | `https://aka.ms/downloadazcopy-v10-linux` |
| macOS x64 | `https://aka.ms/downloadazcopy-v10-mac` |
| macOS ARM64 | `https://aka.ms/downloadazcopy-v10-mac-arm64` |

## Actual API Endpoints (Discovered from UI Codebase)

The actual API endpoints differ from the original assumptions. All operations go through the
Azure AI Foundry services endpoint, not separate FDP endpoints.

### Base URL

```
https://{account}.services.ai.azure.com/api/projects/{projectName}
```

### Authentication

| Parameter | Value |
|-----------|-------|
| Token Scope | `https://ai.azure.com/.default` |
| Token Type | `aml_default` (Bearer token) |
| API Version | `2025-11-15-preview` |

### Endpoints

| Method | HTTP | Endpoint | Description |
|--------|------|----------|-------------|
| `ListModels` | GET | `/models?api-version={version}` | List all custom models |
| `GetModel` | GET | `/models/{name}/versions/{version}?api-version={version}` | Get model details |
| `StartPendingUpload` | POST | `/models/{name}/versions/{version}/startPendingUpload?api-version={version}` | Get SAS URI for upload |
| `RegisterModel` | PUT | `/models/{name}/versions/{version}?api-version={version}` | Register model after upload |
| `DeleteModel` | DELETE | `/models/{name}/versions/{version}?api-version={version}` | Delete a model |

### StartPendingUpload Response

```json
{
    "blobReferenceForConsumption": {
        "blobUri": "https://storage.blob.core.windows.net:443/container",
        "storageAccountArmId": "/subscriptions/.../providers/Microsoft.Storage/storageAccounts/...",
        "credential": {
            "credentialType": "SAS",
            "sasUri": "https://storage.blob.core.windows.net/container?sv=...&sig=..."
        }
    },
    "temporaryDataReferenceId": "uuid"
}
```

### RegisterModel Request Body

```json
{
    "blobUri": "https://storage.blob.core.windows.net/container",
    "description": "optional description",
    "tags": {
        "baseArchitecture": "FW-DeepSeek-v3.1"
    }
}
```

### Model Name Validation (from UI)

- 2-30 characters
- Must start with a letter
- Only `[A-Za-z0-9-]` allowed

## Error Handling

| Scenario | User Message |
|----------|--------------|
| File not found | "Error: File not found: {path}" |
| Network failure during upload | "Upload interrupted. Run the same command to resume." |
| Model already exists | "Error: Model 'X' already exists. Use --overwrite to replace." |
| FDP API error | "Error: FDP API returned: {message}" |
| AzCopy download failed | "Error: Could not download AzCopy. Check network and try again." |

## Implementation Plan

Implementation follows a **phased approach** across 4 phases, with each phase focusing on a specific entity.

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                         Implementation Phases                                   │
│                                                                                 │
│   Phase 1              Phase 2                Phase 3            Phase 4        │
│   ─────────────────    ─────────────────      ─────────────────  ───────────    │
│   Custom Model         Custom Model           Base Model         Base Model     │
│   (This doc)           Deployment                                Deployment     │
│                                                                                 │
│   • Upload + Register  • Deploy               • List             • Deploy       │
│   • List               • List                 • Show             • List         │
│   • Show               • Show                                    • Show         │
│   • Delete             • Delete                                  • Delete       │
│                                                                                 │
│   ┌─────────────┐      ┌─────────────┐       ┌─────────────┐    ┌───────────┐  │
│   │ azd ext     │      │ azd ext     │       │ azd ext     │    │ azd ext   │  │
│   │ custom-     │ ───► │ custom-     │ ───►  │ models      │───►│ models    │  │
│   │ models      │      │ models +    │       │ (base)      │    │ (deploy)  │  │
│   │             │      │ deployments │       │             │    │           │  │
│   └─────────────┘      └─────────────┘       └─────────────┘    └───────────┘  │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### Phase 1: Custom Model (This Document) ← ✅ Implemented

**Entity:** Custom Model

**Objective:** Enable users to upload and manage custom models in Foundry registry.

| Milestone | Tasks | Status |
|-----------|-------|--------|
| **1.0 Init Command** | azd project/env setup, Azure context prompts, env var storage | ✅ Done |
| **1.1 Core Upload** | AzCopy discovery, auto-download, Foundry client (startPendingUpload) | ✅ Done |
| **1.2 Registration** | Foundry client (registerModel), `create` command with progress bar | ✅ Done |
| **1.3 Read Operations** | `list` and `show` commands with table/json output | ✅ Done |
| **1.4 Delete** | `delete` command with confirmation prompt, --force flag | ✅ Done |
| **1.5 Endpoint Resolution** | Auto-resolve from env, lightweight prompt fallback | ✅ Done |

**Commands Delivered:**
- `azd ai models init`
- `azd ai models custom create`
- `azd ai models custom list`
- `azd ai models custom show`
- `azd ai models custom delete`

---

### Phase 2: Custom Model Deployment (Future)

**Entity:** Custom Model Deployment

**Objective:** Enable users to deploy custom models to inference endpoints.

| Milestone | Tasks | Status |
|-----------|-------|--------|
| **2.1 Deploy** | Deploy custom model to endpoint | 🔲 |
| **2.2 Read Operations** | List and show deployments | 🔲 |
| **2.3 Delete** | Delete deployment | 🔲 |

**Commands Delivered:**
- `azd ai models custom deployments create`
- `azd ai models custom deployments list`
- `azd ai models custom deployments show`
- `azd ai models custom deployments delete`

> **Note:** Until Phase 2, users can deploy custom models using Azure CLI: `az cognitiveservices account deployment create`

---

### Phase 3: Base Model (Future)

**Entity:** Base Model

**Objective:** Enable users to browse and view base models from catalog.

| Milestone | Tasks | Status |
|-----------|-------|--------|
| **3.1 Read Operations** | List and show base models from catalog | 🔲 |

**Commands Delivered:**
- `azd ai models list` (base models from catalog)
- `azd ai models show`

---

### Phase 4: Base Model Deployment (Future)

**Entity:** Base Model Deployment

**Objective:** Enable users to deploy base models to inference endpoints.

| Milestone | Tasks | Status |
|-----------|-------|--------|
| **4.1 Deploy** | Deploy base model to endpoint | 🔲 |
| **4.2 Read Operations** | List and show deployments | 🔲 |
| **4.3 Delete** | Delete deployment | 🔲 |

**Commands Delivered:**
- `azd ai models deployments create`
- `azd ai models deployments list`
- `azd ai models deployments show`
- `azd ai models deployments delete`

---

### Phase Summary

| Phase | Entity | Operations | Status |
|-------|--------|------------|--------|
| **Phase 1** | Custom Model | Init, Create, List, Show, Delete | ✅ Implemented |
| **Phase 2** | Custom Model Deployment | Deploy, List, Show, Delete | 🔲 Future |
| **Phase 3** | Base Model | List, Show | 🔲 Future |
| **Phase 4** | Base Model Deployment | Deploy, List, Show, Delete | 🔲 Future |

## Advantages of This Approach

| Advantage | Description |
|-----------|-------------|
| **Focused scope** | Only custom models: upload + list. No deployment complexity. |
| **Faster delivery** | Smaller surface area = faster to implement and test |
| **Clear responsibility** | This extension = custom model creation/listing only |
| **Leverages existing tools** | Deployment via existing Azure CLI (`az cognitiveservices`) |
| **Simple mental model** | Users understand "azd for upload, az for deploy" |

## Limitations & Known Issues (v1)

| Limitation | Description | Mitigation |
|------------|-------------|------------|
| No deployment | Deployment not in scope | Use Azure CLI (see below) |
| No base model support | Only custom models | Use Azure Portal or Model Catalog directly |
| **Terminal must stay open** | Upload + registration requires terminal to remain open | Warn user at start about expected duration |
| **No resume on failure** | If terminal closes, must restart from scratch | Keep it simple for v1; re-run command |
| **Shell URL escaping** | SAS URLs with `&` are truncated by cmd.exe/PowerShell | Use `--source-file` flag to provide URL from a file |
| **No `list-uploads`** | Cannot see raw blobs in data store | Users only see registered models via `list` |
| **Orphaned blobs** | Failed uploads may leave orphaned blobs | Handled automatically by service (TTL-based cleanup) |

## Deployment via Azure CLI

> **Note**: Deployment is intentionally out of scope for this extension. Users can deploy custom models using the existing Azure CLI.

### Azure CLI Deployment Commands

```bash
# Create deployment for a custom model
az cognitiveservices account deployment create \
  --resource-group <resource-group> \
  --name <cognitive-services-account> \
  --deployment-name <deployment-name> \
  --model-name <custom-model-name> \
  --model-version "1" \
  --model-format <format> \
  --model-source <source> \
  --sku-name "Standard" \
  --sku-capacity 1

# List deployments
az cognitiveservices account deployment list \
  --resource-group <resource-group> \
  --name <cognitive-services-account>

# Show deployment details
az cognitiveservices account deployment show \
  --resource-group <resource-group> \
  --name <cognitive-services-account> \
  --deployment-name <deployment-name>

# Delete deployment
az cognitiveservices account deployment delete \
  --resource-group <resource-group> \
  --name <cognitive-services-account> \
  --deployment-name <deployment-name>
```

### End-to-End Workflow Example

```bash
# Step 1: Upload and register custom model (this extension)
azd ai models custom create --source ./my-model.safetensors --name my-custom-llama

# Step 2: Deploy the model (Azure CLI)
az cognitiveservices account deployment create \
  -g myResourceGroup \
  -n myAIServicesAccount \
  --deployment-name my-llama-deployment \
  --model-name my-custom-llama \
  --model-version "1" \
  --model-format safetensors \
  --sku-name "Standard" \
  --sku-capacity 1

# Step 3: Use the deployment endpoint for inference
curl https://myAIServicesAccount.openai.azure.com/... 
```

## Technical Implementation Details

### 1. Foundry Client (No Go SDK)

There is **no official Go SDK** for the Foundry project API. A custom HTTP client wrapper
was built at `internal/client/foundry_client.go`.

```go
// FoundryClient is an HTTP client for Azure AI Foundry project APIs.
type FoundryClient struct {
    baseURL    string   // e.g., "https://account.services.ai.azure.com"
    subPath    string   // e.g., "/api/projects/project-name"
    apiVersion string   // "2025-11-15-preview"
    credential azcore.TokenCredential
    httpClient *http.Client
}
```

**Methods:**
| Method | Description |
|--------|-------------|
| `ListModels` | GET all custom models |
| `GetModel` | GET a specific model version |
| `StartPendingUpload` | POST to get SAS URI for upload |
| `RegisterModel` | PUT to register after upload |
| `DeleteModel` | DELETE a model version |

### 2. AzCopy Integration

AzCopy is **mandatory** for uploads. Implemented at `internal/azcopy/runner.go` and `internal/azcopy/installer.go`.

**Discovery Priority:**
1. `--azcopy-path` explicit flag
2. `PATH` lookup via `exec.LookPath`
3. Well-known paths: `~/.azd/bin/azcopy`, `~/.azure/bin/azcopy`
4. Windows `Downloads` folder (scans `azcopy_windows_*` directories)
5. **Auto-download** to `~/.azd/bin/azcopy` (last resort)

**Auto-Download URLs:**

| Platform | Download URL |
|----------|--------------|
| Windows x64 | `https://aka.ms/downloadazcopy-v10-windows` |
| Linux x64 | `https://aka.ms/downloadazcopy-v10-linux` |
| Linux ARM64 | `https://aka.ms/downloadazcopy-v10-linux-arm64` |
| macOS x64 | `https://aka.ms/downloadazcopy-v10-mac` |
| macOS ARM64 | `https://aka.ms/downloadazcopy-v10-mac-arm64` |

The installer downloads the archive, extracts the `azcopy` binary, and installs to `~/.azd/bin/`.
Subsequent runs find it via the well-known path check (no re-download).

### 3. Progress Parsing from AzCopy

AzCopy with `--output-type json` streams NDJSON (one JSON object per line).

**Key Discovery:** All numeric fields in AzCopy JSON output are **strings, not numbers**.

```json
{
  "TimeStamp": "2026-02-13T08:05:00Z",
  "MessageType": "Progress",
  "MessageContent": "{\"TotalBytesTransferred\":\"104857600\",\"PercentComplete\":\"0.76\",\"BytesOverWire\":\"110000000\"}"
}
```

**Important implementation details:**
- `MessageContent` for Progress type is **stringified JSON** (requires double-parse)
- All numeric values are strings: `"PercentComplete":"0.76255155"` not `0.76255155`
- Must use `strconv.ParseInt`/`strconv.ParseFloat` to parse
- `BytesOverWire` updates smoothly every ~2s; `TotalBytesTransferred` only updates on block completion
- `BytesOverWire` includes protocol overhead, can exceed `TotalBytesExpected` — capped at 100%
- MessageTypes: `Info`, `Init`, `Progress`, `Error`, `EndOfJob`

**Progress Display:**
```
  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 67.3% (8.6 GB / 12.8 GB) | 53.0 MB/s | Elapsed: 2m 42s | ETA: 1m 19s
```

### 4. Source Types

The `--source` flag supports both local and remote sources:

| Source Type | Example | Handling |
|-------------|---------|----------|
| **Local file** | `./model.safetensors` | Passed directly to azcopy |
| **Local directory** | `./model-weights/` | Appends `/*` for recursive copy |
| **Remote blob URL** | `https://account.blob.core.windows.net/...` | Passed directly (blob-to-blob copy) |

**Known Issue:** Remote URLs containing `&` (SAS tokens) are truncated by the shell before
reaching the extension process. Workaround: use `--source-file` to provide a file containing the URL.

### 5. Extension File Structure

```
azure.ai.models/
├── extension.yaml              # Extension manifest
├── version.txt                 # "0.0.1-preview"
├── main.go                     # Entry point
├── go.mod / go.sum             # Go module
├── build.ps1 / build.sh        # Build scripts
├── ci-build.ps1 / ci-test.ps1  # CI scripts
├── design/
│   └── proposal-1-custom-models.md
├── internal/
│   ├── cmd/
│   │   ├── root.go             # Root cobra command
│   │   ├── version.go          # Version command
│   │   ├── metadata.go         # Hidden metadata command for azd
│   │   ├── init.go             # Init command (project + env setup)
│   │   ├── custom.go           # Custom command group + endpoint resolution
│   │   ├── custom_create.go    # 3-step create (SAS → upload → register)
│   │   ├── custom_list.go      # List models
│   │   ├── custom_show.go      # Show model details
│   │   └── custom_delete.go    # Delete with confirmation
│   ├── client/
│   │   └── foundry_client.go   # HTTP client for Foundry API
│   ├── azcopy/
│   │   ├── runner.go           # AzCopy discovery + execution + progress
│   │   └── installer.go        # Auto-download + extract + install
│   └── utils/
│       └── output.go           # Table/JSON output formatting
└── pkg/
    └── models/
        ├── custom_model.go     # CustomModel, SystemData, ListModelsResponse types
        ├── pending_upload.go   # PendingUploadResponse, BlobReference types
        └── register_model.go   # RegisterModelRequest type
```

## Resolved Questions

| Question | Answer |
|----------|--------|
| **Registration Metadata** | `blobUri` (required), `description` (optional), `tags` (optional) |
| **Versioning** | Yes, models are versioned: `/models/{name}/versions/{version}` |
| **API Auth** | Azure AD scope: `https://ai.azure.com/.default` |
| **API Endpoints** | All through `https://{account}.services.ai.azure.com/api/projects/{project}` |
| **Overwrite Behavior** | PUT to same name/version updates the model |

## Open Questions

1. **Supported base model architectures**: Current known list from UI: FW-DeepSeek-v3.1, FW-DeepSeek-v3.2, FW-Kimi-K2-Instruct-0905, FW-Kimi-K2-Thinking, FW-Kimi-K2.5, FW-GLM-4.7, FW-GPT-OSS-120B — is this complete?
2. **Model size limits**: Are there size limits for custom model uploads?
3. **Orphan cleanup**: How does the service handle orphaned blobs from failed uploads?

## Future Enhancements (v2+)

| Feature | Description |
|---------|-------------|
| Resume capability | Resume interrupted uploads instead of restart |
| Background mode | `--background` flag for long uploads with server-side registration |
| `list-uploads` | View raw blobs in FDP data store |
