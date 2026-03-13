# Azure AI Custom Training Extension — Execution Plan

> **Purpose:** Step-by-step implementation guide for a Copilot agent. Each phase builds on the previous one with clear inputs, outputs, and verification steps.
>
> **Design reference:** See `design.md` in this folder for architecture, API contracts, YAML schema, and dedup strategy.
>
> **Pattern references:** Copy patterns from existing extensions:
> - `azure.ai.finetune/` — init flow, validation, PersistentPreRunE, environment variables
> - `azure.ai.models/` — HTTP client, azcopy runner/installer, pkg/models structure
>
> **CRITICAL — Read these docs before starting any phase:**
> - `azure.ai.models/docs/development-guide.md` — **Canonical code patterns** for commands, HTTP client, URL parsing, azcopy, error handling, build config, testing. Follow these exactly.
> - `azure.ai.models/docs/code-review-guide.md` — **Review checklist** to validate every file you write (error wrapping, HTTPS enforcement, nil checks, strict URL parsing, timeout on http.Client, etc.)
> - `azure.ai.models/design/design-spec.md` — Entity/command structure reference

---

## Coding Standards (from `azure.ai.models/docs/development-guide.md`)

**Every file you write MUST follow these rules. Violations will be caught in code review.**

### Error Handling
- **Always return errors** — never ignore. `if err := fn(); err != nil { return err }`
- **Wrap errors with context** — `return fmt.Errorf("failed to create job: %w", err)`
- **Never** `_, _ = fn()` to suppress errors

### HTTP Client
- **Always set timeout** — `&http.Client{Timeout: 30 * time.Second}`
- **Always use context** — `http.NewRequestWithContext(ctx, method, url, body)`, never `http.Get()`
- **Use `bytes.NewReader(data)`** not `strings.NewReader(string(data))`

### Security
- **Enforce HTTPS** — reject `http://` endpoints: `if !strings.EqualFold(parsedURL.Scheme, "https") { return error }`
- **Reject userinfo** — `if parsedURL.User != nil { return error }`
- **Strict URL parsing** — use `==` not `>=` for path segment counts

### Cobra Commands
- **Command factory pattern** — `func newXxxCommand() *cobra.Command`
- **Persistent flags via global var** — don't pass rootFlags by value
- **`azdext.WithAccessToken(cmd.Context())`** — always wrap context
- **`defer azdClient.Close()`** — always close client

### AzCopy
- **Detect file vs directory** — check `os.Stat().IsDir()` before appending `/*`
- **Well-known paths** — `~/.azd/bin/azcopy`, `~/.azure/bin/azcopy`, `/usr/bin/azcopy` (Linux)
- **Allowed redirect hosts** — include `github.com`, `.github.com`, `.githubusercontent.com`

### Build Scripts
- **`$PSNativeCommandArgumentPassing = 'Legacy'`** — add near top of all `.ps1` scripts
- **No single quotes in ldflags** — `-X pkg.Version=$Version` not `-X 'pkg.Version=$Version'`

### Testing
- **Table-driven tests** for all parsing/validation functions
- **Update snapshots** after command changes: `UPDATE_SNAPSHOTS=true go test ./cmd -run 'TestFigSpec|TestUsage'`

---

## Phase 0 — Scaffold Extension

**Goal:** Buildable extension skeleton with `azd ai training --version` working.

### 0.1 Create extension manifest

**File:** `extension.yaml`
```yaml
id: azure.ai.customtraining
namespace: ai.training
displayName: Azure AI Custom Training (Preview)
description: Extension for Azure AI Foundry custom training jobs. (Preview)
usage: azd ai training <command> [options]
version: 0.0.1-preview
language: go
capabilities:
    - custom-commands
    - metadata
examples:
    - name: init
      description: Initialize project configuration for custom training.
      usage: azd ai training init
    - name: job create
      description: Submit a command job from YAML definition.
      usage: azd ai training job create --file job.yaml
```

### 0.2 Create Go module

**File:** `go.mod`
```
module azure.ai.customtraining

go 1.25
```

**Action:** Copy `go.mod` from `azure.ai.finetune/go.mod` and change the module name. Keep the same dependency versions. Remove finetune-specific deps (openai-go). Add `gopkg.in/yaml.v3` if not already present.

### 0.3 Create entrypoint

**File:** `main.go`
**Reference:** Copy exactly from `azure.ai.finetune/main.go`, change import path from `azure.ai.finetune/internal/cmd` to `azure.ai.customtraining/internal/cmd`.

### 0.4 Create version infrastructure

**File:** `version.txt` — content: `0.0.1-preview`

**File:** `internal/cmd/version.go`
**Reference:** Copy from `azure.ai.finetune/internal/cmd/version.go`, update package references.

### 0.5 Create root command

**File:** `internal/cmd/root.go`

