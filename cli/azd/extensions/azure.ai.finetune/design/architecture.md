# Azure AI Fine-Tune Extension - Low Level Design

## 1. Overview

This document describes the proposed three-layer architecture for the Azure AI Fine-Tune CLI extension. The design emphasizes vendor abstraction, separation of concerns, and multi-vendor extensibility.

### Key Objectives

- **Phase 1**: Support OpenAI fine-tuning and Azure Cognitive Services model deployment
- **Future Phases**: Onboard additional vendors without refactoring CLI or service layer
- **Testability**: Enable unit testing of business logic independently from SDK implementations
- **Maintainability**: Clear boundaries between layers for easier debugging and feature development

---

## 2. Architecture Overview

### Complete Layered Architecture with Entities

```
┌──────────────────────────────────────────────────────────────────┐
│                    DOMAIN MODELS / ENTITIES                      │
│                  (pkg/models/ - Shared Foundation)               │
│                                                                   │
│  ├─ FineTuningJob      ← All layers read/write these            │
│  ├─ Deployment                                                   │
│  ├─ BaseModel                                                    │
│  ├─ StandardError                                                │
│  ├─ CreateFineTuningRequest                                      │
│  └─ DeploymentRequest                                            │
│                                                                   │
│  (No SDK imports! Pure data structures)                          │
└──────────────────────────────────────────────────────────────────┘
    ↑                                    ↑                     ↑
    │ (imports)                          │ (imports)          │ (imports)
    │                                    │                    │
┌───┴──────────────────┐  ┌──────────────┴──────┐  ┌─────────┴───────────┐
│  CLI Layer           │  │  Service Layer      │  │  Provider Layer     │
│  (cmd/)              │  │  (services/)        │  │  (providers/)       │
│                      │  │                     │  │                     │
│ Uses:                │  │ Uses:               │  │ Uses:               │
│ - FineTuningJob ✅   │  │ - FineTuningJob ✅  │  │ - FineTuningJob ✅  │
│ - Deployment ✅      │  │ - Deployment ✅     │  │ - Deployment ✅     │
│ - Request DTOs ✅    │  │ - Request DTOs ✅   │  │ - Request DTOs ✅   │
│                      │  │ - StandardError ✅  │  │ - StandardError ✅  │
│ Does:                │  │                     │  │                     │
│ - Parse input        │  │ Does:               │  │ Does:               │
│ - Format output      │  │ - Validate          │  │ - IMPORT SDK ⚠️      │
│ - Call Service ↓     │  │ - Orchestrate       │  │ - Convert domain →  │
│                      │  │ - Call Provider ↓   │  │   SDK models        │
│                      │  │ - State management  │  │ - Call SDK          │
│                      │  │ - Error transform   │  │ - Convert SDK →     │
│                      │  │                     │  │   domain models     │
└──────────────────────┘  └─────────────────────┘  └─────────────────────┘
                                                             ↓
                                ┌────────────────────────────┴─────────┐
                                │   SDK Layer (External)                │
                                │                                       │
                                │  - OpenAI SDK                         │
                                │  - Azure Cognitive Services SDK       │
                                │  - Future Vendor SDKs                 │
                                └───────────────────────────────────────┘
```

---

## 3. Layer Responsibilities

### 3.1 Domain Models Layer (pkg/models/)

**Responsibility**: Define vendor-agnostic data structures used across all layers.

**Characteristics**:
- Zero SDK imports
- Pure data structures (Go structs)
- Single source of truth for data contracts
- Includes request/response DTOs and error types

**What it Contains**:
- `FineTuningJob` - represents a fine-tuning job
- `Deployment` - represents a model deployment
- `CreateFineTuningRequest` - request to create a job
- `Hyperparameters` - training hyperparameters
- `ErrorDetail` - standardized error response
- `JobStatus`, `DeploymentStatus` - enums

**Who Uses It**: All layers (CLI, Service, Provider)

**Example Structure**:
```go
package models

type FineTuningJob struct {
    ID              string
    Status          JobStatus
    BaseModel       string
    FineTunedModel  string
    CreatedAt       time.Time
    CompletedAt     *time.Time
    VendorJobID     string                   // Vendor-specific ID
    VendorMetadata  map[string]interface{}   // Vendor-specific details
    ErrorDetails    *ErrorDetail
}

type JobStatus string
const (
    StatusPending   JobStatus = "pending"
    StatusTraining  JobStatus = "training"
    StatusSucceeded JobStatus = "succeeded"
    StatusFailed    JobStatus = "failed"
)
```

