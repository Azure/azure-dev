# Output Examples and Error Handling

## Output

### Success

```
Preflight passed — all 8 checks clean.

  ✓ gofmt
  ✓ go fix
  ✓ copyright
  ✓ lint
  ✓ cspell
  ✓ build
  ✓ test
  ✓ playback tests
```

### Success After Fixes

```
Preflight passed after fixes.

  ✓ gofmt (fixed: 3 files reformatted)
  ✓ go fix (fixed: 2 modernizations applied)
  ✓ copyright (no issues)
  ✓ lint (fixed: 5 findings resolved)
  ✓ cspell (fixed: 1 word added to dictionary)
  ✓ build (no issues)
  ✓ test (no issues)
  ✓ playback tests (no issues)

Files modified: {list of changed files}
```

### Partial Success

```
Preflight partially passed — {N} of 8 checks clean, {M} skipped.

  ✓ gofmt
  ✓ go fix
  ✓ copyright
  ✓ lint
  ✓ cspell
  ✓ build
  ✗ test (skipped — user chose to skip after 3 attempts)
  ✓ playback tests

Skipped checks require manual intervention.
```

## Error Handling

- **mage not installed** → offer to install: `go install github.com/magefile/mage@latest`
- **golangci-lint not installed** → offer: `go install github.com/golangci/golangci-lint/cmd/golangci-lint@v2.11.4`
- **cspell not installed** → offer to install: `npm install -g cspell@8.13.1`
- **bash/sh not found (Windows)** → suggest Git for Windows: https://git-scm.com/downloads/win
- **Go version mismatch** → preflight sets `GOTOOLCHAIN` automatically; report version conflict if persists
- **Preflight timeout** → unit tests can take 10+ min; use at least 15-min timeout
- **Cannot determine repo root** → ensure cwd is within the `azure-dev` repository
