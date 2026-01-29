# Azure Developer CLI (`azd`)

> **From code to cloud in minutes.** A developer-centric CLI to build, deploy, and operate Azure applications.

[![azd version](https://img.shields.io/endpoint?url=https%3A%2F%2Fazuresdkartifacts.z5.web.core.windows.net%2Fazd%2Fstandalone%2Flatest%2Fshield.json)](https://github.com/Azure/azure-dev/releases)
[![VS Code Extension](https://img.shields.io/endpoint?url=https%3A%2F%2Fazuresdkartifacts.z5.web.core.windows.net%2Fazd%2Fvscode%2Flatest%2Fshield.json)](https://marketplace.visualstudio.com/items?itemName=ms-azuretools.azure-dev)
[![GitHub Discussions](https://img.shields.io/github/discussions/Azure/azure-dev)](https://github.com/Azure/azure-dev/discussions)

---

## Built for you

- ⚡ **Get productive fast** — Streamlined workflows to go from code to cloud in minutes
- 🏗️ **Azure recommended practices built-in** — Opinionated templates that follow Azure development standards
- 🧠 **Learn as you build** — Understand core Azure constructs through hands-on experience

📖 **[Get Started](https://aka.ms/azd)** · 💬 **[Join the Discussion](https://github.com/Azure/azure-dev/discussions)** · 📦 **[Browse Templates](https://azure.github.io/awesome-azd/)**

---

## Downloads

| Artifact | Version | Download |
| -------- | ------- | -------- |
| CLI | ![azd version](https://img.shields.io/endpoint?url=https%3A%2F%2Fazuresdkartifacts.z5.web.core.windows.net%2Fazd%2Fstandalone%2Flatest%2Fshield.json) | [Windows](https://azuresdkartifacts.z5.web.core.windows.net/azd/standalone/latest/azd-windows-amd64.zip) · [Linux](https://azuresdkartifacts.z5.web.core.windows.net/azd/standalone/latest/azd-linux-amd64.tar.gz) · [macOS](https://azuresdkartifacts.z5.web.core.windows.net/azd/standalone/latest/azd-darwin-amd64.zip) |
| VS Code Extension | ![vscode extension version](https://img.shields.io/endpoint?url=https%3A%2F%2Fazuresdkartifacts.z5.web.core.windows.net%2Fazd%2Fvscode%2Flatest%2Fshield.json) | [Marketplace](https://marketplace.visualstudio.com/items?itemName=ms-azuretools.azure-dev) |

## 🤖 AI Agents

**Contributing to this repo?** See [AGENTS.md](cli/azd/AGENTS.md) for coding standards and guidelines.

**Using `azd` with an AI coding assistant?** Check out the [docs](https://aka.ms/azd) and [templates](https://azure.github.io/awesome-azd/).

---

## Installation

Install or upgrade to the latest version. For advanced scenarios, see the [installer docs](cli/installer/README.md).

### Windows

```powershell
# Using winget (recommended)
winget install microsoft.azd

# Or Chocolatey
choco install azd

# Or install script
powershell -ex AllSigned -c "Invoke-RestMethod 'https://aka.ms/install-azd.ps1' | Invoke-Expression"
```

### macOS

```bash
brew tap azure/azd && brew install azd
```

> **Note:** If upgrading from a non-Homebrew installation, remove the existing `azd` binary first.

### Linux

```bash
curl -fsSL https://aka.ms/install-azd.sh | bash
```

### Shell Completion

Enable tab completion for `bash`, `zsh`, `fish`, or `powershell`:

```bash
azd completion <shell> --help
```

---

## Uninstall

<details>
<summary><strong>Windows</strong></summary>

- **v0.5.0+**: Use "Add or remove programs" or your package manager (`winget uninstall`, `choco uninstall`)
- **v0.4.0 and earlier**:
  ```powershell
  powershell -ex AllSigned -c "Invoke-RestMethod 'https://aka.ms/uninstall-azd.ps1' | Invoke-Expression"
  ```

</details>

<details>
<summary><strong>Linux / macOS</strong></summary>

```bash
curl -fsSL https://aka.ms/uninstall-azd.sh | bash
```

Or use your package manager's uninstall command.

</details>

---

## Data Collection

This software may collect usage data and send it to Microsoft to help improve our products. You can opt out by setting:

```bash
export AZURE_DEV_COLLECT_TELEMETRY=no
```

See the [Microsoft Privacy Statement](https://go.microsoft.com/fwlink/?LinkId=521839) for details.

---

## Contributing

We welcome contributions! Please see our [contributing guide](cli/azd/CONTRIBUTING.md) for details.

Most contributions require a [Contributor License Agreement (CLA)](https://cla.microsoft.com). A bot will guide you through this when you open a PR.

This project follows the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/). Questions? Contact [opencode@microsoft.com](mailto:opencode@microsoft.com).

### Template Authors

Microsoft employees and partners contributing official templates should follow the [template standardization guidelines](https://github.com/Azure-Samples/azd-template-artifacts).

---

## Trademarks

This project may contain Microsoft trademarks or logos. Use of these must follow [Microsoft's Trademark & Brand Guidelines](https://www.microsoft.com/legal/intellectualproperty/trademarks). Third-party trademarks are subject to their respective policies.

## License

Learn more about running third-party templates on [Microsoft Learn](https://learn.microsoft.com/azure/developer/azure-developer-cli/azd-templates#guidelines-for-using-azd-templates).
