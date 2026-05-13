## Summary

Adds **code deploy (ZIP upload)** support for hosted agents as an alternative to container-based deployment. This enables deploying Python agent source code directly without requiring Docker/ACR.

### Init Flow: 4 Supported Paths

The deploy mode prompt appears in all init flows. Users can re-run `azd ai agent init` at any time to switch between code and container modes. Template files (Dockerfile, etc.) are never deleted.

#### 1. New Project (Template) -> Container Deploy
```
azd ai agent init           # empty dir -> template selection
-> Language -> Python
-> Template -> select template
-> Deploy mode -> Container deploy (Docker image)
-> Model -> Use existing model deployment(s)
-> Subscription -> select
-> Foundry Project -> select
-> App Insights -> optional
-> Model deployment -> select
-> ACR -> select
-> Container resources -> select
-> Writes agent.yaml (no code_configuration), azure.yaml (with docker block)
```

#### 2. New Project (Template) -> Code Deploy
```
azd ai agent init           # empty dir -> template selection
-> Language -> Python
-> Template -> select template
-> Deploy mode -> Code deploy (ZIP upload)
-> Runtime -> Python 3.12
-> Entry point -> main.py
-> Dependency resolution -> Remote build / Bundled
-> Model -> Use existing model deployment(s)
-> Subscription -> select
-> Foundry Project -> select
-> App Insights -> optional
-> Model deployment -> select
-> Container resources -> select (for local run compatibility)
-> Writes agent.yaml (with code_configuration), azure.yaml (no docker block)
-> Template Dockerfile preserved (unused)
```

#### 3. Existing Code -> Container Deploy
```
azd ai agent init           # non-empty dir with code
-> "Use the code in the current directory"
-> Deploy mode -> Container deploy (Docker image)
-> Protocols / agent name / description
-> Model -> Use existing model deployment(s)
-> Subscription -> select
-> Foundry Project -> select
-> ACR -> select
-> Writes agent.yaml (no code_configuration), azure.yaml (with docker block)
```

#### 4. Existing Code -> Code Deploy
```
azd ai agent init           # non-empty dir with code
-> "Use the code in the current directory"
-> Deploy mode -> Code deploy (ZIP upload)
-> Runtime -> Python 3.12
-> Entry point -> main.py
-> Dependency resolution -> Remote build / Bundled
-> Protocols / agent name / description
-> Model -> Use existing model deployment(s)
-> Subscription -> select
-> Foundry Project -> select
-> Writes agent.yaml (with code_configuration), azure.yaml (no docker block)
```

#### 5. Re-init with Existing Manifest (mode switch)
```
azd ai agent init           # dir with agent.manifest.yaml
-> "An existing agent manifest was found at agent.manifest.yaml. Use it?" -> Yes
-> Deploy mode -> Code deploy (ZIP upload) / Container deploy
-> Runtime -> Python 3.12 (code deploy only)
-> Entry point -> main.py (code deploy only)
-> Dependency resolution -> Remote build / Bundled (code deploy only)
-> Model -> reuses environment values
-> App Insights -> optional
-> Model deployment -> select
-> Container resources -> select
```

### Deploy Path Changes

- **Deploy path** (`azd deploy`): New `packageCodeDeploy()` creates a ZIP archive of agent source, `deployHostedCodeAgent()` uploads via multipart form-data POST with SHA-256 verification. Auto-detected from `code_configuration` in `agent.yaml`.
- **API integration**: Uses `Foundry-Features: CodeAgents=V1Preview,HostedAgents=V1Preview` header, `x-ms-code-zip-sha256` for integrity, `dependency_resolution` field (string enum).
- **azure.yaml**: Code deploy uses `language: python` (no docker block), but still includes `container.resources` and `startupCommand` for `azd ai agent run` compatibility.

### Files Modified

