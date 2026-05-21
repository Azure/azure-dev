# Azure AI ML SDK — Command Job Guide

## Overview

The **CommandJob** is the most fundamental job type in the Azure AI ML SDK. It executes a single command on a specified compute target with defined inputs, outputs, and environment configuration.

A `CommandJob` is composed of 4 classes:

1. **Resource** (base) — metadata and serialization
2. **Job** (base) — job-level configuration
3. **ParameterizedCommand** (mixin) — command execution details
4. **JobIOMixin** (mixin) — input/output handling

---

## Parameters

### Core Parameters (from `ParameterizedCommand`)

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `command` | `str` | `""` | The command to execute (e.g., `"python train.py"`) |
| `code` | `str \| PathLike` | `None` | Path to source code directory (local path or URL) |
| `environment` | `Environment \| str` | `None` | Environment to run in (e.g., curated or custom Docker image) |
| `distribution` | `MpiDistribution \| PyTorchDistribution \| TensorFlowDistribution \| RayDistribution` | `None` | Distributed training configuration |
| `resources` | `JobResourceConfiguration` | `None` | Compute resource specs (instance count, instance type, shm_size, docker_args) |
| `environment_variables` | `Dict` | `{}` | Environment variables for the process |
| `queue_settings` | `QueueSettings` | `None` | Job priority and tier settings |

### Job-level Parameters (from `Job`)

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `name` | `str` | `None` | Unique job name |
| `display_name` | `str` | `None` | Human-readable display name |
| `description` | `str` | `None` | Description of the job |
| `experiment_name` | `str` | `None` | Experiment the job belongs to (defaults to current directory name) |
| `compute` | `str` | `None` | Compute target (e.g., `"gpu-cluster"`) |
| `tags` | `Dict` | `None` | Tag dictionary for organizing jobs |
| `properties` | `Dict` | `None` | Job property dictionary |

### CommandJob-specific Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `inputs` | `Dict[str, Input \| str \| bool \| int \| float]` | `None` | Input data bindings used in the command |
| `outputs` | `Dict[str, Output]` | `None` | Output data bindings |
| `limits` | `CommandJobLimits` | `None` | Job limits (e.g., `timeout` in seconds) |
| `identity` | `ManagedIdentityConfiguration \| AmlTokenConfiguration \| UserIdentityConfiguration` | `None` | Identity the job uses while running on compute |
| `services` | `Dict[str, JobService \| JupyterLabJobService \| SshJobService \| TensorBoardJobService \| VsCodeJobService]` | `None` | Services associated with the job |

---

## Creating a Command Job

### Option 1: Direct Instantiation

```python
from azure.ai.ml import MLClient, Input
from azure.ai.ml.entities import CommandJob, CommandJobLimits
from azure.identity import DefaultAzureCredential

# Connect to workspace
ml_client = MLClient(
    DefaultAzureCredential(),
    subscription_id="<subscription-id>",
    resource_group_name="<resource-group>",
    workspace_name="<workspace>",
)

# Define the job
job = CommandJob(
    command="python train.py --lr 0.01 --epochs 10",
    code="./src",
    environment="AzureML-sklearn-1.0@latest",
    compute="gpu-cluster",
    inputs={
        "data": Input(type="uri_folder", path="azureml:my-data:1"),
    },
    outputs={
        "model": Output(type="uri_folder"),
    },
    display_name="my-training-job",
    experiment_name="my-experiment",
    limits=CommandJobLimits(timeout=3600),
)

# Submit the job
returned_job = ml_client.jobs.create_or_update(job)
print(f"Job submitted: {returned_job.name}")
```

### Option 2: Builder Function `command()` (Recommended)

The `command()` helper function provides a more convenient API with flattened parameters:

```python
from azure.ai.ml import command, Input

job = command(
    command="python train.py --lr ${{inputs.lr}} --data ${{inputs.data}}",
    code="./src",
    environment="AzureML-sklearn-1.0@latest",
    compute="gpu-cluster",
    inputs={
        "lr": 0.01,
        "data": Input(type="uri_folder", path="azureml:my-data:1"),
    },
    instance_count=1,
    timeout=3600,
    display_name="my-training-job",
    experiment_name="my-experiment",
)

# Submit the job
returned_job = ml_client.jobs.create_or_update(job)
```