```go
package cmd

import "github.com/spf13/cobra"

type rootFlagsDefinition struct {
    Debug    bool
    NoPrompt bool
}

var rootFlags rootFlagsDefinition

func NewRootCommand() *cobra.Command {
    rootCmd := &cobra.Command{
        Use:           "training <command> [options]",
        Short:         "Extension for Azure AI Foundry custom training jobs. (Preview)",
        SilenceUsage:  true,
        SilenceErrors: true,
        CompletionOptions: cobra.CompletionOptions{
            DisableDefaultCmd: true,
        },
    }

    rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
    rootCmd.PersistentFlags().BoolVar(&rootFlags.Debug, "debug", false, "Enable debug mode")
    rootCmd.PersistentFlags().BoolVar(&rootFlags.NoPrompt, "no-prompt", false,
        "accepts the default value instead of prompting, or fails if there is no default")

    rootCmd.AddCommand(newVersionCommand())
    // Phase 1: rootCmd.AddCommand(newInitCommand(rootFlags))
    // Phase 3: rootCmd.AddCommand(newJobCommand())
    rootCmd.AddCommand(newMetadataCommand())

    return rootCmd
}
```

**File:** `internal/cmd/metadata.go`
**Reference:** Copy from `azure.ai.finetune/internal/cmd/metadata.go` — this returns extension capabilities to azd.

### 0.6 Create build scripts

**File:** `ci-build.ps1`
**Reference:** Copy from `azure.ai.finetune/ci-build.ps1`. Add `$PSNativeCommandArgumentPassing = 'Legacy'` near top. Update output binary name to `azure-ai-customtraining`.

**File:** `build.ps1`, `build.sh`
**Reference:** Copy from `azure.ai.finetune/`, update binary names.

**File:** `ci-test.ps1`
**Reference:** Copy from `azure.ai.finetune/ci-test.ps1` (even if no tests yet — CI pipeline expects it).

### 0.7 Verify

```bash
cd cli/azd/extensions/azure.ai.customtraining
go mod tidy
go build ./...
# Expected: compiles with no errors
```

---

## Phase 1 — Init Flow (Layer 3 → Environment)

**Goal:** `azd ai training init` collects subscription + endpoint, stores env vars.

### 1.1 Create environment utilities

**File:** `internal/utils/environment.go`

```go
package utils

const (
    EnvAzureTenantID         = "AZURE_TENANT_ID"
    EnvAzureSubscriptionID   = "AZURE_SUBSCRIPTION_ID"
    EnvAzureResourceGroup    = "AZURE_RESOURCE_GROUP_NAME"
    EnvAzureLocation         = "AZURE_LOCATION"
    EnvAzureAccountName      = "AZURE_ACCOUNT_NAME"
    EnvAzureProjectName      = "AZURE_PROJECT_NAME"
)

// GetEnvironmentValues retrieves all env values from the current azd environment.
// Reference: azure.ai.finetune/internal/utils/environment.go lines 26-47
func GetEnvironmentValues(ctx context.Context, azdClient *azdext.Client) (map[string]string, error) {
    // ... copy pattern from finetune
}
```

### 1.2 Create validation + implicit init

**File:** `internal/cmd/validation.go`
**Reference:** Copy from `azure.ai.finetune/internal/cmd/validation.go` (lines 1-210).

**Changes from finetune:**
- Required env vars: `AZURE_TENANT_ID`, `AZURE_SUBSCRIPTION_ID`, `AZURE_LOCATION`, `AZURE_ACCOUNT_NAME`, `AZURE_PROJECT_NAME` (drop `AZURE_API_VERSION`, `AZURE_FINETUNING_ROUTE`, `AZURE_FINETUNING_TOKEN_SCOPE` — not needed)
- Same `sanitizeEnvironmentName()`, `validateOrInitEnvironment()` functions
- Same `ensureProject()` and `ensureEnvironment()` helpers

### 1.3 Create init command

**File:** `internal/cmd/init.go`
**Reference:** Copy from `azure.ai.finetune/internal/cmd/init.go`.

**Changes from finetune:**
- Remove finetune-specific env vars (`AZURE_API_VERSION`, `AZURE_FINETUNING_ROUTE`, `AZURE_FINETUNING_TOKEN_SCOPE`)
- Keep: subscription selection, project endpoint parsing, account/project name extraction, resource group resolution
- Same `setEnvValue()` helper — but **return errors** instead of ignoring them (PR review fix from models ext)

### 1.4 Wire init into root command

**File:** `internal/cmd/root.go` — uncomment `rootCmd.AddCommand(newInitCommand(rootFlags))`

### 1.5 Verify

```bash
go build ./...
# Expected: compiles
# Manual test: azd ai training init (should prompt for subscription + endpoint)
```

---

## Phase 2 — Layer 1: API Client (`pkg/`)

**Goal:** Pure REST client with typed models. Zero CLI dependencies. Future Go SDK candidate.

> **IMPORTANT:** Follow the HTTP client pattern from `azure.ai.models/docs/development-guide.md` §2 exactly.
> Validate against the code review checklist in `azure.ai.models/docs/code-review-guide.md` §2 (Security) and §3 (HTTP Client Usage).

### 2.1 Create base HTTP client

**File:** `pkg/client/client.go`
**Reference:** `azure.ai.models/docs/development-guide.md` §2 "HTTP Client Pattern" — use that exact template.

