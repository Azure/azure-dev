# Extension Development Guidelines for `azd`

## Overview

This guide provides design guidelines and best practices for developing extensions to the Azure Developer CLI (`azd`). Following these guidelines ensures that extensions maintain consistency with core `azd` functionality and provide a seamless user experience.

## Design Guidelines for Extensions

### 1. **Command Integration Strategy**

- New functionality should extend existing command categories
- Use a verb-first structure, where the primary action (e.g., add, create, delete, list) is the top-level command, and the target entity or context follows as an argument or subcommand.
- Example: `azd add <new-resource-type>` instead of `azd <new-resource-type> add`

### 2. **Parameter and Flag Consistency**

- Reuse established parameter patterns across new commands
- Maintain consistent naming conventions (e.g., `--subscription`, `--name`, `--type`)
- Provide sensible defaults to reduce cognitive load

### 3. **Help and Discoverability**

- Integrate new functionality into existing `azd help` structure
- Provide contextual guidance within established command flows
- Maintain documentation consistency across core and extended features

### 4. **Template and Resource Integration**

- Leverage existing template system for new resource types
- Follow established template discovery and management patterns
- Integrate new resources into azd's resource lifecycle management

### 5. **CI/CD and IaC Guidance**

- Provide support for GitHub Actions and Azure DevOps
- Consider support for a range of IaC providers (Bicep, Terraform, etc.)

## Implementation Benefits

- **User Familiarity**: Builds on known command patterns and reduces learning curve
- **Discoverability**: New capabilities are found through existing workflows
- **Consistency**: Predictable behavior across all azd commands and extensions
- **Maintainability**: Systematic approach reduces complexity and technical debt
- **Extensibility**: Clear framework for adding capabilities without breaking existing patterns
- **Ecosystem Growth**: Provides foundation for third-party extensions and integrations

## Future Considerations

This framework enables:

- Advanced workflow automation
- Enhanced developer productivity features
- Consistent user experience across all `azd` functionality
- Integration of new Azure services and capabilities
- Third-party extension development

---

*For core design principles that apply to all `azd` functionality, see [guiding-principles.md](guiding-principles.md).*
