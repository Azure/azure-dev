# Go 1.26 Modernization Analysis — Azure Developer CLI

**Date:** 2026-03-12  
**Go Version:** 1.26 (go1.26.1 darwin/arm64)  
**Analyzed by:** GPT-5.4 (feature research) + Claude Opus 4.6 (codebase scan)  
**Scope:** `cli/azd/` — pkg, cmd, internal

---

## Executive Summary

The azd codebase is **already well-modernized** — 266 usages of `slices`/`maps` packages, zero `ioutil`, and `errors.Join` in use. However, Go 1.24–1.26 introduced several impactful features the project hasn't adopted yet. This document maps new Go features to concrete azd opportunities, organized by impact and effort.

### Top 5 Opportunities

| # | Feature | Impact | Effort | Details |
|---|---------|--------|--------|---------|
| 1 | `errors.AsType[T]` (1.26) | 🟢 High | Low | Generic, type-safe error matching — eliminates boilerplate |
| 2 | `WaitGroup.Go` (1.25) | 🟢 High | Medium | Simplifies concurrent goroutine patterns across CLI |
| 3 | `omitzero` JSON tag (1.24) | 🟡 Medium | Low | Better zero-value omission for `time.Time` and optional fields |
| 4 | `os.Root` (1.24/1.25) | 🟡 Medium | Medium | Secure directory-scoped file access |
| 5 | `testing.T.Context` / `B.Loop` (1.24) | 🟡 Medium | Low | Modern test patterns |

---

## Part 1: New Go 1.24–1.26 Features & azd Applicability

### 🔤 Language Changes

#### Generic Type Aliases (Go 1.24)
```go
// Now supported:
type Set[T comparable] = map[T]bool
type Result[T any] = struct { Value T; Err error }
```
**azd applicability:** Could simplify type layering in `pkg/project/` and `pkg/infra/` where wrapper types are common. Low priority — most types are already well-defined.

#### `new(expr)` — Initialize Pointer from Expression (Go 1.26)
```go
// Before
v := "hello"
ptr := &v

// After
ptr := new("hello")
```
**azd applicability:** Useful for optional pointer fields in protobuf/JSON structs. The codebase has several `to.Ptr()` helper patterns that this could partially replace. Medium value in new code.

#### Self-Referential Generic Constraints (Go 1.26)
```go
type Adder[A Adder[A]] interface { Add(A) A }
```
**azd applicability:** Niche — useful for F-bounded polymorphism patterns. No immediate need in azd.

---

### ⚡ Performance Improvements (Free Wins)

These improvements apply automatically with Go 1.26 — no code changes needed:

| Feature | Source | Expected Impact |
|---------|--------|-----------------|
| **Green Tea GC** (default in 1.26) | Runtime | 10–40% GC overhead reduction |
| **Swiss Tables map impl** (1.24) | Runtime | 2–3% CPU reduction overall |
| **`io.ReadAll` optimization** (1.26) | stdlib | ~2x faster, lower memory |
| **cgo overhead -30%** (1.26) | Runtime | Faster cgo calls |
| **Stack-allocated slice backing** (1.25/1.26) | Compiler | Fewer heap allocations |
| **Container-aware GOMAXPROCS** (1.25) | Runtime | Better perf in containers |
| **`fmt.Errorf` for plain strings** (1.26) | stdlib | Less allocation, closer to `errors.New` |

**Action:** Simply building with Go 1.26 gets these for free. Consider benchmarking before/after to quantify improvements for azd's specific workload.

---

### 🛠️ Standard Library Additions to Adopt

#### 1. `errors.AsType[T]` (Go 1.26) — **HIGH IMPACT**

Generic, type-safe replacement for `errors.As`:

```go
// Before (current azd pattern — verbose, requires pre-declaring variable)
var httpErr *azcore.ResponseError
if errors.As(err, &httpErr) {
    // use httpErr
}

// After (Go 1.26 — single expression, type-safe)
if httpErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
    // use httpErr
}
```

**azd occurrences:** The codebase has ~200+ `errors.As` calls. This is the single highest-impact modernization:
- Eliminates pre-declared error variables
- Type is inferred from the generic parameter
- Faster than `errors.As` in many cases

**Files with heavy `errors.As` usage:**
- `pkg/azapi/` — Azure API error handling
- `pkg/infra/provisioning/` — deployment error classification
- `cmd/` — command error handling
- `internal/cmd/` — action error handling

#### 2. `sync.WaitGroup.Go` (Go 1.25) — **HIGH IMPACT**

