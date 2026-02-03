# Proposal 2: Azure AI Models Extension (Full End-to-End)

## Overview

This document outlines a **comprehensive** extension (`azure.ai.models`) that provides full end-to-end model management including both **base models** and **custom models**, covering upload, registration, deployment, and all read operations.

## Scope

| In Scope | Out of Scope |
|----------|--------------|
| Upload custom model weights | Inference/endpoint invocation |
| Register custom models | Fine-tuning (see `azure.ai.finetune`) |
| List/show base models (catalog) | Training jobs |
| List/show custom models | |
| Deploy base models | |
| Deploy custom models | |
| Manage deployments | |

## Key Concepts

### Two Model Types, Two Registries

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                                                                                 │
│   ┌───────────────────────────────┐      ┌───────────────────────────────┐     │
│   │     Base Model Registry       │      │    Custom Model Registry      │     │
│   │     (Model Catalog)           │      │    (FDP Custom Registry)      │     │
│   ├───────────────────────────────┤      ├───────────────────────────────┤     │
│   │ • Pre-trained models          │      │ • User-uploaded models        │     │
│   │ • GPT-4, Llama, Mistral, etc. │      │ • Weights in FDP data store   │     │
│   │ • Managed by Azure            │      │ • User manages lifecycle      │     │
│   │ • Read-only for users         │      │ • Full CRUD operations        │     │
│   └───────────────────────────────┘      └───────────────────────────────┘     │
│                  │                                      │                       │
│                  │         ┌────────────────────────────┘                       │
│                  │         │                                                    │
│                  ▼         ▼                                                    │
│          ┌─────────────────────────────────┐                                   │
│          │         Deployment API          │                                   │
│          │   (Same API, different params)  │                                   │
│          └─────────────────────────────────┘                                   │
│                          │                                                      │
│                          ▼                                                      │
│          ┌─────────────────────────────────┐                                   │
│          │        Deployments              │                                   │
│          │   (Inference Endpoints)         │                                   │
│          └─────────────────────────────────┘                                   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### Entity Relationships

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│   Base Model    │     │  Custom Model   │     │   Deployment    │
│   (Catalog)     │     │  (Registry)     │     │   (Endpoint)    │
├─────────────────┤     ├─────────────────┤     ├─────────────────┤
│ • Read-only     │     │ • Upload        │     │ • Create        │
│ • List          │     │ • Register      │     │ • List          │
│ • Show          │     │ • List          │     │ • Show          │
│ • Deploy ───────┼─────┤ • Show          │     │ • Delete        │
│                 │     │ • Delete        │     │ • Scale         │
│                 │     │ • Deploy ───────┼────►│                 │
└─────────────────┘     └─────────────────┘     └─────────────────┘
```

## Commands Overview

### Command Structure

```
azd ai models <resource> <action> [options]

Resources:
  models      - Base models from catalog + Custom models from registry
  deployments - Model deployments (inference endpoints)
  uploads     - Raw uploaded weights (custom models only)
```

### Full Command List

| Command | Description | Base | Custom |
|---------|-------------|:----:|:------:|
| **Model Operations** | | | |
| `azd ai models list` | List all models | ✓ | ✓ |
| `azd ai models show` | Show model details | ✓ | ✓ |
| `azd ai models upload` | Upload + register custom model | | ✓ |
| `azd ai models delete` | Delete custom model | | ✓ |
| **Deployment Operations** | | | |
| `azd ai models deploy` | Deploy a model | ✓ | ✓ |
| `azd ai models deployments list` | List deployments | ✓ | ✓ |
| `azd ai models deployments show` | Show deployment details | ✓ | ✓ |
| `azd ai models deployments delete` | Delete deployment | ✓ | ✓ |
| `azd ai models deployments scale` | Scale deployment | ✓ | ✓ |
| **Upload Operations** (Custom only) | | | |
| `azd ai models uploads list` | List uploaded weights | | ✓ |
| `azd ai models uploads show` | Show upload details | | ✓ |

## Command Details

---

## Model Commands

### `models list` - List All Models

List models from both base catalog and custom registry.

```bash
azd ai models list [options]
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--type, -t` | Filter by type: `base`, `custom`, or `all` (default: `all`) |
| `--format` | Output format: `table`, `json` (default: `table`) |
| `--query` | Filter by name pattern |

**Output:**
```
$ azd ai models list