Key requirements (from development guide):
- HTTPS enforcement: `if !strings.EqualFold(parsedURL.Scheme, "https") { return error }`
- Reject userinfo: `if parsedURL.User != nil { return error }`
- Strict URL parsing: exactly 3 path segments (`api/projects/{project}`)
- Timeout: `&http.Client{Timeout: 30 * time.Second}`
- Context-aware: `http.NewRequestWithContext(ctx, ...)`
- `bytes.NewReader(data)` not `strings.NewReader(string(data))`

```go
package client

import (
    "context"
    "net/http"
    "github.com/Azure/azure-sdk-for-go/sdk/azcore"
    "github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const (
    DataPlaneScope   = "https://ai.azure.com/.default"
    ControlPlaneScope = "https://management.azure.com/.default"
    DefaultAPIVersion = "2025-09-01"
)

type ClientConfig struct {
    ProjectEndpoint string   // https://{account}.services.ai.azure.com/api/projects/{project}
    SubscriptionID  string   // For ARM compute resolution
    ResourceGroup   string   // For ARM compute resolution
    AccountName     string   // For ARM compute resolution
    APIVersion      string   // Default: 2025-09-01
}

type Client struct {
    dataPlaneBaseURL string   // https://{account}.services.ai.azure.com/api/projects/{project}
    armBaseURL       string   // https://management.azure.com/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.CognitiveServices/accounts/{account}
    credential       azcore.TokenCredential
    httpClient       *http.Client
    apiVersion       string
}

func NewClient(config ClientConfig, credential azcore.TokenCredential) (*Client, error) {
    // Validate HTTPS scheme (see azure.ai.models/internal/client/foundry_client.go HTTPS enforcement pattern)
    // Parse endpoint URL
    // Construct ARM base URL from config fields
    // Return client
}

// Internal helpers:
// - doDataPlane(ctx, method, path, body) (*http.Response, error)  — adds ai.azure.com token
// - doARM(ctx, method, path, body) (*http.Response, error)        — adds management.azure.com token
// - addAuth(ctx, req, scope) error                                — gets token, sets Authorization header
// Reference: azure.ai.models/internal/client/foundry_client.go lines 38-76, 435-445
```

### 2.2 Create job models

**File:** `pkg/models/job.go`

```go
package models

// JobBase — common fields for all job types (maps to FoundryJobBase)
type JobBase struct {
    JobType     string            `json:"jobType"`               // "Command"
    DisplayName string            `json:"displayName,omitempty"`
    Status      string            `json:"status,omitempty"`      // read-only
    ComputeID   string            `json:"computeId,omitempty"`
    IsArchived  bool              `json:"isArchived,omitempty"`
    Tags        map[string]string `json:"tags,omitempty"`
    Description string            `json:"description,omitempty"`
}

// Resource wraps JobBase in ARM resource envelope
type Resource[T any] struct {
    Properties T                 `json:"properties"`
    Tags       map[string]string `json:"tags,omitempty"`
}
```

**File:** `pkg/models/command_job.go`

```go
package models

// CommandJob — maps to FoundryCommandJob server contract
type CommandJob struct {
    JobBase
    Command              string                `json:"command"`
    EnvironmentID        string                `json:"environmentId"`
    CodeID               string                `json:"codeId,omitempty"`
    Inputs               map[string]JobInput   `json:"inputs,omitempty"`
    Outputs              map[string]JobOutput  `json:"outputs,omitempty"`
    Distribution         *Distribution         `json:"distribution,omitempty"`
    Resources            *ResourceConfig       `json:"resources,omitempty"`
    Limits               *CommandJobLimits     `json:"limits,omitempty"`
    EnvironmentVariables map[string]string     `json:"environmentVariables,omitempty"`
    QueueSettings        *QueueSettings        `json:"queueSettings,omitempty"`
}
```

**File:** `pkg/models/common.go`

```go
package models

// JobInput — discriminated union for inputs
type JobInput struct {
    JobInputType string `json:"jobInputType"`           // "uri_folder", "uri_file", "literal"
    URI          string `json:"uri,omitempty"`           // for data inputs
    Value        string `json:"value,omitempty"`         // for literal inputs
    Mode         string `json:"mode,omitempty"`          // "download", "ro_mount"
}

// JobOutput — discriminated union for outputs
type JobOutput struct {
    JobOutputType string `json:"jobOutputType"`          // "uri_folder", "uri_file"
    URI           string `json:"uri,omitempty"`
    Mode          string `json:"mode,omitempty"`          // "rw_mount", "upload"
}

// Distribution — discriminated union
type Distribution struct {
    DistributionType        string `json:"distributionType"`        // "PyTorch", "Mpi", "TensorFlow"
    ProcessCountPerInstance int    `json:"processCountPerInstance,omitempty"`
}

// ResourceConfig — maps to JobResourceConfiguration
type ResourceConfig struct {
    InstanceCount int    `json:"instanceCount,omitempty"`
    InstanceType  string `json:"instanceType,omitempty"`
    ShmSize       string `json:"shmSize,omitempty"`
}

// CommandJobLimits
type CommandJobLimits struct {
    Timeout int `json:"timeout,omitempty"`
}

// QueueSettings
type QueueSettings struct {
    JobTier  string `json:"jobTier,omitempty"`
    Priority string `json:"priority,omitempty"`
}

// PagedResponse — for list APIs
type PagedResponse[T any] struct {
    Value    []T    `json:"value"`
    NextLink string `json:"nextLink,omitempty"`
}

// ErrorResponse — API error envelope
type ErrorResponse struct {
    Error struct {
        Code    string `json:"code"`
        Message string `json:"message"`
    } `json:"error"`
}
```