---

### 3.2 CLI Layer (cmd/)

**Responsibility**: Handle command parsing, user input validation, output formatting, and orchestration of user interactions.

**Characteristics**:
- Does NOT import vendor SDKs
- Does NOT contain business logic
- Calls only the Service layer
- Responsible for presentation (table formatting, JSON output, etc.)

**What it Does**:
- Parse command-line arguments and flags
- Validate user input format and constraints
- Call service methods to perform business logic
- Format responses for terminal output (tables, JSON, etc.)
- Handle error presentation to users
- Support multiple output formats (human-readable, JSON)

**What it Does NOT Do**:
- Call SDK methods directly
- Implement business logic (validation, state management)
- Transform between vendor models
- Manage long-running operations (polling is in Service layer)

**Imports**:
```go
import (
    "azure.ai.finetune/pkg/models"
    "azure.ai.finetune/internal/services"
    "github.com/spf13/cobra" // CLI framework
)
```

**Example**:
```go
func newOperationSubmitCommand(svc services.FineTuningService) *cobra.Command {
    return &cobra.Command{
        Use:   "submit",
        Short: "Submit fine-tuning job.",
        RunE: func(cmd *cobra.Command, args []string) error {
            // 1. Parse input
            req := &models.CreateFineTuningRequest{
                BaseModel:      parseBaseModel(args),
                TrainingDataID: parseTrainingFile(args),
            }
            
            // 2. Call service (business logic)
            job, err := svc.CreateFineTuningJob(cmd.Context(), req)
            if err != nil {
                return err
            }
            
            // 3. Format output
            printFineTuningJobTable(job)
            return nil
        },
    }
}
```

---

### 3.3 Service Layer (internal/services/)

**Responsibility**: Implement business logic, orchestration, state management, and error standardization.

**Characteristics**:
- Does NOT import vendor SDKs
- Imports Provider interface (abstraction, not concrete implementations)
- Central location for business rules
- Handles cross-vendor concerns
- Manages job lifecycle and state persistence

**What it Does**:
- Validate business constraints (e.g., model limits, file sizes)
- Orchestrate multi-step operations
- Call provider methods to perform vendor-specific operations
- Transform vendor-specific errors to standardized `ErrorDetail`
- Manage job state persistence (local storage)
- Implement polling logic for long-running operations
- Handle retries and resilience patterns
- Manage job lifecycle state transitions

**What it Does NOT Do**:
- Import SDK packages
- Format output for CLI
- Parse command-line arguments
- Call SDK methods directly

**Key Interfaces**:
```go
type FineTuningProvider interface {
    CreateFineTuningJob(ctx context.Context, req *CreateFineTuningRequest) (*FineTuningJob, error)
    GetFineTuningStatus(ctx context.Context, jobID string) (*FineTuningJob, error)
    ListFineTuningJobs(ctx context.Context) ([]*FineTuningJob, error)
}

type StateStore interface {
    SaveJob(job *FineTuningJob) error
    GetJob(id string) (*FineTuningJob, error)
    ListJobs() ([]*FineTuningJob, error)
    UpdateJobStatus(id string, status JobStatus) error
}
```

**Imports**:
```go
import (
    "azure.ai.finetune/pkg/models"
    "azure.ai.finetune/internal/providers"
    "context"
    "fmt"
)
```

**Example**:
```go
type FineTuningService struct {
    provider   providers.FineTuningProvider
    stateStore StateStore
}

func (s *FineTuningService) CreateFineTuningJob(
    ctx context.Context,
    req *models.CreateFineTuningRequest,
) (*models.FineTuningJob, error) {
    // Business logic: validation
    if err := s.validateRequest(req); err != nil {
        return nil, fmt.Errorf("invalid request: %w", err)
    }
    
    // Call abstracted provider (could be OpenAI, Azure, etc.)
    job, err := s.provider.CreateFineTuningJob(ctx, req)
    if err != nil {
        // Transform vendor error to standard error
        return nil, s.transformError(err)
    }
    
    // State management: persist job
    s.stateStore.SaveJob(job)
    
    return job, nil
}
```

---

