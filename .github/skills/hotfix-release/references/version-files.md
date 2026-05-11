# Version Files — Hotfix Update

When creating a hotfix release, three files must be updated to the new version.

## 1. `cli/version.txt`

Contains the bare version string (no `v` prefix):

```
1.24.4
```

Update:
```bash
echo "HOTFIX_VERSION" > cli/version.txt
```

## 2. `cli/azd/pkg/azdext/version.go`

Contains the `Version` constant. Find and update the line:

```go
const Version = "1.24.3"
```

Change to:

```go
const Version = "1.24.4"
```

Use `sed` or direct file edit. Verify:
```bash
grep 'const Version' cli/azd/pkg/azdext/version.go
```

## 3. `cli/azd/CHANGELOG.md`

Add a new release section at the top (below the `# Changelog` header and any `## Unreleased` section):

```markdown
## 1.24.4 (2026-05-07) — Hotfix

### Bugs Fixed

- PR title here [[#NNNN]](https://github.com/Azure/azure-dev/pull/NNNN)
```

Place **above** the previous release entry (e.g., above `## 1.24.3`).

## Validation

After updating all three files, verify consistency:

```bash
VERSION_TXT=$(cat cli/version.txt | tr -d '\n')
VERSION_GO=$(grep 'const Version' cli/azd/pkg/azdext/version.go | grep -oP '"[^"]*"' | tr -d '"')
echo "version.txt: $VERSION_TXT"
echo "version.go:  $VERSION_GO"

if [ "$VERSION_TXT" != "$VERSION_GO" ]; then
  echo "❌ Version mismatch!"
else
  echo "✅ Versions match: $VERSION_TXT"
fi
```
