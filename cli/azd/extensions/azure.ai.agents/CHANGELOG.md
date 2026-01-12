# Release History

## 0.1.5-preview (2026-01-12)

- [[#6468]](https://github.com/Azure/azure-dev/pull/6468) Add support for retrieving existing Application Insights connections when using `--project-id`
- [[#6482]](https://github.com/Azure/azure-dev/pull/6482) Improve `azd ai agent init -m` validation and fix GitHub URL parsing issue

## 0.1.4-preview (2025-12-15)

- [[#6326]](https://github.com/Azure/azure-dev/pull/6326) Fix correlation ID propagation and improve tracing for API calls
- [[#6343]](https://github.com/Azure/azure-dev/pull/6343) Improve `azd ai agent init` completion message to recommend `azd up` first
- [[#6344]](https://github.com/Azure/azure-dev/pull/6344) Rename `AI_FOUNDRY_PROJECT_APP_ID` environment variable to `AZURE_AI_PROJECT_PRINCIPAL_ID`
- [[#6366]](https://github.com/Azure/azure-dev/pull/6366) Fix manifest URL path when branch name contains "/"

## 0.1.3-preview (2025-12-03)

- Improve agent service debug logging via `AZD_EXT_DEBUG` env var and `--debug` flag

## 0.1.2-preview (2025-11-20)

- Update extension name and descriptions
- Update user facing text to use Microsoft Foundry

## 0.1.1-preview (2025-11-17)

- Fix min and max replicas not being set during agent deployment
- Fix `azd show` not displaying agent endpoint
- Polish user prompts and messages

## 0.1.0-preview (2025-11-14)

- Apply defaults instead of prompting in event handlers
- Process model resources as parameters
- Update env var generation to support multi-agent projects
- Polish error messages
- Improve local manifest handling
- Fix agent playground URL generation
- Fix panic when container settings is nil

## 0.0.7 (2025-11-13)

- Add prompting for container resources
- Add "preview" label to extension name and command descriptions
- Show agent playground URL post-deploy
- Support fetching ACR connections from existing AI Foundry projects
- Fix environment variable references
- Improve agent name validation

## 0.0.6 (2025-11-11)

- Add support for using existing AI model deployments
- Add `--project-id` flag for initializing using existing AI Foundry projects
- Fix agent definition handling for saved templates

## 0.0.5 (2025-11-06)

- Add support for tools
- Improve defaulting logic and --no-prompt support
- Fix remote build support

## 0.0.4 (2025-11-05)

- Add support for --no-prompt and --environment flags in `azd ai agent init`
- Include operation ID in timeout error
- Fix env vars not being included in agent create request

## 0.0.3 (2025-11-04)

- Add support for latest MAML format
- Fix agent endpoint handling for prompt agents

## 0.0.2 (2025-10-31)

- Add --host flag to `azd ai agent init`
- Rename host type to `azure.ai.agent`
- Store model information in service config
- Display agent endpoint on successful deploy
- Improve error handling
- Fix panic when no default model capacity is returned

## 0.0.1 (2025-10-28)

- Initial release
