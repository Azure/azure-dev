## Problem

Remote environment state stored in Azure Blob Storage was written with `UploadStream(..., nil)`, so uploads sent no `If-Match` / `If-None-Match` access conditions. Concurrent `azd` runs that read-modify-write the same environment could silently overwrite each other. There was no ETag captured on download, no precondition on upload, and no distinct conflict error.

## Change

Optimistic concurrency for blob-backed environment state (no leases in this change):

- `Download` now returns the blob body plus its ETag.
- `Upload` sends `If-Match` with the ETag from load, or `If-None-Match: *` when the blob did not exist (empty match).
- `StorageBlobDataStore` stores per-blob ETags from `Reload` (and from a not-found download when saving a new blob) and passes them on save. A successful upload refreshes the stored ETag.
- HTTP 412 / `ConditionNotMet` is mapped to a typed `ConcurrencyConflictError` (`ErrConcurrencyConflict`). The data store wraps that as `ErrRemoteStateConflict` with an `ErrorWithSuggestion` telling the user the remote environment was modified by another process and to refresh/retry.
- `error_suggestions.yaml` was not updated: AGENTS.md says `ErrorMiddleware` skips the YAML pipeline when the error is already `ErrorWithSuggestion` with fields populated.

## Testing

Interleaved-save test against unmodified code (assertion failure, not a compile error):

```
=== RUN   TestStorageBlobDataStore_InterleavedSavesConflict
    storage_blob_data_store_test.go:201:
        	Error Trace:	.../storage_blob_data_store_test.go:201
        	Error:      	An error is expected but got nil.
        	Test:       	TestStorageBlobDataStore_InterleavedSavesConflict
        	Messages:   	second save must fail with a conflict instead of overwriting
--- FAIL: TestStorageBlobDataStore_InterleavedSavesConflict (0.00s)
FAIL	github.com/azure/azure-dev/cli/azd/pkg/environment	0.022s
```

After the fix:

```
=== RUN   TestStorageBlobDataStore_InterleavedSavesConflict
--- PASS: TestStorageBlobDataStore_InterleavedSavesConflict (0.00s)
PASS
ok  	github.com/azure/azure-dev/cli/azd/pkg/environment	0.025s
```

Also covered:

1. Two stores load the same ETag; first save succeeds; second fails with `ErrRemoteStateConflict` / `ErrConcurrencyConflict` and does not overwrite.
2. Load-then-save sends `If-Match` with the loaded ETag (httptest + in-memory client).
3. New blob upload sends `If-None-Match: *`.
4. HTTP 412 `ConditionNotMet` maps to `ConcurrencyConflictError`.

From `cli/azd`:

```
gofmt -s -l .          # clean (0 files)
go vet ./pkg/azsdk/storage/... ./pkg/environment/...
golangci-lint run ./pkg/azsdk/storage/... ./pkg/environment/...   # 0 issues
go test ./pkg/azsdk/storage/... ./pkg/environment/...
```

Suite results (`go test -count=1`):

| Package | Result | Coverage before | Coverage after |
|---|---|---|---|
| `pkg/azsdk/storage` | ok, 28 tests | 64.2% | 64.9% (+0.7 pp) |
| `pkg/environment` | ok, 248 tests | 76.7% | 76.8% (+0.1 pp) |
| `pkg/environment/azdcontext` | ok, 126 tests | 89.2% | 89.2% (unchanged) |

No package dropped more than 0.5 pp. `go tool mage coverage:pr` was attempted after commit if the mage target is available.

Fixes #9317

This change was prepared with AI assistance and reviewed by the author.