```go
// Before (current azd pattern)
var wg sync.WaitGroup
for _, item := range items {
    wg.Add(1)
    go func() {
        defer wg.Done()
        process(item)
    }()
}
wg.Wait()

// After (Go 1.25)
var wg sync.WaitGroup
for _, item := range items {
    wg.Go(func() {
        process(item)
    })
}
wg.Wait()
```

**azd occurrences:** Multiple goroutine fan-out patterns across:
- `pkg/account/subscriptions_manager.go` — parallel tenant subscription fetching
- `pkg/project/` — parallel service operations
- `pkg/infra/` — parallel resource operations
- `internal/cmd/` — parallel deployment steps

**Benefits:** Eliminates `Add(1)` + `defer Done()` boilerplate, reduces goroutine leak risk.

#### 3. `omitzero` JSON Struct Tag (Go 1.24) — **MEDIUM IMPACT**

```go
// Before — omitempty doesn't work well with time.Time (zero time is not "empty")
type Config struct {
    CreatedAt time.Time `json:"createdAt,omitempty"` // BUG: zero time still serialized
}

// After — omitzero uses IsZero() method
type Config struct {
    CreatedAt time.Time `json:"createdAt,omitzero"` // Correct: omits zero time
}
```

**azd applicability:** Any struct with `time.Time` fields or custom types implementing `IsZero()`. Check `pkg/config/`, `pkg/project/`, `pkg/account/` for candidates.

#### 4. `os.Root` / `os.OpenRoot` (Go 1.24, expanded 1.25) — **MEDIUM IMPACT**

Secure, directory-scoped filesystem access that prevents path traversal:

```go
root, err := os.OpenRoot("/app/workspace")
if err != nil { ... }
defer root.Close()

// All operations are confined to /app/workspace
data, err := root.ReadFile("config.yaml")   // OK
data, err := root.ReadFile("../../etc/passwd") // Error: escapes root
```

**Go 1.25 additions:** `Chmod`, `Chown`, `MkdirAll`, `ReadFile`, `RemoveAll`, `Rename`, `Symlink`, `WriteFile`

**azd applicability:**
- `pkg/project/` — reading `azure.yaml` and project files
- `pkg/osutil/` — file utility operations
- Extension framework — sandboxing extension file access
- Any user-supplied path handling for security hardening

#### 5. `testing.T.Context` / `testing.B.Loop` (Go 1.24) — **MEDIUM IMPACT**

```go
// T.Context — automatic context tied to test lifecycle
func TestFoo(t *testing.T) {
    ctx := t.Context() // cancelled when test ends
    result, err := myService.Do(ctx)
}

// B.Loop — cleaner benchmarks
func BenchmarkFoo(b *testing.B) {
    for b.Loop() { // replaces for i := 0; i < b.N; i++
        doWork()
    }
}
```

**azd applicability:** The test suite has 2000+ test functions. `T.Context()` would simplify context creation in tests that currently use `context.Background()` or `context.TODO()`.

#### 6. `T.Chdir` (Go 1.24)

```go
// Before
oldDir, _ := os.Getwd()
os.Chdir(tempDir)
defer os.Chdir(oldDir)

// After
t.Chdir(tempDir) // auto-restored when test ends
```

**azd applicability:** Multiple test files change directory manually. `T.Chdir` is safer and cleaner.

#### 7. `bytes.Buffer.Peek` (Go 1.26)

Access buffered bytes without consuming them. Useful in streaming/parsing scenarios.

#### 8. `testing/synctest` (Go 1.25 GA)

Fake-time testing for concurrent code:

```go
synctest.Run(func() {
    go func() {
        time.Sleep(time.Hour) // instant in fake time
        ch <- result
    }()
    synctest.Wait() // wait for all goroutines in bubble
})
```

**azd applicability:** Could simplify testing of timeout/retry logic in `pkg/retry/`, `pkg/httputil/`, and polling operations.

#### 9. String/Bytes Iterator Helpers (Go 1.24)

```go
// New iterator-returning functions
for line := range strings.Lines(text) { ... }
for part := range strings.SplitSeq(text, ",") { ... }
for field := range strings.FieldsSeq(text) { ... }
```

**azd applicability:** Can replace `strings.Split` + loop patterns where only iteration is needed (avoids allocating the intermediate slice).

#### 10. `testing.T.Attr` / `T.ArtifactDir` (Go 1.25/1.26)

Structured test metadata and artifact directories for richer test output.

---

### 🔧 Tooling Changes to Leverage

#### `go fix` Modernizers (Go 1.26) — **Use Now**

