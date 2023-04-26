# Infrastructure-as-code (IaC) Starter

This starter includes both Bicep and Terraform infrastructure provider files.

Remove unused file assets from the other provider using the instructions below, after picking a specific provider:

## Bicep

Remove all files with `.tf` in the name.

Example script that works in most shells:

```bash
rm *.tf.*
```

### PowerShell

Remove files that ends with `.bicep`, and also include `main.parameters.json`, `abbreviations.json`.

Example script that works in most shells:

```bash
rm *.bicep
rm main.parameters.json
rm abbreviations.json
```
