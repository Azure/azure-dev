# Release History


## 0.0.5-preview (2026-03-24)

- Added `deployment create` command to deploy models (custom and base) using the ARM Cognitive Services SDK
- Added `deployment list` command to list all model deployments with table/JSON output
- Added `deployment show` command to view detailed deployment information
- Added `deployment delete` command to remove model deployments with confirmation prompt
- Auto-resolves `--model-source` (project ARM resource ID) for custom model formats
- Layered context resolution: explicit flags → azd environment → interactive prompt
- User-friendly error handling for 403 (RBAC), 409 (conflict), and quota errors
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