Models
┌──────────────────────┬──────────┬─────────────┬─────────┬────────────────────┐
│ Name                 │ Type     │ Format      │ Size    │ Status             │
├──────────────────────┼──────────┼─────────────┼─────────┼────────────────────┤
│ gpt-4                │ Base     │ -           │ -       │ Available          │
│ gpt-4o               │ Base     │ -           │ -       │ Available          │
│ Llama-3.1-70B        │ Base     │ -           │ -       │ Available          │
│ Mistral-7B           │ Base     │ -           │ -       │ Available          │
│ my-llama-7b          │ Custom   │ safetensors │ 13.5 GB │ Ready              │
│ my-mistral-finetune  │ Custom   │ gguf        │ 4.1 GB  │ Ready              │
└──────────────────────┴──────────┴─────────────┴─────────┴────────────────────┘

6 models found (4 base, 2 custom)
```

**Filtered by type:**
```
$ azd ai models list --type custom

Custom Models
┌──────────────────────┬─────────────┬─────────┬─────────┬────────────────────┐
│ Name                 │ Format      │ Size    │ Version │ Status             │
├──────────────────────┼─────────────┼─────────┼─────────┼────────────────────┤
│ my-llama-7b          │ safetensors │ 13.5 GB │ 1.0     │ Ready              │
│ my-mistral-finetune  │ gguf        │ 4.1 GB  │ 1.0     │ Ready              │
└──────────────────────┴─────────────┴─────────┴─────────┴────────────────────┘

2 custom models found
```

### `models show` - Show Model Details

Show details of a specific model (auto-detects base vs custom).

```bash
azd ai models show --name <model-name> [--type base|custom]
```

**Base Model Output:**
```
$ azd ai models show --name gpt-4

Base Model: gpt-4

General:
  Name:         gpt-4
  Type:         Base (Catalog)
  Publisher:    OpenAI
  Description:  GPT-4 is a large multimodal model capable of processing 
                image and text inputs and producing text outputs.

Capabilities:
  • Chat completion
  • Function calling
  • JSON mode

Available SKUs:
  • Standard
  • Global-Standard

Deployments:
  You have 2 deployments of this model.
  Run 'azd ai models deployments list --model gpt-4' to see them.

To deploy this model:
  azd ai models deploy --name gpt-4 --deployment-name my-gpt4
```

**Custom Model Output:**
```
$ azd ai models show --name my-llama-7b

Custom Model: my-llama-7b

General:
  Name:         my-llama-7b
  Type:         Custom
  Format:       safetensors
  Version:      1.0
  Description:  Fine-tuned Llama 7B for code generation
  Status:       Ready for deployment

Storage:
  Size:         13.5 GB
  Path:         uploads/my-llama-7b/model.safetensors
  Uploaded:     2026-02-03 10:30:00 UTC

Tags:
  team:         ml-platform
  project:      code-assist

Deployments:
  No deployments found.

To deploy this model:
  azd ai models deploy --name my-llama-7b --deployment-name my-llama-deploy
```

### `models upload` - Upload and Register Custom Model

Upload weights and register in custom model registry (custom models only).

```bash
azd ai models upload --source <local-path> --name <model-name> [options]
```

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
$ azd ai models upload --source ./llama-7b.safetensors --name my-llama-7b

Initializing upload...
  Model: my-llama-7b
  Size: 13.5 GB
  Format: safetensors (auto-detected)

Uploading to FDP data store...
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 100% (13.5 GB / 13.5 GB)
Speed: 142 MB/s

Registering model...
✓ Upload complete
✓ Model registered: my-llama-7b

Model Details:
  Name:     my-llama-7b
  Type:     Custom
  Format:   safetensors
  Size:     13.5 GB
  Status:   Ready for deployment

Next steps:
  • Deploy: azd ai models deploy --name my-llama-7b --deployment-name my-deploy
  • View:   azd ai models show --name my-llama-7b
```

### `models delete` - Delete Custom Model

Delete a custom model (base models cannot be deleted).

```bash
azd ai models delete --name <model-name> [options]
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--keep-weights` | Remove from registry but keep weights in data store |
| `--force, -f` | Skip confirmation prompt |

**Output:**
```
$ azd ai models delete --name my-llama-7b

Are you sure you want to delete 'my-llama-7b'? This will:
  • Remove model from custom registry
  • Delete uploaded weights (13.5 GB)
  
Note: This will NOT delete any existing deployments.

Type 'my-llama-7b' to confirm: my-llama-7b

Deleting model...
✓ Model 'my-llama-7b' deleted
```

