---
name: rle-extension-release
description: >-
  **WORKFLOW SKILL** — Publishes a new version of the `azure.ai.rle` extension to the
  `rle-dev` registry by driving `prepare-dev-release.ps1`. Aligns version files, cross-compiles
  all six platform artifacts, updates `registry.rle-dev.json`, optionally marks the release
  as a forced (breaking) update, verifies the result, and opens the PR.

  INVOKES: pwsh, azd x (microsoft.azd.extensions), go, git CLI, gh CLI.

  USE FOR: publish rle extension, release rle extension, push rle to registry,
  force users to update rle, breaking update rle, prepare-dev-release, bump rle registry,
  new rle dev release, rle-dev registry.

  DO NOT USE FOR: writing CHANGELOG entries (use changelog-generation), releasing azd core,
  releasing non-RLE extensions, publishing to the public azd registry.
---

# rle-extension-release

**WORKFLOW SKILL** — Cuts a development release of the `azure.ai.rle` extension.

INVOKES: `pwsh`, `azd x`, `go`, `git` CLI, `gh` CLI.

Run everything from `cli/azd/extensions/azure.ai.rle/`.

This skill starts where [`changelog-generation`](../changelog-generation/SKILL.md) stops. That
skill writes `CHANGELOG.md` and bumps `version.txt` + `extension.yaml`; this one builds the
artifacts and publishes them to the registry.

## Workflow

### Step 1 — Check the toolchain

All three are hard requirements. `prepare-dev-release.ps1` throws on missing `go`, and fails
at the build step on missing `azd x`.

```bash
pwsh --version          # script is PowerShell
go version              # required: cross-compiles 6 platforms
azd x version           # microsoft.azd.extensions
```

Install what is missing:

- `azd extension install microsoft.azd.extensions`
- PowerShell: follow the current Microsoft install docs for the host OS. On Linux CI images
  `pwsh` is frequently absent and must be installed before anything else works.

### Step 2 — Reconcile the three versions

Three places carry a version and they drift, because a hand-edit of `extension.yaml` bypasses
the script entirely:

| source | purpose |
|---|---|
| `version.txt` | what `-VersionBump` reads and what `-Version` defaults to |
| `extension.yaml` (`version:`) | shipped inside every artifact |
| `registry.rle-dev.json` | what users resolve against |

```bash
cat version.txt
grep '^version:' extension.yaml
python3 -c "import json;print([v['version'] for v in json.load(open('../registry.rle-dev.json'))['extensions'][0]['versions']])"
```

Then pick a path:

- **`version.txt` == `extension.yaml`** → use `-VersionBump major|minor|patch`. It computes the
  next version, preserves the prerelease suffix, and writes **both** files for you. Preferred.
- **They disagree** → `-VersionBump` throws
  `Version 'X' in version.txt must match version 'Y' in extension.yaml.` Decide which is
  intended (`CHANGELOG.md` having a section for one of them is strong evidence), align
  `version.txt` to `extension.yaml` by hand, then use explicit `-Version`.

**Gotcha:** explicit `-Version` skips the bump block, so it does **not** write `version.txt`.
Align it yourself or the *next* release's `-VersionBump` throws again. `-Version` and
`-VersionBump` together also throw — they are mutually exclusive.

Registry versions need not be contiguous. Skipped versions that were never published are
harmless; the update check compares by semver ordering.

### Step 3 — Decide whether the release is breaking

`internal/cmd/breaking_update.go:114` OR-accumulates `breakingChanges` over **every registry
version newer than the installed one**. If any is set, lifecycle commands fail with
`rle_breaking_update_required` — "Update the RLE extension to X before continuing." `version`
and `--help` still work.

So **marking the newest version breaking forces every user below it to update.** Use it when
the extension and its service must move together: wire-protocol changes, changed command
defaults, registry/endpoint moves. Do not use it for additive features.

The switch is three-state — this matters:

| invocation | effect on the flag |
|---|---|
| `-BreakingChanges` | sets `breakingChanges: true` |
| `-BreakingChanges:$false` | **removes** the property |
| switch omitted | leaves any existing flag untouched |

### Step 4 — Run the release

```bash
# preferred, versions already aligned
pwsh -NoProfile -File ./prepare-dev-release.ps1 -VersionBump patch -BreakingChanges

# recovery path, when version.txt was realigned by hand
pwsh -NoProfile -File ./prepare-dev-release.ps1 -Version <x.y.z-preview> -BreakingChanges
```

Takes several minutes — it cross-compiles six binaries. Allow at least 300s before treating it
as hung.