> **Note:** The `command()` builder internally creates a `CommandComponent` and a `Command` node object. It supports additional flattened parameters like `instance_count`, `instance_type`, `docker_args`, `shm_size`, `timeout`, `is_deterministic`, `job_tier`, and `priority`.

---

## Job Operations

All operations are accessed via `ml_client.jobs`:

### Submit a Job

```python
returned_job = ml_client.jobs.create_or_update(job)
```

Creates or updates a job. Automatically resolves inline dependencies (Environment, Code, Components).

### Get a Job

```python
job = ml_client.jobs.get(name="my-job-name")
print(job.status)
```

### List Jobs

```python
# List active jobs
jobs = ml_client.jobs.list()

# List all jobs (including archived)
from azure.ai.ml.constants import ListViewType
jobs = ml_client.jobs.list(list_view_type=ListViewType.ALL)

# List child jobs of a pipeline
child_jobs = ml_client.jobs.list(parent_job_name="pipeline-job-name")
```

### Cancel a Job

```python
ml_client.jobs.begin_cancel(name="my-job-name")
```

Returns an `LROPoller` (long-running operation).

### Stream Logs

```python
ml_client.jobs.stream(name="my-job-name")
```

Streams real-time logs from a running job to stdout.

### Download Outputs

```python
# Download all outputs and logs
ml_client.jobs.download(name="my-job-name", download_path="./downloads", all=True)

# Download a specific named output
ml_client.jobs.download(name="my-job-name", output_name="model", download_path="./model")
```

### Validate a Job

```python
result = ml_client.jobs.validate(job)
if result.passed:
    print("Validation passed")
else:
    print(result.error_messages)
```

### Archive / Restore

```python
# Archive a job (hides from active list)
ml_client.jobs.archive(name="my-job-name")

# Restore an archived job
ml_client.jobs.restore(name="my-job-name")
```

### Show Services

```python
services = ml_client.jobs.show_services(name="my-job-name", node_index=0)
```

Returns services associated with a job node (e.g., Jupyter, SSH, TensorBoard endpoints).

---

## Operations Summary

| Operation | Method | Description |
|-----------|--------|-------------|
| **Submit** | `create_or_update(job)` | Creates/submits a job, auto-resolves dependencies |
| **Get** | `get(name)` | Retrieve a job by name |
| **List** | `list(parent_job_name=None, list_view_type=...)` | List jobs (active, archived, or all) |
| **Cancel** | `begin_cancel(name)` | Cancel a running job (async LRO) |
| **Stream** | `stream(name)` | Stream real-time logs to stdout |
| **Download** | `download(name, download_path, output_name, all)` | Download logs/outputs locally |
| **Validate** | `validate(job)` | Validate job before submission |
| **Archive** | `archive(name)` | Hide from active job list |
| **Restore** | `restore(name)` | Restore an archived job |
| **Services** | `show_services(name, node_index)` | Get associated services (endpoints, ports) |

---

## REST API Endpoints

The base ARM URL pattern is:

```
https://management.azure.com/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.MachineLearningServices/workspaces/{workspaceName}
```

The base RunHistory dataplane URL is:

```
https://{region}.api.azureml.ms/history/v1.0/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.MachineLearningServices/workspaces/{workspaceName}
```

### Job Operations Endpoints

| Operation | HTTP Method | URL Pattern | API Version | Notes |
|-----------|-------------|-------------|-------------|-------|
| **create_or_update** | `PUT` | `.../jobs/{name}` | `2025-01-01-preview` | LRO with ARM polling. `{name}` = user-provided or auto-generated (`adj_noun_suffix`). Returns `201` (create) or `200` (update). |
| **get** | `GET` | `.../jobs/{name}` | `2024-01-01-preview` | — |
| **list** | `GET` | `.../jobs` | `2024-01-01-preview` | Query params: `$skip`, `jobType`, `tag`, `listViewType`, `properties` |
| **begin_cancel** | `POST` | `.../jobs/{name}/cancel` | `2023-02-01-preview` | LRO. |
| **archive** | `PUT` | `.../jobs/{name}` | `2025-01-01-preview` | Sets `is_archived=true` via create_or_update internally. |
| **restore** | `PUT` | `.../jobs/{name}` | `2025-01-01-preview` | Sets `is_archived=false` via create_or_update internally. |
| **validate** | — | — | — | **Local only** — no REST call. Schema validation + optional remote compute check. |
| **stream** | `GET` | RunHistory dataplane | — | HTTP polling (1s→180s). Reads log files from blob storage. |
| **download** | `GET` | Blob storage direct | — | Resolves `azureml://` URIs → downloads from Azure Blob/ADLS Gen2. |
| **show_services** | `GET` | `.../runs/{name}/serviceinstances/{nodeIndex}` | RunHistory dataplane | Returns Jupyter/SSH/TensorBoard endpoints. |

