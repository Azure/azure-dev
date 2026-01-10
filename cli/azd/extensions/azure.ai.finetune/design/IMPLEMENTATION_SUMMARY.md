# Architecture Implementation - Folder Structure & Interfaces

## Created Folder Structure

```
azure.ai.finetune/
├── pkg/
│   └── models/                          # Domain Models (Shared Foundation)
│       ├── finetune.go                  # FineTuningJob, JobStatus, CreateFineTuningRequest
│       ├── deployment.go                # Deployment, DeploymentStatus, DeploymentRequest
│       ├── errors.go                    # ErrorDetail, Error codes
│       └── requests.go                  # All request/response DTOs
│
├── internal/
│   ├── services/                        # Service Layer (Business Logic)
│   │   ├── interface.go                 # FineTuningService, DeploymentService interfaces
│   │   ├── state_store.go              # StateStore, ErrorTransformer interfaces
│   │   ├── finetune_service.go         # FineTuningService implementation (stub)
│   │   └── deployment_service.go       # DeploymentService implementation (stub)
│   │
│   └── providers/                       # Provider Layer (SDK Adapters)
│       ├── interface.go                 # FineTuningProvider, ModelDeploymentProvider interfaces
│       ├── openai/
│       │   └── provider.go             # OpenAI provider implementation (stub)
│       └── azure/
│           └── provider.go             # Azure provider implementation (stub)
│
├── design/
│   └── architecture.md                  # Architecture documentation
└── [existing files unchanged]
```

## Files Created

### 1. Domain Models (pkg/models/)

#### finetune.go
- `JobStatus` enum: pending, queued, running, succeeded, failed, cancelled, paused
- `FineTuningJob` - main domain model for jobs
- `CreateFineTuningRequest` - request DTO
- `Hyperparameters` - hyperparameter configuration
- `ListFineTuningJobsRequest` - pagination request
- `FineTuningJobDetail` - detailed job info
- `JobEvent` - event information
- `JobCheckpoint` - checkpoint data

#### deployment.go
- `DeploymentStatus` enum: pending, active, updating, failed, deleting
- `Deployment` - main domain model for deployments
- `DeploymentRequest` - request DTO
- `DeploymentConfig` - configuration for deployments
- `BaseModel` - base model information

#### errors.go
- `ErrorDetail` - standardized error structure
- Error code constants: INVALID_REQUEST, NOT_FOUND, UNAUTHORIZED, RATE_LIMITED, etc.
- Error method implementation

#### requests.go
- All request DTOs: PauseJobRequest, ResumeJobRequest, CancelJobRequest, etc.
- ListDeploymentsRequest, GetDeploymentRequest, UpdateDeploymentRequest, etc.

---

### 2. Provider Layer (internal/providers/)

#### interface.go
Defines two main interfaces:

**FineTuningProvider Interface**
- `CreateFineTuningJob()`
- `GetFineTuningStatus()`
- `ListFineTuningJobs()`
- `GetFineTuningJobDetails()`
- `GetJobEvents()`
- `GetJobCheckpoints()`
- `PauseJob()`
- `ResumeJob()`
- `CancelJob()`
- `UploadFile()`
- `GetUploadedFile()`

**ModelDeploymentProvider Interface**
- `DeployModel()`
- `GetDeploymentStatus()`
- `ListDeployments()`
- `UpdateDeployment()`
- `DeleteDeployment()`

#### openai/provider.go (Stub Implementation)
- `OpenAIProvider` struct
- Implements both `FineTuningProvider` and `ModelDeploymentProvider`
- All methods have TODO comments (ready for implementation)
- Constructor: `NewOpenAIProvider(apiKey, endpoint)`

#### azure/provider.go (Stub Implementation)
- `AzureProvider` struct
- Implements both `FineTuningProvider` and `ModelDeploymentProvider`
- All methods have TODO comments (ready for implementation)
- Constructor: `NewAzureProvider(endpoint, apiKey)`

---

### 3. Service Layer (internal/services/)

#### interface.go
Defines two service interfaces:

**FineTuningService Interface**
- `CreateFineTuningJob()` - with business validation
- `GetFineTuningStatus()`
- `ListFineTuningJobs()`
- `GetFineTuningJobDetails()`
- `GetJobEvents()` - with filtering
- `GetJobCheckpoints()` - with pagination
- `PauseJob()` - with state validation
- `ResumeJob()` - with state validation
- `CancelJob()` - with proper validation
- `UploadTrainingFile()` - with validation
- `UploadValidationFile()` - with validation
- `PollJobUntilCompletion()` - async polling

**DeploymentService Interface**
- `DeployModel()` - with validation
- `GetDeploymentStatus()`
- `ListDeployments()`
- `UpdateDeployment()` - with validation
- `DeleteDeployment()` - with validation
- `WaitForDeployment()` - timeout support