**File:** `pkg/models/dataset.go`

```go
package models

// PendingUploadRequest
type PendingUploadRequest struct {
    PendingUploadType string `json:"pendingUploadType"` // "BlobReference"
}

// PendingUploadResponse
type PendingUploadResponse struct {
    BlobReference   BlobReferenceCredential `json:"blobReference"`
    PendingUploadID string                  `json:"pendingUploadId"`
    Version         string                  `json:"version,omitempty"`
}

// BlobReferenceCredential
type BlobReferenceCredential struct {
    BlobURI    string        `json:"blobUri"`
    Credential SASCredential `json:"credential"`
}

// SASCredential
type SASCredential struct {
    SASUri string `json:"sasUri"`
}

// DatasetVersion
type DatasetVersion struct {
    Name        string            `json:"name,omitempty"`
    Version     string            `json:"version,omitempty"`
    Description string            `json:"description,omitempty"`
    Tags        map[string]string `json:"tags,omitempty"`
    DataURI     string            `json:"dataUri,omitempty"`
    DataType    string            `json:"dataType,omitempty"`    // "uri_folder"
}
```

**File:** `pkg/models/compute.go`

```go
package models

type Compute struct {
    ID         string            `json:"id"`
    Name       string            `json:"name"`
    Properties ComputeProperties `json:"properties"`
}

type ComputeProperties struct {
    ComputeType       string `json:"computeType"`
    ProvisioningState string `json:"provisioningState"`
}
```

**File:** `pkg/models/artifact.go`

```go
package models

type Artifact struct {
    Path        string `json:"path"`
    ContentType string `json:"contentType,omitempty"`
    Size        int64  `json:"size,omitempty"`
}

type ArtifactContentInfo struct {
    ContentURI string `json:"contentUri"` // SAS URI
}
```

### 2.3 Create jobs API methods

**File:** `pkg/client/jobs.go`

```go
package client

// CreateOrUpdateJob — PUT .../jobs/{id}
func (c *Client) CreateOrUpdateJob(ctx context.Context, id string, job *models.Resource[models.CommandJob]) (*models.Resource[models.CommandJob], error)

// GetJob — GET .../jobs/{id}
func (c *Client) GetJob(ctx context.Context, id string) (*models.Resource[models.CommandJob], error)

// ListJobs — GET .../jobs
func (c *Client) ListJobs(ctx context.Context) (*models.PagedResponse[models.Resource[models.CommandJob]], error)

// CancelJob — POST .../jobs/{id}/cancel
func (c *Client) CancelJob(ctx context.Context, id string) error

// DeleteJob — DELETE .../jobs/{id}
func (c *Client) DeleteJob(ctx context.Context, id string) error
```

### 2.4 Create datasets API methods

**File:** `pkg/client/datasets.go`

```go
package client

// GetDatasetVersion — GET .../datasets/{name}/versions/{ver}
func (c *Client) GetDatasetVersion(ctx context.Context, name, version string) (*models.DatasetVersion, error)

// CreateOrUpdateDatasetVersion — PATCH .../datasets/{name}/versions/{ver}
func (c *Client) CreateOrUpdateDatasetVersion(ctx context.Context, name, version string, ds *models.DatasetVersion) (*models.DatasetVersion, error)

// DeleteDatasetVersion — DELETE .../datasets/{name}/versions/{ver}
func (c *Client) DeleteDatasetVersion(ctx context.Context, name, version string) error

// StartPendingUpload — POST .../datasets/{name}/versions/{ver}/startPendingUpload
func (c *Client) StartPendingUpload(ctx context.Context, name, version string) (*models.PendingUploadResponse, error)
```

### 2.5 Create computes API methods

**File:** `pkg/client/computes.go`

```go
package client

// GetComputeARM — GET https://management.azure.com/.../computes/{name} (V1: ARM)
func (c *Client) GetComputeARM(ctx context.Context, name string) (*models.Compute, error)

// GetComputeDataPlane — GET .../computes/{name} (V2: data plane, when available)
// func (c *Client) GetComputeDataPlane(ctx context.Context, name string) (*models.Compute, error)
```

### 2.6 Create artifacts API methods

**File:** `pkg/client/artifacts.go`