It wipes and recreates `bin/` and `artifacts/rle-dev/<version>/`, runs
`azd x build --all --skip-install` then `azd x pack`, validates the artifact set, re-reads the
registry to **preserve `breakingChanges` flags on older versions** (`azd x publish` drops
them), publishes, applies the new flag, and rewrites artifact URLs to
`https://raw.githubusercontent.com/<Repository>/<RepositoryBranch>/<path>` — defaulting to
`sujit-kamireddy/azure-dev` and `main`. Override with `-Repository` / `-RepositoryBranch` when
releasing from a different fork.

`Skipped Building extension` in the **pack** phase is normal — `azd x pack --input bin`
consumes the binaries the build phase already produced.

### Step 5 — Verify before committing

The script validates the artifact set and platform keys itself, so check the things it cannot:

```bash
python3 -c "
import json
ext = json.load(open('../registry.rle-dev.json'))['extensions'][0]
print('versions:', [v['version'] for v in ext['versions']])
print('flags:', {v['version']: v.get('breakingChanges') for v in ext['versions'] if 'breakingChanges' in v})
"
```

1. The new version is present and last.
2. Its `breakingChanges` flag matches intent.
3. Flags on **older** versions survived — this is the regression the preservation logic exists
   to prevent.
4. Six artifacts on disk: `windows/amd64`, `windows/arm64`, `darwin/amd64`, `darwin/arm64` as
   `.zip`; `linux/amd64`, `linux/arm64` as `.tar.gz`.
5. Registry checksum matches the file — `sha256sum` the artifact and compare.
6. End-to-end: unpack the host-platform artifact and run it.

```bash
mkdir -p /tmp/rlecheck
tar xzf artifacts/rle-dev/<version>/azure-ai-rle-linux-amd64.tar.gz -C /tmp/rlecheck
/tmp/rlecheck/azure-ai-rle-linux-amd64 version     # must print the new version
grep '^version:' /tmp/rlecheck/extension.yaml      # must match
```

The binary name inside the archive is platform-suffixed and matches the registry `entryPoint`
— it is not a bare `azure-ai-rle`.

### Step 6 — Commit and open the PR

Artifacts **are** checked in: `.gitignore` ignores `bin/` but explicitly negates
`artifacts/rle-dev/**`. Commit them with the registry and version files in a single commit, or
the registry will reference URLs that 404.

```bash
git checkout -b rle/release-<version>
git add version.txt extension.yaml ../registry.rle-dev.json artifacts/rle-dev/<version>
git commit   # include the Co-authored-by trailer if an agent authored the change
git push -u origin rle/release-<version>
gh pr create --base main --title "Release RLE extension <version>"
```

State in the PR body: why the release is (or is not) breaking, any skipped versions, and the
verification evidence from Step 5.

**Artifact URLs target the branch, not the PR head — the release is not installable until this
merges.** Users will resolve 404s if the registry lands without the artifacts.

---

## Error Handling

- `Version 'X' in version.txt must match version 'Y' in extension.yaml.` → Step 2; align by
  hand, then use explicit `-Version`.
- `Version and VersionBump cannot be specified together.` → pass exactly one.
- `Version 'X' must match the version in extension.yaml.` → the explicit `-Version` disagrees
  with the manifest; fix the manifest or pass the manifest's version.
- `Go is required to cross-compile…` → install Go and put it on `PATH`.
- `Missing packaged artifacts: …` / `Unexpected packaged artifacts: …` → the build produced the
  wrong set; inspect `bin/` and re-run. A stale `bin/` cannot cause this (the script wipes it),
  so suspect a build failure for one `GOOS`/`GOARCH`.
- `Registry is missing platform entries: …` → `azd x publish` did not ingest every artifact;
  confirm all six exist under `artifacts/rle-dev/<version>/`.
- `Expected one '<version>' entry in the registry, but found 0.` → the publish silently
  no-op'd; check the `azd x publish` output above the throw.
- `OutputDirectory must be inside the repository…` → drop the custom `-OutputDirectory`.
- Registry `breakingChanges` flags vanished from older versions → the preservation pass did not
  run, meaning the script exited between publish and rewrite. Restore from `git diff` and re-run.

## Notes

- `breakingChanges` is **not** in `registry.schema.json`, so a schema check will not catch a
  typo. The Go struct tag at `internal/cmd/breaking_update.go:43` is the source of truth, and it
  is camelCase.
- The extension README's claim that the script "currently builds the Windows AMD64 extension
  artifact" is stale — it cross-compiles all six.
