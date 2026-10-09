# Azure AI Search (Foundry IQ)

An azd extension scaffold for Azure AI Search. Currently provides help, version,
and hidden host metadata only; Search operations are not implemented yet.

## Development

Requires the Go version in `go.mod`, an azd version satisfying `requiredAzdVersion`
in `extension.yaml`, and the `microsoft.azd.extensions` authoring extension.

From this directory, run:

```powershell
azd x build
azd ai search --help
azd ai search version --output json
go test ./... -short
```

`azd x build` builds and installs the extension locally.

Learn more in the [azd extension framework](https://github.com/Azure/azure-dev/blob/main/cli/azd/docs/extensions/extension-framework.md).