```go
package client

// ListArtifacts — GET .../jobs/{id}/artifacts
func (c *Client) ListArtifacts(ctx context.Context, jobID string) ([]models.Artifact, error)

// ListArtifactsInPath — GET .../jobs/{id}/artifacts/path?path={prefix}
func (c *Client) ListArtifactsInPath(ctx context.Context, jobID, path string) ([]models.Artifact, error)

// GetArtifactContent — GET .../jobs/{id}/artifacts/getcontent/{path}?tailBytes=N&offset=M
// Returns: body reader, total content length (from X-VW-Content-Length header), error
func (c *Client) GetArtifactContent(ctx context.Context, jobID, path string, offset, tailBytes *int64) (io.ReadCloser, int64, error)

// GetArtifactContentInfo — GET .../jobs/{id}/artifacts/contentinfo?path={path}
func (c *Client) GetArtifactContentInfo(ctx context.Context, jobID, path string) (*models.ArtifactContentInfo, error)

// GetBatchArtifactContentInfo — GET .../jobs/{id}/artifacts/prefix/contentinfo?path={prefix}
func (c *Client) GetBatchArtifactContentInfo(ctx context.Context, jobID, prefix string) ([]models.ArtifactContentInfo, error)
```

### 2.7 Verify

```bash
go build ./...
# Expected: compiles (no callers yet — just struct + method definitions)
```

---

## Phase 3 — Layer 2: Services (`internal/service/`)

**Goal:** Business logic: upload orchestration, compute resolution, YAML translation, log streaming.

### 3.1 Create YAML parser

**File:** `internal/utils/yaml_parser.go`

Parse AML-compatible YAML into internal Go structs. See `design.md` §9 for field mapping.

```go
package utils

// JobDefinition — parsed from YAML file (AML-compatible, snake_case)
type JobDefinition struct {
    Schema              string                       `yaml:"$schema"`
    Type                string                       `yaml:"type"`        // "command"
    Name                string                       `yaml:"name"`        // required
    DisplayName         string                       `yaml:"display_name"`
    Description         string                       `yaml:"description"`
    Command             string                       `yaml:"command"`     // required
    Environment         interface{}                  `yaml:"environment"` // string or map (see §9.3)
    Compute             string                       `yaml:"compute"`     // required
    Code                string                       `yaml:"code"`
    Inputs              map[string]InputDefinition   `yaml:"inputs"`
    Outputs             map[string]OutputDefinition  `yaml:"outputs"`
    Distribution        *DistributionDefinition      `yaml:"distribution"`
    InstanceCount       int                          `yaml:"instance_count"`
    ProcessPerNode      int                          `yaml:"process_per_node"`
    EnvironmentVariables map[string]string           `yaml:"environment_variables"`
    Resources           *ResourceDefinition          `yaml:"resources"`
    Limits              *LimitsDefinition            `yaml:"limits"`
    QueueSettings       *QueueSettingsDefinition     `yaml:"queue_settings"`
    Tags                map[string]string            `yaml:"tags"`
    Timeout             string                       `yaml:"timeout"`
    // Unsupported fields — detect and error/warn:
    Identity            interface{}                  `yaml:"identity"`
    Services            interface{}                  `yaml:"services"`
    ExperimentName      string                       `yaml:"experiment_name"`
}

// ParseJobFile reads YAML file, validates required fields, rejects unsupported fields.
func ParseJobFile(path string) (*JobDefinition, error)

// ValidateJobDefinition checks required fields and unsupported field usage.
func ValidateJobDefinition(job *JobDefinition) error

// ResolveEnvironment extracts container image URI from environment field.
// Supports: plain string, map with "image" key. Rejects: conda_file, build context.
// See design.md §9.3 for full rules.
func ResolveEnvironment(env interface{}) (string, error)
```

### 3.2 Create upload service

**File:** `internal/service/upload_service.go`

Handles code + input upload with dedup. See `design.md` §8 for full flow.

```go
package service

type UploadService struct {
    client       *client.Client
    azcopyRunner *azcopy.Runner
}

// UploadResult contains the dataset reference after upload
type UploadResult struct {
    DatasetName    string
    DatasetVersion string
    WasDeduped     bool
}

// UploadDirectory uploads a local directory as a dataset version.
// Uses hash-as-version dedup with sentinel blob verification.
// See design.md §8.1 for the complete flow.
//
// Steps:
// 1. Compute SHA256 of directory → full (64 char) + truncated (49 char)
// 2. GET dataset version by hash49 → check if exists
// 3. If exists → startPendingUpload → GET .content_hash sentinel → compare full SHA256
//    - Match → skip upload (deduped)
//    - Mismatch → collision fallback to job-scoped naming
//    - Missing sentinel → zombie, delete and re-upload
// 4. If not exists → PATCH create → startPendingUpload → azcopy → write sentinel → PATCH confirm
func (s *UploadService) UploadDirectory(ctx context.Context, localPath, datasetName, jobName string) (*UploadResult, error)

// computeDirectoryHash returns both full (64-char) and truncated (49-char) SHA256.
// See design.md §8.6 for deterministic hash algorithm.
func computeDirectoryHash(dir string) (fullHash, truncHash string, err error)

// writeSentinel writes the .content_hash blob containing the full SHA256.
// Uses the SAS URI from startPendingUpload (container-level read/write/list).
func (s *UploadService) writeSentinel(sasBaseURI, fullHash string) error

// readSentinel reads the .content_hash blob. Returns full hash or empty string if not found.
func (s *UploadService) readSentinel(sasBaseURI string) (string, error)
```

### 3.3 Create compute resolver

**File:** `internal/service/resolve_service.go`

Pluggable interface for ARM → data plane migration. See `design.md` §3.4.

