# Azure AI Models Extension - Architecture Design

## Overview

This document outlines the architecture and design for the `azure.ai.models` extension. The extension enables users to manage AI models including uploading models from local or remote locations to the FDP (Foundational Data Platform) data store backed by Azure Storage.

## Key Concepts

### Two Distinct Entities

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                                                                             │
│   ┌─────────────────────┐              ┌─────────────────────┐             │
│   │  Binaries/Weights   │              │      Models         │             │
│   │  (FDP Data Store)   │  ──────────► │  (FDP Registry)     │             │
│   ├─────────────────────┤   register   ├─────────────────────┤             │
│   │ • Raw model files   │              │ • Hosted on FDP     │             │
│   │ • Stored in Blob    │              │ • Ready for         │             │
│   │ • Just storage      │              │   inference         │             │
│   │ • Listed via SAS    │              │ • Has API endpoint  │             │
│   └─────────────────────┘              └─────────────────────┘             │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

| Entity | Description | Storage | Listing |
|--------|-------------|---------|---------|
| **Binaries/Weights** | Raw model files uploaded by user | Blob Storage | Via List SAS or FDP API |
| **Models** | Registered models ready for inferencing | FDP Registry | Via FDP API |

## Purpose

- Provide CLI commands to upload, list, and manage AI model binaries in FDP data store
- Enable resilient upload of large model files (multi-GB) to Azure Blob Storage
- Offer progress tracking for long-running upload operations
- Support both local file paths and remote URLs as model sources

## Features

1. **Binary Upload** - Upload model weights from local paths to FDP data store
2. **Progress Tracking** - Real-time progress display for large file uploads
3. **Resilient Upload** - Handle network failures with automatic retry and resume
4. **List Uploads** - List uploaded binaries in the FDP data store
5. **Model Info** - Get details about a specific upload

## Commands

```
azd ai models upload --source <local-path> --name <model-name> [--format <format>]
azd ai models list-uploads [--prefix <prefix>] [--format <format>]
azd ai models show-upload --name <model-name>
azd ai models delete-upload --name <model-name>
```

## Architecture

### High-Level Upload Flow

```
┌─────────────┐     1. Request Upload      ┌─────────────┐
│             │ ─────────────────────────► │             │
│   CLI       │                            │   FDP API   │
│             │ ◄───────────────────────── │             │
└─────────────┘     2. Return SAS URI      └─────────────┘
       │
       │ 3. Upload with SAS URI
       ▼
┌─────────────────────────────────────────────────────────┐
│                   Azure Blob Storage                     │
│  ┌─────────────────────────────────────────────────┐    │
│  │  Block Blob (using Put Block + Put Block List)  │    │
│  └─────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────┘
```

### Components

#### 1. FDP Client
- Handles authentication and communication with FDP API
- Requests upload destination (SAS URI)
- Registers model metadata after successful upload

#### 2. Blob Uploader (Resilient Upload Engine)
- Manages chunked upload to Azure Blob Storage
- Implements retry logic with exponential backoff
- Tracks upload progress and supports resume

#### 3. Progress Reporter
- Displays real-time upload progress in CLI
- Shows transfer speed, ETA, and percentage complete

### Resilient Upload Design for Large Files

> **Note**: AzCopy is **mandatory** for the model upload capability. If AzCopy is not found on the system, the extension will automatically download it on first use.

#### Why AzCopy?

AzCopy is Microsoft's official, high-performance command-line utility for Azure Storage transfers. Rather than implementing our own upload logic, we leverage AzCopy because:

- **Battle-tested**: Years of production hardening by Microsoft
- **Highly optimized**: Native code with memory-mapped I/O, connection pooling
- **Built-in resilience**: Automatic retry, resume via journal files, exponential backoff
- **Auto-tuning**: Parallelism adjusted based on system resources
- **Bandwidth control**: Built-in throttling (`--cap-mbps`)
- **Checksum validation**: Built-in MD5/CRC64 verification
- **MIT licensed**: Free to use and distribute

#### Prior Art

This approach follows established patterns in the Azure ecosystem:

| Tool | AzCopy Usage |
|------|--------------|
| **Azure CLI** | `az storage blob upload` and `az storage copy` use AzCopy internally for large file transfers |
| **Azure Storage Explorer** | Bundles AzCopy for all blob upload/download operations |
| **Azure Data Box CLI** | Uses AzCopy for data transfer to Data Box devices |

