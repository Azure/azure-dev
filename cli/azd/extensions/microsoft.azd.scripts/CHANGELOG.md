# Release History

## 0.1.0-alpha.1 (Unreleased)

### Features Added

- Initial scaffold for the Script Provisioning Provider extension.
- Registers `scripts` provisioning provider with azd via gRPC.
- Configuration parsing for `provision` and `destroy` script lists from `azure.yaml`.
- Script execution with 4-layer environment variable merging.
- Output collection from `outputs.json` files.