```go
package service

// ComputeResolver is an interface for pluggable compute resolution.
// V1: ARMComputeResolver (management.azure.com)
// V2: DataPlaneComputeResolver (ai.azure.com, when available)
type ComputeResolver interface {
    ResolveCompute(ctx context.Context, compute string) (string, error)
}

type ARMComputeResolver struct {
    client *client.Client
}

func (r *ARMComputeResolver) ResolveCompute(ctx context.Context, compute string) (string, error) {
    // If starts with /subscriptions/ → return as-is (full ARM ID)
    // Otherwise → client.GetComputeARM(ctx, compute) → return response.ID
    // On 401/403 → return error with guidance to provide full ARM ID
}
```

### 3.4 Create job service (orchestrator)

**File:** `internal/service/job_service.go`

Orchestrates the full job creation flow. See `design.md` §3 flow chart.

```go
package service

type JobService struct {
    client          *client.Client
    uploadService   *UploadService
    computeResolver ComputeResolver
}

type JobConfig struct {
    Definition *utils.JobDefinition  // Parsed from YAML
    FilePath   string                // Original YAML file path (for resolving relative code/input paths)
}

// CreateJob orchestrates: upload code → upload inputs → resolve compute → build payload → submit
// See design.md §3 for the 6-step flow.
func (s *JobService) CreateJob(ctx context.Context, config *JobConfig) (*models.Resource[models.CommandJob], error) {
    // 1. Resolve environment (§9.3) → environmentId string
    // 2. Upload code if local path → codeId
    // 3. Upload each input if local path → input URIs
    // 4. Resolve compute → computeId
    // 5. Build Foundry API payload (translate YAML → FoundryCommandJob, §9.7)
    // 6. PUT .../jobs/{name} → return job
}

// translateToPayload converts parsed YAML + resolved IDs into Foundry API payload.
// Handles: snake_case → camelCase, type → jobType, etc. See design.md §9.2.
func (s *JobService) translateToPayload(def *utils.JobDefinition, codeID, envID, computeID string, inputMap map[string]string) *models.Resource[models.CommandJob]
```

### 3.5 Create stream service

**File:** `internal/service/stream_service.go`

Polling-based log streaming. See `design.md` §6.

```go
package service

type StreamService struct {
    client *client.Client
}

// StreamLogs discovers log files, polls with offset, writes to writer.
// See design.md §6.2 for sequence diagram and §6.3 for polling parameters.
//
// Parameters:
// - Initial tailBytes: 8192
// - Poll interval: 1-2s active, up to 5s idle (exponential backoff)
// - Job status check: every 10 polls
// - Stop: when job reaches terminal status
func (s *StreamService) StreamLogs(ctx context.Context, jobID string, writer io.Writer) error
```

### 3.6 Create download service

**File:** `internal/service/download_service.go`

Download artifacts via SAS URIs. See `design.md` §7.

```go
package service

type DownloadService struct {
    client       *client.Client
    azcopyRunner *azcopy.Runner
}

// DownloadArtifacts lists artifacts, gets SAS URIs, downloads via azcopy.
// See design.md §7.2 for sequence.
func (s *DownloadService) DownloadArtifacts(ctx context.Context, jobID, outputName, localPath string) error
```

### 3.7 Verify

```bash
go build ./...
# Expected: compiles (services have method signatures, may need stub implementations)
```

---

## Phase 4 — AzCopy Integration

**Goal:** Working azcopy runner and installer.

> **Reference:** Follow AzCopy patterns from `azure.ai.models/docs/development-guide.md` §5 (AzCopy Integration).
> Validate against `azure.ai.models/docs/code-review-guide.md` §2 (Security — redirect host validation, Content-Length caps).

### 4.1 Copy azcopy runner

**File:** `internal/azcopy/runner.go`
**Reference:** Copy from `azure.ai.models/internal/azcopy/runner.go`. No changes needed.

### 4.2 Copy azcopy installer

**File:** `internal/azcopy/installer.go`
**Reference:** Copy from `azure.ai.models/internal/azcopy/installer.go`.

**Important:** Include the allowed redirect hosts fix:
- `github.com`, `.github.com`, `.githubusercontent.com` in `allowedHosts`

### 4.3 Verify

```bash
go build ./...
```

---

## Phase 5 — Layer 3: CLI Commands (`internal/cmd/`)

**Goal:** All 7 CLI commands wired up and working.

> **IMPORTANT:** Follow the command pattern from `azure.ai.models/docs/development-guide.md` §1 exactly:
> 1. Create `azdClient` via `azdext.NewAzdClient()`
> 2. `defer azdClient.Close()`
> 3. Wrap context: `ctx := azdext.WithAccessToken(cmd.Context())`
> 4. Create credential via `azidentity.NewAzureDeveloperCLICredential()`
> 5. Create API client, execute, format output
> 6. **Always handle errors from `utils.PrintObject()`**

### 5.1 Create job command group with PersistentPreRunE

**File:** `internal/cmd/job.go`

