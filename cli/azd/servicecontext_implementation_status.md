# ServiceContext Implementation Status

## Overview

This document tracks the implementation progress of the ServiceContext specification for the Azure Developer CLI (azd). The ServiceContext defines shared pipeline state across all phases of the service lifecycle, providing a consistent and extensible format for artifacts.

---

## Implementation Progress

### ✅ COMPLETED TASKS

#### 1. Add ServiceContext and Artifact Types
**Status:** ✅ COMPLETED  
**Files Modified:** `pkg/project/service_models.go`

- Added `ServiceContext` struct with slices for each lifecycle phase
- Added `Artifact` struct with Kind, Path, Ref, and Metadata fields
- Added `NewServiceContext()` constructor function
- Full JSON serialization support implemented

```go
type ServiceContext struct {
    Restore []Artifact            `json:"restore"`
    Build   []Artifact            `json:"build"`
    Package []Artifact            `json:"package"`
    Publish []Artifact            `json:"publish"`
    Deploy  []Artifact            `json:"deploy"`
    Extras  map[string][]Artifact `json:"extras,omitempty"`
}

type Artifact struct {
    Kind     string            `json:"kind"`
    Path     string            `json:"path,omitempty"`
    Ref      string            `json:"ref,omitempty"`
    Metadata map[string]string `json:"metadata,omitempty"`
}
```

#### 2. Update Service Result Types
**Status:** ✅ COMPLETED  
**Files Modified:** `pkg/project/service_models.go`

- Replaced all result-specific properties with `[]Artifact` fields
- Converted `Details interface{}` to artifact metadata
- Updated `ToString()` methods to work with artifacts
- Removed deprecated types like `ContainerPublishDetails`

**Changes Made:**
- `ServiceRestoreResult.Details` → `ServiceRestoreResult.Artifacts []Artifact`
- `ServiceBuildResult.BuildOutputPath` + `Details` → `ServiceBuildResult.Artifacts []Artifact`
- `ServicePackageResult.PackagePath` + `Details` → `ServicePackageResult.Artifacts []Artifact`
- `ServicePublishResult.Details` → `ServicePublishResult.Artifacts []Artifact`
- `ServiceDeployResult.Details` → `ServiceDeployResult.Artifacts []Artifact`

#### 3. Update FrameworkService Interface
**Status:** ✅ COMPLETED  
**Files Modified:** `pkg/project/framework_service.go`

- Updated all methods to accept `*ServiceContext` parameter
- Methods now populate context instead of relying on previous phase results
- Interface changes:
  - `Restore(ctx, serviceConfig, serviceContext, progress)`
  - `Build(ctx, serviceConfig, serviceContext, progress)`
  - `Package(ctx, serviceConfig, serviceContext, progress)`

#### 4. Update ServiceTarget Interface
**Status:** ✅ COMPLETED  
**Files Modified:** `pkg/project/service_target.go`

- Updated all methods to accept `*ServiceContext` parameter
- Simplified Deploy method signature (removed separate package/publish result params)
- Updated `NewServiceDeployResult()` helper to use artifacts
- Interface changes:
  - `Package(ctx, serviceConfig, serviceContext, progress)`
  - `Publish(ctx, serviceConfig, serviceContext, targetResource, progress, options)`
  - `Deploy(ctx, serviceConfig, serviceContext, targetResource, progress)`

#### 5. Update ServiceManager Implementation
**Status:** ✅ COMPLETED  
**Files Modified:** `pkg/project/service_manager.go`

- Updated all interface methods to work with ServiceContext
- Implemented artifact aggregation logic
- Added automatic phase dependency resolution
- Context is passed through and populated by each phase
- Removed unused imports (filepath, osutil)

**Key Implementation Details:**
- Each phase method updates the ServiceContext with new artifacts
- Framework service artifacts are added before service target artifacts
- Automatic restoration/building when required artifacts are missing
- Cache integration preserved for performance

#### 6. Add Protobuf Definitions
**Status:** ✅ COMPLETED  
**Files Modified:** `grpc/proto/service_target.proto`

- Added `ServiceContext` message with artifact arrays for each phase
- Added `Artifact` message with all required fields
- Added `ArtifactList` helper for map values
- Updated all service result messages to use artifact arrays
- Updated all request messages to use ServiceContext instead of individual results

**Protobuf Changes:**
```protobuf
message ServiceContext {
    repeated Artifact restore = 1;
    repeated Artifact build = 2;
    repeated Artifact package = 3;
    repeated Artifact publish = 4;
    repeated Artifact deploy = 5;
    map<string, ArtifactList> extras = 6;
}

message Artifact {
    string kind = 1;
    string path = 2;
    string ref = 3;
    map<string, string> metadata = 4;
}
```

---

## 🚧 REMAINING TASKS

### 7. Update Existing Service Targets
**Status:** ❌ NOT STARTED  
**Priority:** HIGH  
**Estimated Effort:** 2-3 hours

**Files to Update:**
- `pkg/project/service_target_appservice.go`
- `pkg/project/service_target_containerapp.go`
- `pkg/project/service_target_dotnet_containerapp.go`
- `pkg/project/service_target_function.go`
- `pkg/project/service_target_springapp.go`
- `pkg/project/service_target_staticwebapp.go`
- `pkg/project/service_target_aks.go`