### 3.4 Provider Layer (internal/providers/)

**Responsibility**: Adapter pattern implementation. Bridge between domain models and vendor SDKs.

**Characteristics**:
- **ONLY layer that imports vendor SDKs**
- Implements vendor-agnostic provider interface
- Converts between domain models and SDK models
- Handles vendor-specific error semantics
- No business logic (pure technical adaptation)

**What it Does**:
- Import and instantiate vendor SDKs
- Convert domain models → SDK-specific request formats
- Call SDK methods
- Convert SDK response models → domain models
- Handle SDK-specific error codes and map to standard errors
- Manage SDK client lifecycle (initialization, auth)

**What it Does NOT Do**:
- Implement business logic
- Manage state or persistence
- Format output for CLI
- Make decisions about retry logic or state transitions

**Provider Interface** (in `internal/providers/interface.go` - No SDK imports!):
```go
package providers

import (
    "context"
    "azure.ai.finetune/pkg/models"
)

type FineTuningProvider interface {
    CreateFineTuningJob(ctx context.Context, req *models.CreateFineTuningRequest) (*models.FineTuningJob, error)
    GetFineTuningStatus(ctx context.Context, jobID string) (*models.FineTuningJob, error)
    ListFineTuningJobs(ctx context.Context) ([]*models.FineTuningJob, error)
}

type ModelDeploymentProvider interface {
    DeployModel(ctx context.Context, req *models.DeploymentRequest) (*models.Deployment, error)
    GetDeploymentStatus(ctx context.Context, deploymentID string) (*models.Deployment, error)
    DeleteDeployment(ctx context.Context, deploymentID string) error
}
```

**OpenAI Provider Example** (imports OpenAI SDK):
```go
package openai

import (
    "context"
    openaisdk "github.com/openai/openai-go"  // ⚠️ SDK import!
    "azure.ai.finetune/pkg/models"
)

type OpenAIProvider struct {
    client *openaisdk.Client
}

func (p *OpenAIProvider) CreateFineTuningJob(
    ctx context.Context,
    req *models.CreateFineTuningRequest,
) (*models.FineTuningJob, error) {
    // 1. Convert domain → SDK format
    sdkReq := &openaisdk.FineTuningJobCreateParams{
        Model:        openaisdk.F(req.BaseModel),
        TrainingFile: openaisdk.F(req.TrainingDataID),
    }
    
    // 2. Call SDK
    sdkJob, err := p.client.FineTuning.Jobs.Create(ctx, sdkReq)
    if err != nil {
        return nil, err
    }
    
    // 3. Convert SDK response → domain format
    return p.sdkJobToDomain(sdkJob), nil
}

// Helper: SDK model → domain model
func (p *OpenAIProvider) sdkJobToDomain(sdkJob *openaisdk.FineTuningJob) *models.FineTuningJob {
    return &models.FineTuningJob{
        ID:             sdkJob.ID,
        Status:         p.mapStatus(sdkJob.Status),
        BaseModel:      sdkJob.Model,
        FineTunedModel: sdkJob.FineTunedModel,
        VendorJobID:    sdkJob.ID,
        VendorMetadata: p.extractMetadata(sdkJob),
    }
}
```

**Azure Provider Example** (imports Azure SDK):
```go
package azure

import (
    "context"
    cognitiveservices "github.com/Azure/azure-sdk-for-go/sdk/cognitiveservices"  // Different SDK!
    "azure.ai.finetune/pkg/models"
)

type AzureProvider struct {
    client *cognitiveservices.Client
}

func (p *AzureProvider) CreateFineTuningJob(
    ctx context.Context,
    req *models.CreateFineTuningRequest,
) (*models.FineTuningJob, error) {
    // 1. Convert domain → Azure SDK format
    sdkReq := p.domainRequestToAzureSDK(req)
    
    // 2. Call Azure SDK (different from OpenAI!)
    sdkJob, err := p.client.CreateFineTuningJob(ctx, sdkReq)
    if err != nil {
        return nil, err
    }
    
    // 3. Convert Azure SDK response → SAME domain model as OpenAI!
    return p.azureJobToDomain(sdkJob), nil
}
```

---

## 4. Import Dependencies

### Valid Imports by Layer

