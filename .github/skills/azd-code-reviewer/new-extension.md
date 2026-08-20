<!-- cspell:ignore golangci -->

# New first-party extension review

Apply this guide when a PR adds a top-level directory under `cli/azd/extensions/`.

## 1. Classify the extension

Classify it as an executable extension or a dependency-only extension pack before reviewing. Apply executable build, lint, release, capability, and ownership checks only where applicable. The internal scaffold supports Go executable extensions; review another language against the build and release approach proposed in the PR.

## 2. Compare the current scaffold

For a Go executable extension, compare the PR with `createInternalExtensionScaffold` in `cli/azd/extensions/microsoft.azd.extensions/internal/cmd/init.go` and the Go resources under `cli/azd/extensions/microsoft.azd.extensions/internal/resources/`. These show the current expected baseline, but an explained alternative may be appropriate.

Check for easy-to-miss repository wiring such as the extension lint caller, spelling configuration, CODEOWNERS rule, release definition, PR bundle path, and relevant shared-template path filters. Raise a finding only when something needed by the proposed extension is missing or incompatible.

Missing repository wiring has no diff line to anchor a comment to. Report it as a PR-level finding rather than dropping it during triage.

## 3. Check the manifest contract

Validate `extension.yaml`, then trace its ID, sanitized ID, version, public command path where present, capabilities, and dependencies through source, build, lint, release, and customer-facing examples.

For each declared capability, follow its matching extension framework contract and check its implementation, registration, and applicable contract tests. If `providers` is present, check for `internal/cmd/providers_manifest_test.go` with `TestConfigureExtensionHostMatchesManifest` and for the extension lint workflow to call `.github/workflows/verify-ext-providers.yml`.

For Go extensions, check:

- the Go version matches `cli/azd/go.mod`
- `go.mod` has no local `replace` directives or azd SDK pseudo-versions
- direct dependencies shared with core use the core version, unless `cli/azd/dependency-versions.json` records an exact, issue-linked temporary exception
- the azd SDK version agrees with `requiredAzdVersion`

Dependency synchronization does not cover indirect dependencies or extension manifest dependencies. Confirm manifest dependencies can be satisfied in the intended registry before publication, and track unresolved prerequisites in the manual handoff.

## 4. Check publication timing and handoff

Registry updates and any resulting test snapshot updates should be handled in a follow-up PR, separate from the initial extension scaffolding.

Check that the author plans to follow up with a member of the azd core team to register the release YAML as a new Azure DevOps pipeline under `azure-dev/extensions` and verify its access to the shared release infrastructure.

A contributor with repository permissions should create a matching `ext-*` GitHub label for issue tracking.
