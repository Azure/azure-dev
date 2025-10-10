# ServiceContext Specification

## Overview

`ServiceContext` defines the shared pipeline state across all phases of the Azure Developer CLI (azd) service lifecycle.\
It captures the results of each phase (`Restore`, `Build`, `Package`, `Publish`, `Deploy`) in a consistent, extensible format that is accessible to both core and external extension providers.

This context is passed to `FrameworkService` & `ServiceTarget` implementations so they have access to all relevant build/package artifacts and metadata needed to perform their responsibilities.

---

## Design Goals

- **Clarity:** Provide a strongly-typed contract for the canonical azd phases.
- **Extensibility:** Allow extension providers to define new artifact kinds or add metadata without schema changes.
- **Predictability:** Establish consistent rules for when `Path` vs `Ref` are populated.
- **Debuggability:** Enable easy serialization of the context for inspection (JSON/YAML).

---

## Data Model

### ServiceContext

```go
type ServiceContext struct {
    Restore []Artifact
    Build   []Artifact
    Package []Artifact
    Publish []Artifact
    Deploy  []Artifact

    // Escape hatch for provider-specific or experimental phases
    Extras map[string][]Artifact
}
```

- **Restore** – Dependencies installed or restored (e.g., `node_modules`, NuGet packages).
- **Build** – Build or transpilation output (e.g., `bin/`, `dist/`).
- **Package** – Application artifacts staged for distribution (zip, container image tag).
- **Publish** – Artifacts uploaded to remote storage or registries.
- **Deploy** – Deployment-specific artifacts or descriptors (ARM template URL, deployment manifest, etc.).
- **Extras** – Provider-specific phases or experimental additions (e.g., `"scan"`, `"sign"`).

---

### Artifact

```go
type Artifact struct {
    Kind     string            // Required: "zip", "container-image", "blob", "helm-chart", etc.
    Path     string            // Optional: local path on disk, valid during pipeline execution
    Ref      string            // Optional: remote/durable reference (registry URL, blob URL, etc.)
    Metadata map[string]string // Optional: arbitrary key/value pairs for extension-specific data
}
```

- **Kind**

  - Required discriminator describing artifact type.
  - Well-known kinds: `"zip"`, `"container-image"`, `"blob"`, `"directory"`.
  - Extensions may introduce new kinds.

- **Path**

  - Local file system location or local identifier (e.g., Docker local tag).
  - Typically set by `Package` phase.
  - Not guaranteed to be valid outside the executing environment.

- **Ref**

  - Remote, durable identifier suitable for deployment.
  - Typically set by `Publish` phase.
  - Required for any artifact consumed by `Deploy`.

- **Metadata**

  - Free-form extension point for provider-specific details.
  - Examples: `{ "dockerDigest":"sha256:...","runtime":"node18" }`.

---

## Lifecycle Rules

- **Package phase outputs → **``
  - e.g., `/out/app.zip`, `myapp:local`.
- **Publish phase outputs → **``
  - e.g., `https://storage.blob.core.windows.net/.../app.zip`, `myacr.azurecr.io/app:tag`.
- **Deploy phase always consumes **``**.**
- Artifacts may contain both `Path` and `Ref` if relevant (e.g., Docker local tag + pushed registry ref).

---

## Examples

### App Service

```json
{
  "Package": [
    { "Kind": "zip", "Path": "/out/app.zip" }
  ],
  "Publish": [
    { "Kind": "blob", "Ref": "https://storage.blob.core.windows.net/apps/app.zip" }
  ],
  "Deploy": [
    { "Kind": "deployment", "Ref": "https://management.azure.com/.../deployments/app" }
  ]
}
```

### Container Apps

```json
{
  "Package": [
    { "Kind": "container-image", "Ref": "myapp:local" }
  ],
  "Publish": [
    { "Kind": "container-image", "Ref": "myacr.azurecr.io/app:1.2.3" }
  ],
  "Deploy": [
    { "Kind": "deployment", "Ref": "https://management.azure.com/.../deployments/app" }
  ]
}
```

---

## Extension Author Guidance

- Always prefer `Ref` over `Path` when available.
- Use `Kind` to discriminate artifact types.
- Store additional details in `Metadata` rather than introducing new top-level fields.
- Use `Extras` in `ServiceContext` for experimental or non-canonical phases.

