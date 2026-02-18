# Azure AI Models Extension - Code Review Guide

This guide provides a structured checklist for reviewing code changes to the `azure.ai.models` extension. It is designed to be used by reviewers and AI assistants (like GitHub Copilot) to provide consistent, thorough reviews.

---

## Review Categories

1. [Error Handling](#1-error-handling)
2. [Security](#2-security)
3. [HTTP Client Usage](#3-http-client-usage)
4. [Input Validation](#4-input-validation)
5. [Memory & Performance](#5-memory--performance)
6. [Cobra Commands](#6-cobra-commands)
7. [Build Scripts](#7-build-scripts)
8. [Documentation](#8-documentation)
9. [Testing](#9-testing)
10. [User Experience](#10-user-experience)

---

## 1. Error Handling

### Check: Are all errors returned?
Look for function calls that return errors but are ignored:

```go
// ❌ FLAGGED: Error ignored
utils.PrintObject(result, format)

// ✅ CORRECT
if err := utils.PrintObject(result, format); err != nil {
    return err
}
```

**Review Comment Template:**
> `utils.PrintObject(...)` returns an error that is ignored here. If printing fails, the command will report success incorrectly. Please capture and return the error.

### Check: Are errors wrapped with context?
```go
// ❌ FLAGGED: Original error lost
return fmt.Errorf("upload failed")

// ✅ CORRECT
return fmt.Errorf("upload failed: %w", err)
```

**Review Comment Template:**
> The returned error loses the underlying `err`, making debugging harder. Return a wrapped error: `fmt.Errorf("upload failed: %w", err)`

### Check: Silent error suppression
```go
// ❌ FLAGGED: Intentionally ignoring errors
_, _ = azdClient.Environment().SetValue(ctx, req)
```

**Review Comment Template:**
> `SetValue` errors are intentionally ignored. If the write fails, subsequent commands will fail in confusing ways. Either return the error or log it for debugging.

---

## 2. Security

### Check: HTTPS enforcement
```go
// ❌ FLAGGED: Accepts any scheme
baseURL := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)
```

**Review Comment Template:**
> This accepts `http://` URLs, which would send Azure AD bearer tokens over plaintext. Validate that `parsedURL.Scheme` equals `https` before creating the client.

### Check: No embedded credentials in URLs
```go
// ✅ Should check for this
if parsedURL.User != nil {
    return nil, fmt.Errorf("userinfo is not allowed in endpoint URL")
}
```

**Review Comment Template:**
> URLs with embedded credentials (e.g., `https://user:pass@host/...`) should be rejected to prevent credential leakage in logs.

### Check: Downloads verify integrity
When downloading executables:

**Review Comment Template:**
> The installer downloads an executable without integrity verification. Consider validating checksums/signatures, restricting redirects to known hosts, or at minimum checking `Content-Length` against reasonable limits.

### Check: Screenshots don't contain secrets
**Review Comment Template:**
> Screenshots appear to contain SAS tokens / API keys / subscription IDs. Please redact sensitive information. Even expired tokens should be redacted as a best practice.

---

## 3. HTTP Client Usage

### Check: Timeout is set
```go
// ❌ FLAGGED: No timeout
httpClient: &http.Client{}

// ✅ CORRECT
httpClient: &http.Client{Timeout: 30 * time.Second}
```

**Review Comment Template:**
> `http.Client{}` without a timeout can hang indefinitely on network stalls. Set a reasonable `Timeout` so CLI commands fail predictably.

### Check: Context-aware requests
```go
// ❌ FLAGGED: No context
resp, err := http.Get(url)

// ✅ CORRECT
req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
resp, err := client.Do(req)
```

**Review Comment Template:**
> `http.Get` uses the default client with no timeout and ignores the context. Use `http.NewRequestWithContext` with a configured client that has a timeout.

---

## 4. Input Validation

### Check: URL parsing is strict
```go
// ❌ FLAGGED: Allows extra segments
if len(pathParts) >= 3 && pathParts[0] == "api" { ... }

// ✅ CORRECT
if len(pathParts) != 3 || pathParts[0] != "api" { ... }
```

**Review Comment Template:**
> This accepts paths with extra segments (e.g., `/api/projects/p/extra`) and ignores them. This can silently accept malformed endpoints. Require exactly 3 segments and reject extras.

### Check: File vs directory detection
```go
// ❌ FLAGGED: Always appends /* assuming directory
sourceHint := fmt.Sprintf("%s/*", flags.Source)
```

**Review Comment Template:**
> This always appends `/*` to the source, which is incorrect for single files. Check `os.Stat().IsDir()` before appending a wildcard.

### Check: Nil pointer guards
```go
// ❌ FLAGGED: No nil check before Elem()
if v.Kind() == reflect.Ptr {
    v = v.Elem()  // PANICS if v.IsNil()
}
```

**Review Comment Template:**
> `v.Elem()` is called without checking `v.IsNil()`. If a nil pointer is passed, this will panic. Add `if v.IsNil() { return error }` before calling `Elem()`.

---

## 5. Memory & Performance

### Check: Unnecessary allocations
```go
// ❌ FLAGGED: []byte → string → []byte conversion
body, _ := json.Marshal(req)
http.NewRequestWithContext(ctx, method, url, strings.NewReader(string(body)))

// ✅ CORRECT
body, _ := json.Marshal(req)
http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
```

**Review Comment Template:**
> `strings.NewReader(string(body))` causes an extra allocation by converting `[]byte` to `string`. Use `bytes.NewReader(body)` directly.

---

## 6. Cobra Commands

### Check: Persistent flags passed by value
```go
// ❌ FLAGGED: rootFlags passed by value
func newInitCommand(rootFlags rootFlagsDefinition) *cobra.Command {
    // ...flags is a snapshot, won't reflect parsed values
}
```

**Review Comment Template:**
> `rootFlags` is passed by value, so `--no-prompt` and `--debug` flags won't be visible after parsing. Either pass by pointer or reference the global `rootFlags` directly from the command.

### Check: Referenced commands exist
```go
// ❌ FLAGGED: Suggests command that doesn't exist
fmt.Printf("  azd ai models custom register --name %s\n", name)
```

**Review Comment Template:**
> The error message suggests running `custom register`, but this command isn't implemented. Either implement the command or update the message to suggest an existing command.

---

## 7. Build Scripts

### Check: ldflags quoting
```powershell
# ❌ FLAGGED: Single quotes passed to linker
-ldflags="-X 'pkg.Version=$Version'"

# ✅ CORRECT
-ldflags="-X pkg.Version=$Version"
```

**Review Comment Template:**
> The single quotes around `-X` values are passed through to the linker and can break version stamping. Remove the inner single quotes.

### Check: PowerShell argument passing
```powershell
# ❌ FLAGGED: Missing compatibility setting
# (script runs go build without PSNativeCommandArgumentPassing)

# ✅ CORRECT
$PSNativeCommandArgumentPassing = 'Legacy'
```

**Review Comment Template:**
> Other extension CI scripts set `$PSNativeCommandArgumentPassing = 'Legacy'` to avoid argument quoting issues with `go build`. Add this for consistency.

---

## 8. Documentation

### Check: Stable URLs
```markdown
<!-- ❌ FLAGGED: Personal fork with commit hash -->
https://raw.githubusercontent.com/username/repo/abc123/...
```

**Review Comment Template:**
> Documentation links to a personal fork with a specific commit hash. This will break when the fork/commit changes. Use the official repo URL or create an aka.ms short link.

### Check: Link to official docs
```markdown
<!-- ❌ FLAGGED: Duplicating installation instructions -->
Install via winget: winget install microsoft.azd
```

**Review Comment Template:**
> Avoid duplicating installation instructions that may become outdated. Link to the official docs: https://aka.ms/azd-install

---

## 9. Testing

### Check: Parsing functions have tests
**Review Comment Template:**
> `parseProjectEndpoint()` has multiple conditional branches but no unit tests. Add table-driven tests covering valid inputs, edge cases (empty segments), and invalid inputs (extra segments, wrong format).

### Check: Test coverage for error paths
**Review Comment Template:**
> This function has error paths that aren't covered by tests. Add test cases for: nil input, invalid URL format, missing required fields.

---

## 10. User Experience

### Check: Spinner errors handled
```go
// ❌ FLAGGED: Spinner error printed but operation continues
if err := spinner.Start(ctx); err != nil {
    fmt.Printf("failed to start spinner: %v\n", err)
}
```

**Review Comment Template:**
> Consider whether spinner failures should be logged at debug level rather than printed to stdout, as they're not actionable by users.

### Check: Recovery instructions are actionable
**Review Comment Template:**
> The recovery instructions are helpful, but verify that the suggested command exists and works as described.

---

## Quick Review Checklist

Copy this checklist into your PR review:

```markdown
### Code Review Checklist

**Error Handling**
- [ ] All errors are returned, not ignored
- [ ] Errors are wrapped with `%w`
- [ ] No silent error suppression

**Security**
- [ ] HTTPS enforced for endpoints with auth tokens
- [ ] No embedded credentials accepted in URLs
- [ ] Downloaded executables verified (if applicable)
- [ ] Screenshots redact sensitive info (if applicable)

**HTTP Clients**
- [ ] Timeouts configured
- [ ] Context-aware requests used

**Input Validation**
- [ ] URL/path parsing is strict (exact matches)
- [ ] Nil checks before `.Elem()` calls
- [ ] File vs directory detected correctly

**Performance**
- [ ] No unnecessary `[]byte → string → []byte` conversions

**Commands**
- [ ] Persistent flags not passed by value
- [ ] Referenced commands actually exist

**Build Scripts**
- [ ] `-ldflags` quoting is correct
- [ ] `$PSNativeCommandArgumentPassing = 'Legacy'` set (PowerShell)

**Docs**
- [ ] URLs point to stable/official sources
- [ ] Links to official docs instead of duplicating

**Tests**
- [ ] Parsing/validation functions have table-driven tests
- [ ] Error paths are tested
```

---

## Review Comment Templates

### For Error Handling Issues
> **Error not returned:** `{function}` returns an error that is ignored. Please capture and return it to ensure proper error propagation.

### For Security Issues
> **Security concern:** {Description of issue}. This could lead to {consequence}. Please {suggested fix}.

### For Missing Tests
> **Missing tests:** `{function}` has multiple branches but no unit tests. Please add table-driven tests covering valid, empty, and invalid inputs.

### For Documentation Issues
> **Documentation:** {Description}. Consider linking to official docs at {URL} instead.

### For Performance Issues
> **Unnecessary allocation:** {Description}. Use `{suggested alternative}` to avoid the extra allocation.