| File | Change |
|------|--------|
| `init_from_code.go` | Deploy mode prompt, `promptCodeConfiguration()`, updated `addToProject()` path resolution |
| `init.go` | Deploy mode prompt in template flow, skip ACR for code deploy, fixed `addToProject()` path resolution for subdirectory re-init |
| `init_foundry_resources_helpers.go` | `configureFoundryProjectEnv` / `selectFoundryProject` accept `skipACR` param |
| `codes.go` | Added `CodeAgentCreateFailed`, `CodeMissingCodeZipArtifact` error codes |
| `models.go` | Added `CodeConfigurationAPI`, `CodeBasedHostedAgentDefinition` structs |
| `operations.go` | Added `CreateAgentFromZip`, `UpdateAgentFromZip`, `zipDeployRequest` |
| `operations_test.go` | 2 tests for zip deploy request multipart format + headers |
| `map.go` | Branched `CreateHostedAgentAPIRequest` for code deploy |
| `yaml.go` | Added `CodeConfiguration` struct |
| `service_target_agent.go` | `isCodeDeployAgent()`, `packageCodeDeploy()`, `deployHostedCodeAgent()` |
| `cspell.yaml` | Added `mypy` to words list |

---

## How to Build

```bash
cd cli/azd/extensions/azure.ai.agents
go build ./...
go vet ./...
azd x build
```

---

## Manual Test Steps

### Prerequisites
- Azure subscription with a Foundry project
- A model deployment (e.g. `gpt-4o`) in your Foundry project
- `azd` CLI installed, logged in (`azd auth login`)
- Build the extension: `azd x build --cwd <repo>/cli/azd/extensions/azure.ai.agents`

### Test 1: Code Deploy with Remote Build

```powershell
# 1. Create a fresh test directory
mkdir test-code-deploy
cd test-code-deploy

# 2. Init (interactive)
azd ai agent init
# Prompts (expected order):
#   1. Language -> Python
#   2. Template -> Hello World agent (Invocations, without a framework, Python)
#   3. Deploy mode -> Code deploy (ZIP upload)
#   4. Runtime -> Python 3.12
#   5. Entry point -> main.py
#   6. Dependency resolution -> Remote build (server installs dependencies)
#   7. Model -> Use existing model deployment(s) from a Foundry project
#   8. Subscription -> <your subscription>
#   9. Foundry Project -> <your account> / <your project>
#  10. App Insights -> leave blank (press Enter)
#  11. Model deployment -> <select your model>
#  12. Container resources -> default (any option)

# 3. Verify config
Get-Content src\hello-world-python-invocations\agent.yaml
# Expect:
#   code_configuration:
#     runtime: python_3_12
#     entry_point: main.py
#     dependency_resolution: remote_build
Get-Content azure.yaml
# Expect: project: src/hello-world-python-invocations, language: python, NO docker block

# 4. Deploy (remote_build -- no pip install needed)
azd deploy hello-world-python-invocations
# Expect: "Packaging code" -> "Creating agent" -> "Agent is active!"

# 5. Verify & Invoke
$TOKEN = (az account get-access-token --resource https://ai.azure.com --query accessToken -o tsv)
$EP = "<your-endpoint>/api/projects/<your-project>"
$AGENT = "hello-world-python-invocations"

# Check agent status (expect version: 1, status: active)
curl.exe -s "$EP/agents/$AGENT`?api-version=2025-11-15-preview" -H "Authorization: Bearer $TOKEN" | python -m json.tool

# Invoke (wait ~20s after deploy for cold start)
Start-Sleep -Seconds 20
curl.exe -s -N -X POST "$EP/agents/$AGENT/endpoint/protocols/invocations`?api-version=2025-11-15-preview" `
  -H "Authorization: Bearer $TOKEN" `
  -H "Content-Type: application/json" `
  -d '{"message":"hello remote build"}'
# Expect: streaming SSE response with model reply
```

### Test 2: Switch to Bundled (same project, re-init from subdirectory)

