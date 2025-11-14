# Release History

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