> **Note on `name` vs `id`:** The URL path parameter `{name}` is the job's short name (e.g., `bold_banana_abc123def4`). The full ARM resource `id` (e.g., `/subscriptions/.../jobs/bold_banana_abc123def4`) is a read-only property returned by the server.

---

## Complete API Calls During `create_or_update`

When submitting a CommandJob, the SDK makes several preparatory REST calls before the final job creation PUT. The exact calls depend on whether assets are local or already registered.

### Full Execution Flow

```
create_or_update(job)
│
├─ 1. VALIDATE (local only, no REST call)
│
├─ 2. RESOLVE DEPENDENCIES
│  │
│  ├─ 2a. CODE ASSET
│  │   ├─ Compute SHA256 hash locally (respects .amlignore > .gitignore)
│  │   ├─ GET  .../codes/{name}/versions?hash={sha256}&hash_version=202208
│  │   │      → Dedup query: server returns matching asset if hash exists
│  │   │      → If MATCH: reuse existing asset, SKIP all upload steps
│  │   ├─ POST .../codes/{name}/versions/{ver}/pendingUploads
│  │   │      → Request SAS token for blob upload
│  │   ├─ PUT  https://{storage}.blob.core.windows.net/.../LocalUpload/{asset_id}/
│  │   │      → Upload code files to blob storage (SAS-authenticated)
│  │   └─ PUT  .../codes/{name}/versions/{ver}
│  │          → Register code asset with hash metadata
│  │
│  ├─ 2b. ENVIRONMENT
│  │   ├─ PUT  .../environments/{name}/versions/{ver}
│  │   │      → Register environment (only if inline, not a curated reference)
│  │   └─ PUT  Blob storage (SAS URL)
│  │          → Upload build context (only if Dockerfile/context provided)
│  │
│  ├─ 2c. COMPUTE
│  │   └─ GET  .../computes/{name}
│  │          → Verify compute exists, resolve ARM ID
│  │
│  └─ 2d. INPUTS (per input)
│      ├─ PUT  Blob storage (SAS URL)
│      │      → Upload local file/folder (only if input is a local path)
│      └─ GET  .../data/{name}/versions/{ver}
│             → Resolve ARM ID (only if input references an azureml data asset)
│
├─ 3. COLLECT GIT PROPERTIES (local git read, no REST call)
│
├─ 4. CONVERT TO REST OBJECT (local, no REST call)
│     └─ Auto-generates job name if not provided: {adjective}_{noun}_{10-char-suffix}
│
└─ 5. CREATE JOB
      └─ PUT  .../jobs/{name}  (API: 2025-01-01-preview)
```

### REST JSON Payload Examples

All `codeId`, `environmentId`, `computeId` are resolved to full ARM resource IDs by the SDK before the PUT is sent.

#### Simple Command Job

```json
{
  "properties": {
    "jobType": "Command",
    "displayName": "train-sklearn-model",
    "description": "Train a scikit-learn classifier",
    "experimentName": "my-experiment",
    "command": "python train.py --lr 0.01 --epochs 10",
    "codeId": "/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.MachineLearningServices/workspaces/{ws}/codes/my-code/versions/1",
    "environmentId": "/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.MachineLearningServices/workspaces/{ws}/environments/AzureML-sklearn-1.0/versions/latest",
    "computeId": "/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.MachineLearningServices/workspaces/{ws}/computes/gpu-cluster",
    "inputs": {
      "data": {
        "jobInputType": "uri_folder",
        "uri": "azureml://datastores/workspaceblobstore/paths/data/training/",
        "mode": "ReadOnlyMount"
      }
    },
    "outputs": {
      "model": {
        "jobOutputType": "uri_folder",
        "mode": "ReadWriteMount"
      }
    },
    "environmentVariables": {
      "PYTHONPATH": "./src"
    },
    "tags": {
      "team": "ml-engineering"
    },
    "limits": {
      "timeout": "PT1H"
    }
  }
}
```

