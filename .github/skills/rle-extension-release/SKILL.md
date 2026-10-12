---
name: rle-extension-release
description: >-
  **WORKFLOW SKILL** — Publishes a new version of the `azure.ai.rle` extension to the
  internal `rle-dev` or external `rle-ext` registry through separate release wrappers.
  Aligns version files, cross-compiles all six platform artifacts, updates the selected
  registry, optionally marks the release as a forced (breaking) update, verifies the result,
  and opens the PR.

  INVOKES: pwsh, azd x (microsoft.azd.extensions), go, git CLI, gh CLI.

  USE FOR: publish rle extension, release rle extension, push rle to registry,
  force users to update rle, breaking update rle, prepare-dev-release, prepare-ext-release,
  bump rle registry,
  new rle dev release, rle-dev registry, external RLE release, rle-ext registry.

  DO NOT USE FOR: writing CHANGELOG entries (use changelog-generation), releasing azd core,
  releasing non-RLE extensions, publishing to the public azd registry.
---

# rle-extension-release

**WORKFLOW SKILL** — Cuts an internal or external release of the `azure.ai.rle` extension.

INVOKES: `pwsh`, `azd x`, `go`, `git` CLI, `gh` CLI.

Run everything from `cli/azd/extensions/azure.ai.rle/`.

This skill starts where [`changelog-generation`](../changelog-generation/SKILL.md) stops. That
skill writes `CHANGELOG.md` and bumps `version.txt` + `extension.yaml`; this one builds the
artifacts and publishes them to the registry.

## Workflow

### Step 1 — Check the toolchain

All three are hard requirements. The release scripts throw on missing `go` and
fail at the build step on missing `azd x`.

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
| `version.txt` | what `-VersionBump` reads |
| `extension.yaml` (`version:`) | shipped inside every artifact |
| fork `registry.rle-dev.json` or upstream `registry.rle.json` | what users in the selected channel resolve against |

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
  intended (`CHANGELOG.md` having a section for one of them is strong evidence), then align
  the two files before releasing.

Registry versions need not be contiguous. Skipped versions that were never published are
harmless; the update check compares by semver ordering. The channels have independent
histories: external users see only versions published to `Azure/azure-dev`.

### Step 3 — Select the channel and decide whether the release is breaking

| channel | audience | registry | artifacts |
|---|---|---|---|
| `rle-dev` | internal development team | fork `registry.rle-dev.json` on `main` | fork `artifacts/rle-dev/<version>` |
| `rle-ext` | external preview customers | upstream `registry.rle.json` on `rle-releases` | upstream `artifacts/<version>` |

Use `prepare-dev-release.ps1` for dev-only releases and
`prepare-ext-release.ps1` to prepare the same version for both dev and customer
distribution with one version bump. Both are wrappers over
`prepare-release.ps1`, which builds channel-neutral packages once. The installed
azd source selects the update registry at runtime, not build-time flags.
`VersionBump` defaults to `patch`; pass `minor` or `major` explicitly
only when needed.

`internal/cmd/breaking_update.go` OR-accumulates `breakingChanges` over **every registry
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

# external customer channel (existing, clean upstream checkout on rle-releases)
pwsh -NoProfile -File ./prepare-ext-release.ps1 -AzureDevRepositoryPath "<local Azure/azure-dev checkout>" -VersionBump patch
```

Takes several minutes — it cross-compiles six binaries once, including for an
external release. Allow at least 300s before treating it as hung.

It wipes and recreates `bin/` and the selected local package directory, runs
`azd x build --all --skip-install` then `azd x pack`, validates the artifact set, re-reads the
selected registry to **preserve `breakingChanges` flags on older versions** (`azd x publish` drops
them), publishes, applies the new flag, and rewrites artifact URLs to
the selected repository's branch. Development packages are written to
`artifacts/rle-dev/<version>/` in the implementation checkout. External releases
reuse the exact same archives and copy them to `artifacts/<version>/`
in the supplied public checkout. The obsolete fork external registry and tracked
external packages must not be recreated.
After a verified customer package copy, `bin/` is removed; no separate external
package staging is needed. Copy or verification failure retains local output for recovery. Dev
packages, the dev registry, and version-file changes remain for manual commit.

Development repository URLs are inferred from the implementation checkout's
GitHub HTTPS or SSH `origin`, with `main` as the publication branch. Unsupported
or missing origins fail before changing the version.

The external wrapper requires `-AzureDevRepositoryPath` with no environment
fallback and validates that checkout's origin, branch, clean
state, and release-only branch changes, then fetches and fast-forwards if needed.
Neither channel stages, commits, pushes, or opens a PR automatically.

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

Development artifacts **are** checked in. Commit them with the fork registry and
version files in a single commit, or the registry will reference URLs that 404.
No separate external package tree is generated in the implementation checkout.

```bash
git checkout -b rle/release-<version>
git add version.txt extension.yaml ../registry.rle-dev.json artifacts/rle-dev/<version>
git commit   # include the Co-authored-by trailer if an agent authored the change
git push -u origin rle/release-<version>
gh pr create --base main --title "Release RLE extension <version>"
```

State in the PR body: why the release is (or is not) breaking, any skipped versions, and the
verification evidence from Step 5.

For external releases, follow the wrapper's printed staging commands in the
supplied public checkout: force-add only the new version's archives, add
`registry.rle.json`, then commit and push `rle-releases` together. This updates the
existing distribution PR and makes the new release available without merging it.
Commit the source version files, dev registry, and dev packages together in the
implementation checkout; do not include source code
or the dev registry in the upstream distribution PR.

**Development artifact URLs target `main`, not the PR head — the release is not
installable until this merges.** Users will resolve 404s if the registry lands without the artifacts.

---

## Error Handling

- `Version 'X' in version.txt must match version 'Y' in extension.yaml.` → Step 2; align the
  files, then rerun the release.
- `Go is required to cross-compile…` → install Go and put it on `PATH`.
- `Missing packaged artifacts: …` / `Unexpected packaged artifacts: …` → the build produced the
  wrong set; inspect `bin/` and re-run. A stale `bin/` cannot cause this (the script wipes it),
  so suspect a build failure for one `GOOS`/`GOARCH`.
- `Registry is missing platform entries: …` → `azd x publish` did not ingest every artifact;
  confirm all six exist in the selected package directory.
- `Expected one '<version>' entry in the registry, but found 0.` → the publish silently
  no-op'd; check the `azd x publish` output above the throw.
- Registry `breakingChanges` flags vanished from older versions → the preservation pass did not
  run, meaning the script exited between publish and rewrite. Restore from `git diff` and re-run.

## Notes

- `breakingChanges` is **not** in `registry.schema.json`, so a schema check will not catch a
  typo. The Go struct tag at `internal/cmd/breaking_update.go:43` is the source of truth, and it
  is camelCase.
