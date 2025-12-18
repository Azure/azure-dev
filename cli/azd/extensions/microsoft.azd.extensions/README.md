# `azd` X Extension

The `X` extension is used for developing `azd` extensions.

## Local development

```bash
# Use current version of 'microsoft.azd.extensions' in the registry for bootstrapping
azd x build --skip-install

# Perform a manual installation
cp -f bin/* ~/.azd/extensions/microsoft.azd.extensions/
```
