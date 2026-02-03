# Proposal 1: Azure AI Custom Models Extension

## Overview

This document outlines a **focused** extension (`azure.ai.custom-models`) that handles **custom models only**. This approach keeps the scope narrow, avoids complexity of handling two model registries, and delivers value faster.

## Scope

**This extension focuses exclusively on custom model management (upload + list).**

| In Scope | Out of Scope |
|----------|--------------|
| Upload custom model weights | Base models |
| Register custom models | Deployments (use Azure CLI) |
| List custom models | Inference/endpoint management |
| Show custom model details | |
| Delete custom models | |

## Key Concepts

### Custom Models Only

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         Custom Model Lifecycle                              │
│                                                                             │
│   ┌──────────────────────────────────────────┐      ┌──────────────────┐   │
│   │        This Extension (azd)              │      │   Azure CLI      │   │
│   │                                          │      │                  │   │
│   │   ┌──────────────┐    ┌──────────────┐   │      │ ┌──────────────┐ │   │
│   │   │   Upload     │    │   Register   │   │      │ │   Deploy     │ │   │
│   │   │   Weights    │───►│   Model      │───┼─────►│ │   Model      │ │   │
│   │   │              │    │              │   │      │ │              │ │   │
│   │   └──────────────┘    └──────────────┘   │      │ └──────────────┘ │   │
│   │                                          │      │                  │   │
│   │   azd ai custom-models upload            │      │ az cognitiveservices │
│   │   azd ai custom-models list              │      │ account deployment   │
│   │                                          │      │ create               │
│   └──────────────────────────────────────────┘      └──────────────────┘   │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Upload + Register: 3-Step Process

The `upload` command performs three sequential steps internally:

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

### Write Operations

```bash
# Upload weights AND register model in one step
azd ai custom-models upload --source <local-path> --name <model-name> [options]

# Delete a custom model (and optionally its weights)
azd ai custom-models delete --name <model-name> [--keep-weights]
```

### Read Operations

```bash
# List all custom models in the registry
azd ai custom-models list [--format table|json]

# Show details of a specific custom model
azd ai custom-models show --name <model-name>
```

## Command Details

### `upload` - Upload and Register Custom Model

Combines the upload and register steps into a single user-friendly command.

```bash
azd ai custom-models upload --source ./llama-7b.safetensors --name llama-7b
```

**Workflow:**
1. Validate source file exists and is readable
2. Ensure AzCopy is available (auto-download if needed)
3. Request upload SAS from FDP API
4. Upload weights to FDP data store via AzCopy
5. Register model in FDP custom registry
6. Return success with model details

**Flags:**

| Flag | Required | Description |
|------|----------|-------------|
| `--source, -s` | Yes | Local file path to upload |
| `--name, -n` | Yes | Model name in registry |
| `--format, -f` | No | Model format (auto-detected: safetensors, gguf, onnx) |
| `--description` | No | Human-readable description |
| `--tags` | No | Key=value tags (can specify multiple) |
| `--version` | No | Version string (default: "1.0") |
| `--overwrite` | No | Overwrite if model exists |
| `--no-progress` | No | Disable progress bar |
| `--dry-run` | No | Preview without executing |

**Output:**
```
$ azd ai custom-models upload --source ./llama-7b.safetensors --name llama-7b

Initializing upload...
  Model: llama-7b
  Size: 13.5 GB
  Format: safetensors (auto-detected)

Uploading to FDP data store...
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 100% (13.5 GB / 13.5 GB)
Speed: 142 MB/s

Registering model...
✓ Upload complete
✓ Model registered: llama-7b

Model Details:
  Name:     llama-7b
  Format:   safetensors
  Size:     13.5 GB
  Version:  1.0
  Status:   Ready for deployment
```

### `list` - List Custom Models

```bash
azd ai custom-models list
```

**Output:**
```
Custom Models
┌──────────────┬─────────────┬─────────┬─────────┬─────────────────────┬────────────────────┐
│ Name         │ Format      │ Size    │ Version │ Created             │ Status             │
├──────────────┼─────────────┼─────────┼─────────┼─────────────────────┼────────────────────┤
│ llama-7b     │ safetensors │ 13.5 GB │ 1.0     │ 2026-02-03 10:30    │ Ready              │
│ mistral-7b   │ gguf        │ 4.1 GB  │ 1.0     │ 2026-02-01 14:22    │ Ready              │
│ phi-3-mini   │ onnx        │ 2.3 GB  │ 2.0     │ 2026-01-28 09:15    │ Ready              │
└──────────────┴─────────────┴─────────┴─────────┴─────────────────────┴────────────────────┘

3 custom models found
```

### `show` - Show Custom Model Details

```bash
azd ai custom-models show --name llama-7b
```