#### Distributed PyTorch Training

```json
{
  "properties": {
    "jobType": "Command",
    "displayName": "distributed-pytorch-training",
    "experimentName": "deep-learning",
    "command": "python -m torch.distributed.launch --nproc_per_node ${{inputs.gpus_per_node}} train.py --data ${{inputs.data}} --output ${{outputs.model}}",
    "codeId": "/subscriptions/.../codes/training-code/versions/2",
    "environmentId": "/subscriptions/.../environments/pytorch-gpu/versions/1",
    "computeId": "/subscriptions/.../computes/gpu-cluster-v100",
    "inputs": {
      "data": {
        "jobInputType": "uri_folder",
        "uri": "azureml:imagenet-dataset:1",
        "mode": "ReadOnlyMount"
      },
      "gpus_per_node": {
        "jobInputType": "literal",
        "value": "4"
      }
    },
    "outputs": {
      "model": {
        "jobOutputType": "uri_folder",
        "mode": "ReadWriteMount"
      },
      "logs": {
        "jobOutputType": "uri_folder",
        "mode": "ReadWriteMount"
      }
    },
    "distribution": {
      "distributionType": "PyTorch",
      "processCountPerInstance": 4
    },
    "resources": {
      "instanceCount": 4,
      "instanceType": "Standard_NC24rs_v3",
      "shmSize": "8g"
    },
    "limits": {
      "timeout": "PT24H"
    },
    "identity": {
      "identityType": "Managed"
    }
  }
}
```

#### MPI Distributed Job

```json
{
  "properties": {
    "jobType": "Command",
    "displayName": "horovod-mpi-training",
    "experimentName": "distributed-training",
    "command": "python train_horovod.py --epochs 50",
    "codeId": "/subscriptions/.../codes/horovod-src/versions/1",
    "environmentId": "/subscriptions/.../environments/horovod-env/versions/1",
    "computeId": "/subscriptions/.../computes/mpi-cluster",
    "distribution": {
      "distributionType": "Mpi",
      "processCountPerInstance": 2
    },
    "resources": {
      "instanceCount": 8
    },
    "limits": {
      "timeout": "PT12H"
    }
  }
}
```

#### Minimal Job (literal inputs, no code upload)

```json
{
  "properties": {
    "jobType": "Command",
    "displayName": "quick-test",
    "experimentName": "Default",
    "command": "echo Hello ${{inputs.greeting}}",
    "environmentId": "/subscriptions/.../environments/AzureML-minimal/versions/latest",
    "computeId": "/subscriptions/.../computes/cpu-cluster",
    "inputs": {
      "greeting": {
        "jobInputType": "literal",
        "value": "World"
      }
    }
  }
}
```

#### Field Mapping Reference (Python SDK → JSON)

| Python SDK | JSON field | Notes |
|-----------|------------|-------|
| `command` | `command` | — |
| `code` | `codeId` | Resolved to full ARM ID before PUT |
| `environment` | `environmentId` | Resolved to full ARM ID |
| `compute` | `computeId` | Resolved to full ARM ID |
| `display_name` | `displayName` | Defaults to job name |
| `experiment_name` | `experimentName` | Defaults to cwd folder name |
| `environment_variables` | `environmentVariables` | — |
| `inputs` | `inputs` | Each has `jobInputType` + `uri`/`value` + `mode` |
| `outputs` | `outputs` | Each has `jobOutputType` + `mode` |
| `distribution` | `distribution` | `distributionType`: `PyTorch` / `Mpi` / `TensorFlow` |
| `resources` | `resources` | `instanceCount`, `instanceType`, `shmSize` |
| `limits` | `limits` | `timeout` in ISO 8601 duration (e.g., `PT1H`) |
| `identity` | `identity` | `identityType`: `Managed` / `AMLToken` / `UserIdentity` |
| `queue_settings` | `queueSettings` | `jobTier`, `priority` |
| `tags` | `tags` | Key-value dict |

### Code Asset Hashing (SHA256)

The hash determines whether code needs re-uploading. It is computed as:

```
SHA256(
  "<file_count>"
  + "#file1.txt#<size>"           ← metadata per file (sorted case-insensitive by path)
  + "#folder1/file2.txt#<size>"
  + ...
  + <file1_content_chunks>        ← actual file content (1024-byte chunks)
  + <file2_content_chunks>
  + ...
)
```