Reference: [Azure CLI Storage Commands](https://learn.microsoft.com/en-us/cli/azure/storage)

#### AzCopy Auto-Download

```
┌────────────────────────────────────────────────────────────────┐
│                    First Upload - AzCopy Check                 │
├────────────────────────────────────────────────────────────────┤
│                                                                │
│   azd ai models upload                                         │
│        │                                                       │
│        ▼                                                       │
│   ┌─────────────────┐                                         │
│   │ Is AzCopy       │──── Yes ───► Proceed with upload        │
│   │ available?      │                                         │
│   └─────────────────┘                                         │
│        │ No                                                    │
│        ▼                                                       │
│   ┌─────────────────┐                                         │
│   │ Download AzCopy │                                         │
│   │ to ~/.azd/bin/  │                                         │
│   └─────────────────┘                                         │
│        │                                                       │
│        ▼                                                       │
│   Proceed with upload                                          │
│                                                                │
└────────────────────────────────────────────────────────────────┘
```

#### AzCopy Download Details

| Platform | Download URL | Binary Size |
|----------|--------------|-------------|
| Windows x64 | `https://aka.ms/downloadazcopy-v10-windows` | ~70 MB |
| Linux x64 | `https://aka.ms/downloadazcopy-v10-linux` | ~70 MB |
| macOS x64 | `https://aka.ms/downloadazcopy-v10-mac` | ~70 MB |
| macOS ARM64 | `https://aka.ms/downloadazcopy-v10-mac-arm64` | ~70 MB |

Installation location: `~/.azd/bin/azcopy[.exe]`

```go
func ensureAzCopy() (string, error) {
    // 1. Check if azcopy is in PATH
    if path, err := exec.LookPath("azcopy"); err == nil {
        return path, nil
    }
    
    // 2. Check if we've downloaded it before
    azdBinPath := filepath.Join(os.UserHomeDir(), ".azd", "bin", "azcopy")
    if runtime.GOOS == "windows" {
        azdBinPath += ".exe"
    }
    if _, err := os.Stat(azdBinPath); err == nil {
        return azdBinPath, nil
    }
    
    // 3. Download AzCopy
    fmt.Println("AzCopy not found. Downloading (one-time setup)...")
    if err := downloadAzCopy(azdBinPath); err != nil {
        return "", fmt.Errorf("failed to download AzCopy: %w", err)
    }
    
    return azdBinPath, nil
}
```

#### Upload Flow with AzCopy

1. **User initiates upload**: `azd ai models upload --source ./model.safetensors --name my-model`

2. **Ensure AzCopy**: Check/download AzCopy if needed

3. **FDP API Call**: 
   - POST `/models/upload/initialize`
   - Request: `{ "model_name": "my-model", "file_size": 10737418240, "format": "safetensors" }`
   - Response: `{ "upload_id": "xxx", "sas_uri": "https://storage.blob.core.windows.net/models/xxx?sv=...&sig=..." }`

4. **Execute AzCopy**:
   ```bash
   azcopy copy "./model.safetensors" "<sas_uri>" \
     --output-type json \
     --block-size-mb 100 \
     --log-level INFO
   ```

5. **Monitor progress**: Parse AzCopy JSON output for progress updates

6. **Register model with FDP**:
   - POST `/models/upload/complete`
   - Request: `{ "upload_id": "xxx", "model_name": "my-model" }`

#### AzCopy Execution Wrapper

```go
type AzCopyUploader struct {
    azcopyPath string
}

func (u *AzCopyUploader) Upload(ctx context.Context, source, sasURI string, progress func(ProgressInfo)) error {
    cmd := exec.CommandContext(ctx, u.azcopyPath, "copy", source, sasURI,
        "--output-type", "json",
        "--block-size-mb", "100",
    )
    
    stdout, _ := cmd.StdoutPipe()
    if err := cmd.Start(); err != nil {
        return fmt.Errorf("failed to start azcopy: %w", err)
    }
    
    // Parse JSON output for progress
    scanner := bufio.NewScanner(stdout)
    for scanner.Scan() {
        var msg AzCopyMessage
        if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
            continue
        }
        if msg.MessageType == "Progress" {
            progress(parseProgress(msg.MessageContent))
        }
    }
    
    if err := cmd.Wait(); err != nil {
        return fmt.Errorf("azcopy failed: %w", err)
    }
    return nil
}
```

#### Progress Reporting

```
Uploading model: llama-7b-instruct.safetensors
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 45% (4.5 GB / 10 GB)
Speed: 125 MB/s | ETA: 44s
```

#### Upload Output

On successful upload, print the blob path for use in registration:

```
$ azd ai models upload --source ./llama-7b.safetensors --name llama-7b

✓ Upload complete
✓ Model binary uploaded to: uploads/llama-7b/model.safetensors

Use this path to register the model:
  azd ai models register --path uploads/llama-7b/model.safetensors --name llama-7b
```

### List Uploads Architecture

> **Note**: This feature requires support from the FDP API team. Either approach below needs FDP backend implementation.

Since users don't have direct access to the blob storage account, we need FDP API support to list uploaded binaries.

#### Option A: FDP Provides List SAS (Recommended)

```
┌─────────────┐     1. Request list SAS      ┌─────────────┐
│             │ ────────────────────────────►│             │
│    CLI      │                              │   FDP API   │
│             │ ◄────────────────────────────│             │
└─────────────┘     2. Return SAS (list)     └─────────────┘
       │
       │ 3. List blobs with SAS
       │    (filter by prefix, read metadata)
       ▼
┌─────────────────────────────────────────────────────────────┐
│                   Azure Blob Storage                         │
└─────────────────────────────────────────────────────────────┘
```

**FDP API Required:**
```
GET /datastore/list-sas?prefix=uploads/
Response:
{
  "sas_uri": "https://fdpstore.blob.core.windows.net/models?sv=...&sp=l...",
  "expires_at": "2026-02-03T12:00:00Z"
}
```

SAS Permission Needed: `List (l)` on container

**CLI Implementation:**
```go
func listUploads(sasURI string, prefix string) ([]UploadedBinary, error) {
    containerClient, _ := container.NewClientWithNoCredential(sasURI, nil)
    
    pager := containerClient.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{
        Prefix: to.Ptr(prefix),  // e.g., "uploads/" or "uploads/llama-"
        Include: container.ListBlobsInclude{Metadata: true},
    })
    
    var results []UploadedBinary
    for pager.More() {
        page, _ := pager.NextPage(ctx)
        for _, blob := range page.Segment.BlobItems {
            results = append(results, UploadedBinary{
                Name:       getMetadata(blob, "model-name"),
                Format:     getMetadata(blob, "format"),
                Path:       *blob.Name,
                Size:       *blob.Properties.ContentLength,
                UploadedAt: *blob.Properties.LastModified,
            })
        }
    }
    return results, nil
}
```

#### Option B: FDP API Returns List Directly

```
┌─────────────┐     1. Request list          ┌─────────────┐
│             │ ────────────────────────────►│             │
│    CLI      │                              │   FDP API   │
│             │ ◄────────────────────────────│  (queries   │
└─────────────┘     2. Return list           │   blob)     │
                                             └─────────────┘
```

**FDP API Required:**
```
GET /datastore/uploads?prefix=llama&format=safetensors
Response:
{
  "uploads": [
    {
      "name": "llama-7b",
      "path": "uploads/llama-7b/model.safetensors",
      "format": "safetensors",
      "size": 13500000000,
      "uploaded_at": "2026-02-03T10:30:00Z"
    }
  ],
  "next_page": "..."
}
```

#### Comparison

| Aspect | Option A (List SAS) | Option B (FDP API) |
|--------|---------------------|---------------------|
| FDP API complexity | Simple (just return SAS) | More complex (pagination, filtering) |
| Client flexibility | High (client-side filtering) | Lower (depends on API features) |
| Network calls | 2+ (get SAS + list blobs) | 1 |
| Abstraction | User sees storage structure | Clean abstraction |
| **FDP Team Effort** | Low | Medium |

**Recommendation**: Start with **Option A** (List SAS) for faster implementation, migrate to Option B later if needed.

#### Blob Metadata for Listing

During upload, set metadata on the blob for filtering:

```go
// AzCopy supports setting metadata:
// azcopy copy "source" "dest" --metadata="model-name=llama-7b;format=safetensors;uploaded-by=user@contoso.com"

metadata := map[string]string{
    "model-name":  "llama-7b",
    "format":      "safetensors",
    "uploaded-by": userEmail,
    "uploaded-at": time.Now().UTC().Format(time.RFC3339),
}
```

#### List Uploads Output

```
$ azd ai models list-uploads

Uploaded Model Binaries
┌──────────────┬─────────────┬─────────┬─────────────────────┬───────────────────────────────────┐
│ Name         │ Format      │ Size    │ Uploaded            │ Path                              │
├──────────────┼─────────────┼─────────┼─────────────────────┼───────────────────────────────────┤
│ llama-7b     │ safetensors │ 13.5 GB │ 2026-02-03 10:30    │ uploads/llama-7b/model.safetensors│
│ mistral-7b   │ gguf        │ 4.1 GB  │ 2026-02-01 14:22    │ uploads/mistral-7b/model.gguf     │
│ phi-3-mini   │ onnx        │ 2.3 GB  │ 2026-01-28 09:15    │ uploads/phi-3-mini/model.onnx     │
└──────────────┴─────────────┴─────────┴─────────────────────┴───────────────────────────────────┘

$ azd ai models list-uploads --prefix llama --format safetensors

Uploaded Model Binaries (filtered)
┌──────────────┬─────────────┬─────────┬─────────────────────┬───────────────────────────────────┐
│ Name         │ Format      │ Size    │ Uploaded            │ Path                              │
├──────────────┼─────────────┼─────────┼─────────────────────┼───────────────────────────────────┤
│ llama-7b     │ safetensors │ 13.5 GB │ 2026-02-03 10:30    │ uploads/llama-7b/model.safetensors│
└──────────────┴─────────────┴─────────┴─────────────────────┴───────────────────────────────────┘
```

### Error Handling

| Scenario | AzCopy Behavior | Our Action |
|----------|-----------------|------------|
| Network interruption | Auto-retry with journal | Wait for completion |
| Ctrl+C | Saves journal for resume | Show "Resume with same command" |
| SAS token expired | Exit code 1 | Prompt to re-run (get new SAS) |
| File not found | Exit code 1 | Show clear error |
| Disk full | Exit code 1 | Show clear error |

### Fault Tolerance

#### SAS Token Expiration (Critical)

Large file uploads can take hours. SAS must outlive the upload:

| File Size | Est. Time @ 50 MB/s | Recommended SAS Lifetime |
|-----------|---------------------|--------------------------|
| < 10 GB | < 5 min | 1 hour |
| 10-100 GB | 5-35 min | 2 hours |
| 100+ GB | 35+ min | 4+ hours |

**Action**: Request SAS lifetime from FDP based on file size. Validate before upload:
```go
if sasExpiry.Sub(time.Now()) < estimatedDuration+30*time.Minute {
    return error("SAS token may expire during upload")
}
```

#### AzCopy Exit Codes

| Exit Code | Meaning | User Message |
|-----------|---------|--------------|
| 0 | Success | Upload complete |
| 1 | Failure | "Upload failed - run again to retry" |
| 2 | Partial success | "Some files failed - check logs" |

#### Resume Behavior

- AzCopy uses journal files in `~/.azcopy/` for resume
- **Problem**: Journal references old SAS, new run gets new SAS
- **Solution**: Clear journal and restart with new SAS on re-run

```go
func clearAzCopyJournal(jobID string) {
    os.RemoveAll(filepath.Join(os.UserHomeDir(), ".azcopy", jobID))
}
```

#### Pre-Upload Validation

| Check | Action on Failure |
|-------|-------------------|
| Source file exists | Error: "File not found: {path}" |
| Source file readable | Error: "Cannot read file: {path}" |
| Sufficient SAS lifetime | Error: "Upload may take {X}. Request longer SAS." |

## User Experience

### UX Patterns (Based on Azure CLI)

Azure CLI's `az storage blob` commands provide proven UX patterns we should follow:

| Feature | Flag | Description |
|---------|------|-------------|
| Progress Control | `--no-progress` | Disable progress reporting |
| Dry Run | `--dry-run` | Preview operations without executing |
| Overwrite Control | `--overwrite` | Control whether to overwrite existing |
| Bandwidth Cap | `--cap-mbps` | Limit upload bandwidth |
| Quiet Mode | `--quiet` | Suppress non-error output |

Reference: Azure CLI `az storage blob sync` passes extra options directly to AzCopy via `-- --cap-mbps=20`.

### CLI Flags

```
azd ai models upload --source <path> --name <name> [options]

Options:
  --source, -s       Local file path to upload (required)
  --name, -n         Model name in FDP (required)
  --format, -f       Model format (auto-detected if omitted)
  --overwrite        Overwrite existing model (default: false)
  --no-progress      Disable progress bar
  --dry-run          Show what would be uploaded without uploading
  --cap-mbps         Bandwidth cap in Mbps (0 = unlimited)
  --quiet            Suppress non-error output
```

### Output Examples

**Normal upload:**
```
$ azd ai models upload --source ./llama-7b.safetensors --name llama-7b

Initializing upload...
  Model: llama-7b
  Size: 13.5 GB
  Format: safetensors

Uploading to FDP...
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 67% (9.0 GB / 13.5 GB)
Speed: 142 MB/s | ETA: 32s

✓ Upload complete
✓ Model registered: llama-7b
```

**Dry run:**
```
$ azd ai models upload --source ./llama-7b.safetensors --name llama-7b --dry-run

Dry run - no changes will be made

Would upload:
  Source: ./llama-7b.safetensors
  Size: 13.5 GB
  Destination: llama-7b
  Format: safetensors
  Estimated time: ~95 seconds @ 142 MB/s
```

**Quiet mode (for scripts):**
```
$ azd ai models upload --source ./model.bin --name my-model --quiet
# No output on success, only errors
```

**Error with resume hint:**
```
$ azd ai models upload --source ./large-model.bin --name my-model

Uploading to FDP...
━━━━━━━━━━━━━━━━━━━ 34% (3.4 GB / 10 GB)

Error: Network connection lost

To resume, run the same command again:
  azd ai models upload --source ./large-model.bin --name my-model
```

### AzCopy Not Found Experience

```
$ azd ai models upload --source ./model.bin --name my-model

AzCopy not found. Downloading (one-time setup)...
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 100% (70 MB)
✓ AzCopy installed to ~/.azd/bin/azcopy

Uploading to FDP...
...
```

## Dependencies

- **AzCopy v10**: Auto-downloaded if not present (~70 MB)
- **FDP REST API**: For upload initialization and model registration
- **azd extension framework**: `github.com/azure/azure-dev/cli/azd/pkg/azdext`

## Configuration

```yaml
# azure.yaml (optional overrides)
ai:
  models:
    upload:
      block_size_mb: 100      # AzCopy block size (default: 100)
      cap_mbps: 0             # Bandwidth cap in Mbps (0 = unlimited)
```

Environment variables:
- `AZD_AI_MODELS_BLOCK_SIZE_MB` - Override block size
- `AZD_AI_MODELS_CAP_MBPS` - Bandwidth throttling

## Implementation Plan

### Phase 1 - Core Upload

- [ ] AzCopy detection and auto-download
- [ ] FDP client for upload initialization
- [ ] AzCopy wrapper with JSON output parsing
- [ ] Progress reporting
- [ ] `azd ai models upload` command

### Phase 2 - UX & Polish

- [ ] Improved progress bar with speed/ETA
- [ ] Handle Ctrl+C gracefully (show resume hint)
- [ ] Upload from remote URL (if AzCopy supports)
- [ ] Bandwidth throttling option

### Phase 3 - Management Commands

- [ ] `azd ai models list-uploads`
- [ ] `azd ai models show-upload`
- [ ] `azd ai models delete-upload`

## Technical Challenges

### 1. No Go SDK for FDP/Project API

There is no official Go SDK for the FDP (Foundational Data Platform) API. We need to build a custom REST API wrapper.

**Impact:**
- Additional development effort to build and maintain HTTP client
- Need to handle authentication, error handling, retries manually
- API changes require manual updates to our wrapper

**Mitigation:**
```go
// Build custom FDP client with REST calls
type FDPClient struct {
    baseURL    string
    httpClient *http.Client
    credential azcore.TokenCredential
}

func (c *FDPClient) InitializeUpload(ctx context.Context, req UploadRequest) (*UploadInitResponse, error) {
    token, _ := c.credential.GetToken(ctx, policy.TokenRequestOptions{
        Scopes: []string{"https://cognitiveservices.azure.com/.default"},
    })
    
    httpReq, _ := http.NewRequestWithContext(ctx, "POST", 
        c.baseURL+"/datastore/upload/initialize", body)
    httpReq.Header.Set("Authorization", "Bearer "+token.Token)
    
    // Execute and parse response...
}
```

### 2. Cognitive Services SDK Available

For model registration and deployment, the existing Azure Cognitive Services Go SDK can be used (same as finetune extension):

```go
import "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
```

**Note:** Consider sharing common code with `azure.ai.finetune` extension.

### 3. AzCopy Execution

AzCopy execution is straightforward using `os/exec`. No specific SDK needed.

```go
cmd := exec.CommandContext(ctx, azcopyPath, "copy", source, sasURI, "--output-type", "json")
```

## Open Questions

1. **SAS Token Expiration**: What's the typical SAS token lifetime from FDP? Need to handle long uploads.
2. **Model Formats**: What formats should we support? (safetensors, GGUF, ONNX, etc.)
3. **Validation**: Should we validate model file integrity before upload? (checksum)
4. **Remote URL Support**: For remote URLs, should AzCopy pull directly or do we download first?
5. **FDP API Contract**: Need final API spec for upload initialization and completion endpoints.
6. **FDP API Authentication**: What Azure AD scope is required for FDP API calls?
7. **Shared Code**: Should we share Cognitive Services wrapper code between `azure.ai.models` and `azure.ai.finetune` extensions?