**Output:**
```
Custom Model: llama-7b

General:
  Name:         llama-7b
  Format:       safetensors
  Version:      1.0
  Description:  Fine-tuned Llama 7B for code generation
  Status:       Ready for deployment

Storage:
  Size:         13.5 GB
  Path:         uploads/llama-7b/model.safetensors
  Uploaded:     2026-02-03 10:30:00 UTC

Tags:
  team:         ml-platform
  project:      code-assist

To deploy this model, use Azure CLI:
  az cognitiveservices account deployment create \
    -g <resource-group> -n <account-name> \
    --deployment-name llama-7b-deploy \
    --model-name llama-7b --model-version "1" \
    --model-format safetensors --sku-name "Standard"
```

### `delete` - Delete Custom Model

```bash
azd ai custom-models delete --name llama-7b
```

**Output:**
```
$ azd ai custom-models delete --name llama-7b

Are you sure you want to delete 'llama-7b'? This will:
  • Remove model from registry
  • Delete uploaded weights (13.5 GB)

Type 'llama-7b' to confirm: llama-7b

Deleting model...
✓ Model 'llama-7b' deleted
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--keep-weights` | Remove from registry but keep weights in data store |
| `--force, -f` | Skip confirmation prompt |

## Architecture

### High-Level Flow (3 Steps)

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
$ azd ai custom-models upload --source ./large-model.bin --name my-model

Uploading model: large-model.bin (120 GB)
━━━━━━━━━━━━━━━━━ 28% (33.6 GB / 120 GB)
Speed: 98 MB/s | Elapsed: 5m 42s | ETA: 14m 38s

^C  Interrupted!

Upload interrupted at 28% (33.6 GB uploaded).

To resume, run the same command again:
  azd ai custom-models upload --source ./large-model.bin --name my-model

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
$ azd ai custom-models upload --source ./llama-70b.safetensors --name my-llama

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
$ azd ai custom-models upload --source ./llama-70b.safetensors --name my-llama

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

## FDP API Requirements

### Endpoints Needed

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/datastore/upload/initialize` | POST | Get SAS URI for upload |
| `/custom-models` | POST | Register a custom model |
| `/custom-models` | GET | List custom models |
| `/custom-models/{name}` | GET | Get model details |
| `/custom-models/{name}` | DELETE | Delete model |
| `/datastore/list-sas` | GET | Get SAS for listing uploads |

### Example API Calls

**Initialize Upload:**
```http
POST /datastore/upload/initialize
{
  "model_name": "llama-7b",
  "file_size": 13500000000,
  "format": "safetensors"
}

Response:
{
  "upload_id": "abc123",
  "sas_uri": "https://fdpstore.blob.core.windows.net/uploads/llama-7b/model.safetensors?sv=...",
  "expires_at": "2026-02-03T14:00:00Z"
}
```

**Register Model:**
```http
POST /custom-models
{
  "name": "llama-7b",
  "format": "safetensors",
  "upload_id": "abc123",
  "version": "1.0",
  "description": "Fine-tuned Llama 7B",
  "tags": {
    "team": "ml-platform"
  }
}

Response:
{
  "name": "llama-7b",
  "status": "Ready",
  "created_at": "2026-02-03T10:30:00Z"
}
```

## Error Handling

| Scenario | User Message |
|----------|--------------|
| File not found | "Error: File not found: {path}" |
| Network failure during upload | "Upload interrupted. Run the same command to resume." |
| Model already exists | "Error: Model 'X' already exists. Use --overwrite to replace." |
| FDP API error | "Error: FDP API returned: {message}" |
| AzCopy download failed | "Error: Could not download AzCopy. Check network and try again." |

## Implementation Plan

### Phase 1 - Core Upload & Register (MVP)
- [ ] AzCopy detection and auto-download
- [ ] FDP client (upload init, register)
- [ ] `azd ai custom-models upload` command
- [ ] Progress reporting

### Phase 2 - Read Operations
- [ ] FDP client (list, show)
- [ ] `azd ai custom-models list` command
- [ ] `azd ai custom-models show` command
- [ ] `azd ai custom-models list-uploads` command

### Phase 3 - Delete & Polish
- [ ] `azd ai custom-models delete` command
- [ ] Error handling improvements
- [ ] Resume upload support
- [ ] JSON output format

## Advantages of This Approach

| Advantage | Description |
|-----------|-------------|
| **Focused scope** | Only custom models: upload + list. No deployment complexity. |
| **Faster delivery** | Smaller surface area = faster to implement and test |
| **Clear responsibility** | This extension = custom model creation/listing only |
| **Leverages existing tools** | Deployment via existing Azure CLI (`az cognitiveservices`) |
| **Simple mental model** | Users understand "azd for upload, az for deploy" |

## Limitations & Known Gaps (v1)

| Limitation | Description | Mitigation |
|------------|-------------|------------|
| No deployment | Deployment not in scope | Use Azure CLI (see below) |
| No base model support | Only custom models | Use Azure Portal or Model Catalog directly |
| **Terminal must stay open** | Upload + registration requires terminal to remain open | Warn user at start about expected duration |
| **No resume on failure** | If terminal closes, must restart from scratch | Keep it simple for v1; re-run command |
| **No `list-uploads`** | Cannot see raw blobs in FDP data store | Users only see registered models via `list` |
| **Orphaned blobs** | Failed uploads may leave orphaned blobs | Handled automatically by FDP service (TTL-based cleanup) |

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
azd ai custom-models upload --source ./my-model.safetensors --name my-custom-llama

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

## Technical Challenges

### 1. No Go SDK for FDP API

There is **no official Go SDK** for the FDP (Foundational Data Platform) API. We need to build a custom HTTP client wrapper.

**Impact:**
- Additional development effort to build and maintain HTTP client
- Manual handling of authentication, error handling, and retries
- API changes require manual updates to our wrapper

**Solution: Custom FDP Client**

```go
// FDP Client - HTTP wrapper for FDP API
type FDPClient struct {
    baseURL    string
    httpClient *http.Client
    credential azcore.TokenCredential
}