- Files matching `.amlignore` (or `.gitignore` if no `.amlignore`) are excluded from both hash and upload
- Hash version is fixed at `202208`
- Hash is computed at `Code()` instantiation time

### Deduplication Layers

| Layer | Where | Check | Result if Match |
|-------|-------|-------|-----------------|
| **1. Server-side** | `GET .../codes/{name}/versions?hash=...` | Server checks if any code version has this hash | Reuse existing asset — skip upload entirely |
| **2. Blob-level** | `HEAD` blob at `LocalUpload/{asset_id}/{name}` | Blob metadata has `upload_confirmed: true` | Skip re-upload — reuse blob path |

#### Layer 1: Server Hash Query (Code Assets Only)

- SDK sends the SHA256 hash as a query parameter: `GET .../codes/{name}/versions?hash={sha256}&hash_version=202208`
- If server returns a matching asset, SDK calls `self.get()` to retrieve it and reuses the existing code asset ID
- **Entire upload pipeline is bypassed** — no SAS token, no blob upload, no code registration
- Only applies to **code assets** (registered ARM resources); inputs are anonymous blobs and have no server-side registry to query

#### Layer 2: Blob Metadata Check (Code + Inputs)

The SDK calls `check_blob_exists()` which issues Azure Blob Storage API calls:

```
blob_client.get_blob_properties()
→ HEAD https://{account}.blob.core.windows.net/{container}/LocalUpload/{hash}/{name}
```

**Flow:**

