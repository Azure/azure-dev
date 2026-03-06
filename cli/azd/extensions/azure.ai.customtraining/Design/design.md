# Azure AI Custom Training Extension — Design Document

## 1. User Experience

### 1.1 Extension Installation

```bash
azd extension install azure.ai.customtraining
```

### 1.2 Two Modes of Operation

Following the same pattern as the finetune extension, the custom training extension supports **two modes**:

#### Mode 1: Init → Create (Interactive Setup)

```bash
# Step 1: Initialize project configuration (interactive prompts)
azd ai training init

# Step 2: Use commands (config read from azd environment)
azd ai training job create --file job.yaml
azd ai training job stream --name llama-sft
```

`init` prompts for:
- Azure Subscription
- Azure AI Foundry Project endpoint (e.g., `https://account.services.ai.azure.com/api/projects/project-name`)

Stores configuration in the azd environment for subsequent commands.

#### Mode 2: One-Liner (Implicit Init via Flags)

```bash
# Single command — no prior init needed
azd ai training job create --file job.yaml \
  --subscription <sub-id> \
  --project-endpoint <endpoint-url>
```

When `--subscription` and `--project-endpoint` flags are provided on any `job` subcommand, the extension **implicitly initializes** the azd environment before executing the command. This enables:
- CI/CD pipelines (non-interactive)
- Quick one-off submissions
- No separate `azd ai training init` step required