// Get auth token and make REST calls manually
func (c *FDPClient) InitializeUpload(ctx context.Context, req UploadRequest) (*UploadResponse, error) {
    // 1. Get Azure AD token
    token, err := c.credential.GetToken(ctx, policy.TokenRequestOptions{
        Scopes: []string{"https://fdp.azure.com/.default"}, // TBD: actual scope
    })
    if err != nil {
        return nil, fmt.Errorf("failed to get token: %w", err)
    }
    
    // 2. Build HTTP request
    body, _ := json.Marshal(req)
    httpReq, _ := http.NewRequestWithContext(ctx, "POST", 
        c.baseURL+"/datastore/upload/initialize", bytes.NewReader(body))
    httpReq.Header.Set("Authorization", "Bearer "+token.Token)
    httpReq.Header.Set("Content-Type", "application/json")
    
    // 3. Execute request
    resp, err := c.httpClient.Do(httpReq)
    if err != nil {
        return nil, fmt.Errorf("HTTP request failed: %w", err)
    }
    defer resp.Body.Close()
    
    // 4. Handle response
    if resp.StatusCode != http.StatusOK {
        return nil, parseErrorResponse(resp)
    }
    
    var result UploadResponse
    json.NewDecoder(resp.Body).Decode(&result)
    return &result, nil
}

func (c *FDPClient) RegisterModel(ctx context.Context, req RegisterRequest) (*Model, error) {
    // Similar HTTP wrapper pattern...
}

func (c *FDPClient) ListModels(ctx context.Context) ([]Model, error) {
    // Similar HTTP wrapper pattern...
}
```

**FDP Client Methods Required:**

> ⚠️ **Note**: Endpoint paths below are **assumed/placeholder**. Actual paths will be confirmed with FDP team.

| Method | HTTP | Endpoint (assumed) | Description |
|--------|------|----------|-------------|
| `InitializeUpload` | POST | `/datastore/upload/initialize` | Get SAS URI for upload |
| `RegisterModel` | POST | `/custom-models` | Register model after upload |
| `ListModels` | GET | `/custom-models` | List all custom models |
| `GetModel` | GET | `/custom-models/{name}` | Get model details |
| `DeleteModel` | DELETE | `/custom-models/{name}` | Delete a model |

### 2. AzCopy Execution

AzCopy execution is straightforward using `os/exec`. No SDK needed.

```go
cmd := exec.CommandContext(ctx, azcopyPath, "copy", source, sasURI, 
    "--output-type", "json",
    "--block-size-mb", "100")
```

### 3. Progress Parsing from AzCopy

AzCopy with `--output-type json` streams progress that we parse:

| Field | Source | Notes |
|-------|--------|-------|
| Percent | AzCopy JSON | `PercentComplete` |
| Bytes transferred | AzCopy JSON | `TotalBytesTransferred` |
| Total bytes | AzCopy JSON | `TotalBytesEnumerated` |
| Speed | AzCopy JSON | `ThroughputMbps` |
| Elapsed | Calculated | `time.Since(startTime)` |
| ETA | Calculated | `remaining / speed` |

> **Note**: AzCopy progress update frequency varies - updates may come every few seconds rather than continuously.

## Open Questions

1. **Registration Metadata**: What fields does FDP custom registry require/support?
2. **Versioning**: Does FDP support multiple versions of the same model name?
3. **FDP API Auth**: What Azure AD scope is needed for FDP API?
4. **Overwrite Behavior**: If user re-runs upload for same model name, should we:
   - Fail if model already registered?
   - Require `--overwrite` flag?
   - Always overwrite?

## Future Enhancements (v2+)

| Feature | Description |
|---------|-------------|
| Resume capability | Resume interrupted uploads instead of restart |
| Background mode | `--background` flag for long uploads with server-side registration |
| `list-uploads` | View raw blobs in FDP data store |