The rewritten `go fix` tool includes automatic modernizers:
```bash
go fix ./...
```
This will automatically:
- Convert `interface{}` → `any`
- Simplify loop patterns
- Apply other Go version-appropriate modernizations

**Recommendation:** Run `go fix ./...` as a first pass before any manual changes.

#### `tool` Directives in `go.mod` (Go 1.24)

```
// go.mod
tool (
    golang.org/x/tools/cmd/stringer
    github.com/golangci/golangci-lint/cmd/golangci-lint
)
```

Replaces `tools.go` pattern for declaring tool dependencies. Pinned versions, cached execution.

**azd applicability:** The project likely uses build tools that could be declared here.

#### `go.mod` `ignore` Directive (Go 1.25)

Ignore directories that shouldn't be part of the module:
```
ignore vendor/legacy
```

**azd applicability:** Could be useful for the extensions directory structure.

#### `//go:fix inline` (Go 1.26)

Mark functions for source-level inlining by `go fix`:
```go
//go:fix inline
func deprecated() { newFunction() }
```

**azd applicability:** Useful when deprecating internal helper functions — `go fix` will inline callers automatically.

---

## Part 2: Codebase Scan — Specific Modernization Opportunities

### Quick Wins (< 1 hour total)

#### A. `math.Min` → built-in `min()` (1 site)

| File | Line |
|------|------|
| `pkg/account/subscriptions_manager.go` | 304 |

```go
// Before
numWorkers := int(math.Min(float64(len(tenants)), float64(maxWorkers)))
// After
numWorkers := min(len(tenants), maxWorkers)
```

#### B. `sort.Strings` → `slices.Sort` (2 files)

| File | Line |
|------|------|
| `internal/agent/tools/io/file_search.go` | 146 |
| `extensions/azure.ai.finetune/internal/cmd/validation.go` | 75 |

#### C. Manual keys collect → `slices.Sorted(maps.Keys(...))` (1 file)

| File | Line |
|------|------|
| `pkg/azdext/scope_detector.go` | 116-120 |

```go
// Before (5 lines)
keys := make([]string, 0, len(opts.CustomRules))
for k := range opts.CustomRules { keys = append(keys, k) }
slices.Sort(keys)
// After (1 line)
keys := slices.Sorted(maps.Keys(opts.CustomRules))
```

#### D. Manual slice clone → `slices.Clone` (1 file)

| File | Line |
|------|------|
| `pkg/project/service_target_external.go` | 298 |

```go
// Before
return append([]string{}, endpointsResp.Endpoints...), nil
// After
return slices.Clone(endpointsResp.Endpoints), nil
```

#### E. Run `go fix ./...` (automatic)

Converts remaining `interface{}` → `any` (7 hand-written occurrences) and applies other modernizations.

---

### Medium Effort (1–4 hours each)

#### F. `sort.Slice` → `slices.SortFunc` (~8 non-test files)

| File | Pattern |
|------|---------|
| `pkg/infra/provisioning_progress_display.go:182` | `sort.Slice(newlyDeployedResources, ...)` |
| `pkg/account/subscriptions_manager.go:329` | `sort.Slice(allSubscriptions, ...)` |
| `pkg/account/subscriptions.go:84,139,168` | `sort.Slice(...)` |
| + 3 more files | — |

```go
// Before
sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
// After
slices.SortFunc(items, func(a, b Item) int { return cmp.Compare(a.Name, b.Name) })
```

#### G. `http.NewRequest` → `http.NewRequestWithContext` (6 files)

| File |
|------|
| `tools/avmres/main.go:193` |
| `internal/telemetry/appinsights-exporter/transmitter.go:80` |
| `extensions/azure.ai.agents/internal/project/parser.go:1041,1106,1161` |
| `pkg/llm/github_copilot.go:388` |

#### H. Range-over-int conversions (2-3 sites)

| File | Convertible? |
|------|-------------|
| `pkg/password/generator_test.go:84` | ✅ Yes |
| `cmd/extensions.go:56` | ✅ Yes |
| `pkg/output/table.go:141` | ✅ Yes (reflect) |
| `pkg/yamlnode/yamlnode.go:235,297` | ❌ No (modifies i) |
| `pkg/apphost/eval.go:33` | ❌ No (modifies i) |

---

### Large Initiatives (Multi-day)

#### I. `sync.Map` → Generic Typed Wrapper (13 usages, 8 files)

Create a `SyncMap[K, V]` utility type to eliminate type assertions:

```go
type SyncMap[K comparable, V any] struct { m sync.Map }

func (s *SyncMap[K, V]) Load(key K) (V, bool) { ... }
func (s *SyncMap[K, V]) Store(key K, value V)  { ... }
func (s *SyncMap[K, V]) Delete(key K)           { ... }
func (s *SyncMap[K, V]) Range(f func(K, V) bool) { ... }
```

**Files to update:**
- `internal/cmd/add/add_select_ai.go`
- `pkg/grpcbroker/message_broker.go`
- `pkg/auth/credential_providers.go`
- `pkg/devcentersdk/developer_client.go`
- `pkg/ux/canvas.go`
- `pkg/ai/model_service.go`
- `pkg/containerapps/container_app.go`

#### J. `log.Printf` → `slog` Structured Logging (309 sites)

This is the **highest-volume opportunity** but requires an architecture decision:
- Define project-wide `slog.Handler` configuration
- Integrate with existing `internal/tracing/` system
- Map current `log.Printf` calls to appropriate `slog.Info`/`slog.Debug` levels
- Consider `--debug` flag integration

**Recommendation:** Treat as a separate initiative with its own design doc.

#### K. `errors.AsType` Migration (200+ sites)

Systematic migration of `errors.As` → `errors.AsType[T]` across the codebase.

**Recommendation:** Can be done incrementally — start with the highest-traffic error handling paths in `pkg/azapi/` and `pkg/infra/`.

---

## Part 3: Testing Modernization

### Current State
- **2000+ test functions** across the codebase
- Uses `testify/mock` for mocking
- Table-driven tests are the standard pattern

### Opportunities

| Feature | Where to Apply | Impact |
|---------|---------------|--------|
| `T.Context()` | Tests using `context.Background()` | Cleaner test lifecycle |
| `T.Chdir()` | Tests with manual `os.Chdir` | Safer directory changes |
| `B.Loop()` | Benchmarks using `for i := 0; i < b.N; i++` | Cleaner benchmarks |
| `testing/synctest` | Timeout/retry/polling tests | Deterministic timing |
| `T.ArtifactDir()` | Tests generating output files | Organized test artifacts |
| `testing/cryptotest.SetGlobalRandom` | Crypto tests needing determinism | Reproducible crypto tests |

---

## Part 4: Security Improvements

| Feature | Description | azd Relevance |
|---------|-------------|---------------|
| `os.Root` (1.24/1.25) | Path-traversal-safe file access | Project file reading, extension sandbox |
| `net/http.CrossOriginProtection` (1.25) | Built-in CSRF via Fetch Metadata | Local dev server callbacks |
| Post-quantum TLS (1.26) | `SecP256r1MLKEM768`, `SecP384r1MLKEM1024` | Automatic via Go runtime |
| RSA min 1024-bit (1.24) | Enforced by crypto/rsa | Automatic |
| Heap base randomization (1.26) | ASLR improvement | Automatic |

---

## Part 5: PR-Sized Adoption Roadmap

Each item below is scoped to a single, small PR that can be reviewed independently.

---

### PR 1: `go fix` automated modernizations
**Files:** All `.go` files (auto-applied)  
**Changes:** `interface{}` → `any`, loop simplifications, other `go fix` modernizers  
**Review notes:** Pure mechanical transform — reviewer just confirms no behavioral changes  
**Commands:**
```bash
go fix ./...
gofmt -s -w .
```

---

### PR 2: Built-in `min()` and `slices.Sort` quick wins
**Files:** 3 files, ~5 lines changed  
**Changes:**
- `pkg/account/subscriptions_manager.go`: `math.Min(float64, float64)` → `min()`
- `internal/agent/tools/io/file_search.go`: `sort.Strings` → `slices.Sort`
- `extensions/azure.ai.finetune/internal/cmd/validation.go`: `sort.Strings` → `slices.Sort`

---

### PR 3: `slices.Clone` and `slices.Sorted(maps.Keys(...))` one-liners
**Files:** 2 files, ~6 lines changed  
**Changes:**
- `pkg/project/service_target_external.go`: `append([]T{}, s...)` → `slices.Clone(s)`
- `pkg/azdext/scope_detector.go`: manual keys collect + sort → `slices.Sorted(maps.Keys(...))`

---

### PR 4: `sort.Slice` → `slices.SortFunc` in account package
**Files:** 2 files (~4 call sites)  
**Changes:**
- `pkg/account/subscriptions_manager.go`: `sort.Slice` → `slices.SortFunc` with `cmp.Compare`
- `pkg/account/subscriptions.go`: 3 × `sort.Slice` → `slices.SortFunc`

---