---

## Deployment Commands

### `deploy` - Deploy a Model

Deploy a model (base or custom) to create an inference endpoint.

```bash
azd ai models deploy --name <model-name> --deployment-name <deployment-name> [options]
```

**Flags:**

| Flag | Required | Description |
|------|----------|-------------|
| `--name, -n` | Yes | Model name to deploy |
| `--deployment-name, -d` | Yes | Name for the deployment |
| `--type, -t` | No | Model type: `base` or `custom` (auto-detected) |
| `--sku` | No | SKU/compute tier (default varies by model) |
| `--capacity` | No | Initial capacity/instances |
| `--region` | No | Deployment region (default: resource region) |

**Deploy Base Model:**
```
$ azd ai models deploy --name gpt-4 --deployment-name my-gpt4-prod --sku Standard

Deploying model...
  Model:      gpt-4 (Base)
  Deployment: my-gpt4-prod
  SKU:        Standard
  Region:     eastus

Creating deployment...
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 100%

✓ Deployment created: my-gpt4-prod

Deployment Details:
  Name:       my-gpt4-prod
  Model:      gpt-4
  Status:     Running
  Endpoint:   https://my-resource.openai.azure.com/openai/deployments/my-gpt4-prod

To test:
  curl https://my-resource.openai.azure.com/openai/deployments/my-gpt4-prod/chat/completions \
    -H "api-key: $AZURE_OPENAI_KEY" \
    -H "Content-Type: application/json" \
    -d '{"messages": [{"role": "user", "content": "Hello!"}]}'
```

**Deploy Custom Model:**
```
$ azd ai models deploy --name my-llama-7b --deployment-name my-llama-prod

Deploying model...
  Model:      my-llama-7b (Custom)
  Deployment: my-llama-prod
  SKU:        Standard_NC24ads_A100_v4
  Region:     eastus

Creating deployment...
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 100%

✓ Deployment created: my-llama-prod

Deployment Details:
  Name:       my-llama-prod
  Model:      my-llama-7b
  Type:       Custom
  Status:     Running
  Endpoint:   https://my-resource.inference.azure.com/deployments/my-llama-prod

To test:
  curl https://my-resource.inference.azure.com/deployments/my-llama-prod/v1/completions \
    -H "Authorization: Bearer $AZURE_AI_KEY" \
    -H "Content-Type: application/json" \
    -d '{"prompt": "Hello!", "max_tokens": 100}'
```

### `deployments list` - List Deployments

```bash
azd ai models deployments list [options]
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--model` | Filter by model name |
| `--type` | Filter by model type: `base`, `custom`, or `all` |
| `--status` | Filter by status: `running`, `stopped`, `failed` |
| `--format` | Output format: `table`, `json` |

**Output:**
```
$ azd ai models deployments list

Deployments
┌──────────────────┬──────────────────┬──────────┬───────────┬─────────────────────┐
│ Deployment       │ Model            │ Type     │ Status    │ Created             │
├──────────────────┼──────────────────┼──────────┼───────────┼─────────────────────┤
│ my-gpt4-prod     │ gpt-4            │ Base     │ Running   │ 2026-02-01 09:00    │
│ my-gpt4-dev      │ gpt-4            │ Base     │ Running   │ 2026-01-28 14:30    │
│ my-llama-prod    │ my-llama-7b      │ Custom   │ Running   │ 2026-02-03 11:00    │
│ test-mistral     │ my-mistral       │ Custom   │ Stopped   │ 2026-01-20 08:00    │
└──────────────────┴──────────────────┴──────────┴───────────┴─────────────────────┘

4 deployments found (2 base, 2 custom)
```

### `deployments show` - Show Deployment Details

```bash
azd ai models deployments show --name <deployment-name>
```

**Output:**
```
$ azd ai models deployments show --name my-llama-prod

Deployment: my-llama-prod

General:
  Name:         my-llama-prod
  Model:        my-llama-7b
  Model Type:   Custom
  Status:       Running
  Created:      2026-02-03 11:00:00 UTC

Configuration:
  SKU:          Standard_NC24ads_A100_v4
  Capacity:     1
  Region:       eastus

Endpoint:
  URL:          https://my-resource.inference.azure.com/deployments/my-llama-prod
  Auth:         API Key / Azure AD

Metrics (last 24h):
  Requests:     12,450
  Avg Latency:  245ms
  Errors:       0.1%

Actions:
  • Scale:  azd ai models deployments scale --name my-llama-prod --capacity 2
  • Stop:   azd ai models deployments delete --name my-llama-prod
```

