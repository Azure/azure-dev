# Release History


## 0.0.7-preview (Unreleased)

### Features

- Added LoRA adapter support to `create` command with `--lora-rank`, `--lora-alpha`, `--lora-target-modules`, and `--lora-dropout` flags for registering LoRA adapters (`--weight-type LoRA`)
- `show` command now displays LoRA Configuration section (rank, alpha, target modules, dropout) for LoRA adapters
- `list` command now shows Weight Type column to distinguish FullWeight and LoRA models

## 0.0.6-preview (Unreleased)

### Features

- Added top-level `azd ai models create`, `list`, `show`, `delete` commands as the preferred surface; the `custom` subgroup is now deprecated
- Added `--weight-type` flag to `create` command (default: `FullWeight`)
- Added `--source-job-id` filter to `list` command for querying models by training job lineage
- Added `azd ai models update` command for updating model description and tags (JSON Merge Patch)
- `show` command now displays weight type, provisioning state, source lineage, and artifact profile when available
- `--publisher` flag is now optional (previously defaulted to `Fireworks`); only sent when explicitly provided

### Breaking Changes

- Removed `-e` shorthand for `--project-endpoint`; use `--project-endpoint` instead. This resolves a collision with the azd global `-e/--environment` flag.

### Improvements

- `startPendingUpload` request now sends `pendingUploadType: "TemporaryBlobReference"` for explicit upload type declaration
- Model response now supports new fields: `weightType`, `baseModel`, `source`, `artifactProfile`, `provisioningState`

### Deprecations

- `azd ai models custom <command>` is deprecated; use `azd ai models <command>` directly instead

## 0.0.5-preview (2026-03-24)

- Deprecated `-e` shorthand for `--project-endpoint`; use the full flag name instead
- Improved error handling for 403 (Forbidden) during `custom create` upload, with guidance on required roles and links to prerequisites and RBAC documentation (#7278)

## 0.0.4-preview (2026-03-17)

- Added async model registration with server-side validation and polling support
- Removed `--blob-uri` flag from `custom create` to prevent invalid data reference errors when registering models with externally uploaded blobs
- Improved 409 error handling in `custom create` with guidance to use `show` to fetch latest version
- `custom show` now defaults to latest version when `--version` is omitted
- `custom create` auto-extracts version from `--base-model` azureml:// URI when `--version` is not explicitly provided

## 0.0.3-preview (2026-02-19)

- Fixed azcopy download: added `githubusercontent.com` to allowed redirect hosts

## 0.0.2-preview (2026-02-19)

- Fixed azcopy download redirect to allow `github.com` as a trusted host

## 0.0.1-preview (2026-02-19)

- Initial release to support custom model creation