```
pkg/models/
    ↑                ↑                    ↑
    │ imports        │ imports            │ imports
    │ (only)         │ (only)             │ (only)
    │                │                    │
cmd/                 services/            providers/
├─ pkg/models        ├─ pkg/models        ├─ pkg/models
├─ services/         ├─ providers/        ├─ vendor SDKs ✅
├─ pkg/config        │  interface only    └─ Azure SDK
└─ github.com/       │                       OpenAI SDK
   spf13/cobra       └─ github.com/          etc.
                        context
```

### Strict Rules

| Layer | CAN Import | CANNOT Import |
|-------|---|---|
| **cmd/** | `pkg/models`, `services/`, `pkg/config`, `github.com/spf13/cobra` | Any SDK (openai, azure), `providers/` concrete impl |
| **services/** | `pkg/models`, `providers/` (interface only), `context` | Any SDK, cmd, concrete provider implementations |
| **providers/** | `pkg/models`, vendor SDKs ✅ | cmd, services, other providers |
| **pkg/models/** | Nothing | Anything |

---

## 5. Directory Structure

```
azure.ai.finetune/
├── internal/
│   ├── cmd/                          # CLI Layer
│   │   ├── root.go                   # Root command
│   │   ├── operations.go             # Finetune operations (submit, list, etc.)
│   │   ├── deployment.go             # Deployment operations
│   │   └── output.go                 # Output formatting (tables, JSON)
│   │
│   ├── services/                     # Service Layer
│   │   ├── finetune_service.go       # FineTuningService implementation
│   │   ├── deployment_service.go     # DeploymentService implementation
│   │   ├── state_store.go            # State persistence interface
│   │   └── error_transform.go        # Error transformation logic
│   │
│   ├── providers/                    # Provider Layer
│   │   ├── interface.go              # FineTuningProvider, ModelDeploymentProvider interfaces
│   │   │                              # (NO SDK imports here!)
│   │   ├── openai/
│   │   │   ├── provider.go           # OpenAI implementation (SDK import!)
│   │   │   └── converters.go         # Domain ↔ OpenAI SDK conversion
│   │   └── azure/
│   │       ├── provider.go           # Azure implementation (SDK import!)
│   │       └── converters.go         # Domain ↔ Azure SDK conversion
│   │
│   ├── project/                      # Project utilities
│   ├── tools/                        # Misc utilities
│   └── fine_tuning_yaml/             # YAML parsing
│
├── pkg/
│   └── models/                       # Domain Models (Shared)
│       ├── finetune.go               # FineTuningJob, JobStatus, etc.
│       ├── deployment.go             # Deployment, DeploymentStatus, etc.
│       ├── requests.go               # Request DTOs (Create, Update, etc.)
│       ├── errors.go                 # ErrorDetail, StandardError types
│       └── base_model.go             # BaseModel, ModelInfo, etc.
│
├── design/
│   ├── architecture.md               # This file
│   └── sequence_diagrams.md          # Interaction flows (future)
│
├── main.go
├── go.mod
└── README.md
```

---

## 6. Data Flow Examples

### 6.1 Create Fine-Tuning Job Flow

```
User Command:
  azd finetune jobs submit -f config.yaml

  ↓

CLI Layer (cmd/operations.go):
  1. Parse arguments
  2. Read config.yaml → CreateFineTuningRequest {BaseModel, TrainingDataID}
  3. Call service.CreateFineTuningJob(ctx, req)
  
  ↓
  
Service Layer (services/finetune_service.go):
  1. Validate request (model exists, data size valid, etc.)
  2. Get provider from config (OpenAI vs Azure)
  3. Call provider.CreateFineTuningJob(ctx, req)
  4. Transform any errors
  5. Persist job to state store
  6. Return FineTuningJob
  
  ↓
  
Provider Layer (providers/openai/provider.go):
  1. Convert CreateFineTuningRequest → OpenAI SDK format
  2. Call: client.FineTuning.Jobs.Create(ctx, sdkReq)
  3. Convert OpenAI response → FineTuningJob domain model
  4. Return FineTuningJob
  
  ↓
  
Service Layer:
  Gets FineTuningJob back
  Saves to state store
  Returns to CLI
  
  ↓
  
CLI Layer:
  Receives FineTuningJob
  Formats for output (table or JSON)
  Prints: "Job created: ftjob-abc123"
  Exit
```

### 6.2 Switch Provider (OpenAI → Azure)

```
Code Change Needed:
  ✅ internal/providers/azure/provider.go (new file)
  ✅ internal/config/config.yaml (provider: azure)
  ❌ internal/services/finetune_service.go (NO changes!)
  ❌ cmd/operations.go (NO changes!)
  
Why?
  Service layer uses FineTuningProvider interface (abstracted)
  CLI doesn't know about providers at all
  Only provider layer imports SDK
```

### 6.3 Error Flow

```
User submits invalid data:
  azd finetune jobs submit -f config.yaml

  ↓
  
CLI Layer:
  Creates CreateFineTuningRequest from YAML
  
  ↓
  
Service Layer:
  Validates: model not supported
  Returns: &ErrorDetail{
    Code: "INVALID_MODEL",
    Message: "Model 'gpt-5' not supported",
    Retryable: false,
  }
  
  ↓
  
CLI Layer:
  Receives ErrorDetail
  Prints user-friendly message
  Exit with error code
```

---

## 7. Benefits of This Architecture

### 7.1 Vendor Abstraction
- **Add new vendor**: Create `internal/providers/{vendor}/provider.go`
- **CLI changes**: None
- **Service changes**: None
- **Dependencies**: Only provider layer implementation

### 7.2 Testability
- **Test business logic**: Mock provider at interface level
- **Test CLI**: Mock service
- **Test provider**: Use SDK directly (integration tests)

### 7.3 Separation of Concerns
- **CLI**: What to show and how
- **Service**: What to do and how to do it (business rules)
- **Provider**: How to talk to vendor SDKs

### 7.4 Maintainability
- **Vendor SDK updates**: Changes only in provider layer
- **Business logic changes**: Changes in service layer
- **Output format changes**: Changes in CLI layer

### 7.5 Future Flexibility
- **Support multiple vendors simultaneously**: Multiple provider implementations
- **Provider selection at runtime**: Config-driven
- **A/B testing different implementations**: Easy switching

---

## 8. Design Patterns Used

### 8.1 Strategy Pattern
**Where**: Provider interface
```
FineTuningProvider interface (strategy)
├── OpenAIProvider (concrete strategy)
├── AzureProvider (concrete strategy)
└── AnthropicProvider (future strategy)

Service uses any strategy without knowing which
```

### 8.2 Adapter Pattern
**Where**: Provider implementations
- Convert domain models ↔ SDK models
- Standardize error responses

### 8.3 Dependency Injection
**Where**: Service receives provider via constructor
```go
type FineTuningService struct {
    provider providers.FineTuningProvider  // Injected
}
```

### 8.4 Repository Pattern
**Where**: State persistence
```go
type StateStore interface {
    SaveJob(job *FineTuningJob) error
    GetJob(id string) (*FineTuningJob, error)
}
```

---

## 9. Phase 1 Implementation Checklist

- [ ] Create `pkg/models/` with all domain models
- [ ] Create `internal/services/finetune_service.go` with interfaces
- [ ] Create `internal/services/deployment_service.go` with interfaces
- [ ] Create `internal/providers/interface.go` with provider interfaces
- [ ] Create `internal/providers/openai/provider.go` (OpenAI SDK)
- [ ] Create `internal/providers/azure/provider.go` (Azure SDK)
- [ ] Refactor `cmd/operations.go` to use service layer
- [ ] Create state store implementation (file or in-memory)
- [ ] Create unit tests for service layer
- [ ] Create integration tests for providers

---

## 10. Future Considerations

### 10.1 Phase 2: Additional Vendors
- Add `internal/providers/anthropic/provider.go`
- Add `internal/providers/cohere/provider.go`
- Service and CLI remain unchanged

### 10.2 Async Job Tracking
- Service layer implements polling logic
- CLI supports `azd finetune jobs status <job-id>`
- Long-running operations tracked across sessions

### 10.3 Webhook Support
- Service layer could support push notifications
- Provider layer handles webhook registration with vendor

### 10.4 Cost Tracking
- Service layer accumulates cost metadata from providers
- CLI displays cost information

---

## Questions for Team Discussion

1. **State Persistence**: File-based or database-backed state store?
2. **Configuration**: YAML in project root or environment variables?
3. **Async Polling**: Should it run in background or user-initiated?
4. **Error Handling**: Retry logic - exponential backoff or fixed intervals?
5. **Testing**: Unit test requirements for service and provider layers?