### `deployments delete` - Delete Deployment

```bash
azd ai models deployments delete --name <deployment-name> [--force]
```

**Output:**
```
$ azd ai models deployments delete --name my-llama-prod

Are you sure you want to delete deployment 'my-llama-prod'?
  • The inference endpoint will be removed
  • The underlying model will NOT be deleted

Type 'my-llama-prod' to confirm: my-llama-prod

Deleting deployment...
✓ Deployment 'my-llama-prod' deleted
```

### `deployments scale` - Scale Deployment

```bash
azd ai models deployments scale --name <deployment-name> --capacity <count>
```

**Output:**
```
$ azd ai models deployments scale --name my-llama-prod --capacity 2

Scaling deployment...
  Deployment: my-llama-prod
  Current:    1 instance
  Target:     2 instances

Scaling...
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 100%

✓ Deployment scaled to 2 instances
```

---

## Upload Commands

### `uploads list` - List Uploaded Weights

List raw uploads in FDP data store (custom models only).

```bash
azd ai models uploads list [--prefix <prefix>]
```

**Output:**
```
$ azd ai models uploads list

Uploaded Weights (FDP Data Store)
┌──────────────────────────────────────────┬─────────┬─────────────────────┬────────────┐
│ Path                                     │ Size    │ Uploaded            │ Registered │
├──────────────────────────────────────────┼─────────┼─────────────────────┼────────────┤
│ uploads/my-llama-7b/model.safetensors    │ 13.5 GB │ 2026-02-03 10:30    │ Yes        │
│ uploads/my-mistral/model.gguf            │ 4.1 GB  │ 2026-02-01 14:22    │ Yes        │
│ uploads/test-model/model.bin             │ 1.2 GB  │ 2026-01-20 08:00    │ No         │
└──────────────────────────────────────────┴─────────┴─────────────────────┴────────────┘

3 uploads found (1 not registered)

Tip: Unregistered uploads may be from failed operations. 
     Use 'azd ai models uploads delete' to clean up.
```

### `uploads show` - Show Upload Details

```bash
azd ai models uploads show --path <upload-path>
```

---

## Architecture

### Component Overview

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           azure.ai.models Extension                             │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐                 │
│  │  Model Commands │  │ Deploy Commands │  │ Upload Commands │                 │
│  │  (list, show,   │  │ (deploy, list,  │  │ (list, show)    │                 │
│  │   upload, del)  │  │  show, delete)  │  │                 │                 │
│  └────────┬────────┘  └────────┬────────┘  └────────┬────────┘                 │
│           │                    │                    │                           │
│           ▼                    ▼                    ▼                           │
│  ┌─────────────────────────────────────────────────────────────────┐           │
│  │                        Service Layer                            │           │
│  ├─────────────────┬─────────────────┬─────────────────────────────┤           │
│  │ Base Model Svc  │ Custom Model Svc│ Deployment Service          │           │
│  │ (Catalog API)   │ (FDP API)       │ (Unified Deploy API)        │           │
│  └────────┬────────┴────────┬────────┴─────────────┬───────────────┘           │
│           │                 │                      │                            │
│           ▼                 ▼                      ▼                            │
│  ┌─────────────────────────────────────────────────────────────────┐           │
│  │                        Client Layer                             │           │
│  ├─────────────────┬─────────────────┬─────────────────────────────┤           │
│  │ Catalog Client  │ FDP Client      │ Deployment Client           │           │
│  │ (REST)          │ (REST + AzCopy) │ (REST)                      │           │
│  └─────────────────┴─────────────────┴─────────────────────────────┘           │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                              Azure Services                                     │
├───────────────────┬───────────────────┬─────────────────────────────────────────┤
│   Model Catalog   │   FDP Platform    │        Deployment Service               │
│   (Base Models)   │ (Custom Registry) │        (Inference Endpoints)            │
│                   │ (Data Store)      │                                         │
└───────────────────┴───────────────────┴─────────────────────────────────────────┘
```

### Deployment API - Unified with Different Parameters

The deployment API is the same endpoint but accepts different parameters based on model type:

**Deploy Base Model:**
```http
POST /deployments
{
  "deployment_name": "my-gpt4-prod",
  "model": {
    "source": "catalog",
    "name": "gpt-4"
  },
  "sku": "Standard",
  "capacity": 1
}
```

**Deploy Custom Model:**
```http
POST /deployments
{
  "deployment_name": "my-llama-prod",
  "model": {
    "source": "custom",
    "name": "my-llama-7b",
    "registry_path": "custom-models/my-llama-7b"
  },
  "sku": "Standard_NC24ads_A100_v4",
  "capacity": 1
}
```

### Auto-Detection Logic

When user runs `azd ai models deploy --name X`, the CLI auto-detects model type:

```go
func detectModelType(ctx context.Context, modelName string) (ModelType, error) {
    // 1. Check custom registry first (user's models)
    if model, err := fdpClient.GetCustomModel(ctx, modelName); err == nil {
        return ModelTypeCustom, nil
    }
    
    // 2. Check base catalog
    if model, err := catalogClient.GetModel(ctx, modelName); err == nil {
        return ModelTypeBase, nil
    }
    
    return "", fmt.Errorf("model '%s' not found in catalog or custom registry", modelName)
}
```

Users can override with `--type base` or `--type custom` if names conflict.

## API Requirements

### Catalog API (Base Models)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/models` | GET | List base models in catalog |
| `/models/{name}` | GET | Get base model details |