```go
package cmd

// newJobCommand creates the "job" command group with:
// - PersistentPreRunE calling validateOrInitEnvironment()
// - PersistentFlags: --subscription (-s), --project-endpoint (-e)
// - Subcommands: create, get, list, cancel, delete, stream, download
//
// Reference: azure.ai.finetune/internal/cmd/operations.go lines 31-56
func newJobCommand() *cobra.Command
```

### 5.2 Create job create command

**File:** `internal/cmd/job_create.go`

```go
// Flags: --file/-f (required)
// Flow:
// 1. Parse YAML via utils.ParseJobFile()
// 2. Create client, services
// 3. Call jobService.CreateJob()
// 4. Display result table
//
// UX: Show progress spinners for each step (upload code, upload inputs, resolve compute, submit)
// Reference: azure.ai.finetune/internal/cmd/operations.go newOperationSubmitCommand() for spinner pattern
```

### 5.3 Create job get command

**File:** `internal/cmd/job_get.go`

```go
// Flags: --name (required), --output (json|table, default table)
// Flow: client.GetJob() → format output
// Reference: azure.ai.finetune/internal/cmd/operations.go newOperationShowCommand()
```

### 5.4 Create job list command

**File:** `internal/cmd/job_list.go`

```go
// Flags: --top (optional), --output (json|table)
// Flow: client.ListJobs() → format as table
```

### 5.5 Create job cancel command

**File:** `internal/cmd/job_cancel.go`

```go
// Flags: --name (required)
// Flow: client.CancelJob() → display confirmation
```

### 5.6 Create job delete command

**File:** `internal/cmd/job_delete.go`

```go
// Flags: --name (required)
// Flow: Prompt "are you sure?" → client.DeleteJob()
```

### 5.7 Create job stream command

**File:** `internal/cmd/job_stream.go`

```go
// Flags: --name (required)
// Flow: streamService.StreamLogs(ctx, name, os.Stdout)
```

### 5.8 Create job download command

**File:** `internal/cmd/job_download.go`

```go
// Flags: --name (required), --output-name (optional), --path (optional, default ./)
// Flow: downloadService.DownloadArtifacts()
```

### 5.9 Create output utilities

**File:** `internal/utils/output.go`
**Reference:** Copy from `azure.ai.models/internal/utils/output.go`. Adapt table formatting for job fields.

### 5.10 Wire job command into root

**File:** `internal/cmd/root.go` — uncomment `rootCmd.AddCommand(newJobCommand())`

### 5.11 Verify

```bash
go build ./...
# Full compile check
```

---

## Phase 6 — Examples & Documentation

### 6.1 Create example YAML files

**File:** `examples/simple-command-job.yaml`

```yaml
$schema: https://azuremlschemas.azureedge.net/latest/commandJob.schema.json
type: command
name: simple-train
display_name: Simple Training Job
command: python train.py --epochs 10
environment: mcr.microsoft.com/azureml/openmpi4.1.0-cuda11.8-cudnn8-ubuntu22.04:latest
compute: gpu-cluster
code: ./src
tags:
  example: simple
```

**File:** `examples/distributed-pytorch-job.yaml`

```yaml
$schema: https://azuremlschemas.azureedge.net/latest/commandJob.schema.json
type: command
name: distributed-train
display_name: Distributed PyTorch Training
command: python train.py --data ${{inputs.training_data}} --lr ${{inputs.learning_rate}}
environment: mcr.microsoft.com/azureml/openmpi4.1.0-cuda11.8-cudnn8-ubuntu22.04:latest
compute: gpu-cluster
code: ./src

inputs:
  training_data:
    type: uri_folder
    path: ./data/train
    mode: download
  learning_rate: 0.001

outputs:
  model_output:
    type: uri_folder

distribution:
  type: pytorch
  process_count_per_instance: 4

resources:
  instance_count: 2

environment_variables:
  NCCL_DEBUG: INFO
```

### 6.2 Create documentation

**File:** `docs/installation-guide.md`
**Reference:** Copy structure from `azure.ai.models/docs/installation-guide.md`, update for custom training.

**File:** `docs/development-guide.md`
**Reference:** Copy structure from `azure.ai.models/docs/development-guide.md`, update build commands.

### 6.3 Create CHANGELOG and README

**File:** `CHANGELOG.md`
```markdown
# Changelog

## 0.0.1-preview
- Initial preview release
- Job create, get, list, cancel, delete commands
- Log streaming via artifact polling
- Job artifact download
- Code and input upload with hash-based dedup
- AML-compatible YAML schema with translation layer
```

**File:** `README.md` — Brief description, installation, usage examples.

### 6.3 Create development guides for new extension

**Reference:** Copy structure from `azure.ai.models/docs/` and adapt to custom training.

**File:** `docs/development-guide.md`
- Copy from `azure.ai.models/docs/development-guide.md` as base template
- Update project structure to match custom training layout (3-layer: pkg/, internal/service/, internal/cmd/)
- Keep ALL coding patterns identical (HTTP client, error handling, URL parsing, azcopy, build config)
- Add custom training–specific sections: YAML parsing, dedup logic, ComputeResolver
- Remove models-specific sections (catalog, deployments, etc.)

