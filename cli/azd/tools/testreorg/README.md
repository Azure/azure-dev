# testreorg

Tooling used to reorganize the `cli/azd` unit tests so each test lives in the
`*_test.go` file matching the source file it exercises, and to retire the
catch-all coverage files (`*_coverage*_test.go`, `*_additional_test.go`,
`*_extra_test.go`, `final_*`, `wave3_*`, `deeper_*`, …).

See [Azure/azure-dev#8799](https://github.com/Azure/azure-dev/issues/8799) for
the motivation. This is throwaway-ish, single-purpose tooling kept in the
repository so the reorganization is reproducible and the approach is not lost.

It lives in its own nested Go module (like the entries under `cli/azd/extensions/`)
so the test-free `package main` commands do not dilute the main module's coverage
gate or get pulled into `go build ./...` / `golangci-lint run ./...`. Because it is
a separate module, invoke the Go commands with `go -C tools/testreorg run ./<tool>`
and pass file paths as absolute paths (for example with `"$PWD/..."`).

## Safety invariant

Moving a declaration exactly once *within the same package* cannot change which
tests compile or run, because the package already compiled (no cross-file name
collisions are possible) and the same set of `Test*` functions still exists.
`capture.sh` makes this checkable: it prints the package-qualified set of every
test/helper function name. Capture it before and after; an empty diff proves no
test was dropped, duplicated, or renamed by accident.

```bash
# from cli/azd
tools/testreorg/capture.sh > /tmp/before.txt
# ... run the moves ...
tools/testreorg/capture.sh > /tmp/after.txt
diff /tmp/before.txt /tmp/after.txt && echo IDENTICAL
```

## Tools

| Tool | Purpose |
| --- | --- |
| `capture.sh` | Print the package-qualified function-name set used as the safety invariant. |
| `mergetool` | Append the post-import body of one test file into another in the same package (used when a matching `<source>_test.go` already exists). |
| `router` | Route each declaration of a genuine catch-all file to the `<source>_test.go` whose source defines the most symbols the declaration references. `-dry` prints the plan; `-apply` performs the moves and deletes the input. |
| `strip_suffix.py` | Collision-safe removal of stale, file-derived suffixes from tests *and* shared helpers — word suffixes (`_Final`, `_Finish`, `_Push`, `_Deeper`, `_Extra`, `_Additional`, `_More`, `_Coverage`/`_Coverage3`, `_Cov3`, `_Wave3`) and round suffixes (`_r9`, `_r10`, `_round8`, …). References are rewritten too; colliding duplicates are left untouched. |
| `fix_aliases.py` | Restore aliased imports that `goimports` cannot infer, by test-compiling and reading `undefined:` errors. |

## Typical workflow

```bash
# from cli/azd
go -C tools/testreorg run ./router -dry  "$PWD/pkg/foo/foo_coverage3_test.go"  # preview
go -C tools/testreorg run ./router -apply "$PWD/pkg/foo/foo_coverage3_test.go" # redistribute

# or merge a *_extra/_additional file into its base test file
go -C tools/testreorg run ./mergetool "$PWD/pkg/foo/foo_extra_test.go" "$PWD/pkg/foo/foo_test.go"
git rm pkg/foo/foo_extra_test.go

# reconcile imports across everything that changed, then verify
goimports -w -local github.com/azure/azure-dev <changed files>
gofmt -s -w <changed files>
go test ./... -run '^$'   # whole-module compile check
```

After redistributing, run the full pre-commit checklist from
[`cli/azd/AGENTS.md`](../../AGENTS.md) (`gofmt -s`, `go fix`, `golangci-lint`,
copyright check) and confirm `capture.sh` reports no diff.