**How it works** (same as finetune extension):
1. `PersistentPreRunE` on the `job` command group calls `validateOrInitEnvironment()`
2. If env vars already set → proceed (warn if flags also provided — they're ignored)
3. If env vars missing AND both flags provided → implicit init (parse endpoint, resolve project, set env vars)
4. If env vars missing AND flags incomplete → error with guidance

**Flag precedence:** Stored environment values take priority. If already initialized, `--subscription` and `--project-endpoint` are ignored with a warning.

### 1.3 Command Structure

```
azd ai training
├── init                          # Initialize project configuration
├── job
│   ├── create  --file            # Submit a command job from YAML
│   ├── get     --name            # Get job details
│   ├── list    [--top]           # List jobs
│   ├── cancel  --name            # Cancel a running job
│   ├── delete  --name            # Delete a job
│   ├── stream  --name            # Stream job logs
│   └── download --name           # Download job outputs
```

**Persistent flags on `job` command group** (available to all subcommands):

| Flag | Short | Description |
|------|-------|-------------|
| `--subscription` | `-s` | Azure subscription ID (enables implicit init) |
| `--project-endpoint` | `-e` | Azure AI Foundry project endpoint URL |

---

## 2. Command Details

### 2.1 `azd ai training job create`

Submit a command job to Azure AI Foundry from a YAML definition file.

```bash
# Mode 1: After init
azd ai training job create --file job.yaml

# Mode 2: One-liner (no prior init needed)
azd ai training job create --file job.yaml \
  --subscription <sub-id> \
  --project-endpoint https://account.services.ai.azure.com/api/projects/project-name
```

**Flags:**

| Flag | Required | Description |
|------|----------|-------------|
| `--file` / `-f` | Yes | Path to YAML job definition file |
| `--subscription` / `-s` | No* | Azure subscription ID (inherited from `job` group) |
| `--project-endpoint` / `-e` | No* | Azure AI Foundry project endpoint URL (inherited from `job` group) |

\* Required if `azd ai training init` has not been run.

**YAML job definition schema:**

```yaml
name: llama-sft                                              # Required: unique job identifier
command: "python train.py --data ${{inputs.training_data}}"  # Required: command to execute
environment: myregistry.azurecr.io/training-image:v1         # Required: container image URI
compute: gpu-cluster                                         # Required: compute target name

code: ./src                                                  # Optional: local path to source code

inputs:                                                      # Optional: input data bindings
  training_data:
    type: uri_folder
    path: ./data/train                                       # Local path (auto-uploaded) or remote URI
    mode: download                                           # download | ro_mount
  llama_base_model:
    type: uri_folder
    path: azureml://datastores/data/paths/models/llama
  epochs:
    value: "10"                                              # Literal value

outputs:                                                     # Optional: output data bindings
  model:
    type: uri_folder
    mode: rw_mount

distribution: PyTorch                                        # Optional: PyTorch | Mpi | TensorFlow
instance_count: 4                                            # Optional: default 1
process_per_node: 8                                          # Optional: processes per instance

environment_variables:                                       # Optional: env vars for the process
  LEARNING_RATE: "0.001"
  NUM_EPOCHS: "10"

description: "Fine-tune LLaMA model"                        # Optional
display_name: "LLaMA SFT Training"                          # Optional
timeout: PT24H                                               # Optional: ISO 8601 duration
tags:                                                        # Optional: key-value pairs
  team: ml-engineering
```

**Execution flow displayed to user:**

```
Creating command job: llama-sft

| Step 1: Uploading code (./src)...
✓ Code uploaded (dataset: code-llama-sft-v1)

| Step 2: Uploading input data...
  ├─ training_data: uploading ./data/train...
✓ Input data uploaded

| Step 3: Resolving compute...
✓ Compute resolved: gpu-cluster

| Step 4: Submitting job...
✓ Job submitted successfully!

──────────────────────────────────────────────────
  Name:         llama-sft
  Status:       Starting
  Compute:      gpu-cluster
  Environment:  myregistry.azurecr.io/training-image:v1
  Distribution: PyTorch (4 instances)
──────────────────────────────────────────────────
```

**Input types:**

| Input `path` | Behavior |
|-------------|----------|
| Local path (e.g., `./data/train`) | Uploaded via dataset API + azcopy, replaced with dataset ID in payload |
| Remote URI (e.g., `azureml://...` or `https://...`) | Passed through as-is |
| Literal `value` (e.g., `"10"`) | Passed as literal input, no upload |

---

### 2.2 `azd ai training job get`

Retrieve details of a specific job.

```bash
azd ai training job get --name llama-sft
```

**Output:**

```
──────────────────────────────────────────────────
  Name:         llama-sft
  Display Name: LLaMA SFT Training
  Status:       Running
  Job Type:     Command
  Compute:      gpu-cluster
  Environment:  myregistry.azurecr.io/training-image:v1
  Distribution: PyTorch (4 instances)
  Created:      2026-03-06T08:00:00Z
  Duration:     1h 23m
──────────────────────────────────────────────────

Inputs:
  training_data    uri_folder  dataset://training-data-v1
  epochs           literal     10

Outputs:
  model            uri_folder  dataset://output-model-v1
```

Supports `--output json` for machine-readable format.

---

### 2.3 `azd ai training job list`

List all jobs in the project.

```bash
azd ai training job list [--top 20]
```

**Output:**

```
NAME                    STATUS      COMPUTE       CREATED              DURATION
llama-sft               Running     gpu-cluster   2026-03-06T08:00:00  1h 23m
test-job-001            Completed   cpu-cluster   2026-03-05T14:30:00  0h 12m
failed-experiment       Failed      gpu-cluster   2026-03-04T09:15:00  0h 03m
```

Supports `--output json`.

---

### 2.4 `azd ai training job cancel`

Cancel a running job.

```bash
azd ai training job cancel --name llama-sft
```

**Output:**

```
✓ Job 'llama-sft' cancellation requested.
```

---

### 2.5 `azd ai training job delete`

Delete a job.

```bash
azd ai training job delete --name llama-sft
```

**Output:**

```
? Are you sure you want to delete job 'llama-sft'? [y/N] y
✓ Job 'llama-sft' deleted.
```

---

### 2.6 `azd ai training job stream`

Stream logs from a running (or completed) job. Uses polling-based artifact reading.

```bash
azd ai training job stream --name llama-sft
```

**Output:**

```
Streaming logs for job 'llama-sft'...
(Discovering log files...)

--- user_logs/std_log.txt ---
[2026-03-06 08:01:12] Starting training...
[2026-03-06 08:01:13] Loading dataset from /mnt/inputs/training_data
[2026-03-06 08:01:45] Epoch 1/10 - loss: 2.3456 - accuracy: 0.45
[2026-03-06 08:02:30] Epoch 2/10 - loss: 1.8721 - accuracy: 0.58
...
[2026-03-06 09:23:10] Training complete. Model saved to /mnt/outputs/model

✓ Job 'llama-sft' completed with status: Completed
```

**How it works:**
1. Discover log files via `GET .../jobs/{id}/artifacts/path?path=user_logs`
2. Read initial tail via `GET .../artifacts/getcontent/{logPath}?tailBytes=8192`
3. Poll for new content with `offset` at 1-2s intervals (backoff to 5s on idle)
4. Stop when job reaches terminal status (Completed/Failed/Canceled)

See §6 for detailed design.

---

### 2.7 `azd ai training job download`

Download job output artifacts to a local directory.

```bash
azd ai training job download --name llama-sft [--output-name model] [--path ./downloads]
```

**Flags:**

| Flag | Required | Description |
|------|----------|-------------|
| `--name` | Yes | Job ID |
| `--output-name` | No | Specific output to download (e.g., `model`). Default: all outputs |
| `--path` | No | Local directory to download into. Default: `./` |

**Output:**

```
Downloading artifacts for job 'llama-sft'...

| Listing artifacts...
✓ Found 3 artifacts (total: 1.2 GB)

| Downloading...
  ├─ outputs/model/model.bin         (1.1 GB) ██████████ 100%
  ├─ outputs/model/config.json       (2 KB)   ██████████ 100%
  └─ outputs/model/tokenizer.json    (4 KB)   ██████████ 100%

✓ Downloaded to ./downloads/
```

**How it works:**
1. Verify job exists via `GET .../jobs/{id}`
2. List artifacts via `GET .../jobs/{id}/artifacts`
3. Get SAS URIs via `GET .../jobs/{id}/artifacts/prefix/contentinfo?path={prefix}`
4. Download via azcopy from SAS URIs

See §7 for detailed design.

---

## 3. API Details — Job Creation Flow

### 3.0 Auth Strategy

Two token scopes are required:

| Scope | Used For | APIs |
|-------|----------|------|
| `https://ai.azure.com/.default` | Data Plane | Jobs, Datasets |
| `https://management.azure.com/.default` | Control Plane (ARM) | Compute |

### 3.1 End-to-End Flow with API Calls

```
job create --file job.yaml
│
├─ 1. PARSE YAML
│     → validate required fields (name, command, environment, compute)
│     → detect local paths in code/inputs
│
├─ 2. UPLOAD CODE (if `code` is local path)
│     │
│     ├─ 2a. POST api/projects/{project}/datasets/{name}/versions/{ver}/startPendingUpload
│     │       Auth: ai.azure.com    Owner: William Bauman    Status: Existing
│     │       → Returns: SAS URI for blob upload
│     │
│     ├─ 2b. azcopy copy "{localPath}/*" "{sasUri}" --recursive
│     │       → Uploads code files to blob storage
│     │
│     └─ 2c. PATCH api/projects/{project}/datasets/{name}/versions/{ver}
│             Auth: ai.azure.com    Owner: William Bauman    Status: Existing
│             → Confirms dataset, returns dataset resource ID as codeId
│
├─ 3. UPLOAD INPUTS (for each input with local `path`)
│     │
│     ├─ 3a. POST api/projects/{project}/datasets/{name}/versions/{ver}/startPendingUpload
│     │       Auth: ai.azure.com    Owner: William Bauman    Status: Existing
│     │       → Returns: SAS URI for blob upload
│     │
│     ├─ 3b. azcopy copy "{localPath}/*" "{sasUri}" --recursive
│     │       → Uploads input files to blob storage
│     │
│     └─ 3c. PATCH api/projects/{project}/datasets/{name}/versions/{ver}
│             Auth: ai.azure.com    Owner: William Bauman    Status: Existing
│             → Confirms dataset, returns dataset resource ID
│
├─ 4. RESOLVE COMPUTE
│     │
│     └─ GET Microsoft.CognitiveServices/accounts/{accountName}/computes/{computeName}
│           Auth: management.azure.com (ARM)    Owner: Rajat Garg    Status: New
│           Note: Control Plane only today, pending data plane exposure
│           → Returns: ARM resource ID for computeId
│
├─ 5. BUILD PAYLOAD
│     ├─ codeId        = dataset resource ID (from step 2c)
│     ├─ environmentId = raw image URI string (pass-through, no resolution)
│     ├─ computeId     = ARM resource ID (from step 4)
│     ├─ inputs        = dataset IDs (uploaded) or URIs (remote) or literal values
│     └─ outputs       = as defined in YAML
│
└─ 6. SUBMIT JOB
      │
      └─ PUT api/projects/{project}/jobs/{id}
            Auth: ai.azure.com    Owner: Savitha Mittal    Status: New
            → Returns: job object with ID and status
```

### 3.1.1 Other Job Operations — API Map

```
job get --name {id}
└─ GET api/projects/{project}/jobs/{id}
      Auth: ai.azure.com    Owner: Savitha Mittal    Status: New

job list [--top N]
└─ GET api/projects/{project}/jobs
      Auth: ai.azure.com    Owner: Savitha Mittal    Status: New

job cancel --name {id}
└─ POST api/projects/{project}/jobs/{id}/cancel
      Auth: ai.azure.com    Owner: Savitha Mittal    Status: New

job delete --name {id}
└─ DELETE api/projects/{project}/jobs/{id}
      Auth: ai.azure.com    Owner: Savitha Mittal    Status: New

job stream --name {id}  (polling-based log streaming via artifacts API)
├─ GET api/projects/{project}/jobs/{id}
│     → Check job status (terminal state = stop polling)
├─ GET api/projects/{project}/jobs/{id}/artifacts/path?path=user_logs
│     → Discover log files (e.g., user_logs/std_log.txt)
└─ GET api/projects/{project}/jobs/{id}/artifacts/getcontent/user_logs/std_log.txt?tailBytes=N&offset=M
      → Poll at 1-2s intervals, read new bytes via offset
      → X-VW-Content-Length header indicates total blob size
      → Exponential backoff (up to 5s) on empty responses
      Auth: ai.azure.com    Owner: Savitha Mittal    Status: New (PR)

job download --name {id}
├─ GET api/projects/{project}/jobs/{id}/artifacts
│     → List all artifacts to discover output files
├─ GET api/projects/{project}/jobs/{id}/artifacts/prefix/contentinfo?path={outputPrefix}
│     → Get SAS URIs for output artifacts
└─ azcopy download from SAS URIs
      Auth: ai.azure.com    Owner: Savitha Mittal    Status: New (PR)
```

### 3.1.2 Complete API Dependency Matrix

**Data Plane Base URL:** `https://{accountName}.services.ai.azure.com/api/projects/{projectName}`
**Control Plane Base URL:** `https://management.azure.com/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.CognitiveServices/accounts/{accountName}`

| Step | Method | Endpoint | Auth Scope | Owner | Existing? |
|------|--------|----------|------------|-------|-----------|
| Code upload (SAS) | `POST` | `.../datasets/{name}/versions/{ver}/startPendingUpload` | ai.azure.com | William Bauman | ✅ Existing |
| Code upload (confirm) | `PATCH` | `.../datasets/{name}/versions/{ver}` | ai.azure.com | William Bauman | ✅ Existing |
| Input upload (SAS) | `POST` | `.../datasets/{name}/versions/{ver}/startPendingUpload` | ai.azure.com | William Bauman | ✅ Existing |
| Input upload (confirm) | `PATCH` | `.../datasets/{name}/versions/{ver}` | ai.azure.com | William Bauman | ✅ Existing |
| Compute resolve | `GET` | `.../computes/{name}` (ARM) | management.azure.com | Rajat Garg | 🆕 New (Control Plane) |
| Job create | `PUT` | `.../jobs/{id}` | ai.azure.com | Savitha Mittal | 🆕 New |
| Job get | `GET` | `.../jobs/{id}` | ai.azure.com | Savitha Mittal | 🆕 New |
| Job list | `GET` | `.../jobs` | ai.azure.com | Savitha Mittal | 🆕 New |
| Job cancel | `POST` | `.../jobs/{id}/cancel` | ai.azure.com | Savitha Mittal | 🆕 New |
| Job delete | `DELETE` | `.../jobs/{id}` | ai.azure.com | Savitha Mittal | 🆕 New |
| List artifacts | `GET` | `.../jobs/{id}/artifacts` | ai.azure.com | Savitha Mittal | 🆕 New (PR) |
| List artifacts in path | `GET` | `.../jobs/{id}/artifacts/path?path={prefix}` | ai.azure.com | Savitha Mittal | 🆕 New (PR) |
| Get artifact content | `GET` | `.../jobs/{id}/artifacts/getcontent/{path}` | ai.azure.com | Savitha Mittal | 🆕 New (PR) |
| Stream artifact content | `GET` | `.../jobs/{id}/artifacts/content/{path}` | ai.azure.com | Savitha Mittal | 🆕 New (PR) |
| Get artifact SAS URI | `GET` | `.../jobs/{id}/artifacts/contentinfo?path={path}` | ai.azure.com | Savitha Mittal | 🆕 New (PR) |
| Get artifact metadata | `GET` | `.../jobs/{id}/artifacts/metadata?path={path}` | ai.azure.com | Savitha Mittal | 🆕 New (PR) |
| Batch SAS URIs | `GET` | `.../jobs/{id}/artifacts/prefix/contentinfo?path={prefix}` | ai.azure.com | Savitha Mittal | 🆕 New (PR) |
| Append artifact | `POST` | `.../jobs/{id}/artifacts/append/{path}` | ai.azure.com | Savitha Mittal | 🆕 New (PR, server-side only) |

---

### 3.2 Step 2 — Code Upload via Dataset API

When `code` is a local directory path, CLI uploads it as a dataset and obtains a `codeId`.

> **Note on deduplication:** V1 always re-uploads code and input data on each `job create`. The Foundry dataset API does not yet support hash-based querying for dedup. This is acceptable because source code is typically small (best practice). When the dataset API adds hash support in the future, we can add client-side SHA256 hashing + server-side dedup queries to skip redundant uploads — same pattern as the AML SDK's two-layer dedup (see command-job-guide.md).

#### Step 2a: Request Pending Upload

```
POST {endpoint}/api/projects/{project}/datasets/{datasetName}/versions/{version}/startPendingUpload?api-version=2025-09-01
```

- **Dataset naming convention**: `code-{jobName}` (e.g., `code-llama-sft`)
- **Version**: `1` (auto-assigned)

**Request Body:**
```json
{
  "pendingUploadType": "BlobReference"
}
```

**Response (200):**
```json
{
  "blobReference": {
    "blobUri": "https://{storageAccount}.blob.core.windows.net/{container}/datasets/code-llama-sft/v1",
    "credential": {
      "sasUri": "https://{storageAccount}.blob.core.windows.net/{container}/datasets/code-llama-sft/v1?sv=...&sig=...",
      "credentialType": "SAS"
    }
  },
  "pendingUploadId": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
  "pendingUploadType": "BlobReference"
}
```

#### Step 2b: Upload Code Files via AzCopy

```bash
azcopy copy "./src/*" "https://{storageAccount}.blob.core.windows.net/{container}/datasets/code-llama-sft/v1?sv=...&sig=..." --recursive
```

Same azcopy runner pattern as `azure.ai.models` extension — auto-detect or auto-install azcopy binary.

#### Step 2c: Create/Confirm Dataset Version

```
PATCH {endpoint}/api/projects/{project}/datasets/code-llama-sft/versions/1?api-version=2025-09-01
```

**Request Body:**
```json
{
  "dataUri": "https://{storageAccount}.blob.core.windows.net/{container}/datasets/code-llama-sft/v1",
  "dataType": "uri_folder",
  "description": "Code for job llama-sft"
}
```

**Response (200/201):**
```json
{
  "id": "/subscriptions/{sub}/resourceGroups/{rg}/providers/.../datasets/code-llama-sft/versions/1",
  "name": "code-llama-sft",
  "version": "1",
  "dataUri": "https://...",
  "dataType": "uri_folder"
}
```

**Result**: `codeId` = response `id` field (full resource ID)

---

### 3.3 Step 3 — Input Data Upload via Dataset API

For each input in the YAML that has a local `path`, repeat the same dataset upload flow.

#### Input types and handling:

| YAML Input | Action | Value in Job Payload |
|-----------|--------|---------------------|
| `path: ./data/train` (local) | Upload via dataset API (steps 3a-3c) | `{ "jobInputType": "uri_folder", "uri": "{dataset_resource_id}", "mode": "Download" }` |
| `path: azureml://datastores/...` (remote) | Pass through, no upload | `{ "jobInputType": "uri_folder", "uri": "azureml://...", "mode": "Download" }` |
| `path: https://...` (remote URL) | Pass through, no upload | `{ "jobInputType": "uri_folder", "uri": "https://...", "mode": "Download" }` |
| `value: "10"` (literal) | No upload | `{ "jobInputType": "literal", "value": "10" }` |

#### Step 3a: Request Pending Upload (per input)

```
POST {endpoint}/api/projects/{project}/datasets/{inputName}/versions/1/startPendingUpload?api-version=2025-09-01
```

- **Dataset naming convention**: `input-{jobName}-{inputKey}` (e.g., `input-llama-sft-training_data`)

Request/response same structure as Step 2a.

#### Step 3b: Upload Input Files via AzCopy

```bash
azcopy copy "./data/train/*" "{sasUri}" --recursive
```

#### Step 3c: Create/Confirm Dataset Version

```
PATCH {endpoint}/api/projects/{project}/datasets/input-llama-sft-training_data/versions/1?api-version=2025-09-01
```

Request/response same structure as Step 2c.

---

### 3.4 Step 4 — Resolve Compute

Resolve the user-provided compute name to a full ARM resource ID. This uses the **ARM control plane** with a separate token scope.

The `compute` field in YAML accepts two formats:
- **Name only**: `compute: gpu-cluster` → CLI attempts ARM resolution
- **Full ARM ID**: `compute: /subscriptions/.../computes/gpu-cluster` → used as-is, skip resolution

Detection: if value starts with `/subscriptions/`, it's a full ARM ID.

**ARM resolution (when name only):**

```
GET https://management.azure.com/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.CognitiveServices/accounts/{accountName}/computes/{computeName}?api-version=2025-09-01
```

**Auth:** Bearer token with scope `https://management.azure.com/.default`

**Fallback:** If ARM GET returns 401/403 (insufficient RBAC), the CLI will error with a message:
```
✗ Failed to resolve compute 'gpu-cluster': insufficient permissions.
  Provide the full ARM resource ID in your YAML instead:
  compute: /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.CognitiveServices/accounts/{account}/computes/gpu-cluster
```

**Response (200):**
```json
{
  "id": "/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.MachineLearningServices/virtualworkspaces/{ws}/computes/gpu-cluster",
  "name": "gpu-cluster",
  "properties": {
    "computeType": "AmlCompute",
    "provisioningState": "Succeeded"
  }
}
```

**Result**: `computeId` = response `id` field

> **⚠️ Assumption:** A Foundry compute GET API exists at this path. If not available, the user may need to provide the full ARM ID in the YAML, and we skip resolution.

---

### 3.5 Step 5 — Build Job Payload

Assemble the final REST payload from resolved IDs and YAML values.

**Field mapping (YAML → JSON payload):**

| YAML Field | JSON Path | Resolution |
|-----------|-----------|------------|
| `name` | URL path: `.../jobs/{name}` | Direct |
| `command` | `properties.command` | Direct |
| `code` | `properties.codeId` | → dataset resource ID (step 2) |
| `environment` | `properties.environmentId` | Direct pass-through (image URI string) |
| `compute` | `properties.computeId` | → ARM resource ID (step 4) |
| `inputs` | `properties.inputs` | Per-input: dataset ID, remote URI, or literal |
| `outputs` | `properties.outputs` | Direct |
| `distribution` | `properties.distribution` | `{ "distributionType": "PyTorch", "processCountPerInstance": N }` |
| `instance_count` | `properties.resources.instanceCount` | Direct |
| `environment_variables` | `properties.environmentVariables` | Direct |
| `description` | `properties.description` | Direct |
| `display_name` | `properties.displayName` | Direct |
| `timeout` | `properties.limits.timeout` | Direct (ISO 8601) |
| `tags` | `properties.tags` | Direct |

---

### 3.6 Step 6 — Submit Job

```
PUT {endpoint}/api/projects/{project}/jobs/{id}?api-version=2025-09-01
```

> **Note:** The server-side `FoundryJobController` uses `PUT /{id}` (`FoundryJobs_CreateOrUpdate`). The job ID is provided in the URL path (from the YAML `name` field).

**Request Body (complete example):**
```json
{
  "properties": {
    "jobType": "Command",
    "displayName": "LLaMA SFT Training",
    "description": "Fine-tune LLaMA model",
    "command": "python train.py --data ${{inputs.training_data}} --epochs ${{inputs.epochs}}",
    "codeId": "/subscriptions/{sub}/resourceGroups/{rg}/providers/.../datasets/code-llama-sft/versions/1",
    "environmentId": "myregistry.azurecr.io/training-image:v1",
    "computeId": "/subscriptions/{sub}/resourceGroups/{rg}/providers/.../computes/gpu-cluster",
    "inputs": {
      "training_data": {
        "jobInputType": "uri_folder",
        "uri": "/subscriptions/{sub}/resourceGroups/{rg}/providers/.../datasets/input-llama-sft-training_data/versions/1",
        "mode": "Download"
      },
      "epochs": {
        "jobInputType": "literal",
        "value": "10"
      }
    },
    "outputs": {
      "model": {
        "jobOutputType": "uri_folder",
        "mode": "ReadWriteMount"
      }
    },
    "distribution": {
      "distributionType": "PyTorch",
      "processCountPerInstance": 8
    },
    "resources": {
      "instanceCount": 4
    },
    "environmentVariables": {
      "LEARNING_RATE": "0.001",
      "NUM_EPOCHS": "10"
    },
    "limits": {
      "timeout": "PT24H"
    },
    "tags": {
      "team": "ml-engineering"
    }
  }
}
```

**Response (201 Created):**
```json
{
  "id": "/subscriptions/{sub}/resourceGroups/{rg}/providers/.../jobs/llama-sft",
  "name": "llama-sft",
  "properties": {
    "jobType": "Command",
    "status": "Starting",
    "displayName": "LLaMA SFT Training",
    "computeId": "/subscriptions/.../computes/gpu-cluster",
    "services": {},
    "createdDateTime": "2026-03-06T08:00:00Z"
  }
}
```

---

### 3.7 Other Job Operations

#### Get Job

```
GET {endpoint}/api/projects/{project}/jobs/{jobid}?api-version=2025-09-01
```

Returns full job object with current `status`, `properties`, `inputs`, `outputs`.

#### List Jobs

```
GET {endpoint}/api/projects/{project}/jobs?api-version=2025-09-01&$top={limit}
```

**Response:**
```json
{
  "value": [ { ... }, { ... } ],
  "nextLink": "...?$skip=20"
}
```

Supports pagination via `$skip` query parameter.

#### Cancel Job

```
POST {endpoint}/api/projects/{project}/jobs/{jobid}/cancel?api-version=2025-09-01
```

Returns `200 OK` on success. Job transitions to `Canceling` → `Canceled`.

#### Delete Job

```
DELETE {endpoint}/api/projects/{project}/jobs/{jobid}?api-version=2025-09-01
```

Returns `204 No Content` on success.

---

### 3.8 API Call Summary — Job Creation

| Step | Method | Endpoint | Auth Scope |
|------|--------|----------|------------|
| 2a | `POST` | `.../datasets/{name}/versions/{ver}/startPendingUpload` | ai.azure.com |
| 2b | — | AzCopy to blob SAS URI | — |
| 2c | `PATCH` | `.../datasets/{name}/versions/{ver}` | ai.azure.com |
| 3a | `POST` | `.../datasets/{name}/versions/{ver}/startPendingUpload` | ai.azure.com (per input) |
| 3b | — | AzCopy to blob SAS URI | — (per input) |
| 3c | `PATCH` | `.../datasets/{name}/versions/{ver}` | ai.azure.com (per input) |
| 4 | `GET` | `Microsoft.CognitiveServices/.../computes/{name}` | management.azure.com |
| 6 | `PUT` | `.../jobs/{id}` | ai.azure.com |

**Best case** (no local code/inputs): 2 calls (compute GET + job PUT)
**Typical** (local code + 1 local input): 8 calls (3 for code + 3 for input + compute GET + job PUT)
**Worst case** (local code + N local inputs): 2 + 3*(1+N) calls

---

## 4. Known Limitations & Gaps

### 4.1 Log Streaming — Available via Artifact Polling

**Status:** The `FoundryJobController` PR adds artifact read endpoints that support polling-based log streaming.

**Approach:** Client-side polling (no SSE/WebSocket):
1. `GET .../jobs/{id}/artifacts/path?path=user_logs` → discover log files
2. `GET .../jobs/{id}/artifacts/getcontent/user_logs/std_log.txt?tailBytes=8192` → initial tail
3. Poll at 1-2s with `offset={X-VW-Content-Length}` → incremental new bytes
4. Exponential backoff (up to 5s) on empty responses
5. Stop when `job get` returns terminal status (Completed/Failed/Canceled)

**CLI impact:** `azd ai training job stream` is implementable in V1.

> **Note:** The exact log file paths (e.g., `user_logs/std_log.txt`) need to be confirmed for Foundry runtime. Discovery via artifacts list API avoids hardcoding.

### 4.2 Metrics / MLFlow — Not Supported in Foundry

**Status:** Foundry projects will **not** have MLFlow integration. No metric logging capability.

**Impact:** Users cannot log custom metrics (loss, accuracy, etc.) from training scripts.

**CLI impact:** No metrics command in V1. `job get` will show job status and duration but no training metrics.

### 4.3 Model Output — Dataset API Limitation

**Status:** Current dataset API supports custom model as output type, but the new API requires **job service to create model assets**. Changes are being investigated.

**Impact:** Job outputs that produce trained models may not be automatically registered as model assets in Foundry. Users may need to manually register models after job completion (using `azd ai models custom create --blob-uri`).

**CLI impact:** `job download` can download raw output files, but automatic model registration from job outputs is deferred until the job service supports model asset creation.

### 4.4 MFE Payload Schema — Unconfirmed

**Status:** The exact `FoundryJobBase` field mapping (especially `environmentId` accepting raw image URIs vs ARM IDs) is inferred from the requirement doc, not confirmed from a spec.

**Risk:** Payload might be rejected at runtime if field names or formats differ.

**Mitigation:** Test with real API once available; keep payload construction in a single place (Layer 2 service) for easy updates.

### 4.5 Summary

| Capability | V1 Status | Dependency |
|-----------|-----------|------------|
| Job create/get/list/cancel/delete | ✅ Supported | Jobs API (Savitha) |
| Code & data upload | ✅ Supported | Dataset API (William) — Existing |
| Compute resolution | ✅ Supported (ARM fallback to full ID) | Compute API (Rajat) — ARM |
| Log streaming | ✅ Supported (polling-based) | Artifact APIs (PR) |
| Job download | ✅ Supported | Artifact SAS URI APIs (PR) |
| Metrics logging | ❌ Not available | No MLFlow in Foundry |
| Model output registration | ❌ Deferred | Job service model asset creation TBD |

---

## 5. Architecture — Layered Design

Designed for future Go SDK extraction: `pkg/` contains zero CLI dependencies and can be lifted into a standalone module.

### 5.1 Project Structure

```
azure.ai.customtraining/
├── main.go                        # Extension entrypoint
├── extension.yaml                 # Extension manifest
├── version.txt                    # Version stamp
│
├── pkg/                           # Layer 1: API Client (future Go SDK)
│   ├── client/
│   │   ├── client.go              # Base HTTP client (auth, retries, errors)
│   │   ├── jobs.go                # Jobs API: create, get, list, cancel, delete
│   │   ├── datasets.go            # Datasets API: startPendingUpload, create/update, get
│   │   ├── computes.go            # Computes API: get (ARM), list (ARM)
│   │   └── artifacts.go           # Artifacts API: list, getcontent, contentinfo
│   └── models/
│       ├── job.go                 # Base: Job, JobProperties (jobType field)
│       ├── command_job.go         # CommandJobProperties (command, code, env, dist, etc.)
│       ├── dataset.go             # Dataset, DatasetVersion, PendingUploadResponse
│       ├── compute.go             # Compute
│       ├── artifact.go            # ArtifactDto, ArtifactContentInformationDto
│       └── common.go              # Input, Output, PagedResponse, ErrorResponse
│
├── internal/                      # Layer 2 + 3: Business Logic + CLI
│   ├── service/                   # Layer 2: Orchestration
│   │   ├── job_service.go         # Orchestrates: upload code → upload inputs → resolve compute → submit
│   │   ├── upload_service.go      # Code & data upload via dataset API + azcopy
│   │   ├── resolve_service.go     # Resolves compute name → ARM ID (with fallback)
│   │   └── stream_service.go      # Log discovery + polling-based streaming
│   ├── azcopy/                    # AzCopy runner (reuse from models ext)
│   │   ├── runner.go
│   │   └── installer.go
│   ├── cmd/                       # Layer 3: CLI (presentation)
│   │   ├── root.go                # Root command + persistent flags (-e endpoint)
│   │   ├── init.go                # Initialize project (collect sub, rg, account, project)
│   │   ├── job.go                 # "job" command group
│   │   ├── job_create.go          # --file flag, YAML parsing, progress spinners
│   │   ├── job_get.go             # --name, table/json output
│   │   ├── job_list.go            # --top, table/json output
│   │   ├── job_cancel.go          # --name, confirmation
│   │   ├── job_delete.go          # --name, confirmation prompt
│   │   ├── job_stream.go          # --name, live log output
│   │   └── job_download.go        # --name, --output-name, --path
│   └── utils/
│       ├── output.go              # Table/JSON output formatting
│       └── yaml_parser.go         # YAML job definition parser + validation
│
├── examples/                      # Sample YAML job definitions
│   ├── simple-command-job.yaml
│   ├── distributed-pytorch-job.yaml
│   └── mpi-training-job.yaml
│
└── docs/
    ├── installation-guide.md
    └── development-guide.md
```

### 5.2 Layer Responsibilities

**Layer 1 — `pkg/client/` + `pkg/models/` (Future Go SDK)**

- Pure REST client — HTTP calls, serialization, error handling
- Accepts `endpoint` + `TokenCredential`, constructs URLs internally
- Returns typed Go structs, wraps HTTP errors
- **Zero imports from `internal/`** — can be extracted to standalone module
- Handles dual auth: data-plane scope (`ai.azure.com`) and ARM scope (`management.azure.com`)

```go
// Example: pkg/client/client.go
type Client struct {
    dataPlaneURL   string                // https://{account}.services.ai.azure.com/api/projects/{project}
    armURL         string                // https://management.azure.com/subscriptions/{sub}/...
    credential     azcore.TokenCredential
    httpClient     *http.Client
    apiVersion     string
}

func NewClient(config ClientConfig, credential azcore.TokenCredential) (*Client, error)

// Jobs
func (c *Client) CreateOrUpdateJob(ctx context.Context, id string, job *models.JobDefinition) (*models.Job, error)
func (c *Client) GetJob(ctx context.Context, id string) (*models.Job, error)
func (c *Client) ListJobs(ctx context.Context, opts *ListOptions) (*models.PagedJobs, error)
func (c *Client) CancelJob(ctx context.Context, id string) error
func (c *Client) DeleteJob(ctx context.Context, id string) error

// Datasets
func (c *Client) StartPendingUpload(ctx context.Context, name, version string) (*models.PendingUploadResponse, error)
func (c *Client) CreateOrUpdateDataset(ctx context.Context, name, version string, req *models.DatasetRequest) (*models.Dataset, error)

// Computes
func (c *Client) GetCompute(ctx context.Context, name string) (*models.Compute, error)

// Artifacts
func (c *Client) ListArtifacts(ctx context.Context, jobID string) (*models.PagedArtifacts, error)
func (c *Client) GetArtifactContent(ctx context.Context, jobID, path string, offset, tailBytes *int64) (io.ReadCloser, int64, error)
func (c *Client) GetArtifactContentInfo(ctx context.Context, jobID, path string) (*models.ArtifactContentInfo, error)
```

**Layer 2 — `internal/service/` (Business Logic)**

- Composes Layer 1 APIs into workflows
- Handles: local file detection, upload orchestration, compute resolution, YAML → payload conversion
- No CLI/UX concerns — testable with mocks of Layer 1

```go
// Example: internal/service/job_service.go
type JobService struct {
    client         *client.Client
    uploadService  *UploadService
    resolveService *ResolveService
}

func (s *JobService) CreateJob(ctx context.Context, config *JobConfig) (*models.Job, error) {
    // 1. Upload code if local
    // 2. Upload inputs if local
    // 3. Resolve compute
    // 4. Build payload
    // 5. Submit job
}
```

```go
// Example: internal/service/resolve_service.go
type ResolveService struct {
    client *client.Client
}

func (s *ResolveService) ResolveCompute(ctx context.Context, compute string) (string, error) {
    // If starts with /subscriptions/ → return as-is
    // Otherwise → GET ARM compute → return ID
    // On 401/403 → return helpful error asking for full ARM ID
}
```

```go
// Example: internal/service/stream_service.go
type StreamService struct {
    client *client.Client
}

func (s *StreamService) StreamLogs(ctx context.Context, jobID string, writer io.Writer) error {
    // 1. Discover log files via artifacts list
    // 2. Poll getcontent with offset tracking
    // 3. Write new bytes to writer
    // 4. Stop on terminal job status
}
```

**Layer 3 — `internal/cmd/` (CLI Presentation)**

- Cobra commands, flags, UX (spinners, colors, tables)
- YAML file parsing and validation
- Calls Layer 2 services, formats results
- No direct HTTP calls

### 5.3 Design Rules

| Rule | Purpose |
|------|---------|
| `pkg/` has zero imports from `internal/` | SDK extraction readiness |
| Client methods accept `context.Context`, return `(result, error)` | No side effects, testable |
| All HTTP details (URLs, headers, serialization) live in Layer 1 | Single source of truth for API contracts |
| Layer 2 depends on Layer 1 via interfaces | Mockable for testing |
| Layer 3 only calls Layer 2, never Layer 1 directly | Clean separation |
| Job type logic (Command, Pipeline) lives in Layer 2 | Layer 1 is type-agnostic |
| Dual auth handled in Layer 1 client | Transparent to Layer 2/3 |

### 5.4 Init Flow & Implicit Init

**Same dual-mode pattern as the finetune extension** (see §1.2).

#### Explicit Init (`azd ai training init`)

Interactive prompts:
1. Prompt for subscription (or accept `--subscription`)
2. Prompt for project endpoint (or accept `--project-endpoint`)
3. Parse endpoint URL: extract `accountName` from hostname, `projectName` from path
4. Resolve ARM context: find resource group via ARM API or prompt
5. Store in azd environment

#### Implicit Init (via `--subscription` + `--project-endpoint` flags)

Runs automatically in `PersistentPreRunE` on the `job` command group:

```go
// internal/cmd/job.go — PersistentPreRunE hook
func newJobCommand() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "job",
        Short: "Manage training jobs",
        PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
            return validateOrInitEnvironment(cmd.Context(), flags.subscriptionId, flags.projectEndpoint)
        },
    }
    cmd.PersistentFlags().StringVarP(&flags.subscriptionId, "subscription", "s", "", "...")
    cmd.PersistentFlags().StringVarP(&flags.projectEndpoint, "project-endpoint", "e", "", "...")
    // ... subcommands
}
```

```go
// internal/cmd/validation.go — validateOrInitEnvironment logic
func validateOrInitEnvironment(ctx context.Context, subscriptionId, projectEndpoint string) error {
    // 1. Check if env vars already configured
    envVars := getRequiredEnvVars(ctx) // TENANT_ID, SUB_ID, ACCOUNT_NAME, PROJECT_NAME, etc.
    if allConfigured(envVars) {
        if subscriptionId != "" || projectEndpoint != "" {
            warn("Environment already configured. Flags --subscription/--project-endpoint are ignored.")
        }
        return nil
    }

    // 2. Env not configured — need both flags for implicit init
    if subscriptionId == "" || projectEndpoint == "" {
        return fmt.Errorf("environment not configured. Run 'azd ai training init' or provide both --subscription and --project-endpoint")
    }

    // 3. Implicit init: parse endpoint, resolve project, set env vars
    accountName, projectName := parseEndpoint(projectEndpoint)
    // ... resolve ARM context, set env vars
    return nil
}
```

#### Environment Variables Stored

| Environment Variable | Source | Used For |
|---------------------|--------|----------|
| `AZURE_TENANT_ID` | Auth context | Token acquisition |
| `AZURE_SUBSCRIPTION_ID` | User selection or `--subscription` flag | ARM API calls |
| `AZURE_RESOURCE_GROUP_NAME` | ARM lookup or prompt | ARM API calls |
| `AZURE_ACCOUNT_NAME` | Parsed from endpoint hostname | Both base URLs |
| `AZURE_PROJECT_NAME` | Parsed from endpoint path | Data plane base URL |
| `AZURE_LOCATION` | ARM lookup | Display / future use |

### 5.5 Shared Components (Reuse from Models Extension)

| Component | Source | Reuse Strategy |
|-----------|--------|---------------|
| AzCopy runner | `azure.ai.models/internal/azcopy/runner.go` | Copy to new extension |
| AzCopy installer | `azure.ai.models/internal/azcopy/installer.go` | Copy (with allowed hosts) |
| Output formatting | `azure.ai.models/internal/utils/output.go` | Copy pattern |
| HTTPS validation | `azure.ai.models/internal/client/foundry_client.go` | Copy pattern |

> **Note:** Direct Go module dependency between extensions is not supported. Code is copied and adapted per extension, following the same pattern as finetune vs models today.

### 5.6 Extensibility — Future Job Types

Layer 1 (`pkg/models/`) uses a `jobType` discriminator field, allowing new job types without refactoring:

```go
// pkg/models/job.go — base type, job-type agnostic
type JobProperties struct {
    JobType string `json:"jobType"`    // "Command", "Pipeline", "Sweep", etc.
    // ... common fields
}

// pkg/models/command_job.go — Command-specific
type CommandJobProperties struct {
    JobProperties
    Command      string `json:"command"`
    CodeId       string `json:"codeId,omitempty"`
    EnvironmentId string `json:"environmentId"`
    // ... command-specific fields
}
```

Adding a new job type (e.g., Pipeline) requires:
1. New model struct in `pkg/models/pipeline_job.go`
2. New service in `internal/service/pipeline_job_service.go`
3. New CLI command in `internal/cmd/job_create_pipeline.go`

Layer 1 client methods remain unchanged — they serialize whatever `JobProperties` they receive.

---

## 6. Log Streaming — Detailed Design

### 6.1 Overview

Log streaming uses the artifact polling APIs from the `FoundryJobController`. There is no server-sent event (SSE) or WebSocket support — streaming is achieved by polling with byte offsets.

### 6.2 Sequence

```
                              ┌──────────┐
                              │  Client   │
                              └─────┬─────┘
                                    │
        ┌───────────────────────────▼────────────────────────────────┐
        │  1. GET .../jobs/{id}  →  check status != terminal         │
        └───────────────────────────┬────────────────────────────────┘
                                    │
        ┌───────────────────────────▼────────────────────────────────┐
        │  2. GET .../jobs/{id}/artifacts/path?path=user_logs        │
        │     → discover log files (e.g., std_log.txt)               │
        └───────────────────────────┬────────────────────────────────┘
                                    │
        ┌───────────────────────────▼────────────────────────────────┐
        │  3. GET .../jobs/{id}/artifacts/getcontent/{logPath}       │
        │     ?tailBytes=8192                                        │
        │     → initial tail (last 8KB of log)                       │
        │     → read X-VW-Content-Length header → set offset         │
        └───────────────────────────┬────────────────────────────────┘
                                    │
        ┌───────────────────────────▼────────────────────────────────┐
        │  4. POLLING LOOP:                                          │
        │     GET .../artifacts/getcontent/{logPath}?offset={N}      │
        │     → if body non-empty: write to stdout, update offset    │
        │     → if body empty: increase backoff interval             │
        │     → sleep 1-5s (exponential backoff on empty)            │
        │     → check job status periodically (every 10 polls)       │
        │     → exit when job status is terminal                     │
        └───────────────────────────────────────────────────────────-┘
```

### 6.3 Implementation Notes

| Parameter | Value | Reason |
|-----------|-------|--------|
| Initial tailBytes | 8192 | Show last 8KB of existing log output on connect |
| Poll interval (active) | 1-2s | Balance between responsiveness and API load |
| Poll interval (idle) | Up to 5s | Exponential backoff when no new content |
| Job status check | Every 10 polls (~10-20s) | Avoid excessive status API calls |
| Offset tracking | `X-VW-Content-Length` header | Server reports total blob size |

**Edge cases:**
- Log files don't exist yet (job still initializing): retry discovery every 5s
- Multiple log files: stream primary (`std_log.txt`) first, mention others in footer
- Connection errors: retry with exponential backoff, surface after 3 failures

---

## 7. Download — Detailed Design

### 7.1 Overview

Download retrieves job output artifacts to local disk. Uses SAS URIs for direct blob download via azcopy.

### 7.2 Sequence

```
                              ┌──────────┐
                              │  Client   │
                              └─────┬─────┘
                                    │
        ┌───────────────────────────▼────────────────────────────────┐
        │  1. GET .../jobs/{id}  →  verify job status                │
        │     (completed/failed — allow download from both)          │
        └───────────────────────────┬────────────────────────────────┘
                                    │
        ┌───────────────────────────▼────────────────────────────────┐
        │  2. GET .../jobs/{id}/artifacts                            │
        │     → list all artifacts with paths and sizes              │
        │     → filter by --output-name if specified                 │
        └───────────────────────────┬────────────────────────────────┘
                                    │
        ┌───────────────────────────▼────────────────────────────────┐
        │  3. GET .../jobs/{id}/artifacts/prefix/contentinfo         │
        │     ?path={outputPrefix}                                   │
        │     → batch get SAS URIs for all output files              │
        └───────────────────────────┬────────────────────────────────┘
                                    │
        ┌───────────────────────────▼────────────────────────────────┐
        │  4. For each SAS URI:                                      │
        │     azcopy copy "{sasUri}" "{localPath}"                   │
        │     → parallel downloads where possible                    │
        └───────────────────────────────────────────────────────────-┘
```

### 7.3 CLI Flags

```
azd ai training job download --name {id} [--path ./output] [--output-name default]
```

| Flag | Description | Default |
|------|-------------|---------|
| `--name` | Job ID (required) | — |
| `--path` | Local directory to download into | `./` |
| `--output-name` | Specific output to download (e.g., `default`, `model_output`) | all outputs |

### 7.4 Implementation Notes

- Use `prefix/contentinfo` (batch SAS) rather than individual `contentinfo` calls — one API call for all files under a prefix
- SAS URIs have TTL — download must start promptly after retrieval
- Preserve directory structure from artifact paths in local output
- Show progress via azcopy's built-in progress reporting

---

## 8. Upload & Dedup — Design

### 8.1 Code Upload (V1 — Always Upload)

For V1, code is always uploaded as a new dataset version. No dedup.

```
1. PATCH .../datasets/{name}/versions/{version}  → create dataset version (pending state)
2. POST  .../datasets/{name}/versions/{version}/startPendingUpload  → get SAS URI
3. azcopy upload local code directory → SAS URI
4. PATCH .../datasets/{name}/versions/{version}  → confirm upload complete
```

**Naming convention:** Dataset name = `code-{jobName}`, version = timestamp or UUID.

### 8.2 Input Data Upload (V1 — Always Upload)

Same flow as code upload but with input-specific naming:
- Dataset name = `input-{jobName}-{inputName}`, version = 1

### 8.3 Dedup Strategy (Future V2)

**Approach:** Use blob storage metadata for content-addressable dedup.

1. Compute SHA256 hash of local directory (file content + relative paths)
2. Check if dataset version with matching hash metadata already exists:
   `GET .../datasets/{name}/versions?$filter=hash eq '{sha256}'`
3. If exists → reuse dataset ID, skip upload
4. If not → upload as normal, set hash in dataset metadata

> **Note:** Partner team (Anthony Karloff) plans to build hash support into dataset API. For V1, always re-upload. Optimize in V2 when API supports hash metadata queries.

---

## 9. YAML Schema — AML Compatibility Analysis

### 9.1 Approach: Reuse AML Schema with Translation Layer

The AML SDK defines a well-documented command job YAML schema at:
`https://azuremlschemas.azureedge.net/latest/commandJob.schema.json`

**Recommendation:** Accept the AML YAML format as input (familiar to existing AML users), but apply a **translation layer** to convert it into the Foundry API payload (`FoundryCommandJob`). This gives users a zero-migration-cost experience.

### 9.2 Field-by-Field Compatibility

| AML YAML Field | Foundry API Field | Status | Translation Needed |
|---|---|---|---|
| `$schema` | — | ✅ Ignored | Strip (client-only) |
| `type: command` | `jobType: "Command"` | ⚠️ Rename | Rename field + capitalize value |
| `name` | URL path `PUT /{id}` | ⚠️ Moved | Extract from YAML, put in URL path |
| `display_name` | `displayName` | ✅ Rename | snake_case → camelCase |
| `description` | `description` (ResourceBase) | ✅ Direct | snake_case → camelCase |
| `tags` | `tags` (ResourceBase) | ✅ Direct | None |
| `command` | `command` | ✅ **Direct match** | None |
| `environment` | `environmentId` | ❌ **Major** | See §9.3 below |
| `compute` | `computeId` | ⚠️ Resolve | Strip `azureml:` prefix, resolve to ARM ID |
| `code` | `codeId` | ⚠️ Upload | Local path → upload as dataset → use dataset resource ID |
| `inputs` | `inputs` | ⚠️ Format | `azureml:name:ver` → dataset resource ID; local path → upload |
| `outputs` | `outputs` | ⚠️ Subset | No `mlflow_model`/`triton_model` in Foundry |
| `distribution` | `distribution` | ✅ Mostly | PyTorch ✅, MPI ✅, TensorFlow ✅, **Ray ❌** |
| `environment_variables` | `environmentVariables` | ✅ Rename | snake_case → camelCase |
| `resources` | `resources` | ✅ Similar | snake_case → camelCase on sub-fields |
| `limits` | `limits` | ✅ Similar | snake_case → camelCase |
| `queue_settings` | `queueSettings` | ✅ Similar | snake_case → camelCase |
| `identity` | — | ❌ **Not supported** | Error if specified |
| `services` | `services` (readonly) | ❌ **Not writable** | Error if specified |
| `experiment_name` | — | ❌ **Not in Foundry** | Warn and ignore |
| `properties` | — | ⚠️ TBD | May map to ResourceBase properties |

### 9.3 Environment — The Key Incompatibility

**AML `environment` field accepts 4 formats:**

1. **Inline anonymous environment** (Docker image + optional conda):
   ```yaml
   environment:
     image: mcr.microsoft.com/azureml/openmpi4.1.0-cuda11.8-cudnn8-ubuntu22.04
     conda_file: ./conda.yaml
   ```

2. **Inline build context** (Dockerfile):
   ```yaml
   environment:
     build:
       path: ./docker-context
       dockerfile_path: Dockerfile
   ```

3. **Named reference** (`azureml:name:version`):
   ```yaml
   environment: azureml:AzureML-pytorch-1.9-ubuntu18.04-py37-cuda11.1-gpu:26
   ```

4. **Registry reference** (`azureml://registries/...`):
   ```yaml
   environment: azureml://registries/azureml/environments/AzureML-pytorch/versions/26
   ```

**Foundry `environmentId` accepts:**
- A string that serves as the environment identifier — confirmed from `FoundryCommandJob.cs` as a **required string** field
- Based on the requirement doc, this is a **raw container image URI** (pass-through to the runtime)
- No anonymous environment building (no Dockerfile, no conda file resolution)

**Translation strategy:**

| AML YAML Format | Foundry Translation | Supported? |
|---|---|---|
| `environment: docker.io/myimage:tag` | `environmentId: "docker.io/myimage:tag"` | ✅ Direct pass-through |
| `environment: mcr.microsoft.com/...` | `environmentId: "mcr.microsoft.com/..."` | ✅ Direct pass-through |
| `environment: { image: "mcr.microsoft.com/..." }` | `environmentId: "mcr.microsoft.com/..."` | ✅ Extract `image` field |
| `environment: { image: ..., conda_file: ... }` | ❌ **Error** | ❌ No conda resolution in Foundry |
| `environment: { build: { path: ..., dockerfile_path: ... } }` | ❌ **Error** | ❌ No Dockerfile building in Foundry |
| `environment: azureml:name:version` | `environmentId: "azureml:name:version"` | ⚠️ **TBD** — need to confirm if Foundry resolves `azureml:` references |
| `environment: azureml://registries/...` | `environmentId: "azureml://registries/..."` | ⚠️ **TBD** — need to confirm registry reference support |

**V1 recommendation:** Support container image URIs only (formats 1 with `image` only, and plain string URIs). Reject conda_file, build context, and azureml: references with clear error messages:

```
✗ Foundry does not support building environments from Dockerfiles or conda files.
  Please provide a pre-built container image URI instead:
  environment: mcr.microsoft.com/azureml/openmpi4.1.0-cuda11.8-cudnn8-ubuntu22.04
```

### 9.4 Compute Translation

AML YAML `compute` field accepts:
- `azureml:gpu-cluster` → strip `azureml:` prefix, resolve via ARM GET
- `local` → ❌ Not supported in Foundry (no local compute)
- Plain name `gpu-cluster` → resolve via ARM GET
- Full ARM ID → pass through as `computeId`

### 9.5 Code & Inputs Translation

**Code:**
- AML: `code: ./src` (local path) → Upload as dataset, use dataset resource ID as `codeId`
- AML: `code: azureml:code-name:1` → ⚠️ TBD — need to confirm if Foundry resolves `azureml:` code references
- AML: `code: git+https://...` → ❌ Not supported in V1

**Inputs:**
- AML: `path: azureml:data-name:version` → May need to resolve to dataset resource ID
- AML: `path: ./local-data` (local path) → Upload as dataset, use dataset resource ID
- AML: `path: https://...` or `wasbs://...` → ⚠️ TBD — direct URI support

### 9.6 Unsupported AML Fields — Error/Warning Behavior

| Field | Behavior | Reason |
|-------|----------|--------|
| `identity` | **Error** if specified | Not in Foundry API |
| `services` | **Error** if specified | Read-only in Foundry |
| `experiment_name` | **Warning**, ignore | Not in Foundry API |
| `distribution.type: ray` | **Error** | Ray not supported |
| `compute: local` | **Error** | No local compute |
| `environment.conda_file` | **Error** | No conda resolution |
| `environment.build` | **Error** | No Dockerfile building |

### 9.7 Example: AML YAML → Foundry Payload

**User's YAML (AML-compatible format):**
```yaml
$schema: https://azuremlschemas.azureedge.net/latest/commandJob.schema.json
type: command
name: train-cifar10
display_name: CIFAR-10 Training
description: Fine-tune ResNet on CIFAR-10
compute: gpu-cluster
environment: mcr.microsoft.com/azureml/openmpi4.1.0-cuda11.8-cudnn8-ubuntu22.04:latest
code: ./src
command: python train.py --epochs 10 --lr ${{inputs.learning_rate}}

inputs:
  training_data:
    type: uri_folder
    path: ./data/cifar10
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

tags:
  project: cifar10
  team: ml-platform
```

**Translated Foundry API payload (after CLI processing):**
```json
{
  "properties": {
    "jobType": "Command",
    "displayName": "CIFAR-10 Training",
    "description": "Fine-tune ResNet on CIFAR-10",
    "computeId": "/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.CognitiveServices/accounts/{acct}/computes/gpu-cluster",
    "environmentId": "mcr.microsoft.com/azureml/openmpi4.1.0-cuda11.8-cudnn8-ubuntu22.04:latest",
    "codeId": "azureml://datasets/code-train-cifar10/versions/1",
    "command": "python train.py --epochs 10 --lr ${{inputs.learning_rate}}",
    "inputs": {
      "training_data": {
        "jobInputType": "uri_folder",
        "uri": "azureml://datasets/input-train-cifar10-training_data/versions/1"
      },
      "learning_rate": {
        "jobInputType": "literal",
        "value": "0.001"
      }
    },
    "outputs": {
      "model_output": {
        "jobOutputType": "uri_folder"
      }
    },
    "distribution": {
      "distributionType": "PyTorch",
      "processCountPerInstance": 4
    },
    "resources": {
      "instanceCount": 2
    },
    "environmentVariables": {
      "NCCL_DEBUG": "INFO"
    }
  },
  "tags": {
    "project": "cifar10",
    "team": "ml-platform"
  }
}
```

### 9.8 Translation Layer Architecture

The YAML → Foundry API translation lives in **Layer 2** (`internal/service/job_service.go`):

```
YAML file (AML format)
    │
    ▼
internal/utils/yaml_parser.go     ← Parse + validate YAML fields
    │
    ▼
internal/service/job_service.go   ← Translate:
    │                                - environment → environmentId
    │                                - compute → computeId (ARM resolve)
    │                                - code → codeId (upload + reference)
    │                                - inputs → upload local + resolve refs
    │                                - snake_case → camelCase
    │                                - validate unsupported fields
    ▼
pkg/client/jobs.go                ← Send to Foundry API (PUT /{id})
```

### 9.9 Decision: `$schema` Reference

Options:
1. **Reference AML schema** — `$schema: https://azuremlschemas.azureedge.net/latest/commandJob.schema.json`
   - Pro: Users get IDE validation from existing schema
   - Con: Schema allows fields we don't support (services, identity, conda_file)

2. **Custom schema** — `$schema: https://azd.ms/schemas/customtraining/commandJob.schema.json`
   - Pro: Only allows supported fields, no false positives
   - Con: Need to host and maintain

3. **No schema enforcement** — Accept `$schema` but don't validate against it
   - Pro: Simplest, compatible with existing YAMLs
   - Con: No IDE validation

**V1 recommendation:** Option 3 — Accept existing AML YAMLs, ignore `$schema`, validate at the CLI layer with clear errors for unsupported fields. Ship a custom schema in V2 if demand warrants it.

### 9.10 Open Questions

| # | Question | Impact | Action |
|---|----------|--------|--------|
| 1 | Does Foundry `environmentId` resolve `azureml:` references? | Determines if named environment references work | Test with real API |
| 2 | Does Foundry `environmentId` accept raw Docker image URIs? | Core assumption for V1 | Confirm with Savitha |
| 3 | Does Foundry resolve `azureml:` code references in `codeId`? | Determines if pre-uploaded code assets work | Test with real API |
| 4 | What input/output `jobInputType`/`jobOutputType` values does Foundry support? | Validation rules | Check FoundryCommandJob validator |
| 5 | Does Foundry support `${{inputs.x}}` placeholder syntax in `command`? | Critical for parameterized commands | Confirm with Savitha |
