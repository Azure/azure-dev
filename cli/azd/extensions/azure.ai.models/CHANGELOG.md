# Release History


## 0.0.2-preview (2026-02-19)

- Added `catalogInfo` and `derivedModelInformation` to model registration payload
- Added `--publisher` flag (default: `Fireworks`) and made `--base-model` required
- Fixed azcopy download redirect to allow `github.com` as a trusted host
- Fixed remote URL folder structure issue in azcopy copy
- Removed redundant `ci-test.ps1`
- Added `SkipTests: true` to release pipeline
- Added CODEOWNERS entry for `azure.ai.models`

## 0.0.1-preview (2026-02-19)

- Initial release to support custom model creation
