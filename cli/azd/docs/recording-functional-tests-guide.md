# Recording Framework for Azure Developer CLI Functional Tests

## Table of Contents
- [Overview](#overview)
- [How Recording Works](#how-recording-works)
- [Recording Capabilities](#recording-capabilities)
- [Creating a New Functional Test with Recording](#creating-a-new-functional-test-with-recording)
- [Re-recording an Existing Test](#re-recording-an-existing-test)
- [Building azd with Recording Support](#building-azd-with-recording-support)
- [Using Recordings in CI](#using-recordings-in-ci)
- [Recording for Extensions](#recording-for-extensions)
- [Best Practices and Tips](#best-practices-and-tips)
- [Troubleshooting](#troubleshooting)

---

## Overview

The Azure Developer CLI (azd) uses a sophisticated recording framework for functional tests that allows tests to:
- Run against live Azure resources and record HTTP interactions
- Replay recorded interactions for fast, deterministic testing
- Support both HTTP/HTTPS traffic and command-line tool invocations (like `docker`, `dotnet`)

The recording framework is based on [go-vcr](https://github.com/dnaeon/go-vcr) but extends it with:
- HTTP proxy server support for recording/playback
- Command proxy support for tools like Docker and dotnet
- Variable storage across test runs
- Automatic sanitization of sensitive data
- Smart matching for dynamic Azure resources

---

## How Recording Works

### Architecture

The recording system consists of several components:

```
┌─────────────────┐
│  Functional     │
│  Test Code      │
└────────┬────────┘
         │
         ├──► recording.Start(t)
         │
         ▼
┌─────────────────────────────────────┐
│  Recording Session                  │
│  - ProxyUrl (HTTPS proxy)          │
│  - CmdProxyPaths (for docker, etc) │
│  - Variables (env names, etc)      │
│  - ProxyClient (HTTP client)       │
└─────────────────────────────────────┘
         │
         ├──► HTTP Traffic ──► go-vcr recorder ──► YAML cassettes
         │
         └──► Command Calls ──► cmdrecord proxies ──► YAML cassettes
```

### Recording Modes

The framework supports four modes (controlled by `AZURE_RECORD_MODE` env var):

1. **`live`** - No recording/playback, direct passthrough to Azure
2. **`record`** - Records all interactions, overwrites existing recordings
3. **`playback`** - Only replays from recordings, fails if not found
4. **`recordOnce`** (default for local dev) - Records if no cassette exists, otherwise plays back

In CI environments, the default is determined by whether recordings exist.

### What Gets Recorded

#### HTTP/HTTPS Traffic
- All Azure Resource Manager API calls
- Azure Storage operations
- Container Registry interactions
- Most Azure service APIs

#### Command Invocations
The framework can record specific command invocations:
- **Docker**: `docker login`, `docker push`
- **Dotnet**: `dotnet publish` with ContainerRegistry parameter

#### Variables Stored
Session variables are stored separately from interactions:
- `env_name` - Azure environment name
- `subscription_id` - Azure subscription ID
- `time` - Unix timestamp of recording

---

## Recording Capabilities

### HTTP Recording Features

#### Automatic Sanitization
The framework automatically sanitizes sensitive data:
- Authorization headers → `SANITIZED`
- Container registry tokens
- Storage account SAS signatures
- Key Vault secrets
- Container app secrets

#### Smart Matching
Custom matchers handle dynamic Azure resources:
- Role assignment GUIDs are ignored in matching
- Container app operation result query parameters are ignored
- Host mapping for `httptest.NewServer` URLs

#### Passthrough for Personal Data
Certain endpoints bypass recording to avoid storing personal information:
- `login.microsoftonline.com`
- `graph.microsoft.com`
- `applicationinsights.azure.com`
- azd release/update endpoints

#### Fast-Forward Polling
Long-running operations are automatically fast-forwarded during recording to avoid storing hundreds of polling requests.

### Command Recording Features

The `cmdrecord` package intercepts specific command invocations:

```go
cmdrecord.NewWithOptions(cmdrecord.Options{
    CmdName:      "docker",
    CassetteName: name,
    RecordMode:   opt.mode,
    Intercepts: []cmdrecord.Intercept{
        {ArgsMatch: "^login"},
        {ArgsMatch: "^push"},
    },
})
```

This creates a proxy executable that:
1. Intercepts matching command invocations
2. Records inputs/outputs to a separate YAML file
3. Replays during playback mode

---

## Creating a New Functional Test with Recording

### Step-by-Step Guide

#### 1. Create Your Test Function

```go
func Test_CLI_MyNewFeature(t *testing.T) {
    t.Parallel() // Most tests run in parallel

    ctx, cancel := newTestContext(t)
    defer cancel()

    // Create a temporary directory for test files
    dir := tempDirWithDiagnostics(t)
    t.Logf("DIR: %s", dir)

    // Start the recording session
    session := recording.Start(t)
    
    // Generate or retrieve environment name
    envName := randomOrStoredEnvName(session)
    t.Logf("AZURE_ENV_NAME: %s", envName)

    // Create CLI with recording session
    cli := azdcli.NewCLI(t, azdcli.WithSession(session))
    cli.WorkingDirectory = dir
    cli.Env = append(cli.Env, os.Environ()...)
    cli.Env = append(cli.Env, "AZURE_LOCATION=eastus2")

    // Setup cleanup (only runs in live mode, not playback)
    defer cleanupDeployments(ctx, t, cli, session, envName)

    // ... rest of test logic
}
```

#### 2. Use Session-Aware HTTP Clients

For Azure SDK clients, use the session's ProxyClient:

```go
var client *http.Client
subscriptionId := cfg.SubscriptionID

if session != nil {
    client = session.ProxyClient
    
    if session.Playback {
        // Use recorded subscription ID
        subscriptionId = session.Variables[recording.SubscriptionIdKey]
    }
} else {
    client = http.DefaultClient
}

// Create Azure SDK client with session transport
cred := azdcli.NewTestCredential(cli)
rgClient, err := armresources.NewResourceGroupsClient(subscriptionId, cred, &arm.ClientOptions{
    ClientOptions: azcore.ClientOptions{
        Transport: client,
    },
})
```

#### 3. Store and Retrieve Variables

For values that change between recording and playback:

```go
// During test execution
if session != nil {
    // This will be stored in the recording
    session.Variables[recording.SubscriptionIdKey] = env[environment.SubscriptionIdEnvVarName]
}

// When reading back
if session != nil && session.Playback {
    subscriptionId = session.Variables[recording.SubscriptionIdKey]
}
```

Use `randomOrStoredEnvName()` helper for environment names:

```go
// Automatically handles recording vs playback
envName := randomOrStoredEnvName(session)
```

#### 4. Handle Time-Dependent Operations

For operations that validate timing or poll:

```go
if session == nil {
    // Live mode - use real delays
    err = probeServiceHealth(
        t, ctx, http.DefaultClient, 
        retry.NewConstant(5*time.Second), 
        url, expectedResponse)
} else {
    // Recording/playback mode - use minimal delays
    err = probeServiceHealth(
        t, ctx, session.ProxyClient, 
        retry.NewConstant(1*time.Millisecond), 
        url, expectedResponse)
}
```

#### 5. Add Cleanup Logic

```go
defer cleanupDeployments(ctx, t, cli, session, envName)
```

This helper (from `test/functional/aspire_test.go`) deletes subscription deployments only in live mode.

---

## Re-recording an Existing Test

### Local Development

1. **Delete existing recording**:
   ```bash
   rm test/functional/testdata/recordings/Test_CLI_MyNewFeature.yaml
   rm test/functional/testdata/recordings/Test_CLI_MyNewFeature.*.yaml  # if command recordings exist
   ```

2. **Set recording mode**:
   ```bash
   export AZURE_RECORD_MODE=record
   ```

3. **Ensure you're authenticated**:
   ```bash
   azd auth login
   ```

4. **Run the test**:
   ```bash
   cd cli/azd
   go test -v -run ^Test_CLI_MyNewFeature$ ./test/functional -timeout 30m
   ```

5. **Verify recordings were created**:
   ```bash
   ls -lh test/functional/testdata/recordings/Test_CLI_MyNewFeature*
   ```

### What Happens During Recording

1. **azd binary Built with Record Tag**: The test automatically builds `azd-record` with the `record` build tag
2. **Recording Proxy Starts**: An HTTPS proxy starts at a random port
3. **Command Proxies Start**: Proxy executables for `docker`, `dotnet` are placed in temporary directories
4. **Environment Variables Set**:
   - `AZD_TEST_HTTPS_PROXY` → points to recording proxy
   - `PATH` → prepended with command proxy paths
   - `AZD_DEBUG_PROVISION_PROGRESS_DISABLE=true`
5. **Test Executes**: All HTTP calls and command invocations go through proxies
6. **Cassettes Saved**: On test success, interactions are saved to YAML files

### Recording File Structure

After recording, you'll see files like:

```
test/functional/testdata/recordings/
├── Test_CLI_MyNewFeature.yaml          # HTTP interactions
├── Test_CLI_MyNewFeature.docker.yaml   # Docker command recordings (if used)
└── Test_CLI_MyNewFeature.dotnet.yaml   # Dotnet command recordings (if used)
```

Each YAML file contains:
```yaml
---
version: 2
interactions:
  - id: 0
    request:
      method: PUT
      url: https://management.azure.com/...
      headers:
        Authorization: SANITIZED
      body: '...'
    response:
      status: 200 OK
      headers: {...}
      body: '...'
  - id: 1
    # ... more interactions
---
env_name: azdtest-w4c1619
subscription_id: faa080af-c1d8-40ad-9cce-e1a450ca5b57
time: "1744738873"
```

---

## Building azd with Recording Support

### Build Tags

azd has two build configurations relevant to testing:

1. **Standard build** (no tags):
   ```bash
   go build -o azd
   ```
   - No recording support
   - Uses `deps.go` (standard HTTP client, real clock)

2. **Recording build** (`-tags=record`):
   ```bash
   go build -tags=record -o azd-record
   ```
   - Includes recording support
   - Uses `deps_record.go` (accepts recording proxy settings, can use fixed clock)
   - Required for recording new tests or re-recording

### What the `record` Build Tag Enables

When built with `-tags=record`, azd:

1. **Accepts `AZD_TEST_HTTPS_PROXY`** environment variable:
   ```go
   // cmd/deps_record.go
   if val, ok := os.LookupEnv("AZD_TEST_HTTPS_PROXY"); ok {
       proxyUrl, err := url.Parse(val)
       transport.Proxy = http.ProxyURL(proxyUrl)
   }
   ```

2. **Uses self-signed certificates**:
   ```go
   transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
   ```

3. **Supports fixed clock** (via `AZD_TEST_FIXED_CLOCK_UNIX_TIME`):
   ```go
   func createClock() clock.Clock {
       if fixed, ok := fixedClock(); ok {
           return fixed
       }
       return clock.New()
   }
   ```

### Automatic Build Behavior

The test framework automatically builds the correct binary:

```go
// test/azdcli/cli.go
if opt.Session != nil {
    buildRecordOnce.Do(func() {
        build(t, sourceDir, "-tags=record", "-o=azd-record")
    })
} else {
    buildOnce.Do(func() {
        build(t, sourceDir)
    })
}
```

**Skip automatic build**:
```bash
export CLI_TEST_SKIP_BUILD=true
```

**Use custom binary path**:
```bash
export CLI_TEST_AZD_PATH=/path/to/my/azd-record
```

---

## Using Recordings in CI

### CI Configuration

In CI environments (detected via `CI` environment variable), the framework:

1. **Uses `ModeRecordOnce` by default** (if `AZURE_RECORD_MODE` not set)
2. **Expects recordings to exist**
3. **Fails with helpful message if missing**:
   ```
   failed to load recordings: file not found: 
   to record this test, re-run the test with AZURE_RECORD_MODE='record'
   ```

### CI Build Process

The `ci-build.ps1` script has a `-BuildRecordMode` flag:

```powershell
# ci-build.ps1
param(
    [switch] $BuildRecordMode,
    # ...
)

if ($BuildRecordMode) {
    $buildFlags += "-tags=record"
    Write-Host "Building with record tag enabled"
    & go build $buildFlags -o $outputPath
} else {
    Write-Host "Building standard binary"
    & go build -o azd
}
```

**Build both binaries in CI**:
```bash
# Standard build
./ci-build.ps1

# Recording build (for tests)
./ci-build.ps1 -BuildRecordMode
```

### CI Test Execution

```powershell
# ci-test.ps1 runs tests in two phases:

# 1. Unit tests (short mode)
gotestsum -- ./... -short -v -cover

# 2. Integration/Functional tests
gotestsum -- ./... -v -timeout 120m
```

The functional tests automatically:
- Use recordings if available (fast, no Azure calls)
- Skip automatic build (binaries pre-built)
- Use `CLI_TEST_AZD_PATH` if set

### Recommended CI Setup

```yaml
# Pseudo CI configuration
steps:
  - name: Build standard azd
    run: ./ci-build.ps1
    
  - name: Build azd-record for tests
    run: ./ci-build.ps1 -BuildRecordMode
    
  - name: Run tests with recordings
    env:
      AZURE_RECORD_MODE: playback  # Force playback mode
      CLI_TEST_AZD_PATH: ./azd-record
    run: ./ci-test.ps1
```

---

## Recording for Extensions

### Extension Testing with Recording

Extensions present unique challenges for recording because:
1. Extensions make gRPC callbacks to the main azd process
2. Extension operations often trigger Azure API calls through azd
3. Extension installation/build happens outside the recording session

### Example: `Test_CLI_Extension_Capabilities`

```go
func Test_CLI_Extension_Capabilities(t *testing.T) {
    // Skip in playback mode - extensions are too complex to record
    session := recording.Start(t)
    if session != nil && session.Playback {
        t.Skip("Skipping test in playback mode. This test is live only.")
    }

    // Generate env name before session for use in both phases
    envName := randomOrStoredEnvName(session)

    // Phase 1: Extension setup (NOT recorded)
    cliNoSession := azdcli.NewCLI(t)  // No session
    cliNoSession.WorkingDirectory = dir
    
    _, err := cliNoSession.RunCommand(ctx, "ext", "install", "microsoft.azd.extensions")
    require.NoError(t, err)
    
    // Phase 2: Main test (CAN be recorded)
    cli := azdcli.NewCLI(t, azdcli.WithSession(session))
    cli.WorkingDirectory = dir
    
    _, err = cli.RunCommandWithStdIn(ctx, stdinForInit(envName), "init")
    require.NoError(t, err)
}
```

### Why Extensions Are Challenging

1. **gRPC Callbacks**: Extensions communicate with azd via gRPC, which isn't captured by HTTP recording
2. **Nested Operations**: Extension calls trigger azd commands, creating complex recording chains
3. **Build Dependencies**: Extensions need to be built before tests, outside recording context

### Strategies for Extension Testing

#### Option 1: Live-Only Tests (Current Approach)
```go
if session != nil && session.Playback {
    t.Skip("Skipping test in playback mode. This test is live only.")
}
```

**Pros**: Simple, reliable
**Cons**: Slow in CI, requires Azure credentials

#### Option 2: Partial Recording
```go
// Install extension without recording
cliNoSession := azdcli.NewCLI(t)
_, err := cliNoSession.RunCommand(ctx, "ext", "install", "...")

// Record the actual test operations
cli := azdcli.NewCLI(t, azdcli.WithSession(session))
_, err = cli.RunCommand(ctx, "up")
```

**Pros**: Records Azure interactions
**Cons**: Still requires extension installation in CI

#### Option 3: Mock Extension Services
For unit tests of extension framework:
```go
// Use mock gRPC services
mockExtension := &mockExtensionService{...}
// Test framework behavior without real extensions
```

**Pros**: Fast, deterministic
**Cons**: Doesn't test real extension integration

### Extension CI Builds

Extensions also use recording build tags:

```powershell
# extensions/microsoft.azd.demo/ci-build.ps1
if ($BuildRecordMode) {
    $buildFlags += "-tags=record"
}
& go build $buildFlags -o azd-ext-microsoft-azd-demo
```

This allows extensions themselves to be tested with recordings if needed.

---

## Best Practices and Tips

### 1. Use `randomOrStoredEnvName()` Helper

**DO**:
```go
envName := randomOrStoredEnvName(session)
```

**DON'T**:
```go
envName := randomEnvName()  // Same name won't be used in playback!
```

### 2. Always Check Session Mode for Live Operations

**DO**:
```go
if session != nil && session.Playback {
    // Skip cleanup in playback
    return
}
// Perform cleanup
client.DeleteResourceGroup(...)
```

**DON'T**:
```go
// Always try cleanup
client.DeleteResourceGroup(...)  // Will fail in playback mode!
```

### 3. Use Session's ProxyClient for HTTP Operations

**DO**:
```go
client := http.DefaultClient
if session != nil {
    client = session.ProxyClient
}
azureClient, err := armresources.NewResourceGroupsClient(subId, cred, &arm.ClientOptions{
    ClientOptions: azcore.ClientOptions{
        Transport: client,
    },
})
```

### 4. Minimize Live Test Dependencies

**DO**:
```go
// Copy sample project to temp dir
err := copySample(dir, "webapp")

// Run azd commands
_, err = cli.RunCommand(ctx, "init")
```

**DON'T**:
```go
// Clone from GitHub (not recorded!)
exec.Command("git", "clone", "https://github.com/...").Run()
```

### 5. Store Dynamic Values in Session Variables

**DO**:
```go
if session != nil {
    session.Variables[recording.SubscriptionIdKey] = actualSubId
    session.Variables["custom_resource_id"] = resourceId
}
```

### 6. Use Appropriate Timeouts

**DO**:
```go
if session == nil {
    // Live - real delays
    time.Sleep(5 * time.Second)
} else {
    // Playback - minimal delays
    time.Sleep(1 * time.Millisecond)
}
```

### 7. Add Debug Logging

```go
t.Logf("Recording mode: playback=%v", session != nil && session.Playback)
t.Logf("Environment name: %s", envName)
t.Logf("Subscription ID: %s", subscriptionId)
```

### 8. Handle Skipped Tests Gracefully

**DO**:
```go
func Test_CLI_ComplexFeature(t *testing.T) {
    session := recording.Start(t)
    
    // Skip if specific conditions aren't met
    if session != nil && session.Playback && someComplexCondition {
        t.Skip("This test requires live mode for complex operations")
    }
    
    // ... test continues
}
```

---

## Troubleshooting

### Problem: Recording Not Found

**Error**:
```
failed to load recordings: file not found
to record this test, re-run the test with AZURE_RECORD_MODE='record'
```

**Solutions**:
1. Set `AZURE_RECORD_MODE=record`
2. Run test to create recording
3. Commit the recording file to git

---

### Problem: Test Fails During Playback But Passes Live

**Symptoms**:
- Test passes with `AZURE_RECORD_MODE=live`
- Test fails with `AZURE_RECORD_MODE=playback`

**Common Causes**:

1. **Using wrong HTTP client**:
   ```go
   // WRONG - bypasses proxy
   client := http.DefaultClient
   
   // RIGHT - uses session client
   client := http.DefaultClient
   if session != nil {
       client = session.ProxyClient
   }
   ```

2. **Hard-coded values instead of session variables**:
   ```go
   // WRONG - uses live subscription
   subId := cfg.SubscriptionID
   
   // RIGHT - uses recorded subscription
   subId := cfg.SubscriptionID
   if session != nil && session.Playback {
       subId = session.Variables[recording.SubscriptionIdKey]
   }
   ```

3. **Not storing subscription ID in session**:
   ```go
   // Add this after provision/deployment:
   if session != nil {
       session.Variables[recording.SubscriptionIdKey] = env.GetSubscriptionId()
   }
   ```

---

### Problem: Request Not Matching Recorded Interaction

**Error**:
```
could not find matching request
```

**Diagnosis**:
1. Enable debug logging: `export RECORDER_PROXY_DEBUG=1`
2. Run test and check logs for matching differences

**Common Causes**:

1. **Query parameter differences**:
   - Check if query params are in different order
   - Consider adding custom matcher

2. **Dynamic request IDs or timestamps**:
   - These should be sanitized
   - Check sanitization code in `test/recording/sanitize.go`

3. **Host differences**:
   - Use `WithHostMapping` option if needed:
   ```go
   session := recording.Start(t, recording.WithHostMapping(server.URL, "localhost:8080"))
   ```

---

### Problem: Sensitive Data in Recordings

**Issue**: Authorization tokens, SAS signatures, or secrets appearing in recordings

**Solutions**:

1. **Check existing sanitization**:
   Look at `test/recording/sanitize.go` for examples

2. **Add custom sanitization**:
   ```go
   // In recording.go, add a new hook:
   vcr.AddHook(func(i *cassette.Interaction) error {
       if strings.Contains(i.Request.URL, "/myservice/") {
           i.Request.Headers.Set("X-Custom-Token", "SANITIZED")
       }
       return nil
   }, recorder.BeforeSaveHook)
   ```

3. **Add passthrough for sensitive endpoints**:
   ```go
   vcr.AddPassthrough(func(req *http.Request) bool {
       return strings.Contains(req.URL.Host, "sensitive-service.com")
   })
   ```

---

### Problem: Command Not Being Recorded

**Symptoms**: Docker or dotnet commands execute but aren't recorded

**Diagnosis**:
1. Check if command proxy is in PATH:
   ```go
   t.Logf("PATH: %s", os.Getenv("PATH"))
   ```

2. Check if command matches intercept pattern:
   ```go
   // Only these patterns are intercepted for docker:
   Intercepts: []cmdrecord.Intercept{
       {ArgsMatch: "^login"},
       {ArgsMatch: "^push"},
   }
   ```

**Solutions**:

1. **Add new intercept pattern**:
   ```go
   // In recording.go
   recorders = append(recorders, cmdrecord.NewWithOptions(cmdrecord.Options{
       CmdName:      "docker",
       CassetteName: name,
       RecordMode:   opt.mode,
       Intercepts: []cmdrecord.Intercept{
           {ArgsMatch: "^login"},
           {ArgsMatch: "^push"},
           {ArgsMatch: "^build"},  // Add new pattern
       },
   }))
   ```

2. **Verify proxy is first in PATH**:
   ```go
   cli.Env = append(cli.Env, 
       "PATH="+strings.Join(session.CmdProxyPaths, string(os.PathListSeparator))+
       string(os.PathListSeparator)+os.Getenv("PATH"))
   ```

---

### Problem: Recordings Are Too Large

**Issue**: Recording files over 1MB, causing git/review issues

**Solutions**:

1. **Enable response trimming** (already enabled for deployments):
   - Large deployment responses are automatically trimmed
   - See `test/recording/trim_response.go`

2. **Add polling fast-forward**:
   - Polling operations are automatically discarded
   - See `httpPollDiscarder` in `recording.go`

3. **Split test into smaller tests**:
   ```go
   // Instead of one large Test_CLI_FullWorkflow
   func Test_CLI_Provision(t *testing.T) { ... }
   func Test_CLI_Deploy(t *testing.T) { ... }
   func Test_CLI_Down(t *testing.T) { ... }
   ```

---

### Problem: Test Timing Out

**Symptoms**: Test runs forever in recording mode

**Common Causes**:

1. **Waiting for resources that never complete**:
   - Check if provision is hanging
   - Look for infinite retry loops

2. **Not using session-aware delays**:
   ```go
   // Use conditional delays
   backoff := retry.NewConstant(5*time.Second)
   if session != nil {
       backoff = retry.NewConstant(1*time.Millisecond)
   }
   ```

---

### Problem: Recording Proxy Fails to Start

**Error**:
```
failed to create proxy client: ...
```

**Solutions**:

1. **Port already in use**:
   - Proxy uses random port, should be rare
   - Check for other tests running in parallel

2. **Certificate issues**:
   - Ensure `-tags=record` build accepts self-signed certs
   - Check `cmd/deps_record.go` configuration

---

## Advanced Topics

### Custom Matchers

Add custom request matching logic:

```go
// In your test or in recording.go
vcr.SetMatcher(func(r *http.Request, i cassette.Request) bool {
    // Custom matching logic
    if strings.Contains(r.URL.Path, "/special-resource/") {
        // Ignore resource ID in matching
        return r.Method == i.Method && 
               matchesWithoutId(r.URL.Path, i.URL)
    }
    
    return cassette.DefaultMatcher(r, i)
})
```

### Host Mapping for Test Servers

When using `httptest.NewServer`:

```go
server := httptest.NewServer(handler)
defer server.Close()

session := recording.Start(t, 
    recording.WithHostMapping(
        strings.TrimPrefix(server.URL, "http://"), 
        "localhost:8080"))
```

### Fixed Clock for Time-Dependent Tests

```go
// Set environment variable
os.Setenv("AZD_TEST_FIXED_CLOCK_UNIX_TIME", "1744738873")

// azd will use fixed time in recording mode
// Useful for deployment name generation
```

---

## Summary Checklist

### Creating a New Test ✓
- [ ] Start with `recording.Start(t)`
- [ ] Use `randomOrStoredEnvName(session)`
- [ ] Create CLI with `azdcli.WithSession(session)`
- [ ] Use `session.ProxyClient` for HTTP operations
- [ ] Store dynamic values in `session.Variables`
- [ ] Add cleanup with session checks
- [ ] Use appropriate timeouts for recording/playback

### Recording a Test ✓
- [ ] Set `AZURE_RECORD_MODE=record`
- [ ] Ensure Azure authentication is configured
- [ ] Run test: `go test -v -run ^TestName$ ./test/functional -timeout 30m`
- [ ] Verify recordings created in `testdata/recordings/`
- [ ] Commit recordings to git

### Debugging Recording Issues ✓
- [ ] Enable debug logging: `RECORDER_PROXY_DEBUG=1`
- [ ] Check session mode: `t.Logf("Playback: %v", session.Playback)`
- [ ] Verify HTTP client usage
- [ ] Check session variable storage
- [ ] Review sanitization for sensitive data

### CI Integration ✓
- [ ] Build with `-BuildRecordMode` for test binaries
- [ ] Commit recordings to repository
- [ ] Set `AZURE_RECORD_MODE=playback` in CI (optional)
- [ ] Ensure recordings are up to date

---

## Resources

- **Recording Package**: `cli/azd/test/recording/`
- **Command Recording**: `cli/azd/test/cmdrecord/`
- **Test Helpers**: `cli/azd/test/azdcli/`
- **Example Tests**: `cli/azd/test/functional/up_test.go`
- **go-vcr Documentation**: https://github.com/dnaeon/go-vcr

---

**Last Updated**: December 2025  
**Maintainers**: Azure Developer CLI Team
