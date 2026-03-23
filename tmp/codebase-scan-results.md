# Go Modernization Analysis — Azure Developer CLI

**Date:** 2025-07-22
**Go Version:** 1.26
**Branch:** `feat/local-fallback-remote-build`
**Scope:** `cli/azd/pkg/`, `cli/azd/cmd/`, `cli/azd/internal/`

---

## Executive Summary

The codebase already makes strong use of modern Go features: `slices`, `maps`, `slices.Sorted(maps.Keys(...))`,
`slices.Contains`, `maps.Clone`, `context.WithoutCancel`, and `errors.Join` are all present. The remaining
modernization opportunities are mostly **low-to-medium impact** incremental cleanups.

| Category | Occurrences | Impact | Effort |
|---|---|---|---|
| 1. sort.Slice → slices.SortFunc | 16 | Medium | Low |
| 2. interface{} → any | 7 (non-generated) | Low | Low |
| 3. math.Min(float64) → built-in min | 1 | Low | Trivial |
| 4. sync.Map → generic typed map | 13 | Medium | Medium |
| 5. http.NewRequest → WithContext | 6 | Medium | Low |
| 6. sort.Strings → slices.Sort | 2 (non-test) | Low | Trivial |
| 7. Manual keys collect → slices.Sorted(maps.Keys) | 1 | Low | Trivial |
| 8. for i := 0; i < n; i++ → range n | 6 | Low | Trivial |
| 9. Manual slice clone → slices.Clone | 1 real case | Low | Trivial |
| 10. log.Printf → structured logging | 309 | High | High |
| 11. C-style for loops in parsers | 3 | Low | Low |

---

## 1. `sort.Slice` / `sort.Sort` / `sort.Strings` → `slices` package

**Count:** 16 total (10 in non-test code)

The project already uses `slices.Sort`, `slices.SortFunc`, and `slices.Sorted(maps.Keys(...))` extensively
(~266 `slices.*` / `maps.*` usages). These 16 remaining `sort.*` calls are stragglers.

**Non-test files to update:**

| File | Line | Current | Replacement |
|---|---|---|---|
| `pkg/infra/provisioning_progress_display.go` | 182 | `sort.Slice(newlyDeployedResources, ...)` | `slices.SortFunc(newlyDeployedResources, ...)` |
| `pkg/extensions/manager.go` | 350 | `sort.Sort(semver.Collection(availableVersions))` | Keep — `semver.Collection` implements `sort.Interface`; no `slices` equivalent without wrapper |
| `pkg/account/subscriptions_manager.go` | 329 | `sort.Slice(allSubscriptions, ...)` | `slices.SortFunc(allSubscriptions, ...)` |
| `pkg/account/subscriptions.go` | 84, 139, 168 | `sort.Slice(...)` | `slices.SortFunc(...)` |
| `internal/agent/tools/io/file_search.go` | 146 | `sort.Strings(secureMatches)` | `slices.Sort(secureMatches)` |
| `internal/telemetry/appinsights-exporter/transmitter.go` | 186 | `sort.Sort(result.Response.Errors)` | Keep — custom `sort.Interface` type |

**Recommendation:** Replace `sort.Slice` → `slices.SortFunc` and `sort.Strings` → `slices.Sort` where the type
is a plain slice (not implementing `sort.Interface`). Use `cmp.Compare` for the comparison function.

**Example transformation:**
```go
// Before
sort.Slice(allSubscriptions, func(i, j int) bool {
    return allSubscriptions[i].Name < allSubscriptions[j].Name
})

// After
slices.SortFunc(allSubscriptions, func(a, b Subscription) int {
    return cmp.Compare(a.Name, b.Name)
})
```

**Impact:** Medium — improves type safety and readability, removes `sort` import in several files.

---

## 2. `interface{}` → `any`

**Count:** 140 total, but **133 are in generated `.pb.go` files** (protobuf/gRPC stubs).

**Non-generated occurrences (7):**