**File:** `docs/code-review-guide.md`
- Copy from `azure.ai.models/docs/code-review-guide.md` as base template
- Keep ALL review categories (error handling, security, HTTP, validation, Cobra, build, testing, UX)
- Add custom training–specific checks: YAML field validation, hash computation, sentinel blob operations
- Remove models-specific checks (catalog payload, publisher flag, etc.)

---

## Phase 7 — CI/CD & Release Pipeline

### 7.1 Create release pipeline

**File:** `cli/azd/eng/pipelines/release-ext-azure-ai-customtraining.yml`
**Reference:** Copy from `cli/azd/eng/pipelines/release-ext-azure-ai-models.yml`.

**Changes:**
- Update extension path to `azure.ai.customtraining`
- Set `SkipTests: true` (initially, until tests exist)
- Update binary names

### 7.2 Update CODEOWNERS

**File:** `cli/azd/.github/CODEOWNERS`
**Action:** Add line: `/cli/azd/extensions/azure.ai.customtraining/ @AzureAJ @AJamiable`
(Match the same owners as finetune/models extensions, or adjust as needed)

### 7.3 Update registry

**File:** `cli/azd/extensions/registry.json`
**Action:** Add entry for `azure.ai.customtraining` extension.

### 7.4 Update test snapshots

```bash
cd cli/azd
$env:UPDATE_SNAPSHOTS="true"
go test ./cmd -run 'TestFigSpec|TestUsage'
git add cmd/testdata/TestUsage-azd-ai-training.snap
```

---

## Implementation Order & Dependencies

```
Phase 0 (Scaffold)        ← no dependencies, start here
    │
Phase 1 (Init Flow)       ← depends on Phase 0
    │
Phase 2 (Layer 1: Client) ← depends on Phase 0 (parallel with Phase 1)
    │
Phase 4 (AzCopy)          ← depends on Phase 0 (parallel with Phase 1, 2)
    │
Phase 3 (Layer 2: Services) ← depends on Phase 2 + Phase 4
    │
Phase 5 (Layer 3: CLI)    ← depends on Phase 1 + Phase 3
    │
Phase 6 (Examples/Docs)   ← depends on Phase 5
    │
Phase 7 (CI/CD)           ← depends on Phase 0 (can run in parallel with Phase 5)
```

**Parallelizable:** Phases 1, 2, and 4 can all be done simultaneously after Phase 0.

---

## Verification Checklist

After all phases complete:

- [ ] `go build ./...` compiles with no errors
- [ ] `go vet ./...` passes
- [ ] `azd ai training --version` prints version
- [ ] `azd ai training init` prompts for subscription + endpoint, stores env vars
- [ ] `azd ai training job create --file examples/simple-command-job.yaml` succeeds (needs live endpoint)
- [ ] `azd ai training job create --file examples/simple-command-job.yaml -s SUB -e ENDPOINT` works without prior init
- [ ] `azd ai training job list` shows jobs
- [ ] `azd ai training job get --name JOB` shows job details
- [ ] `azd ai training job stream --name JOB` streams logs
- [ ] `azd ai training job cancel --name JOB` cancels
- [ ] `azd ai training job download --name JOB --path ./out` downloads artifacts
- [ ] `azd ai training job delete --name JOB` deletes with confirmation
- [ ] CI build pipeline produces cross-platform binaries
- [ ] Test snapshots updated and pass

---

## Quality Gate — Code Review Checklist

**Before submitting the PR, run through every file using the code review guide.**

Reference: `azure.ai.models/docs/code-review-guide.md`

### Mandatory checks for every Go file:

| # | Category | What to verify |
|---|----------|---------------|
| 1 | Error handling | Every `err` is checked, wrapped with `%w`, never silenced |
| 2 | Security | HTTPS enforced, no userinfo in URLs, redirect hosts restricted |
| 3 | HTTP client | Timeout set, context-aware requests, `bytes.NewReader` not `strings.NewReader` |
| 4 | Input validation | Strict URL parsing, path segment count `==` not `>=`, required fields validated |
| 5 | Memory/perf | No unbounded reads, io.LimitReader for HTTP responses, closers deferred |
| 6 | Cobra commands | Factory pattern, azdClient closed, context wrapped, flags registered correctly |
| 7 | Build scripts | `$PSNativeCommandArgumentPassing = 'Legacy'`, no single-quote ldflags |
| 8 | Documentation | README up to date, CHANGELOG matches version, examples valid |
| 9 | Testing | Table-driven tests for all parsing, snapshots updated |
| 10 | UX | Consistent output format, spinners for long ops, confirmation prompts for destructive actions |

### Custom training–specific checks:

| # | What to verify |
|---|---------------|
| 1 | YAML parser rejects `conda_file`, `build.context`, `azureml:` environment refs with clear error message |
| 2 | Hash computation uses SHA-256 (never MD5/SHA-1), truncated to 49 chars for version |
| 3 | `.content_hash` sentinel contains full 64-char SHA-256, written only after azcopy completes |
| 4 | ComputeResolver interface is used (not direct ARM calls from CLI layer) |
| 5 | Job ID in PUT URL path matches YAML `name` field |
| 6 | snake_case → camelCase translation covers all YAML fields |
| 7 | Dedup fallback to `code-{jobName}` works when hash collision detected |