| Step | Action | API Call |
|------|--------|---------|
| 1 | Check current blob path (`LocalUpload/{hash}/{name}`) | `HEAD` blob (get_blob_properties) |
| 2 | Check legacy path (`ExperimentRun/dcid.{name}/{name}`) | `HEAD` blob (get_blob_properties) |
| 3a | If metadata contains `{"upload_confirmed": "true"}` | → Raise `AssetNotChangedError` → **skip upload** |
| 3b | If blob exists but no confirmation metadata | → Set `overwrite=True` → re-upload (partial upload detected) |
| 3c | If `ResourceNotFoundError` (blob doesn't exist) | → Proceed with full upload |

**Confirmation metadata:** After a successful upload, the SDK writes metadata to the blob via:

```
blob_client.set_blob_metadata({"name": ..., "version": ..., "upload_confirmed": "true"})
→ PUT https://{storage}.blob.core.windows.net/{container}/{blob}?comp=metadata
```

This ensures partial uploads (interrupted mid-transfer) are detected and overwritten.

#### Why Two Layers?

| | Layer 1 (Server Hash Query) | Layer 2 (Blob HEAD Check) |
|--|---|---|
| **Skips** | Entire code registration + upload | Just the blob upload |
| **Still happens without it** | New ARM code asset registered (pointing to same blob) | Redundant blob transfer |
| **Applies to** | Code assets only | Both code and input uploads |
| **Saves** | Bandwidth + ARM registration call | Bandwidth only |

Layer 1 is the **optimization** — it short-circuits everything. Layer 2 is the **safety net** — prevents redundant blob transfers even if Layer 1 is absent or misses. This is also why **inputs only have Layer 2**: they are anonymous blobs with no ARM resource to deduplicate against.

### API Call Count

| Scenario | REST Calls |
|----------|------------|
| **Best case** (code hash matches, curated env, remote data) | 4: code dedup GET → code asset GET → compute GET → job PUT |
| **Typical** (code hash matches, curated env, 1 azureml input) | 5: code dedup GET → code asset GET → compute GET → input resolve GET → job PUT |
| **Worst case** (new code, inline env with Dockerfile, N local inputs) | 8+N: code dedup GET → SAS POST → blob PUT → code PUT → env PUT → env blob PUT → compute GET → N×input blob PUT → job PUT |

---

## Two API Planes

The SDK uses **two separate API planes** for job operations:

| Plane | Base URL | Purpose |
|-------|----------|---------|
| **ARM (Control Plane)** | `https://management.azure.com/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.MachineLearningServices/workspaces/{ws}` | Resource lifecycle — create, get, list, cancel, archive jobs |
| **RunHistory (Data Plane)** | `https://{region}.api.azureml.ms/history/v1.0/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.MachineLearningServices/workspaces/{ws}` | Execution data — status, logs, outputs, services |

ARM manages **what the job is**. RunHistory serves **what the job is doing** (lives closer to compute).

---

## Stream Operation (Deep Dive)

### Entry Point

```
stream(name)
 ├─ GET .../jobs/{name}                        → ARM (fetch job, validate it exists)
 ├─ Check: pipeline child? → raise PipelineChildJobError
 └─ stream_logs_until_completion()             → All subsequent calls go to DATAPLANE
```

### Polling Loop

```
GET .../runs/{name}                            → RunHistory dataplane (initial)
│   Returns: { status, log_files: {"filename": "SAS blob URL"}, warnings, error }
│
WHILE status in [NOT_STARTED, QUEUED, PREPARING, PROVISIONING, STARTING, RUNNING, FINALIZING]:
│
├─ sleep(adaptive sigmoid)
│   • Formula: int(MAX / (1 + 100 * exp(-elapsed/20)))
│   • MIN = 2s, MAX = 60s, tapers near ~180s total elapsed
│
├─ GET .../runs/{name}                         → RunHistory dataplane (poll status + log_files)
│
├─ Discover log files:
│   • Priority 1: Datastore listing (if available)
│   • Priority 2: RunDetails.log_files dict (fallback)
│
├─ Filter & sort logs by pattern:
│   • Preferred:  user_logs/std_log[0-9]*(?:_ps)?\.txt
│   • Command:    azureml-logs/[0-9]{2}.*\.txt  (e.g., 00_docker.txt, 70_driver_log.txt)
│   • Sweep:      azureml-logs/hyperdrive\.txt
│   • Pipeline:   logs/azureml/executionlogs\.txt
│
├─ FOR each log file:
│   ├─ GET https://{storage}.blob.core.windows.net/...?sv=...&sig=...
│   │      → Download full log text via SAS URL (timeout: 5s connect, 120s read)
│   │      → 404 = empty string (file not created yet)
│   │
│   └─ _incremental_print():
│       ├─ Split into lines, skip already-printed lines (tracked per file)
│       ├─ Print ONLY new lines to stdout
│       └─ Update: processed_logs["std_log.txt"] = total_line_count
│
└─ If FINALIZING + "Finalizing run..." in content → break

POST-LOOP:
├─ Print execution summary (RunId, Studio Web View URL)
├─ Print warnings (if any)
└─ If FAILED:
    ├─ raise_exception_on_failed_job=True (default) → raise JobException
    └─ raise_exception_on_failed_job=False → print error only
```

### Stream API Calls

| When | HTTP | URL | Plane |
|------|------|-----|-------|
| Once at start | `GET` | `.../jobs/{name}` | ARM (control plane) |
| Once at start | `GET` | `.../runs/{name}` | RunHistory (data plane) |
| Every poll iteration | `GET` | `.../runs/{name}` | RunHistory (data plane) |
| Every poll, per log file | `GET` | `https://{storage}.blob.core.windows.net/...?sig=...` | Azure Blob Storage (SAS) |

### Key Behaviors

| Aspect | Details |
|--------|---------|
| **Log URLs** | SAS-authenticated blob URLs embedded in RunDetails response — no extra auth needed |
| **Incremental printing** | Re-downloads full log file each poll, but prints only new lines (tracks line count per file) |
| **Ctrl+C** | Raises `JobException` but **job keeps running** on backend |
| **Pipeline children** | Not supported — raises `PipelineChildJobError` |
| **Polling frequency** | ~2s initially → ~60s max, sigmoid curve over ~3 min elapsed |
| **Timeouts** | 5s connect, 120s read per log file download |

---

## Download Operation

### Download Call Chain

```
download(name)
 ├─ get(name)                          → ARM: GET .../jobs/{name}
 ├─ _get_named_output_uri(name, ...)   → RunHistory dataplane: resolve output URIs
 │   └─ aml_datastore_path_exists()    → Verify paths in datastore
 └─ download_artifact_from_aml_uri()   → Direct blob download
     ├─ AzureMLDatastorePathUri(uri)   → Parse azureml:// URI
     ├─ get_storage_client()           → BlobStorageClient or Gen2StorageClient
     └─ storage_client.download()      → HTTP GET from Azure Blob Storage
```