| File | Line | Code |
|---|---|---|
| `pkg/llm/github_copilot.go` | 205 | `func saveToFile(filePath string, data interface{}) error` |
| `pkg/llm/github_copilot.go` | 220 | `func loadFromFile(filePath string, data interface{}) error` |
| `pkg/llm/github_copilot.go` | 410 | `var copilotResp map[string]interface{}` |
| `pkg/azapi/deployments.go` | 339-340 | Comment: `interface{}` |
| `internal/grpcserver/project_service.go` | 420 | Comment: `interface{}` |
| `internal/grpcserver/project_service.go` | 714 | Comment: `interface{}` |

**Recommendation:** Run `go fix ./...` which should auto-convert `interface{}` → `any`. The generated `.pb.go`
files are controlled by protobuf codegen and should be left as-is (they'll update when protos are regenerated).

**Impact:** Low — cosmetic modernization. Only 4 actual code changes needed (3 in `github_copilot.go`, 1 in `deployments.go`).

---

## 3. `math.Min(float64(...), float64(...))` → built-in `min()`

**Count:** 1

| File | Line | Current |
|---|---|---|
| `pkg/account/subscriptions_manager.go` | 304 | `numWorkers := int(math.Min(float64(len(tenants)), float64(maxWorkers)))` |

**Recommendation:**
```go
// Before
numWorkers := int(math.Min(float64(len(tenants)), float64(maxWorkers)))

// After
numWorkers := min(len(tenants), maxWorkers)
```

**Impact:** Low — single occurrence, but a clean improvement. Removes the awkward float64 casting.

---

## 4. `sync.Map` → Generic Typed Map

**Count:** 13 usages across 8 files

| File | Line | Field |
|---|---|---|
| `internal/cmd/add/add_select_ai.go` | 357 | `var sharedResults sync.Map` |
| `pkg/grpcbroker/message_broker.go` | 81-82 | `responseChans sync.Map`, `handlers sync.Map` |
| `pkg/auth/credential_providers.go` | 27 | `tenantCredentials sync.Map` |
| `pkg/devcentersdk/developer_client.go` | 38 | `cache sync.Map` |
| `pkg/ux/canvas.go` | 153 | `items sync.Map` |
| `pkg/ai/model_service.go` | 218, 304 | `var sharedResults sync.Map` |
| `pkg/containerapps/container_app.go` | 115-116 | `appsClientCache sync.Map`, `jobsClientCache sync.Map` |

**Recommendation:** Consider creating a generic `SyncMap[K, V]` wrapper or using a third-party typed
concurrent map. Go 1.23+ doesn't add a generic `sync.Map` in stdlib, but a project-level utility would
eliminate `any` casts at every `Load`/`Store` call site.

```go
// Utility type
type SyncMap[K comparable, V any] struct {
    m sync.Map
}

func (s *SyncMap[K, V]) Load(key K) (V, bool) {
    v, ok := s.m.Load(key)
    if !ok {
        var zero V
        return zero, false
    }
    return v.(V), true
}
```

**Impact:** Medium — removes type assertions at every call site, prevents type mismatch bugs.

---

## 5. `http.NewRequest` → `http.NewRequestWithContext`

**Count:** 6 non-test occurrences using `http.NewRequest` without context

| File | Line |
|---|---|
| `tools/avmres/main.go` | 193 |
| `internal/telemetry/appinsights-exporter/transmitter.go` | 80 |
| `extensions/azure.ai.agents/internal/project/parser.go` | 1041, 1106, 1161 |
| `pkg/llm/github_copilot.go` | 388 |

The codebase already uses `http.NewRequestWithContext` in 30 other places — these 6 are inconsistent.

**Recommendation:** Replace all `http.NewRequest(...)` with `http.NewRequestWithContext(ctx, ...)` to ensure
proper cancellation propagation. If no context is available, use `context.Background()` explicitly.

**Impact:** Medium — ensures cancellation and timeout propagation works correctly for HTTP calls.

---

## 6. `sort.Strings` → `slices.Sort`

**Count:** 2 non-test files

| File | Line | Current |
|---|---|---|
| `internal/agent/tools/io/file_search.go` | 146 | `sort.Strings(secureMatches)` |
| `extensions/azure.ai.finetune/internal/cmd/validation.go` | 75 | `sort.Strings(missingFlags)` |

**Recommendation:** `slices.Sort(secureMatches)` — direct 1:1 replacement, no behavior change.

**Impact:** Low — trivial cleanup.

---

## 7. Manual Map Keys Collection → `slices.Sorted(maps.Keys(...))`

**Count:** 1 remaining instance (the codebase already uses the modern pattern in 6+ places)

| File | Line | Current Pattern |
|---|---|---|
| `pkg/azdext/scope_detector.go` | 116-120 | Manual `keys := make([]string, 0, len(m)); for k := range m { keys = append(keys, k) }; slices.Sort(keys)` |

**Recommendation:**
```go
// Before (5 lines)
keys := make([]string, 0, len(opts.CustomRules))
for k := range opts.CustomRules {
    keys = append(keys, k)
}
slices.Sort(keys)

// After (1 line)
keys := slices.Sorted(maps.Keys(opts.CustomRules))
```

**Impact:** Low — single occurrence, but a good example of the pattern the rest of the codebase already follows.

---

## 8. C-style `for i := 0; i < n; i++` → `for i := range n`

**Count:** 6 candidates

| File | Line | Current |
|---|---|---|
| `pkg/password/generator_test.go` | 84 | `for i := 0; i < len(choices); i++` |
| `pkg/yamlnode/yamlnode.go` | 235, 297 | `for i := 0; i < len(s); i++` — character-by-character parsing |
| `pkg/apphost/eval.go` | 33 | `for i := 0; i < len(src); i++` — character parsing |
| `pkg/output/table.go` | 141 | `for i := 0; i < v.Len(); i++` — reflect.Value iteration |
| `cmd/extensions.go` | 56 | `for i := 0; i < len(namespaceParts)-1; i++` |

**Recommendation:** Only convert simple iteration patterns. The character-parsing loops in `yamlnode.go` and
`eval.go` modify `i` inside the loop body (e.g., `i++` to skip chars), so they **cannot** use `range n`.

Convertible:
- `pkg/password/generator_test.go:84` → `for i := range len(choices)`
- `cmd/extensions.go:56` → `for i := range len(namespaceParts)-1`

Not convertible (loop variable modified inside body):
- `pkg/yamlnode/yamlnode.go:235,297` — skip
- `pkg/apphost/eval.go:33` — skip
- `pkg/output/table.go:141` — uses `reflect.Value.Len()`, fine to convert: `for i := range v.Len()`

**Impact:** Low — readability improvement for simple cases.

---

## 9. Manual Slice Clone → `slices.Clone`

**Count:** 82 total matches for `append([]T{}, ...)`, but most are in generated `.pb.go` files or
are actually prepend patterns (building a new slice with a prefix element), not clones.

**Actual clone candidate (1):**

| File | Line | Current |
|---|---|---|
| `pkg/project/service_target_external.go` | 298 | `return append([]string{}, endpointsResp.Endpoints...), nil` |

**Recommendation:**
```go
// Before
return append([]string{}, endpointsResp.Endpoints...), nil

// After
return slices.Clone(endpointsResp.Endpoints), nil
```

Most other `append([]T{}, ...)` patterns are prepend operations (e.g., `append([]string{cmd}, args...)`),
which are correct as-is — `slices.Clone` wouldn't apply.

**Impact:** Low — single real clone candidate.

---

## 10. `log.Printf` / `log.Println` → Structured Logging

**Count:** 309 non-test occurrences of `log.Print*`

**Hotspot files:**

| File | Count (approx) |
|---|---|
| `pkg/pipeline/pipeline_manager.go` | ~20+ |
| `pkg/tools/pack/pack.go` | ~10 |
| `pkg/tools/github/github.go` | ~10 |
| `pkg/cmdsubst/secretOrRandomPassword.go` | 2 |

**Recommendation:** This is the **highest-volume** modernization opportunity. Consider adopting `log/slog`
for structured, leveled logging. However, this is a large-effort change that would require:

1. Defining a project-wide `slog.Handler` configuration
2. Replacing all `log.Printf` calls with `slog.Info`, `slog.Debug`, etc.
3. Ensuring log levels map correctly (many current `log.Printf` calls are debug-level)

The codebase already has an internal tracing/telemetry system (`internal/tracing/`), so `slog` adoption
should integrate with that.

**Impact:** High value, but **high effort**. Best done as a dedicated initiative, not incremental cleanup.

---

## 11. Context Patterns

**Count:** ~90 context usage sites

The codebase already uses modern context patterns well:
- `context.WithoutCancel` — used in `main.go` and tests (Go 1.21+) ✅
- `context.WithCancel`, `context.WithTimeout` — standard usage ✅
- `context.AfterFunc` — not used (Go 1.21+, niche utility)

**No actionable items** — context usage is modern.

---

## 12. Error Handling Patterns

**Count:** 2,742 `errors.New` / `fmt.Errorf` occurrences; 12 `errors.Join` usages

The project already uses `errors.Join` where multi-error aggregation is needed. Error wrapping with
`fmt.Errorf("...: %w", err)` is the standard pattern and is used correctly throughout.

**No actionable items** — error handling follows Go best practices.

---

## 13. File I/O Patterns

**Count:** 0 `ioutil.*` usages ✅

The codebase has already migrated from `io/ioutil` (deprecated in Go 1.16) to `os.ReadFile`,
`os.WriteFile`, etc. No action needed.

---

## 14. Iterator Patterns (`iter.Seq` / `iter.Seq2`)

**Count:** 0 direct usages

The `iter` package (Go 1.23+) is not used directly, though `slices.Collect(maps.Keys(...))` and
range-over-func are used implicitly via the `slices`/`maps` packages.

**Recommendation:** No immediate action. Iterator patterns are most useful for custom collection types;
the codebase doesn't have strong candidates for custom iterators currently.

---

## 15. JSON Handling

**Count:** 309 `json.Marshal`/`Unmarshal`/`NewDecoder`/`NewEncoder` usages

Standard `encoding/json` usage throughout. Go 1.24+ introduced `encoding/json/v2` as an experiment,
but it's not stable yet.

**No actionable items.**

---

## 16. Crypto/TLS

**Count:** 13 usages

TLS configuration in `pkg/httputil/util.go` correctly sets `MinVersion: tls.VersionTLS12`. Standard
crypto usage (`crypto/sha256`, `crypto/rand`) throughout.

**No actionable items.**

---

## Priority Recommendations

### Quick Wins (< 1 hour each)

1. **`math.Min` → built-in `min`** — 1 file, 1 line
2. **`sort.Strings` → `slices.Sort`** — 2 files
3. **Manual keys collect → `slices.Sorted(maps.Keys(...))`** — 1 file
4. **`interface{}` → `any`** via `go fix ./...` — automatic
5. **`slices.Clone` for actual clone** — 1 file

### Medium Effort (1-4 hours)

6. **`sort.Slice` → `slices.SortFunc`** — ~8 non-test files
7. **`http.NewRequest` → `http.NewRequestWithContext`** — 6 files
8. **Range-over-int conversions** — 2-3 convertible sites

### Large Effort (multi-day initiative)

9. **`sync.Map` → generic typed wrapper** — 8 files, requires design
10. **`log.Printf` → `slog` structured logging** — 309 call sites, requires architecture decision

---

## Already Modern ✅

The codebase is **already well-modernized** in several areas:

- **266 usages** of `slices.*` / `maps.*` packages
- `slices.Sorted(maps.Keys(...))` pattern used in 6+ places
- `maps.Clone`, `maps.Copy` used in pipeline and infra packages
- `slices.Contains`, `slices.ContainsFunc`, `slices.IndexFunc` used extensively
- `slices.DeleteFunc` used for filtering
- `context.WithoutCancel` used correctly
- `errors.Join` used for multi-error scenarios
- Zero `ioutil.*` usages (fully migrated)
- `any` used throughout (only 7 hand-written `interface{}` remain)
