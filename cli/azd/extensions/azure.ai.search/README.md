# Azure AI Search (Preview)

Development scaffold for `azure.ai.search`.

## Development

```powershell
azd x build
azd x pack --bundle --output .\bin\azure-ai-search.zip
azd ext install .\bin\azure-ai-search.zip
azd ai search --help
azd ai search version --output json
go test ./... -short
```

Learn more in the [azd extension framework](https://github.com/Azure/azure-dev/blob/main/cli/azd/docs/extensions/extension-framework.md).