### FDP API (Custom Models)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/datastore/upload/initialize` | POST | Get SAS URI for upload |
| `/custom-models` | POST | Register custom model |
| `/custom-models` | GET | List custom models |
| `/custom-models/{name}` | GET | Get custom model details |
| `/custom-models/{name}` | DELETE | Delete custom model |
| `/datastore/list-sas` | GET | Get SAS for listing uploads |

### Deployment API (Unified)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/deployments` | POST | Create deployment (base or custom) |
| `/deployments` | GET | List all deployments |
| `/deployments/{name}` | GET | Get deployment details |
| `/deployments/{name}` | DELETE | Delete deployment |
| `/deployments/{name}/scale` | POST | Scale deployment |

## Implementation Plan

### Phase 1 - Custom Model Upload (MVP)
- [ ] AzCopy detection and auto-download
- [ ] FDP client (upload init, register)
- [ ] `azd ai models upload` command
- [ ] Progress reporting

### Phase 2 - Model Read Operations
- [ ] Catalog client (list, show base models)
- [ ] FDP client (list, show custom models)
- [ ] `azd ai models list` command (unified)
- [ ] `azd ai models show` command (unified)
- [ ] Auto-detection logic (base vs custom)

### Phase 3 - Deployment Operations
- [ ] Deployment client
- [ ] `azd ai models deploy` command
- [ ] `azd ai models deployments list` command
- [ ] `azd ai models deployments show` command
- [ ] `azd ai models deployments delete` command

### Phase 4 - Polish & Advanced
- [ ] `azd ai models deployments scale` command
- [ ] `azd ai models delete` command
- [ ] `azd ai models uploads list` command
- [ ] JSON output format
- [ ] Error handling improvements

## Comparison: Proposal 1 vs Proposal 2

| Aspect | Proposal 1 (Custom Only) | Proposal 2 (Full) |
|--------|--------------------------|-------------------|
| **Scope** | Custom models only | Base + Custom + Deployments |
| **Complexity** | Low | High |
| **Time to MVP** | Faster | Slower |
| **User Value** | Partial (need other tools) | Complete end-to-end |
| **API Integration** | FDP only | Catalog + FDP + Deployment |
| **Commands** | 6 commands | 12+ commands |
| **Registry Handling** | Single (custom) | Dual (base + custom) |

## Advantages of This Approach

| Advantage | Description |
|-----------|-------------|
| **Complete solution** | Users can manage entire model lifecycle from one tool |
| **Unified experience** | Consistent commands for base and custom models |
| **Discoverability** | Users can explore catalog and their custom models together |
| **Single tool** | No need to switch between tools for different operations |

## Challenges

| Challenge | Mitigation |
|-----------|------------|
| **Higher complexity** | Phased implementation, MVP first |
| **Multiple APIs** | Clear abstraction layers |
| **Different params for deploy** | Auto-detection with manual override |
| **Longer development time** | Prioritize most-used commands |

## Open Questions

1. **Catalog API Access**: What API provides base model catalog? Is it publicly documented?
2. **Deployment API Spec**: Need final spec for unified deployment API with base/custom params.
3. **SKU Mapping**: Different SKUs for base vs custom models - how to present to users?
4. **Region Availability**: Base models have different regional availability - how to handle?
5. **Shared Code**: Should deployment logic be shared with `azure.ai.finetune` extension?
