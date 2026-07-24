## Reference: Release & Publish

There are two distinct flows. **Pick based on audience.**

---

## A. External extension (your own repo)

The `azd x` suite drives the full release directly against **your** GitHub repo and registry.

### 1. Bump version & changelog

- Update `version` in `extension.yaml` (semver: MAJOR breaking / MINOR feature / PATCH fix).
- Add a new section to `CHANGELOG.md` (used as default release notes).

### 2. Build & pack

```bash
azd x build --all      # binaries for all platforms
azd x pack             # package artifacts into the local registry artifacts path (~/.azd/registry)
# or, in one step:
azd x pack --rebuild   # rebuild then pack
```

`azd x pack` flags: `-i/--input` (binary dir, default `./bin`), `-o/--output` (artifacts dir,
default local registry path), `--rebuild`, `--bundle` (produce a single self-contained `.zip`
installable via `azd extension install <bundle.zip>`).

### 3. Create the GitHub release

```bash
azd x release --repo <owner>/<repo>
```

Flags: `--repo` (required, `owner/repo`), `-v/--version` (defaults to manifest version),
`-t/--title`, `-n/--notes` / `-F/--notes-file` (defaults to `CHANGELOG.md`),
`--artifacts` (glob patterns, defaults to local artifacts path), `--prerelease`, `-d/--draft`,
`--confirm` (skip prompt). Requires GitHub auth (`gh auth login` or `GITHUB_TOKEN`).

### 4. Publish to a registry

```bash
azd x publish --repo <owner>/<repo>
```

Updates a `registry.json` (default: your local extension source registry; override with
`-r/--registry`) with the new version's metadata, artifact URLs, and checksums. Point users at that
registry with `azd extension source add -n <name> -t url -l <registry-url>`.

### Combined workflow

```bash
cd path/to/your/extension
azd x pack --rebuild
azd x release --repo owner/repo
azd x publish --repo owner/repo
```

`azd x init` already publishes to a **local** source and installs the extension, so during
development users on the same machine can install immediately without a GitHub release.

---

## B. First-party extension (inside Azure/azure-dev)

First-party extensions ship into the azure-dev **official** registry through a **2-PR flow** with a
CI release pipeline in between. Do **not** run `azd x release`/`publish` against `Azure/azure-dev`
directly.

### PR 1 — Version bump

Change only these files for the extension under `cli/azd/extensions/<id>/`:

- `version.txt` — new semver string (present in first-party scaffolds).
- `extension.yaml` — matching `version`.
- `CHANGELOG.md` — new release section at the top.

Once merged, the team triggers the CI release pipeline, which builds, signs, and publishes the
extension binaries as a GitHub release.

### PR 2 — Registry update

After the GitHub release is live, a follow-up PR updates
`cli/azd/extensions/registry.json` so azd users can install the new version. Generate that entry by
running `azd x publish` against the published release artifacts (it computes artifact URLs and
checksums). The PR should contain **only** the regenerated `registry.json` entry for this
extension.

> Some first-party extensions document their exact release steps in their own `AGENTS.md`
> (e.g. `cli/azd/extensions/azure.ai.toolboxes/AGENTS.md`). Follow the extension-specific file when
> present.

### Registries in the repo

- `cli/azd/extensions/registry.json` — official/production registry.
- `cli/azd/extensions/registry.dev.json` — dev/experimental registry.
- A **nightly** registry (`registry.nightly.json` on the `nightly` branch) auto-builds first-party
  extensions from `main`.

Modifying `registry.json` triggers CI snapshot tests (help text + IntelliSense) — regenerate
snapshots if needed (see build-test-install reference).

---

## GitHub authentication (both flows, for release/publish)

```bash
gh auth login                      # interactive
# or
export GITHUB_TOKEN=<pat_with_repo_scope>
```

## Common issues

- **Version conflict** — the release version already exists; bump `version`.
- **Registry conflict / schema** — the id already exists or the manifest fails schema validation.
- **Permission denied** — token lacks `repo` scope.
- **Slow builds** — Python ~4 min vs Go ~15s; expect longer `--all` builds.