### PR 5: `sort.Slice` → `slices.SortFunc` in remaining packages
**Files:** ~4 files  
**Changes:**
- `pkg/infra/provisioning_progress_display.go`
- Other non-test files with `sort.Slice`
- Does **not** touch `sort.Sort` on `sort.Interface` types (leave those as-is)

---

### PR 6: `http.NewRequest` → `http.NewRequestWithContext`
**Files:** 6 files  
**Changes:**
- `tools/avmres/main.go`
- `internal/telemetry/appinsights-exporter/transmitter.go`
- `extensions/azure.ai.agents/internal/project/parser.go` (3 sites)
- `pkg/llm/github_copilot.go`
- Thread existing `ctx` or use `context.Background()` explicitly

---

### PR 7: Range-over-int modernization
**Files:** 3 files, ~3 lines each  
**Changes:**
- `pkg/password/generator_test.go`: `for i := 0; i < len(choices); i++` → `for i := range len(choices)`
- `cmd/extensions.go`: similar
- `pkg/output/table.go`: `for i := 0; i < v.Len(); i++` → `for i := range v.Len()`
- Skips parser loops where `i` is modified inside the body

---

### PR 8: Generic `SyncMap[K, V]` utility type
**Files:** 1 new file + 8 files updated  
**Changes:**
- Create `pkg/sync/syncmap.go` with `SyncMap[K, V]` generic wrapper
- Update 8 files to use typed map instead of `sync.Map` + type assertions

---

### PR 9: Adopt `errors.AsType[T]` — Azure API error paths
**Files:** `pkg/azapi/` (focused scope)  
**Changes:**
- Replace `var errT *T; errors.As(err, &errT)` → `errT, ok := errors.AsType[*T](err)`
- Start with the highest-traffic error handling paths

---

### PR 10: Adopt `errors.AsType[T]` — infra/provisioning
**Files:** `pkg/infra/provisioning/` and `pkg/infra/`  
**Changes:** Same pattern as PR 9, scoped to infrastructure error handling

---

### PR 11: Adopt `errors.AsType[T]` — commands and actions
**Files:** `cmd/` and `internal/cmd/`  
**Changes:** Same pattern, scoped to command-level error handling

---

### PR 12: Adopt `WaitGroup.Go` in concurrent patterns
**Files:** 4-6 files with goroutine fan-out  
**Changes:**
- `pkg/account/subscriptions_manager.go`: parallel tenant fetching
- `pkg/project/`: parallel service operations
- Other files with `wg.Add(1); go func() { defer wg.Done(); ... }()` pattern
- Replace with `wg.Go(func() { ... })`

---

### PR 13: Test modernization — `T.Context()` adoption (batch 1)
**Files:** ~20 test files (scoped to one package, e.g., `pkg/project/`)  
**Changes:**
- Replace `context.Background()` / `context.TODO()` → `t.Context()` in tests
- One package at a time to keep PRs reviewable

---

### PR 14: Test modernization — `T.Chdir()` adoption
**Files:** Test files with manual `os.Chdir` + defer restore  
**Changes:** Replace with `t.Chdir(dir)` — auto-restored when test ends

---

### Future PRs (require design discussion first)

| PR | Topic | Notes |
|----|-------|-------|
| 15+ | `omitzero` JSON tags | Audit all JSON structs with `time.Time` fields |
| 16+ | `os.Root` secure file access | Needs design doc for project file reading |
| 17+ | `slog` structured logging | 309 sites — needs architecture decision |
| 18+ | `testing/synctest` for retry/timeout tests | Evaluate which tests benefit |
| 19+ | `tool` directives in `go.mod` | Replace `tools.go` pattern |

---

## Appendix A: Already Modern ✅

The codebase already excels in these areas:
- **266 usages** of `slices.*` / `maps.*` packages
- `slices.Sorted(maps.Keys(...))` pattern in 6+ places
- `maps.Clone`, `maps.Copy` used correctly
- `context.WithoutCancel` used where appropriate
- `errors.Join` for multi-error aggregation
- Zero `ioutil.*` — fully migrated
- `any` used throughout (only 7 hand-written `interface{}` remain)

## Appendix B: No Action Needed

| Area | Status |
|------|--------|
| `ioutil` migration | ✅ Complete |
| `context.WithoutCancel` | ✅ Already used |
| `errors.Join` | ✅ Already used (12 sites) |
| `slices.Contains` | ✅ Used extensively |
| Crypto/TLS config | ✅ Modern (TLS 1.2 minimum) |
| File I/O patterns | ✅ Uses `os.ReadFile`/`os.WriteFile` |
