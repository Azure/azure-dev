# Command Structure Design Principles for `azd`

## Overview

The Azure Developer CLI (`azd`) follows a systematic approach to command structure design that ensures discoverability, consistency, and extensibility across all features and future enhancements. These principles provide a foundation for building any new functionality while maintaining user familiarity and operational efficiency.

## Core Design Principles

### 1. **Verb-First Command Structure**

- Keep verb selection minimal to avoid user confusion: `up`, `add`, `deploy`
- Use simple, common words that users already understand
- Each command group should deal with one type of object to maintain clarity

### 2. **Build on Existing Foundations**

- Leverage established `azd verbs` rather than creating parallel command structures (eg `azd init` vs `azd foo init`)
- Extend existing workflows to accommodate new functionality (eg `init`, `up`, `down`, workflow with pickers)
- Maintain backward compatibility with current usage patterns
- Avoid ambiguous or similarly-named verbs; build on core verbs instead of creating new ones

### 3. **Progressive Disclosure**

- Basic commands work simply, advanced features are available when needed
- Users should naturally discover advanced capabilities within familiar command flows
- Maintain graceful degradation when advanced features are unavailable

### 4. **Consistency Across All Operations**

- Maintain consistent parameter patterns across similar commands
- Use established naming conventions and flag structures
- Ensure similar operations follow similar command patterns

## Command Structure Framework

### Primary Command Categories

#### **View** (Information & Status)

- Purpose: Display current state, configuration, and system information
- Examples: `list`, `show`, `version`
- Principle: Read-only operations that help users understand current state

#### **Edit** (Configuration & Management)

- Purpose: Modify configuration, manage resources, and setup operations
- Examples: `set`, `reset`, `unset`, `config`, `refresh`, `select`, `add`, `remove`, `install`, `uninstall`, `new`
- Principle: Operations that change system state or configuration

#### **Run** (Actions & Operations)

- Purpose: Execute workflows, deploy resources, and perform active operations
- Examples: `up`, `down`, `init`, `package`, `provision`, `deploy`, `run`
- Principle: Operations that perform work or execute processes

#### **Other** (Specialized Operations)

- Purpose: Specialized tasks that don't fit standard categories
- Examples: `restore`, `monitor`, `generate`, `login`, `logout`
- Principle: Unique operations specific to particular workflows

## Extension Development

For guidelines on developing extensions to `azd`, see [extensions-style-guide.md](extensions-style-guide.md).

---

*These principles ensure that azd can grow and evolve while maintaining its core strengths of simplicity, discoverability, and developer productivity.*