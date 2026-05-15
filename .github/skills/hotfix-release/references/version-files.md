# Version Files — Hotfix Update

When creating a hotfix release, two version files must be updated.

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

## Validation

After updating both files, verify consistency:

```bash
VERSION_TXT=$(cat cli/version.txt | tr -d '\n')
VERSION_GO=$(grep 'const Version' cli/azd/pkg/azdext/version.go | sed 's/.*"\(.*\)".*/\1/')
echo "version.txt: $VERSION_TXT"
echo "version.go:  $VERSION_GO"

if [ "$VERSION_TXT" != "$VERSION_GO" ]; then
  echo "❌ Version mismatch!"
else
  echo "✅ Versions match: $VERSION_TXT"
fi
```