#### state_store.go
Defines persistence interfaces:

**StateStore Interface**
- Job persistence: SaveJob, GetJob, ListJobs, UpdateJobStatus, DeleteJob
- Deployment persistence: SaveDeployment, GetDeployment, ListDeployments, UpdateDeploymentStatus, DeleteDeployment

**ErrorTransformer Interface**
- `TransformError()` - converts vendor errors to standardized ErrorDetail

#### finetune_service.go (Stub Implementation)
- `fineTuningServiceImpl` struct
- Implements `FineTuningService` interface
- Constructor: `NewFineTuningService(provider, stateStore)`
- All methods have TODO comments (ready for implementation)
- Takes `FineTuningProvider` and `StateStore` as dependencies

#### deployment_service.go (Stub Implementation)
- `deploymentServiceImpl` struct
- Implements `DeploymentService` interface
- Constructor: `NewDeploymentService(provider, stateStore)`
- All methods have TODO comments (ready for implementation)
- Takes `ModelDeploymentProvider` and `StateStore` as dependencies

---

## Architecture Verification

### Import Rules Enforced

✅ **pkg/models/** - No imports from other layers
- Pure data structures only

✅ **internal/providers/interface.go** - Only imports models
- Vendor-agnostic interface definitions

✅ **internal/providers/openai/provider.go** - Can import:
- `pkg/models` (domain models)
- OpenAI SDK (when implemented)

✅ **internal/providers/azure/provider.go** - Can import:
- `pkg/models` (domain models)
- Azure SDK (when implemented)

✅ **internal/services/interface.go** - Only imports:
- `pkg/models`
- `context`

✅ **internal/services/finetune_service.go** - Only imports:
- `pkg/models`
- `internal/providers` (interface, not concrete)
- `internal/services` (own package for StateStore)

✅ **internal/services/deployment_service.go** - Only imports:
- `pkg/models`
- `internal/providers` (interface, not concrete)
- `internal/services` (own package for StateStore)

---

## Next Steps

### To Implement Provider Layer:

1. **OpenAI Provider** (`internal/providers/openai/provider.go`)
   - Add OpenAI SDK imports
   - Implement domain ↔ SDK conversions
   - Fill in method bodies
   - Add error transformation logic

2. **Azure Provider** (`internal/providers/azure/provider.go`)
   - Add Azure SDK imports
   - Implement domain ↔ SDK conversions
   - Fill in method bodies
   - Add error transformation logic

### To Implement Service Layer:

1. **FineTuningService** (`internal/services/finetune_service.go`)
   - Implement validation logic
   - Add state persistence calls
   - Error transformation
   - Fill in method bodies

2. **DeploymentService** (`internal/services/deployment_service.go`)
   - Implement validation logic
   - Add state persistence calls
   - Error transformation
   - Fill in method bodies

3. **StateStore Implementation**
   - File-based storage (JSON files)
   - Or in-memory with persistence

### To Refactor CLI Layer:

1. Update `internal/cmd/operations.go`
   - Remove direct SDK calls
   - Use service layer instead
   - Inject services via DI
   - Format output only

2. Create command factory
   - Initialize providers
   - Initialize services
   - Pass to command constructors

---

## Key Benefits of This Structure

✅ **No Existing Files Modified**
- All new files
- Extension to existing code without breaking changes

✅ **Clear Separation of Concerns**
- Models: Pure data
- Providers: SDK integration
- Services: Business logic
- CLI: User interface

✅ **Multi-Vendor Ready**
- Add new vendor: Just implement provider interface
- No CLI or service changes needed

✅ **Testable**
- Mock provider at interface level
- Test services independently
- Integration tests for providers

✅ **Future Proof**
- Easy to add Anthropic, Cohere, etc.
- Easy to swap implementations
- Easy to add new features

---

## File Summary

| File | Lines | Purpose |
|------|-------|---------|
| pkg/models/finetune.go | ~100 | Fine-tuning domain models |
| pkg/models/deployment.go | ~80 | Deployment domain models |
| pkg/models/errors.go | ~40 | Error handling models |
| pkg/models/requests.go | ~60 | Request DTOs |
| internal/providers/interface.go | ~70 | Provider interfaces |
| internal/providers/openai/provider.go | ~150 | OpenAI stub (TODO) |
| internal/providers/azure/provider.go | ~150 | Azure stub (TODO) |
| internal/services/interface.go | ~100 | Service interfaces |
| internal/services/state_store.go | ~60 | Persistence interfaces |
| internal/services/finetune_service.go | ~120 | Fine-tuning service stub |
| internal/services/deployment_service.go | ~90 | Deployment service stub |
| **Total** | **~920** | **Complete stub structure** |