**Required Changes:**
1. Update method signatures to match new ServiceTarget interface
2. Replace PackagePath property access with artifact iteration
3. Convert Details objects to artifact metadata
4. Update artifact creation with proper Kind values

**Implementation Notes:**
- AppService: zip artifacts with PackagePath → Path
- ContainerApp: container-image artifacts with registry refs
- Function: zip artifacts similar to AppService
- Static Web App: directory/zip artifacts for static content

### 8. Update Framework Services
**Status:** ❌ NOT STARTED  
**Priority:** HIGH  
**Estimated Effort:** 2-3 hours

**Files to Update:**
- `pkg/project/framework_service_*.go` (all framework implementations)
- `pkg/project/framework_service_noop.go`
- `pkg/project/framework_service_swa.go`

**Required Changes:**
1. Update method signatures to match new FrameworkService interface
2. Create artifacts instead of setting result properties
3. Add artifacts to ServiceContext before returning results

**Well-Known Artifact Kinds to Use:**
- `"directory"` - source code directories
- `"zip"` - packaged applications
- `"container-image"` - Docker images (local tags in Path, registry refs in Ref)
- `"binary"` - compiled executables
- `"static-files"` - web assets

### 9. Update External Service Target gRPC
**Status:** ❌ NOT STARTED  
**Priority:** MEDIUM  
**Estimated Effort:** 1-2 hours

**Files to Update:**
- `pkg/project/service_target_external.go`
- Generated protobuf files (need regeneration)

**Required Changes:**
1. Regenerate protobuf Go files: `make generate`
2. Update conversion functions (`toProto*`, `fromProto*`)
3. Update gRPC message handling for new ServiceContext messages
4. Test external extension compatibility

**Critical Step:** Must regenerate protobuf files before proceeding with other changes.

### 10. Update Unit Tests
**Status:** ❌ NOT STARTED  
**Priority:** HIGH  
**Estimated Effort:** 3-4 hours

**Files to Update:**
- `pkg/project/service_manager_test.go`
- `pkg/project/service_target_*_test.go`
- `pkg/project/framework_service_*_test.go`

**Required Changes:**
1. Update test mocks to implement new interfaces
2. Replace property assertions with artifact assertions
3. Create test helpers for ServiceContext creation
4. Update test data to use artifact model

---

## 🔧 IMPLEMENTATION NOTES

### Artifact Kind Conventions
Based on current usage patterns, these artifact kinds should be used:

- `"zip"` - Packaged applications (App Service, Functions)
- `"container-image"` - Docker images
  - Path: local Docker tag (e.g., `myapp:local`)
  - Ref: registry reference (e.g., `myacr.azurecr.io/app:1.2.3`)
- `"directory"` - Source code or build output directories
- `"binary"` - Compiled executables
- `"static-files"` - Static web content
- `"deployment"` - Deployment descriptors/results
- `"blob"` - File uploads to storage

### ServiceContext Usage Patterns

1. **Framework Services populate ServiceContext**:
   - Restore phase: Add dependency artifacts
   - Build phase: Add build output artifacts
   - Package phase: Add packaged artifacts

2. **Service Targets consume and produce**:
   - Package phase: Transform framework artifacts
   - Publish phase: Upload artifacts, populate Ref fields
   - Deploy phase: Create deployment artifacts

3. **Automatic phase dependencies**:
   - ServiceManager ensures required phases run automatically
   - ServiceContext tracks what's been completed

### Migration Strategy
1. Complete remaining service targets (task 7)
2. Complete framework services (task 8)
3. Regenerate protobuf and update external targets (task 9)
4. Update tests to ensure compatibility (task 10)
5. Integration testing with real scenarios

### Potential Issues to Watch For
1. **Backward Compatibility**: Extensions using old APIs
2. **Performance**: Artifact array copying vs. reference passing
3. **Cache Invalidation**: ServiceContext changes may affect caching
4. **Error Handling**: Missing artifacts should fail gracefully

---

## 🎯 NEXT STEPS FOR RESUMING WORK

1. **FIRST:** Run `make generate` to regenerate protobuf files
2. **Start with service targets** (task 7) - they're most critical
3. **Focus on AppService and ContainerApp first** - most commonly used
4. **Create test cases as you go** to validate each change
5. **Use compiler errors as a guide** - they'll show exactly what needs updating

### Quick Test Command
```bash
go build ./... # Will show compilation errors for missing method implementations
```

### Files Modified Summary
- ✅ `pkg/project/service_models.go` - Core types
- ✅ `pkg/project/framework_service.go` - Interface
- ✅ `pkg/project/service_target.go` - Interface  
- ✅ `pkg/project/service_manager.go` - Implementation
- ✅ `grpc/proto/service_target.proto` - Protobuf definitions
- ❌ All concrete service target implementations (not started)
- ❌ All concrete framework service implementations (not started)
- ❌ External service target gRPC (not started)
- ❌ Unit tests (not started)

The core infrastructure is complete - now need to update all the concrete implementations to use the new model.