# Release History


## 0.0.6-preview (Unreleased)

### Breaking Changes

- Removed `-e` shorthand for `--project-endpoint`; use `--project-endpoint` instead. This resolves a collision with azd's global `-e/--environment` flag.

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