```powershell
# 6. Re-init from the src subdirectory (manifest detection flow)
cd src\hello-world-python-invocations
azd ai agent init
# Expected prompts:
#   1. "An existing agent manifest was found at agent.manifest.yaml. Use it?" -> Yes
#   2. Deploy mode -> Code deploy (ZIP upload)
#   3. Runtime -> Python 3.12
#   4. Entry point -> main.py
#   5. Dependency resolution -> Bundled (pre-install dependencies locally)
#   6. Model -> (reuses environment values)
#   7. App Insights -> blank
#   8. Model deployment -> <select your model>
#   9. Container resources -> select

# 7. Verify agent.yaml shows bundled
Get-Content agent.yaml | Select-String "dependency_resolution"
# Expect: dependency_resolution: bundled

# 8. Verify azure.yaml project path (bug fix validation)
cd ..\..
Get-Content azure.yaml | Select-String "project:"
# Expect: project: src/hello-world-python-invocations  (NOT "project: .")

# 9. Install dependencies for Linux into source directory
cd src\hello-world-python-invocations
pip install -r requirements.txt `
  -t . `
  --platform manylinux_2_17_x86_64 `
  --platform linux_x86_64 `
  --platform any `
  --python-version 3.12 `
  --implementation cp `
  --only-binary=:all: `
  --upgrade

# 10. Deploy (bundled)
cd ..\..
azd deploy hello-world-python-invocations
# Expect: "Packaging code" -> "Creating agent" -> done (no "Waiting for remote build")

# 11. Verify version incremented & Invoke
$TOKEN = (az account get-access-token --resource https://ai.azure.com --query accessToken -o tsv)

# Check version (expect version: 2, dependency_resolution: bundled)
curl.exe -s "$EP/agents/$AGENT`?api-version=2025-11-15-preview" -H "Authorization: Bearer $TOKEN" | python -m json.tool

# Invoke (wait ~30s for new version to activate)
Start-Sleep -Seconds 30
curl.exe -s -N -X POST "$EP/agents/$AGENT/endpoint/protocols/invocations`?api-version=2025-11-15-preview" `
  -H "Authorization: Bearer $TOKEN" `
  -H "Content-Type: application/json" `
  -d '{"message":"hello bundled mode"}'
# Expect: streaming SSE response with model reply

# 12. Cleanup (optional)
curl.exe -s -X DELETE "$EP/agents/$AGENT`?api-version=2025-11-15-preview" -H "Authorization: Bearer $TOKEN"
```

---

## Test Results

All configurations passed (deploy + invoke verified):

| # | Protocol | Package Mode | Deploy | Invoke |
|---|----------|-------------|:---:|:---:|
| 1 | invocations | remote_build | PASS | PASS (SSE) |
| 2 | invocations | bundled | PASS | PASS (SSE) |
| 3 | responses | remote_build | PASS | PASS (JSON) |
| 4 | responses | bundled | PASS | PASS (JSON) |

---

## Notes

- **Auto-detection**: `azd deploy` reads `agent.yaml` -- if `code_configuration` is present, code deploy; otherwise container deploy. No flags needed.
- **No impact on container deploy**: All container deploy paths are unchanged. The `skipACR` parameter defaults to `false` at all existing call sites.
- **Mode switching**: Re-run `azd ai agent init` to switch modes. Template files (Dockerfile, .dockerignore) are never deleted.
- **`--no-prompt` defaults**: Deploy mode -> container (backward compatible). Runtime -> `python_3_12`. Entry point -> `main.py`. Dependency resolution -> `remote_build`.
- **Known issue (pre-existing)**: The `postdeploy` hook looks up agent by azure.yaml service name instead of agent.yaml name, causing a 404 after successful deploy. Not related to this PR.

---

## Related

- Spec: [Azure/foundrysdk-specs#164](https://github.com/Azure/foundrysdk-specs/pull/164) -- CLI spec for code deployment

Fixes #7430

